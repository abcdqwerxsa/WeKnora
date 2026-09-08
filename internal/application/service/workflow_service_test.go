package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validDSL = `{
  "version": 1,
  "graph": {"nodes": [{"id": "start", "type": "Start"}], "edges": []},
  "components": {
    "start": {"obj": {"component_name": "Start", "params": {}}, "upstream": [], "downstream": ["llm"]},
    "llm": {"obj": {"component_name": "LLM", "params": {"prompt": "{start@query}"}}, "upstream": ["start"], "downstream": []}
  }
}`

func TestValidateWorkflowDSL_Valid(t *testing.T) {
	out, err := ValidateWorkflowDSL(types.JSON(validDSL))
	require.NoError(t, err)
	// Pass-through: bytes are returned verbatim, not rewritten.
	assert.Equal(t, validDSL, string(out))
}

func TestValidateWorkflowDSL_MissingEntry(t *testing.T) {
	// Both components have upstream — no entry point.
	dsl := `{"version":1,"components":{
		"a":{"obj":{"component_name":"LLM"},"upstream":["b"]},
		"b":{"obj":{"component_name":"LLM"},"upstream":["a"]}}}`
	_, err := ValidateWorkflowDSL(types.JSON(dsl))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWorkflowInvalidDSL)
	assert.Contains(t, err.Error(), "entry component")
}

func TestValidateWorkflowDSL_BadJSON(t *testing.T) {
	_, err := ValidateWorkflowDSL(types.JSON(`{"version":1,`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWorkflowInvalidDSL)
}

func TestValidateWorkflowDSL_Empty(t *testing.T) {
	_, err := ValidateWorkflowDSL(nil)
	assert.ErrorIs(t, err, ErrWorkflowDSLRequired)
}

func TestValidateWorkflowDSL_WrongVersion(t *testing.T) {
	_, err := ValidateWorkflowDSL(types.JSON(`{"version":2,"components":{"s":{"obj":{"component_name":"Start"},"upstream":[]}}}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWorkflowInvalidDSL)
	assert.Contains(t, err.Error(), "version")
}

func TestValidateWorkflowDSL_EmptyComponentName(t *testing.T) {
	_, err := ValidateWorkflowDSL(types.JSON(`{"version":1,"components":{"s":{"obj":{"component_name":""},"upstream":[]}}}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWorkflowInvalidDSL)
	assert.Contains(t, err.Error(), "component_name")
}

func TestValidateWorkflowDSL_NoComponents(t *testing.T) {
	_, err := ValidateWorkflowDSL(types.JSON(`{"version":1,"components":{}}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWorkflowInvalidDSL)
	assert.Contains(t, err.Error(), "at least one component")
}

func TestValidateWorkflowFieldLimits(t *testing.T) {
	validDSLJSON := types.JSON(validDSL)

	err := validateWorkflowFields("", "", "", validDSLJSON)
	assert.ErrorIs(t, err, ErrWorkflowNameRequired)

	err = validateWorkflowFields(strings.Repeat("名", 256), "", "", validDSLJSON)
	assert.ErrorIs(t, err, ErrWorkflowNameTooLong)

	err = validateWorkflowFields("ok", strings.Repeat("d", 2001), "", validDSLJSON)
	assert.ErrorIs(t, err, ErrWorkflowDescriptionTooLong)

	err = validateWorkflowFields("ok", "", "bogus", validDSLJSON)
	assert.ErrorIs(t, err, ErrWorkflowInvalidStatus)

	err = validateWorkflowFields("ok", "", types.WorkflowStatusPublished, validDSLJSON)
	assert.NoError(t, err)

	// nil DSL is allowed here (update keeps the stored document).
	err = validateWorkflowFields("ok", "", "", nil)
	assert.NoError(t, err)
}

// stubWorkflowRepo is a minimal in-memory WorkflowRepository used to assert
// that the service derives tenant/creator from the context, never the payload.
type stubWorkflowRepo struct {
	saved *types.Workflow
}

func (r *stubWorkflowRepo) CreateWorkflow(_ context.Context, wf *types.Workflow) error {
	r.saved = wf
	return nil
}
func (r *stubWorkflowRepo) GetWorkflowByIDAndTenant(_ context.Context, id string, tenantID uint64) (*types.Workflow, error) {
	if r.saved != nil && r.saved.ID == id && r.saved.TenantID == tenantID {
		return r.saved, nil
	}
	return nil, errWorkflowNotFoundStub
}
func (r *stubWorkflowRepo) ListWorkflowsByTenantID(_ context.Context, _ uint64, _, _ int) ([]*types.Workflow, int64, error) {
	return nil, 0, nil
}
func (r *stubWorkflowRepo) UpdateWorkflow(_ context.Context, wf *types.Workflow) error {
	r.saved = wf
	return nil
}
func (r *stubWorkflowRepo) DeleteWorkflow(_ context.Context, _ string, _ uint64) error { return nil }
func (r *stubWorkflowRepo) CreateWorkflowRun(_ context.Context, _ *types.WorkflowRun) error {
	return nil
}
func (r *stubWorkflowRepo) ListWorkflowRunsByTenantAndWorkflow(_ context.Context, _ uint64, _ string) ([]*types.WorkflowRun, error) {
	return nil, nil
}
func (r *stubWorkflowRepo) UpdateWorkflowRun(_ context.Context, _ *types.WorkflowRun) error {
	return nil
}
func (r *stubWorkflowRepo) MarkWorkflowRunCancelled(_ context.Context, _ string, _ uint64) error {
	return nil
}
func (r *stubWorkflowRepo) GetWorkflowRunByIDAndTenant(_ context.Context, _ string, _ uint64) (*types.WorkflowRun, error) {
	return nil, errWorkflowNotFoundStub
}

var errWorkflowNotFoundStub = errors.New("workflow not found")

func TestWorkflowService_CreateDerivesTenantAndCreatorFromContext(t *testing.T) {
	repo := &stubWorkflowRepo{}
	svc := newTestWFService(repo, nil, nil)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10001))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "user-a")

	// Payload tries to forge tenant and creator — both must be overwritten.
	created, err := svc.CreateWorkflow(ctx, &types.Workflow{
		Name:      "wf",
		TenantID:  99999,
		CreatorID: "attacker",
		DSL:       types.JSON(validDSL),
	})
	require.NoError(t, err)
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, uint64(10001), created.TenantID, "tenant must come from ctx")
	assert.Equal(t, "user-a", created.CreatorID, "creator must come from ctx")
	assert.Equal(t, types.WorkflowStatusDraft, created.Status)
	assert.Equal(t, 1, created.Version)
}

func TestWorkflowService_CreateWithoutTenantFails(t *testing.T) {
	svc := newTestWFService(&stubWorkflowRepo{}, nil, nil)
	_, err := svc.CreateWorkflow(context.Background(), &types.Workflow{Name: "wf", DSL: types.JSON(validDSL)})
	assert.ErrorIs(t, err, ErrWorkflowTenantRequired)
}

func TestWorkflowService_UpdateBumpsVersionAndKeepsDSLWhenOmitted(t *testing.T) {
	repo := &stubWorkflowRepo{}
	svc := newTestWFService(repo, nil, nil)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10001))
	created, err := svc.CreateWorkflow(ctx, &types.Workflow{Name: "wf", DSL: types.JSON(validDSL)})
	require.NoError(t, err)

	// Status-only update: empty DSL keeps the stored document.
	updated, err := svc.UpdateWorkflow(ctx, created.ID, &types.UpdateWorkflowRequest{
		Name:   "renamed",
		Status: types.WorkflowStatusPublished,
	})
	require.NoError(t, err)
	assert.Equal(t, "renamed", updated.Name)
	assert.Equal(t, types.WorkflowStatusPublished, updated.Status)
	assert.Equal(t, 2, updated.Version, "version must bump on update")
	assert.Equal(t, validDSL, string(updated.DSL), "empty DSL must keep the stored document")
}

// ---- publish snapshot + run gate (Phase 4) --------------------------------

func publishTestService(t *testing.T, status string, published types.JSON) (interfaces.WorkflowService, *runRepoStub) {
	t.Helper()
	wf := &types.Workflow{ID: "wf-1", TenantID: 10001, CreatorID: "user-9", Name: "wf", DSL: types.JSON(linearDSL), Status: status, PublishedDSL: published, Version: 1}
	repo := newRunRepoStub(wf)
	return newTestWFService(repo, &wfStubModelSvc{reply: "ok"}, &wfStubKBSvc{}), repo
}

func publishCtx(userID string) context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10001))
	return context.WithValue(ctx, types.UserIDContextKey, userID)
}

func TestPublishWorkflowFreezesSnapshot(t *testing.T) {
	svc, _ := publishTestService(t, types.WorkflowStatusDraft, nil)
	wf, err := svc.PublishWorkflow(publishCtx("user-9"), "wf-1")
	require.NoError(t, err)
	assert.Equal(t, types.WorkflowStatusPublished, wf.Status)
	assert.Equal(t, wf.DSL, wf.PublishedDSL, "publish freezes the current DSL")
	assert.Equal(t, 2, wf.Version, "publish bumps the version")
}

func TestPublishWorkflowRejectsBrokenDSL(t *testing.T) {
	wf := &types.Workflow{ID: "wf-1", TenantID: 10001, CreatorID: "u", Name: "wf", DSL: types.JSON(`{"version":1,"components":{}}`), Status: "draft"}
	repo := newRunRepoStub(wf)
	svc := newTestWFService(repo, nil, nil)
	_, err := svc.PublishWorkflow(publishCtx("u"), "wf-1")
	assert.ErrorIs(t, err, ErrWorkflowNotPublishable)
}

func TestRunWorkflowGate_DraftOnlyCreatorOrAdmin(t *testing.T) {
	// Another contributor cannot run a draft.
	svc, _ := publishTestService(t, types.WorkflowStatusDraft, nil)
	_, err := svc.RunWorkflow(publishCtx("user-other"), "wf-1", &types.RunWorkflowRequest{Query: "q"})
	assert.ErrorIs(t, err, ErrWorkflowNotDebuggable)

	// Creator can (debug affordance).
	_, err = svc.RunWorkflow(publishCtx("user-9"), "wf-1", &types.RunWorkflowRequest{Query: "q"})
	assert.NoError(t, err)
}

func TestRunWorkflowGate_PublishedRunsSnapshot(t *testing.T) {
	// Snapshot with a DIFFERENT answer than the draft: the run must prove
	// it executed the snapshot, and any contributor may call it.
	snapshot := `{"version":1,"components":{
		"start": {"obj": {"component_name": "Start", "params": {}}, "upstream": [], "downstream": ["ans"]},
		"ans":   {"obj": {"component_name": "Answer", "params": {"template": "from snapshot"}}, "upstream": ["start"], "downstream": []}
	}}`
	svc, _ := publishTestService(t, types.WorkflowStatusPublished, types.JSON(snapshot))
	run, err := svc.RunWorkflow(publishCtx("user-any"), "wf-1", &types.RunWorkflowRequest{Query: "q"})
	require.NoError(t, err)
	assert.Equal(t, types.WorkflowRunStatusSucceeded, run.Status)
	assert.Contains(t, string(run.Output), "from snapshot")
}

func TestSetWorkflowStatusRejectsPublished(t *testing.T) {
	svc, _ := publishTestService(t, types.WorkflowStatusPublished, types.JSON(linearDSL))
	_, err := svc.SetWorkflowStatus(publishCtx("u"), "wf-1", types.WorkflowStatusPublished)
	assert.ErrorIs(t, err, ErrWorkflowInvalidStatus)
	wf, err := svc.SetWorkflowStatus(publishCtx("u"), "wf-1", types.WorkflowStatusArchived)
	require.NoError(t, err)
	assert.Equal(t, types.WorkflowStatusArchived, wf.Status)
	assert.NotNil(t, wf.PublishedDSL, "unpublish/archive keeps the snapshot for re-publish")
}
