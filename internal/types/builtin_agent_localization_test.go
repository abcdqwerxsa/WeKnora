package types

import (
	"context"
	"testing"
)

func TestApplyBuiltinAgentLocalizationOverlaysYAMLLocale(t *testing.T) {
	restore := OverrideBuiltinAgentEntriesForTest(map[string]*BuiltinAgentEntry{
		BuiltinQuickAnswerID: {
			ID:     BuiltinQuickAnswerID,
			Avatar: "quick.png",
			I18n: map[string]BuiltinAgentI18n{
				"default": {Name: "快速问答", Description: "中文 RAG"},
				"en-US":   {Name: "Quick Answer", Description: "Knowledge base RAG Q&A"},
				"zh-CN":   {Name: "快速问答", Description: "中文 RAG"},
			},
		},
	})
	t.Cleanup(restore)

	agent := &CustomAgent{
		ID:          BuiltinQuickAnswerID,
		Name:        "快速问答",
		Description: "中文 RAG",
		Avatar:      "",
		IsBuiltin:   true,
		TenantID:    1,
	}
	ctx := context.WithValue(context.Background(), LanguageContextKey, "en-US")
	ApplyBuiltinAgentLocalization(ctx, agent)

	if agent.Name != "Quick Answer" {
		t.Fatalf("Name = %q, want Quick Answer", agent.Name)
	}
	if agent.Description != "Knowledge base RAG Q&A" {
		t.Fatalf("Description = %q, want English copy", agent.Description)
	}
	if agent.Avatar != "quick.png" {
		t.Fatalf("Avatar = %q, want quick.png", agent.Avatar)
	}
}

func TestApplyBuiltinAgentLocalizationNilSafe(t *testing.T) {
	ApplyBuiltinAgentLocalization(context.Background(), nil)
}

func TestApplyBuiltinAgentLocalizationLeavesUnknownAgents(t *testing.T) {
	agent := &CustomAgent{ID: "custom-agent-1", Name: "Mine", Description: "keep me"}
	ctx := context.WithValue(context.Background(), LanguageContextKey, "en-US")
	ApplyBuiltinAgentLocalization(ctx, agent)
	if agent.Name != "Mine" || agent.Description != "keep me" {
		t.Fatalf("custom agent was rewritten: %+v", agent)
	}
}

// TestApplyBuiltinAgentLocalizationOverlaysEmptySystemPrompt covers the
// "save stripped system_prompt because it matched YAML default" path. The
// read path must fill it back from the YAML registry so operators don't
// see an empty prompt and re-type the YAML default (which would freeze
// the agent to today's snapshot).
func TestApplyBuiltinAgentLocalizationOverlaysEmptySystemPrompt(t *testing.T) {
	const yamlPrompt = "default prompt from YAML"
	restore := OverrideBuiltinAgentEntriesForTest(map[string]*BuiltinAgentEntry{
		BuiltinQuickAnswerID: {
			ID:        BuiltinQuickAnswerID,
			Avatar:    "qa.png",
			I18n:      map[string]BuiltinAgentI18n{"default": {Name: "QA"}},
			Config:    CustomAgentConfig{SystemPrompt: yamlPrompt},
		},
	})
	t.Cleanup(restore)

	agent := &CustomAgent{
		ID:       BuiltinQuickAnswerID,
		Name:     "QA",
		IsBuiltin: true,
		TenantID: 1,
		Config:   CustomAgentConfig{SystemPrompt: ""},
	}
	ApplyBuiltinAgentLocalization(context.Background(), agent)
	if agent.Config.SystemPrompt != yamlPrompt {
		t.Fatalf("SystemPrompt = %q, want %q", agent.Config.SystemPrompt, yamlPrompt)
	}
}

// TestApplyBuiltinAgentLocalizationKeepsCustomSystemPrompt makes sure a
// tenant-supplied system_prompt (non-empty) is never silently replaced
// by the YAML default — the overlay must only fill blanks.
func TestApplyBuiltinAgentLocalizationKeepsCustomSystemPrompt(t *testing.T) {
	const (
		yamlPrompt = "YAML default"
		userPrompt = "operator's own prompt"
	)
	restore := OverrideBuiltinAgentEntriesForTest(map[string]*BuiltinAgentEntry{
		BuiltinQuickAnswerID: {
			ID:     BuiltinQuickAnswerID,
			I18n:   map[string]BuiltinAgentI18n{"default": {Name: "QA"}},
			Config: CustomAgentConfig{SystemPrompt: yamlPrompt},
		},
	})
	t.Cleanup(restore)

	agent := &CustomAgent{
		ID:       BuiltinQuickAnswerID,
		IsBuiltin: true,
		TenantID: 1,
		Config:   CustomAgentConfig{SystemPrompt: userPrompt},
	}
	ApplyBuiltinAgentLocalization(context.Background(), agent)
	if agent.Config.SystemPrompt != userPrompt {
		t.Fatalf("SystemPrompt = %q, want custom %q preserved", agent.Config.SystemPrompt, userPrompt)
	}
}

// TestApplyBuiltinAgentLocalizationEmptyWithPromptIDKeepsEmpty: when the
// tenant row has an explicit system_prompt_id (even with empty
// system_prompt), treat it as deliberate customisation and DON'T overlay
// the YAML default. Without this guard a row seeded with only
// system_prompt_id would suddenly get a hardcoded prompt stuffed in.
func TestApplyBuiltinAgentLocalizationEmptyWithPromptIDKeepsEmpty(t *testing.T) {
	const yamlPrompt = "YAML default"
	restore := OverrideBuiltinAgentEntriesForTest(map[string]*BuiltinAgentEntry{
		BuiltinQuickAnswerID: {
			ID:     BuiltinQuickAnswerID,
			I18n:   map[string]BuiltinAgentI18n{"default": {Name: "QA"}},
			Config: CustomAgentConfig{SystemPrompt: yamlPrompt},
		},
	})
	t.Cleanup(restore)

	agent := &CustomAgent{
		ID:       BuiltinQuickAnswerID,
		IsBuiltin: true,
		TenantID: 1,
		Config:   CustomAgentConfig{SystemPromptID: "custom-template"},
	}
	ApplyBuiltinAgentLocalization(context.Background(), agent)
	if agent.Config.SystemPrompt != "" {
		t.Fatalf("SystemPrompt = %q, want empty (SystemPromptID set, no overlay)", agent.Config.SystemPrompt)
	}
}
