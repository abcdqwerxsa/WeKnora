package handler

// Built-in workflow template endpoints: read-only listing/detail backed by
// the engine's startup-loaded template registry, plus the instantiate
// action that copies a template into the caller's tenant as a published
// workflow (KB placeholders bound from the request payload).

import (
	"errors"
	"net/http"

	wfengine "github.com/Tencent/WeKnora/internal/agent/workflow"
	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// WorkflowTemplateHandler handles built-in workflow template requests.
type WorkflowTemplateHandler struct {
	service interfaces.WorkflowService
}

// NewWorkflowTemplateHandler creates a new workflow template handler.
func NewWorkflowTemplateHandler(service interfaces.WorkflowService) *WorkflowTemplateHandler {
	return &WorkflowTemplateHandler{service: service}
}

func localeFromCtx(c *gin.Context) string {
	lang, _ := types.LanguageFromContext(c.Request.Context())
	return lang
}

// ListWorkflowTemplates godoc
// @Summary      内置工作流模板列表
// @Description  列出平台内置的只读工作流模板（Viewer 及以上）
// @Tags         工作流
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Failure      500 {object} apperrors.AppError
// @Security     Bearer
// @Router       /workflow-templates [get]
func (h *WorkflowTemplateHandler) ListWorkflowTemplates(c *gin.Context) {
	locale := localeFromCtx(c)
	list := wfengine.ListWorkflowTemplates()
	out := make([]gin.H, 0, len(list))
	for _, tpl := range list {
		display := tpl.Localized(locale)
		out = append(out, gin.H{
			"id":              tpl.ID,
			"category":        tpl.Category,
			"name":            display.Name,
			"description":     display.Description,
			"kb_placeholders": tpl.KBPlaceholders,
			"node_count":      tpl.NodeCount(),
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}

// GetWorkflowTemplate godoc
// @Summary      内置工作流模板详情
// @Description  返回模板元数据与完整 DSL（编辑器预览用）
// @Tags         工作流
// @Produce      json
// @Param        id path string true "模板 id"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} apperrors.AppError
// @Security     Bearer
// @Router       /workflow-templates/{id} [get]
func (h *WorkflowTemplateHandler) GetWorkflowTemplate(c *gin.Context) {
	tpl := wfengine.GetWorkflowTemplate(c.Param("id"))
	if tpl == nil {
		c.Error(apperrors.NewNotFoundError("workflow template not found"))
		return
	}
	display := tpl.Localized(localeFromCtx(c))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"id":              tpl.ID,
			"category":        tpl.Category,
			"name":            display.Name,
			"description":     display.Description,
			"kb_placeholders": tpl.KBPlaceholders,
			"node_count":      tpl.NodeCount(),
			"dsl":             tpl.DSL,
		},
	})
}

// InstantiateWorkflowTemplate godoc
// @Summary      从模板创建工作流
// @Description  将内置模板复制为当前空间的已发布工作流；模板声明的每个知识库占位符必须在 kb_bindings 中绑定（Contributor 及以上）
// @Tags         工作流
// @Accept       json
// @Produce      json
// @Param        id path string true "模板 id"
// @Param        request body types.InstantiateWorkflowTemplateRequest true "名称覆盖与知识库绑定"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} apperrors.AppError
// @Failure      404 {object} apperrors.AppError
// @Security     Bearer
// @Router       /workflow-templates/{id}/instantiate [post]
func (h *WorkflowTemplateHandler) InstantiateWorkflowTemplate(c *gin.Context) {
	var req types.InstantiateWorkflowTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	workflow, err := h.service.InstantiateWorkflowTemplate(c.Request.Context(), c.Param("id"), &req)
	if err != nil {
		c.Error(templateHTTPError(err))
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    workflow,
	})
}

// templateHTTPError maps template service errors onto apperrors.
func templateHTTPError(err error) *apperrors.AppError {
	var missing *service.MissingKBBindingsError
	switch {
	case errors.As(err, &missing):
		return apperrors.NewBadRequestError(err.Error()).WithDetails(gin.H{"missing_placeholders": missing.Missing})
	case errors.Is(err, service.ErrWorkflowTemplateNotFound):
		return apperrors.NewNotFoundError("workflow template not found")
	case errors.Is(err, service.ErrWorkflowTemplateInvalidKB),
		errors.Is(err, service.ErrWorkflowTemplateMissingKB):
		return apperrors.NewBadRequestError(err.Error())
	default:
		return workflowHTTPError(err)
	}
}
