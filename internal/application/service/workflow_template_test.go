package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	wfengine "github.com/Tencent/WeKnora/internal/agent/workflow"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tplKBStub answers GetKnowledgeBaseByID: "kb-ok" exists in the tenant,
// everything else is not-found.
type tplKBStub struct{ wfStubKBSvc }

func (s *tplKBStub) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	switch id {
	case "kb-ok":
		return &types.KnowledgeBase{ID: id, TenantID: 10001, Name: "ok"}, nil
	case "kb-foreign":
		// Exists, but belongs to another tenant: GetKnowledgeBaseByID is
		// deliberately not tenant-scoped, so the service must enforce it.
		return &types.KnowledgeBase{ID: id, TenantID: 99999, Name: "foreign"}, nil
	}
	return nil, errors.New("kb not found")
}

// writeTemplateDir creates a config dir containing one template file with a
// declared KB placeholder (Start → Retrieval → Answer) and one without.
func writeTemplateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sub := filepath.Join(dir, "workflow_templates")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	withKB := map[string]any{
		"id":              "tpl-test-kb",
		"category":        "test",
		"kb_placeholders": []string{"KB_TEST_BASE"},
		"i18n": map[string]any{
			"default": map[string]any{"name": "KB Template", "description": "retrieval + answer"},
			"zh-CN":   map[string]any{"name": "知识库模板", "description": "检索并回答"},
		},
		"dsl": map[string]any{
			"version": 1,
			"components": map[string]any{
				"start-1": map[string]any{
					"obj":        map[string]any{"component_name": "Start", "params": map[string]any{}},
					"downstream": []string{"retrieval-1"},
				},
				"retrieval-1": map[string]any{
					"obj": map[string]any{"component_name": "Retrieval", "params": map[string]any{
						"query":  "{sys.query}",
						"kb_ids": []any{"KB_TEST_BASE"},
						"top_k":  5,
					}},
					"upstream":   []string{"start-1"},
					"downstream": []string{"answer-1"},
				},
				"answer-1": map[string]any{
					"obj":      map[string]any{"component_name": "Answer", "params": map[string]any{"template": "{retrieval-1@chunks}"}},
					"upstream": []string{"retrieval-1"},
				},
			},
		},
	}
	raw, err := json.Marshal(withKB)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sub, "tpl-test-kb.json"), raw, 0o644))
	noKB := map[string]any{
		"id":       "tpl-test-nokb",
		"category": "test",
		"i18n": map[string]any{
			"default": map[string]any{"name": "Plain Template", "description": "llm + answer"},
		},
		"dsl": map[string]any{
			"version": 1,
			"components": map[string]any{
				"start-1": map[string]any{
					"obj":        map[string]any{"component_name": "Start", "params": map[string]any{}},
					"downstream": []string{"llm-1"},
				},
				"llm-1": map[string]any{
					"obj":        map[string]any{"component_name": "LLM", "params": map[string]any{"prompt": "{sys.query}"}},
					"upstream":   []string{"start-1"},
					"downstream": []string{"answer-1"},
				},
				"answer-1": map[string]any{
					"obj":      map[string]any{"component_name": "Answer", "params": map[string]any{"template": "{llm-1@content}"}},
					"upstream": []string{"llm-1"},
				},
			},
		},
	}
	raw, err = json.Marshal(noKB)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sub, "tpl-test-nokb.json"), raw, 0o644))
	require.NoError(t, wfengine.LoadWorkflowTemplates(dir))
	return dir
}

func templateTestCtx() context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10001))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "user-a")
	return context.WithValue(ctx, types.LanguageContextKey, "zh-CN")
}

func TestInstantiateWorkflowTemplateMissingBinding(t *testing.T) {
	writeTemplateDir(t)
	svc := NewWorkflowService(&stubWorkflowRepo{}, nil, &tplKBStub{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	_, err := svc.InstantiateWorkflowTemplate(templateTestCtx(), "tpl-test-kb", &types.InstantiateWorkflowTemplateRequest{
		KBBindings: map[string]string{"KB_OTHER": "kb-ok"},
	})
	var missing *MissingKBBindingsError
	require.ErrorAs(t, err, &missing)
	assert.Equal(t, []string{"KB_TEST_BASE"}, missing.Missing)
	assert.ErrorIs(t, err, ErrWorkflowTemplateMissingKB)
}

func TestInstantiateWorkflowTemplateInvalidKB(t *testing.T) {
	writeTemplateDir(t)
	svc := NewWorkflowService(&stubWorkflowRepo{}, nil, &tplKBStub{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	_, err := svc.InstantiateWorkflowTemplate(templateTestCtx(), "tpl-test-kb", &types.InstantiateWorkflowTemplateRequest{
		KBBindings: map[string]string{"KB_TEST_BASE": "kb-missing"},
	})
	assert.ErrorIs(t, err, ErrWorkflowTemplateInvalidKB)
}

func TestInstantiateWorkflowTemplateForeignTenantKB(t *testing.T) {
	writeTemplateDir(t)
	svc := NewWorkflowService(&stubWorkflowRepo{}, nil, &tplKBStub{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	_, err := svc.InstantiateWorkflowTemplate(templateTestCtx(), "tpl-test-kb", &types.InstantiateWorkflowTemplateRequest{
		KBBindings: map[string]string{"KB_TEST_BASE": "kb-foreign"},
	})
	assert.ErrorIs(t, err, ErrWorkflowTemplateInvalidKB)
}

func TestInstantiateWorkflowTemplateNotFound(t *testing.T) {
	writeTemplateDir(t)
	svc := NewWorkflowService(&stubWorkflowRepo{}, nil, &tplKBStub{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	_, err := svc.InstantiateWorkflowTemplate(templateTestCtx(), "tpl-nope", nil)
	assert.ErrorIs(t, err, ErrWorkflowTemplateNotFound)
}

func TestInstantiateWorkflowTemplatePublishesBoundCopy(t *testing.T) {
	writeTemplateDir(t)
	repo := &stubWorkflowRepo{}
	svc := NewWorkflowService(repo, nil, &tplKBStub{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	created, err := svc.InstantiateWorkflowTemplate(templateTestCtx(), "tpl-test-kb", &types.InstantiateWorkflowTemplateRequest{
		KBBindings: map[string]string{"KB_TEST_BASE": "kb-ok"},
	})
	require.NoError(t, err)

	// Published immediately, owned by the caller's tenant, localized name.
	assert.Equal(t, types.WorkflowStatusPublished, created.Status)
	assert.Equal(t, uint64(10001), created.TenantID)
	assert.Equal(t, "知识库模板", created.Name)
	assert.Equal(t, "user-a", created.CreatorID)
	require.NotNil(t, repo.saved)
	assert.Equal(t, created.ID, repo.saved.ID)

	// The published snapshot carries the bound kb id, never the placeholder.
	var dsl struct {
		Components map[string]struct {
			Obj struct {
				Params struct {
					KBIDs []string `json:"kb_ids"`
				} `json:"params"`
			} `json:"obj"`
		} `json:"components"`
	}
	require.NoError(t, json.Unmarshal(created.PublishedDSL, &dsl))
	assert.Equal(t, []string{"kb-ok"}, dsl.Components["retrieval-1"].Obj.Params.KBIDs)
}

func TestInstantiateWorkflowTemplateWithoutPlaceholders(t *testing.T) {
	writeTemplateDir(t)
	repo := &stubWorkflowRepo{}
	svc := NewWorkflowService(repo, nil, &tplKBStub{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	created, err := svc.InstantiateWorkflowTemplate(templateTestCtx(), "tpl-test-nokb", &types.InstantiateWorkflowTemplateRequest{
		Name: "自定义名称",
	})
	require.NoError(t, err)
	assert.Equal(t, types.WorkflowStatusPublished, created.Status)
	assert.Equal(t, "自定义名称", created.Name)
}
