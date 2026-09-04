package types

import (
	"time"

	"gorm.io/gorm"
)

// Workflow status lifecycle values. A workflow starts as draft, is
// published when its DSL is considered stable, and can be archived to
// hide it from editors without deleting run history.
const (
	WorkflowStatusDraft     = "draft"
	WorkflowStatusPublished = "published"
	WorkflowStatusArchived  = "archived"
)

// ValidWorkflowStatuses is the closed set of accepted workflow.status values.
var ValidWorkflowStatuses = []string{WorkflowStatusDraft, WorkflowStatusPublished, WorkflowStatusArchived}

// IsValidWorkflowStatus reports whether status is one of ValidWorkflowStatuses.
func IsValidWorkflowStatus(status string) bool {
	switch status {
	case WorkflowStatusDraft, WorkflowStatusPublished, WorkflowStatusArchived:
		return true
	default:
		return false
	}
}

// CreateWorkflowRequest is the REST payload for POST /api/v1/workflows.
// DSL is the verbatim workflow document; status defaults to draft.
type CreateWorkflowRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	DSL         JSON   `json:"dsl"`
	Status      string `json:"status"` // optional; empty = draft
}

// UpdateWorkflowRequest is the REST payload for PUT /api/v1/workflows/:id.
// The update is a full replace of the mutable fields: omitted fields reset
// to their zero value. Bump of `version` is server-side.
type UpdateWorkflowRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	DSL         JSON   `json:"dsl"`
	Status      string `json:"status"` // optional; empty = keep current
}

// Workflow represents a tenant-scoped workflow orchestration definition.
//
// The DSL document is stored verbatim (types.JSON). It carries a dual view —
// `graph` (nodes/edges for canvas rendering) and `components` (upstream/
// downstream execution topology) — whose structural validation lives in the
// service layer; this type intentionally treats it as opaque.
type Workflow struct {
	// Unique identifier of the workflow (UUID, generated in Go)
	ID string `yaml:"id" json:"id" gorm:"type:varchar(36);primaryKey"`
	// Tenant ID (data-isolation scope; every repository query filters on it)
	TenantID uint64 `yaml:"tenant_id" json:"tenant_id" gorm:"not null;index"`
	// Creator user ID; empty means tenant-owned (Admin+ only to mutate)
	CreatorID string `yaml:"creator_id" json:"creator_id" gorm:"type:varchar(36);not null;default:''"`
	// Display name (1-255 characters)
	Name string `yaml:"name" json:"name" gorm:"type:varchar(255);not null"`
	// Free-form description (<= 2000 characters)
	Description string `yaml:"description" json:"description" gorm:"type:text"`
	// Workflow DSL document, stored verbatim (dual view: graph + components)
	DSL JSON `yaml:"dsl" json:"dsl" gorm:"type:jsonb;not null;default:'{}'"`
	// Lifecycle status: draft | published | archived
	Status string `yaml:"status" json:"status" gorm:"type:varchar(50);not null;default:'draft'"`
	// bumped by 1 on every successful update
	Version int `yaml:"version" json:"version" gorm:"not null;default:1"`

	CreatedAt time.Time      `yaml:"created_at" json:"created_at"`
	UpdatedAt time.Time      `yaml:"updated_at" json:"updated_at"`
	DeletedAt gorm.DeletedAt `yaml:"deleted_at" json:"deleted_at"`
}

// WorkflowRun represents one execution record of a workflow.
//
// Populated by the execution-wiring slice; until then no endpoint creates
// rows and the list endpoint returns an empty history.
// RunWorkflowRequest is the input document of one workflow execution.
type RunWorkflowRequest struct {
	// Query is materialized into sys.query by the Start node.
	Query string `json:"query"`
	// Files (optional) are materialized into sys.files.
	Files []string `json:"files,omitempty"`
}

// Workflow run statuses (persisted in workflow_runs.status).
const (
	WorkflowRunStatusPending   = "pending"
	WorkflowRunStatusRunning   = "running"
	WorkflowRunStatusSucceeded = "succeeded"
	WorkflowRunStatusFailed    = "failed"
	WorkflowRunStatusCancelled = "cancelled"
)

type WorkflowRun struct {
	// Unique identifier of the run (UUID, generated in Go)
	ID string `yaml:"id" json:"id" gorm:"type:varchar(36);primaryKey"`
	// Tenant ID (data-isolation scope)
	TenantID uint64 `yaml:"tenant_id" json:"tenant_id" gorm:"not null;index:idx_workflow_runs_tenant_workflow"`
	// Owning workflow ID (paired with TenantID in the composite index)
	WorkflowID string `yaml:"workflow_id" json:"workflow_id" gorm:"type:varchar(36);not null;index:idx_workflow_runs_tenant_workflow"`
	// Run status: pending | running | succeeded | failed | cancelled
	Status string `yaml:"status" json:"status" gorm:"type:varchar(50);not null;default:'pending'"`
	// Run input document (opaque JSON)
	Input JSON `yaml:"input" json:"input" gorm:"type:jsonb"`
	// Run output document (opaque JSON)
	Output JSON `yaml:"output" json:"output" gorm:"type:jsonb"`
	// Terminal error message when status=failed
	Error string `yaml:"error" json:"error" gorm:"type:text"`

	CreatedAt time.Time      `yaml:"created_at" json:"created_at"`
	UpdatedAt time.Time      `yaml:"updated_at" json:"updated_at"`
	DeletedAt gorm.DeletedAt `yaml:"deleted_at" json:"deleted_at"`
}
