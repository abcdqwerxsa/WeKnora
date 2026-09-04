// Package handler — workflow CRUD + run endpoints.
//
// The workflow-orchestration feature was assembled from parallel slices;
// this file hosts the REST surface. Run execution is wired to the engine
// package through WorkflowService.RunWorkflow (synchronous MVP).
package handler

import (
	"errors"
	"net/http"
	"strings"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// WorkflowHandler handles workflow CRUD requests.
type WorkflowHandler struct {
	service interfaces.WorkflowService
}

// NewWorkflowHandler creates a new workflow handler instance.
func NewWorkflowHandler(service interfaces.WorkflowService) *WorkflowHandler {
	return &WorkflowHandler{service: service}
}

// WorkflowCreatorLookup resolves the :id workflow into its creator user id
// for the OwnedWorkflowOrAdmin guard. Same contract as AgentCreatorLookup:
// (creatorID, nil) grants ownership access; ("", ErrResourceNotFound) lets
// the handler answer 404; ("", nil) means tenant-owned (Admin+ only).
func (h *WorkflowHandler) WorkflowCreatorLookup(c *gin.Context) (string, error) {
	id := c.Param("id")
	if id == "" {
		return "", errors.New("missing :id param for workflow creator lookup")
	}
	ctx := c.Request.Context()
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return "", errors.New("workspace context missing")
	}
	workflow, err := h.service.GetWorkflowByID(ctx, id)
	if err != nil {
		if errors.Is(err, apprepo.ErrWorkflowNotFound) {
			return "", middleware.ErrResourceNotFound
		}
		return "", err
	}
	if workflow == nil {
		return "", middleware.ErrResourceNotFound
	}
	if workflow.TenantID != tenantID {
		// Defensive: the service is tenant-scoped already; a mismatch here
		// would mean a wiring bug, treat it as not-found rather than leak.
		return "", middleware.ErrResourceNotFound
	}
	return workflow.CreatorID, nil
}

// workflowHTTPError maps service-layer errors onto apperrors.
func workflowHTTPError(err error) *apperrors.AppError {
	switch {
	case errors.Is(err, apprepo.ErrWorkflowNotFound):
		return apperrors.NewNotFoundError("workflow not found")
	case errors.Is(err, service.ErrWorkflowNameRequired),
		errors.Is(err, service.ErrWorkflowNameTooLong),
		errors.Is(err, service.ErrWorkflowDescriptionTooLong),
		errors.Is(err, service.ErrWorkflowInvalidStatus),
		errors.Is(err, service.ErrWorkflowDSLRequired),
		errors.Is(err, service.ErrWorkflowInvalidDSL),
		errors.Is(err, service.ErrWorkflowTenantRequired):
		return apperrors.NewBadRequestError(err.Error())
	default:
		return apperrors.NewInternalServerError(err.Error())
	}
}

// CreateWorkflow godoc
// @Summary      创建工作流
// @Description  在当前空间创建工作流定义（Contributor 及以上；creator 固定为当前用户）
// @Tags         工作流
// @Accept       json
// @Produce      json
// @Param        request body types.CreateWorkflowRequest true "工作流定义"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} apperrors.AppError
// @Security     Bearer
// @Router       /workflows [post]
func (h *WorkflowHandler) CreateWorkflow(c *gin.Context) {
	ctx := c.Request.Context()
	var req types.CreateWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	workflow := &types.Workflow{
		Name:        req.Name,
		Description: req.Description,
		DSL:         req.DSL,
		Status:      req.Status,
	}
	created, err := h.service.CreateWorkflow(ctx, workflow)
	if err != nil {
		c.Error(workflowHTTPError(err))
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    created,
	})
}

// ListWorkflows godoc
// @Summary      工作流列表
// @Description  分页列出当前空间的工作流
// @Tags         工作流
// @Produce      json
// @Param        page query int false "页码（默认 1）"
// @Param        page_size query int false "每页数量（默认 20，最大 100）"
// @Success      200 {object} map[string]interface{}
// @Security     Bearer
// @Router       /workflows [get]
func (h *WorkflowHandler) ListWorkflows(c *gin.Context) {
	page, pageSize, ok := parseListPagination(c)
	if !ok {
		return
	}
	workflows, total, err := h.service.ListWorkflows(c.Request.Context(), page, pageSize)
	if err != nil {
		c.Error(workflowHTTPError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"workflows": workflows, "total": total, "page": page, "page_size": pageSize},
	})
}

// GetWorkflow godoc
// @Summary      工作流详情
// @Description  按 ID 获取当前空间的工作流
// @Tags         工作流
// @Produce      json
// @Param        id path string true "工作流 ID"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} apperrors.AppError
// @Security     Bearer
// @Router       /workflows/{id} [get]
func (h *WorkflowHandler) GetWorkflow(c *gin.Context) {
	workflow, err := h.service.GetWorkflowByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.Error(workflowHTTPError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    workflow,
	})
}

// UpdateWorkflow godoc
// @Summary      更新工作流
// @Description  全量替换可变字段（creator 本人或 Admin 及以上；版本号自增）
// @Tags         工作流
// @Accept       json
// @Produce      json
// @Param        id path string true "工作流 ID"
// @Param        request body types.UpdateWorkflowRequest true "更新内容"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} apperrors.AppError
// @Failure      404 {object} apperrors.AppError
// @Security     Bearer
// @Router       /workflows/{id} [put]
func (h *WorkflowHandler) UpdateWorkflow(c *gin.Context) {
	var req types.UpdateWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	updated, err := h.service.UpdateWorkflow(c.Request.Context(), c.Param("id"), &req)
	if err != nil {
		c.Error(workflowHTTPError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    updated,
	})
}

// DeleteWorkflow godoc
// @Summary      删除工作流
// @Description  软删除（creator 本人或 Admin 及以上）
// @Tags         工作流
// @Param        id path string true "工作流 ID"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} apperrors.AppError
// @Security     Bearer
// @Router       /workflows/{id} [delete]
func (h *WorkflowHandler) DeleteWorkflow(c *gin.Context) {
	if err := h.service.DeleteWorkflow(c.Request.Context(), c.Param("id")); err != nil {
		c.Error(workflowHTTPError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Workflow deleted successfully",
	})
}

// CreateWorkflowRun godoc
// @Summary      执行工作流
// @Description  同步执行一次工作流（MVP：120s 超时上限），运行记录写入 workflow_runs；执行失败时返回 run 记录（status=failed）
// @Tags         工作流
// @Accept       json
// @Produce      json
// @Param        id      path  string                     true "工作流 ID"
// @Param        request body  types.RunWorkflowRequest   true "执行输入（query 必填）"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} apperrors.AppError
// @Failure      404 {object} apperrors.AppError
// @Failure      500 {object} apperrors.AppError
// @Security     Bearer
// @Router       /workflows/{id}/runs [post]
func (h *WorkflowHandler) CreateWorkflowRun(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	var req types.RunWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		c.Error(apperrors.NewValidationError("query is required"))
		return
	}
	run, err := h.service.RunWorkflow(ctx, id, &req)
	if err != nil {
		if errors.Is(err, apprepo.ErrWorkflowNotFound) {
			c.Error(apperrors.NewNotFoundError("workflow not found"))
			return
		}
		if errors.Is(err, service.ErrWorkflowInvalidDSL) {
			c.Error(apperrors.NewValidationError("invalid workflow DSL").WithDetails(err.Error()))
			return
		}
		// A persisted failed run is a legitimate execution outcome, not a
		// transport error — surface the run record itself.
		if run != nil && run.Status == types.WorkflowRunStatusFailed {
			c.JSON(http.StatusOK, gin.H{"run": run})
			return
		}
		c.Error(apperrors.NewInternalServerError("failed to run workflow").WithDetails(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"run": run})
}

// ListWorkflowRuns godoc
// @Summary      工作流运行历史
// @Description  列出工作流的执行记录（新到旧）
// @Tags         工作流
// @Produce      json
// @Param        id path string true "工作流 ID"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} apperrors.AppError
// @Security     Bearer
// @Router       /workflows/{id}/runs [get]
func (h *WorkflowHandler) ListWorkflowRuns(c *gin.Context) {
	runs, err := h.service.ListWorkflowRuns(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.Error(workflowHTTPError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"runs": runs, "total": len(runs)},
	})
}
