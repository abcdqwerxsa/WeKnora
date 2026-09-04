package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// workflowsTestDDL mirrors migrations/sqlite/000013_workflows.up.sql. We
// inline the DDL (same approach as knowledgeBasesTestDDL) because running
// the real migration chain in unit tests would drag in unrelated tables.
const workflowsTestDDL = `
CREATE TABLE IF NOT EXISTS workflows (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    creator_id VARCHAR(36) NOT NULL DEFAULT '',
    name VARCHAR(255) NOT NULL,
    description TEXT,
    dsl TEXT NOT NULL DEFAULT '{}',
    status VARCHAR(50) NOT NULL DEFAULT 'draft',
    version INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE TABLE IF NOT EXISTS workflow_runs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    workflow_id VARCHAR(36) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    input TEXT,
    output TEXT,
    error TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
`

func setupWorkflowTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(workflowsTestDDL).Error)
	return db
}

func newTestWorkflow(id string, tenantID uint64, creatorID string) *types.Workflow {
	return &types.Workflow{
		ID:        id,
		TenantID:  tenantID,
		CreatorID: creatorID,
		Name:      "wf-" + id,
		DSL:       types.JSON(`{"version":1,"components":{"start":{"obj":{"component_name":"Start","params":{}},"upstream":[]}}}`),
		Status:    types.WorkflowStatusDraft,
		Version:   1,
	}
}

func TestWorkflowRepository_CRUDLifecycle(t *testing.T) {
	db := setupWorkflowTestDB(t)
	repo := NewWorkflowRepository(db)
	ctx := context.Background()

	wf := newTestWorkflow("wf-1", 10001, "user-a")
	require.NoError(t, repo.CreateWorkflow(ctx, wf))

	// Read back within the same tenant.
	got, err := repo.GetWorkflowByIDAndTenant(ctx, "wf-1", 10001)
	require.NoError(t, err)
	assert.Equal(t, "wf-1", got.ID)
	assert.Equal(t, uint64(10001), got.TenantID)
	assert.Equal(t, "user-a", got.CreatorID)
	assert.Equal(t, `{"version":1,"components":{"start":{"obj":{"component_name":"Start","params":{}},"upstream":[]}}}`, string(got.DSL))

	// Update mutates selected columns.
	got.Name = "renamed"
	got.Status = types.WorkflowStatusPublished
	got.Version = 2
	require.NoError(t, repo.UpdateWorkflow(ctx, got))
	updated, err := repo.GetWorkflowByIDAndTenant(ctx, "wf-1", 10001)
	require.NoError(t, err)
	assert.Equal(t, "renamed", updated.Name)
	assert.Equal(t, types.WorkflowStatusPublished, updated.Status)
	assert.Equal(t, 2, updated.Version)

	// List with pagination.
	wf2 := newTestWorkflow("wf-2", 10001, "user-a")
	require.NoError(t, repo.CreateWorkflow(ctx, wf2))
	list, total, err := repo.ListWorkflowsByTenantID(ctx, 10001, 0, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, list, 2)

	// Soft delete removes it from reads.
	require.NoError(t, repo.DeleteWorkflow(ctx, "wf-1", 10001))
	_, err = repo.GetWorkflowByIDAndTenant(ctx, "wf-1", 10001)
	assert.ErrorIs(t, err, ErrWorkflowNotFound)
}

func TestWorkflowRepository_TenantIsolation(t *testing.T) {
	db := setupWorkflowTestDB(t)
	repo := NewWorkflowRepository(db)
	ctx := context.Background()

	// Distinct rows per tenant (ids are UUIDs in production; the PK is a
	// single column on id per the migration spec, unlike custom_agents'
	// composite key — so the same id cannot exist twice).
	require.NoError(t, repo.CreateWorkflow(ctx, newTestWorkflow("wf-a", 10001, "user-a")))
	require.NoError(t, repo.CreateWorkflow(ctx, newTestWorkflow("wf-b", 10002, "user-b")))

	// Each tenant only sees its own row.
	gotA, err := repo.GetWorkflowByIDAndTenant(ctx, "wf-a", 10001)
	require.NoError(t, err)
	assert.Equal(t, "user-a", gotA.CreatorID)
	gotB, err := repo.GetWorkflowByIDAndTenant(ctx, "wf-b", 10002)
	require.NoError(t, err)
	assert.Equal(t, "user-b", gotB.CreatorID)

	// A tenant cannot read the other tenant's rows by id.
	_, err = repo.GetWorkflowByIDAndTenant(ctx, "wf-b", 10001)
	assert.ErrorIs(t, err, ErrWorkflowNotFound)
	_, err = repo.GetWorkflowByIDAndTenant(ctx, "wf-a", 10002)
	assert.ErrorIs(t, err, ErrWorkflowNotFound)

	// List stays scoped per tenant.
	_, totalA, err := repo.ListWorkflowsByTenantID(ctx, 10001, 0, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), totalA)
	_, totalB, err := repo.ListWorkflowsByTenantID(ctx, 10002, 0, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), totalB)

	// Delete is scoped: tenant A cannot delete tenant B's row.
	err = repo.DeleteWorkflow(ctx, "wf-b", 10001)
	assert.ErrorIs(t, err, ErrWorkflowNotFound)
	_, err = repo.GetWorkflowByIDAndTenant(ctx, "wf-b", 10002)
	require.NoError(t, err, "tenant B row must survive tenant A's delete attempt")
}

func TestWorkflowRepository_Runs(t *testing.T) {
	db := setupWorkflowTestDB(t)
	repo := NewWorkflowRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.CreateWorkflow(ctx, newTestWorkflow("wf-1", 10001, "user-a")))
	run := &types.WorkflowRun{
		ID:         "run-1",
		TenantID:   10001,
		WorkflowID: "wf-1",
		Status:     "succeeded",
		Output:     types.JSON(`{"answer":"ok"}`),
	}
	require.NoError(t, repo.CreateWorkflowRun(ctx, run))

	runs, err := repo.ListWorkflowRunsByTenantAndWorkflow(ctx, 10001, "wf-1")
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, "succeeded", runs[0].Status)
	assert.Equal(t, `{"answer":"ok"}`, string(runs[0].Output))

	// Run history is tenant-scoped: the other tenant sees nothing.
	otherRuns, err := repo.ListWorkflowRunsByTenantAndWorkflow(ctx, 10002, "wf-1")
	require.NoError(t, err)
	assert.Empty(t, otherRuns)
}
