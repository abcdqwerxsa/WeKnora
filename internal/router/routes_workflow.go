package router

import (
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// RegisterWorkflowRoutes registers workflow orchestration routes.
//
// Slice 2 of the workflow-orchestration feature (storage + CRUD + RBAC).
// Execution (POST /:id/runs) is a 501 stub until the engine wiring slice
// lands.
//
// Guard matrix (mirrors the agent matrix — workflows are creator-owned
// resources, not tenant infrastructure):
//
//   - POST /workflows       Contributor+  (entry point; the copy is owned
//     by the caller, exactly like POST /agents)
//   - GET  (list/detail)    Viewer+       (tenant-scoped reads)
//   - PUT/DELETE /:id       creator OR Admin+ (OwnedWorkflowOrAdmin)
//   - POST /:id/runs        Contributor+  (entry point for a run; sync
//     execution returns 200+terminal state, async=true enqueues and
//     returns 202+pending)
//   - GET  /:id/runs        Viewer+       (tenant-scoped read)
//   - GET  /:id/runs/:run_id/events  Viewer+ (SSE progress stream)
//
// API keys: workflow routes are deliberately NOT declared in the
// APIKeyRouteAuthorizer, so X-API-Key principals are default-denied. They
// will be declared once execution semantics (and therefore the right
// capability set) exist.
func RegisterWorkflowRoutes(r *gin.RouterGroup, workflowHandler *handler.WorkflowHandler, g *rbacGuards) {
	if workflowHandler == nil {
		return
	}
	workflows := r.Group("/workflows")
	{
		workflows.POST("", g.Contributor(), workflowHandler.CreateWorkflow)
		workflows.GET("", g.Viewer(), workflowHandler.ListWorkflows)
		workflows.GET("/:id", g.Viewer(), workflowHandler.GetWorkflow)
		workflows.PUT("/:id", g.OwnedWorkflowOrAdmin(workflowHandler), workflowHandler.UpdateWorkflow)
		workflows.DELETE("/:id", g.OwnedWorkflowOrAdmin(workflowHandler), workflowHandler.DeleteWorkflow)
		workflows.POST("/:id/runs", g.Contributor(), workflowHandler.CreateWorkflowRun)
		workflows.GET("/:id/runs", g.Viewer(), workflowHandler.ListWorkflowRuns)
		workflows.GET("/:id/runs/:run_id/events", g.Viewer(), workflowHandler.GetWorkflowRunEvents)
		workflows.POST("/:id/runs/:run_id/cancel", g.Contributor(), workflowHandler.CancelWorkflowRun)
	}
}

// OwnedWorkflowOrAdmin: mutating a specific workflow requires the creator
// OR Admin+ (same rule as OwnedAgentOrAdmin; the lookup comes from the
// handler rather than rbacGuards so newRBACGuards' signature stays stable).
func (g *rbacGuards) OwnedWorkflowOrAdmin(h *handler.WorkflowHandler) gin.HandlerFunc {
	return middleware.RequireOwnershipOrRole(types.TenantRoleAdmin, h.WorkflowCreatorLookup, g.cfg)
}
