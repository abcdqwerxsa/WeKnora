package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// builtinSaveRepo is a minimal CustomAgentRepository stub that records the
// agent passed to UpdateAgent / CreateAgent so the assertions below can
// verify what actually landed on (the in-memory representation of) the row.
type builtinSaveRepo struct {
	interfaces.CustomAgentRepository

	existing      *types.CustomAgent // pre-seeded tenant row (nil means none)
	updatedAgent  *types.CustomAgent
	createdAgent  *types.CustomAgent
	hardDeleteHit bool
}

func (r *builtinSaveRepo) GetAgentByID(_ context.Context, id string, _ uint64) (*types.CustomAgent, error) {
	if r.existing != nil && r.existing.ID == id {
		return r.existing, nil
	}
	return nil, repository.ErrCustomAgentNotFound
}

func (r *builtinSaveRepo) UpdateAgent(_ context.Context, agent *types.CustomAgent) error {
	r.updatedAgent = agent
	return nil
}

func (r *builtinSaveRepo) CreateAgent(_ context.Context, agent *types.CustomAgent) error {
	r.createdAgent = agent
	return nil
}

func (r *builtinSaveRepo) HardDeleteAgent(_ context.Context, _ string, _ uint64) error {
	r.hardDeleteHit = true
	return nil
}

// ListAgentsByTenantID is required by the interface; not exercised here
// but kept so the stub satisfies the full CustomAgentRepository contract.
func (r *builtinSaveRepo) ListAgentsByTenantID(_ context.Context, _ uint64) ([]*types.CustomAgent, error) {
	return nil, errors.New("not implemented")
}

// newBuiltinSaveService wires a customAgentService around the stub repo
// and registers a synthetic built-in registry entry with a known default
// system_prompt, mirroring what cmd/server's bootstrap loads at startup.
func newBuiltinSaveService(repo *builtinSaveRepo) *customAgentService {
	const defaultPrompt = "YAML default prompt"
	restore := types.OverrideBuiltinAgentEntriesForTest(map[string]*types.BuiltinAgentEntry{
		types.BuiltinQuickAnswerID: {
			ID:     types.BuiltinQuickAnswerID,
			Avatar: "qa",
			I18n:   map[string]types.BuiltinAgentI18n{"default": {Name: "QA", Description: "qa"}},
			Config: types.CustomAgentConfig{
				SystemPrompt: defaultPrompt,
				AgentMode:    types.AgentModeQuickAnswer,
			},
		},
	})
	// keep the restore reachable to the test's Cleanup via t.Cleanup;
	// the closure here is just so the registry is populated immediately.
	_ = restore
	return &customAgentService{repo: repo}
}

// TestUpdateBuiltinAgent_RerankModelPersistsWhenSystemPromptMatchesDefault
// is the regression test for the "save shows success but rerank_model_id
// never lands in DB" bug. When the operator edits a built-in agent and
// only changes the rerank model (leaving system_prompt equal to the YAML
// default), the save MUST persist the rerank_model_id, NOT short-circuit
// and drop the whole change set.
func TestUpdateBuiltinAgent_RerankModelPersistsWhenSystemPromptMatchesDefault(t *testing.T) {
	t.Cleanup(func() {
		restore := types.OverrideBuiltinAgentEntriesForTest(nil)
		restore()
	})
	const defaultPrompt = "YAML default prompt"
	types.OverrideBuiltinAgentEntriesForTest(map[string]*types.BuiltinAgentEntry{
		types.BuiltinQuickAnswerID: {
			ID:     types.BuiltinQuickAnswerID,
			Avatar: "qa",
			I18n:   map[string]types.BuiltinAgentI18n{"default": {Name: "QA", Description: "qa"}},
			Config: types.CustomAgentConfig{
				SystemPrompt: defaultPrompt,
				AgentMode:    types.AgentModeQuickAnswer,
			},
		},
	})

	repo := &builtinSaveRepo{} // no existing row yet
	svc := &customAgentService{repo: repo}

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(42))
	updated := &types.CustomAgent{
		ID:          types.BuiltinQuickAnswerID,
		Name:        "QA",
		Description: "qa",
		IsBuiltin:   true,
		Config: types.CustomAgentConfig{
			SystemPrompt:   defaultPrompt, // matches YAML default → used to short-circuit
			AgentMode:      types.AgentModeQuickAnswer,
			RerankModelID:  "rerank-model-xyz",
			RerankTopK:     7,
			RerankThreshold: 0.42,
			ModelID:        "chat-model-abc",
		},
	}

	got, err := svc.UpdateAgent(ctx, updated)
	require.NoError(t, err)

	// Save must have happened via CreateAgent (no existing row).
	assert.NotNil(t, repo.createdAgent, "CreateAgent should have been called for the first save")
	assert.Nil(t, repo.updatedAgent, "UpdateAgent must not be called when there's no existing row")
	assert.False(t, repo.hardDeleteHit, "HardDeleteAgent must not run — that erases prior customisations")

	// system_prompt was the YAML default, so it must be stripped from the
	// persisted row. The other field changes (the whole point of the fix)
	// must round-trip.
	assert.Equal(t, "", repo.createdAgent.Config.SystemPrompt,
		"system_prompt matching YAML default must be stripped, not frozen to DB")
	assert.Equal(t, "rerank-model-xyz", repo.createdAgent.Config.RerankModelID,
		"rerank_model_id must persist on first save")
	assert.Equal(t, "chat-model-abc", repo.createdAgent.Config.ModelID,
		"model_id must persist on first save")
	assert.Equal(t, 7, repo.createdAgent.Config.RerankTopK)
	assert.InDelta(t, 0.42, repo.createdAgent.Config.RerankThreshold, 1e-9)

	// Returned agent must have the YAML default system_prompt re-applied
	// (overlay), so the editor doesn't show an empty box.
	assert.Equal(t, defaultPrompt, got.Config.SystemPrompt,
		"read path must overlay YAML default back into returned agent")
}

// TestUpdateBuiltinAgent_PreservesPriorCustomizationsOnDefaultPromptSave
// guards the second foot-gun: when an existing tenant row already holds
// customisations (e.g. rerank_model_id from a previous save) and the
// operator's next save re-submits a system_prompt equal to the YAML
// default, the customisations must NOT be wiped by HardDeleteAgent.
func TestUpdateBuiltinAgent_PreservesPriorCustomizationsOnDefaultPromptSave(t *testing.T) {
	t.Cleanup(func() {
		restore := types.OverrideBuiltinAgentEntriesForTest(nil)
		restore()
	})
	const defaultPrompt = "YAML default prompt"
	types.OverrideBuiltinAgentEntriesForTest(map[string]*types.BuiltinAgentEntry{
		types.BuiltinQuickAnswerID: {
			ID:     types.BuiltinQuickAnswerID,
			Avatar: "qa",
			I18n:   map[string]types.BuiltinAgentI18n{"default": {Name: "QA", Description: "qa"}},
			Config: types.CustomAgentConfig{
				SystemPrompt: defaultPrompt,
				AgentMode:    types.AgentModeQuickAnswer,
			},
		},
	})

	existing := &types.CustomAgent{
		ID:       types.BuiltinQuickAnswerID,
		TenantID: 42,
		Config: types.CustomAgentConfig{
			SystemPrompt:  "user-customised prompt", // operator changed it last time
			RerankModelID: "rerank-from-last-save",
			ModelID:       "chat-from-last-save",
			AgentMode:     types.AgentModeQuickAnswer,
		},
	}
	repo := &builtinSaveRepo{existing: existing}
	svc := &customAgentService{repo: repo}

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(42))
	updated := &types.CustomAgent{
		ID:       types.BuiltinQuickAnswerID,
		IsBuiltin: true,
		Config: types.CustomAgentConfig{
			SystemPrompt:   defaultPrompt, // reverted to YAML default → used to trigger HardDelete
			AgentMode:      types.AgentModeQuickAnswer,
			RerankModelID:  "rerank-from-this-save", // new value the operator just picked
			RerankTopK:     9,
			ModelID:        "chat-from-this-save",
		},
	}

	got, err := svc.UpdateAgent(ctx, updated)
	require.NoError(t, err)

	assert.False(t, repo.hardDeleteHit,
		"HardDeleteAgent must NOT fire — it would erase the row that holds prior customisations")
	assert.NotNil(t, repo.updatedAgent, "UpdateAgent should have been called on the existing row")

	// The pre-existing customisations from earlier saves were carried in
	// by `existing`, and we just overlaid the new submission on top. The
	// new rerank_model_id must be on the row.
	assert.Equal(t, "rerank-from-this-save", repo.updatedAgent.Config.RerankModelID)
	assert.Equal(t, "chat-from-this-save", repo.updatedAgent.Config.ModelID)
	assert.Equal(t, 9, repo.updatedAgent.Config.RerankTopK)

	// system_prompt matches the YAML default, so it must be stripped
	// (not persisted verbatim, not the previous user's customised value).
	assert.Equal(t, "", repo.updatedAgent.Config.SystemPrompt,
		"matching-YAML-default system_prompt must be stripped, not retained as user customisation")

	// Returned agent must overlay the YAML default so the editor shows
	// the current YAML prompt, not the stripped empty value.
	assert.Equal(t, defaultPrompt, got.Config.SystemPrompt)
}
