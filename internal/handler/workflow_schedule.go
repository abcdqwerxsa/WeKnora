package handler

// Workflow schedule endpoints — CRUD for cron-scheduled runs of published
// workflows. Guard matrix mirrors the workflow matrix: mutations are
// OwnedWorkflowOrAdmin, reads are Viewer. The scheduler itself (cron firing,
// run creation) is server-side only and not reachable over REST.

import (
	"errors"
	"net/http"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// WorkflowScheduleHandler handles workflow schedule requests.
type WorkflowScheduleHandler struct {
	service interfaces.WorkflowScheduleService
}

// NewWorkflowScheduleHandler creates a new handler instance.
func NewWorkflowScheduleHandler(service interfaces.WorkflowScheduleService) *WorkflowScheduleHandler {
	return &WorkflowScheduleHandler{service: service}
}

// scheduleHTTPError maps schedule service errors onto apperrors.
func scheduleHTTPError(err error) *apperrors.AppError {
	switch {
	case errors.Is(err, apprepo.ErrWorkflowNotFound):
		return apperrors.NewNotFoundError("workflow not found")
	case errors.Is(err, apprepo.ErrWorkflowScheduleNotFound):
		return apperrors.NewNotFoundError("workflow schedule not found")
	case errors.Is(err, service.ErrWorkflowScheduleBadCron),
		errors.Is(err, service.ErrWorkflowScheduleNotPublished),
		errors.Is(err, service.ErrWorkflowTenantRequired):
		return apperrors.NewBadRequestError(err.Error())
	default:
		return apperrors.NewInternalServerError(err.Error())
	}
}

// CreateWorkflowSchedule godoc
// @Summary      创建工作流定时计划
// @Description  为已发布的工作流创建 cron 定时计划（标准 5 段表达式：分 时 日 月 周）。仅已发布工作流可建计划；未发布返回 400
// @Tags         工作流
// @Accept       json
// @Produce      json
// @Param        id      path string                              true "工作流 ID"
// @Param        request body types.CreateWorkflowScheduleRequest true "计划内容"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} apperrors.AppError
// @Failure      404 {object} apperrors.AppError
// @Security     Bearer
// @Router       /workflows/{id}/schedules [post]
func (h *WorkflowScheduleHandler) CreateWorkflowSchedule(c *gin.Context) {
	var req types.CreateWorkflowScheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	schedule, err := h.service.CreateWorkflowSchedule(c.Request.Context(), c.Param("id"), &req)
	if err != nil {
		c.Error(scheduleHTTPError(err))
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": schedule})
}

// ListWorkflowSchedules godoc
// @Summary      工作流定时计划列表
// @Description  列出工作流的定时计划（新到旧）
// @Tags         工作流
// @Produce      json
// @Param        id path string true "工作流 ID"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} apperrors.AppError
// @Security     Bearer
// @Router       /workflows/{id}/schedules [get]
func (h *WorkflowScheduleHandler) ListWorkflowSchedules(c *gin.Context) {
	schedules, err := h.service.ListWorkflowSchedules(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.Error(scheduleHTTPError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"schedules": schedules, "total": len(schedules)},
	})
}

// DeleteWorkflowSchedule godoc
// @Summary      删除工作流定时计划
// @Description  删除计划并注销其 cron 条目（creator 本人或 Admin 及以上）
// @Tags         工作流
// @Param        id           path string true "工作流 ID"
// @Param        schedule_id  path string true "计划 ID"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} apperrors.AppError
// @Security     Bearer
// @Router       /workflows/{id}/schedules/{schedule_id} [delete]
func (h *WorkflowScheduleHandler) DeleteWorkflowSchedule(c *gin.Context) {
	if err := h.service.DeleteWorkflowSchedule(c.Request.Context(), c.Param("id"), c.Param("schedule_id")); err != nil {
		c.Error(scheduleHTTPError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Workflow schedule deleted successfully"})
}

// EnableWorkflowSchedule godoc
// @Summary      启用工作流定时计划
// @Description  重新启用计划并注册 cron 条目（creator 本人或 Admin 及以上）
// @Tags         工作流
// @Param        id           path string true "工作流 ID"
// @Param        schedule_id  path string true "计划 ID"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} apperrors.AppError
// @Security     Bearer
// @Router       /workflows/{id}/schedules/{schedule_id}/enable [post]
func (h *WorkflowScheduleHandler) EnableWorkflowSchedule(c *gin.Context) {
	h.setEnabled(c, true)
}

// DisableWorkflowSchedule godoc
// @Summary      停用工作流定时计划
// @Description  停用计划并注销 cron 条目（行保留，不再触发）
// @Tags         工作流
// @Param        id           path string true "工作流 ID"
// @Param        schedule_id  path string true "计划 ID"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} apperrors.AppError
// @Security     Bearer
// @Router       /workflows/{id}/schedules/{schedule_id}/disable [post]
func (h *WorkflowScheduleHandler) DisableWorkflowSchedule(c *gin.Context) {
	h.setEnabled(c, false)
}

func (h *WorkflowScheduleHandler) setEnabled(c *gin.Context, enabled bool) {
	schedule, err := h.service.SetWorkflowScheduleEnabled(c.Request.Context(), c.Param("id"), c.Param("schedule_id"), enabled)
	if err != nil {
		c.Error(scheduleHTTPError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": schedule})
}
