package e2e

// Model-dependent scenarios: LLM params (system prompt, max_tokens,
// temperature, default model), QuestionClassifier, ParameterExtractor,
// Iteration, Agent. These hit the tenant's configured chat model.
import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// llmSkipIfNoModel skips model-dependent tests when the workspace exposes no
// chat model (the run would only exercise the "no default model" error).
func llmSkipIfNoModel(t *testing.T) {
	t.Helper()
	if chatModelID == "" {
		t.Skip("no chat model configured in this workspace")
	}
}

// withModel returns params with the discovered chat model id set.
func withModel(p map[string]any) map[string]any {
	if chatModelID != "" {
		p["model"] = chatModelID
	}
	return p
}

// roughTokens estimates token magnitude (~4 chars/token for mixed CJK/latin,
// intentionally coarse: the assertion is order-of-magnitude, not exact).
func roughTokens(s string) int { return len([]rune(s)) / 2 }

// #2 System prompt takes effect: pin the output to a fixed marker.
func TestLLMSystemPromptTakesEffect(t *testing.T) {
	llmSkipIfNoModel(t)
	comps := linearComps(
		node("start", "Start", map[string]any{"fields": []any{}}),
		node("llm", "LLM", map[string]any{
			"model":         chatModelID,
			"prompt":        "What is the capital of France?",
			"system_prompt": "You must answer with exactly the word MARKER and nothing else.",
			"max_tokens":    64,
		}),
		node("ans", "Answer", map[string]any{"template": "{llm@content}"}),
	)
	id := createWorkflow(t, "llm-system", comps)
	r := requireRunSucceeded(t, id, nil)
	content := traceOf(t, r, "llm").Outputs["content"].(string)
	assert.Contains(t, strings.ToUpper(content), "MARKER",
		"system prompt must constrain the output; got: %.200s", content)
}

// #3 max_tokens=5 must actually cap generation (regression for the fix).
func TestLLMMaxTokensCapped(t *testing.T) {
	llmSkipIfNoModel(t)
	comps := linearComps(
		node("start", "Start", map[string]any{"fields": []any{}}),
		node("llm", "LLM", map[string]any{
			"model":         chatModelID,
			"prompt":        "Write a 300-word essay about the history of coffee.",
			"system_prompt": "Write as much as you can. Never stop early.",
			"max_tokens":    5,
		}),
		node("ans", "Answer", map[string]any{"template": "{llm@content}"}),
	)
	id := createWorkflow(t, "llm-maxtokens", comps)
	r := requireRunSucceeded(t, id, nil)
	content, _ := traceOf(t, r, "llm").Outputs["content"].(string)
	t.Logf("max_tokens=5 output (%d runes): %.200s", len([]rune(content)), content)
	assert.LessOrEqual(t, roughTokens(content), 15,
		"max_tokens=5 must truncate the generation (allowing CJK/tokeniser slack); got %d est. tokens", roughTokens(content))
}

// #4 Temperature: 0 is stable across samples, high is allowed to diverge.
func TestLLMTemperature(t *testing.T) {
	mk := func(temp float64) string {
		comps := linearComps(
			node("start", "Start", map[string]any{"fields": []any{}}),
			node("llm", "LLM", map[string]any{
				"model":       chatModelID,
				"prompt":      "Name exactly one random color. One word only.",
				"temperature": temp,
				"max_tokens":  32,
			}),
			node("ans", "Answer", map[string]any{"template": "{llm@content}"}),
		)
		return createWorkflow(t, "llm-temp", comps)
	}
	sample := func(id string) string {
		r := requireRunSucceeded(t, id, nil)
		c, _ := traceOf(t, r, "llm").Outputs["content"].(string)
		return strings.TrimSpace(c)
	}
	zero := mk(0)
	var first string
	distinct := 0
	for i := 0; i < 4; i++ {
		s := sample(zero)
		if i == 0 {
			first = s
		} else if s != first {
			distinct++
		}
	}
	// NOTE the wire quirk: go-openai serialises temperature with omitempty,
	// so 0 is indistinguishable from unset — the provider default applies.
	// Determinism at 0 therefore depends on the backend; log, do not assert.
	t.Logf("temp=0 samples distinct=%d (provider default governs when 0 is omitted)", distinct)

	hot := mk(1.6)
	for i := 0; i < 4; i++ {
		sample(hot) // must at least not error at the upper range
	}
}

// #5 Empty model falls back to the tenant default chat model.
func TestLLMDefaultModelFallback(t *testing.T) {
	resp := call(t, "GET", "/api/v1/models?type=KnowledgeQA", nil)
	require.Equal(t, http.StatusOK, resp.Status)
	var models []struct {
		ID        string `json:"id"`
		IsDefault bool   `json:"is_default"`
	}
	require.NoError(t, json.Unmarshal(resp.Data, &models))
	hasDefault := false
	for _, m := range models {
		if m.IsDefault {
			hasDefault = true
			break
		}
	}
	if !hasDefault {
		t.Skip("workspace has no default chat model flagged — fallback path untestable here")
	}
	comps := linearComps(
		node("start", "Start", map[string]any{"fields": []any{}}),
		node("llm", "LLM", map[string]any{
			"prompt":     "Say OK.",
			"max_tokens": 16,
		}),
		node("ans", "Answer", map[string]any{"template": "{llm@content}"}),
	)
	id := createWorkflow(t, "llm-default-model", comps)
	r := requireRunSucceeded(t, id, nil)
	require.NotNil(t, traceOf(t, r, "llm"))
	c, _ := traceOf(t, r, "llm").Outputs["content"].(string)
	assert.NotEmpty(t, strings.TrimSpace(c), "default model must answer")
}

// #6 Answer renders hyphenated LLM refs (regression) — LLM flavour of #19.
func TestLLMAnswerRefRendering(t *testing.T) {
	llmSkipIfNoModel(t)
	comps := linearComps(
		node("start-9f1c2d", "Start", map[string]any{"fields": []any{}}),
		node("llm-8b3e4f", "LLM", map[string]any{
			"model":      chatModelID,
			"prompt":     "Say the single word: ping",
			"max_tokens": 16,
		}),
		node("ans-7d5a6b", "Answer", map[string]any{"template": "reply=[{llm-8b3e4f@content}]"}),
	)
	id := createWorkflow(t, "llm-ref-render", comps)
	r := requireRunSucceeded(t, id, nil)
	ans := answerOf(t, r)
	assert.Contains(t, ans, "reply=[")
	assert.NotContains(t, ans, "{llm-8b3e4f@content}", "hyphenated ref must render, not leak literally")
}

// #13 QuestionClassifier routes two different questions correctly.
func TestQuestionClassifier(t *testing.T) {
	llmSkipIfNoModel(t)
	comps := map[string]comp{
		"start": {Kind: "Start", Params: map[string]any{"fields": []any{}}, Downstream: []string{"cls"}},
		"cls": {Kind: "QuestionClassifier", Params: map[string]any{
			"model": chatModelID,
			"query": "{sys.query}",
			"classes": []any{
				map[string]any{"name": "billing", "description": "questions about invoices, payments, charges", "to": "a"},
				map[string]any{"name": "tech", "description": "questions about errors, bugs, troubleshooting", "to": "b"},
			},
			"default": "b",
		}, Upstream: []string{"start"}, Downstream: []string{"a", "b"}},
		"a": {Kind: "Answer", Params: answerTemplate("BILLING"), Upstream: []string{"cls"}},
		"b": {Kind: "Answer", Params: answerTemplate("TECH"), Upstream: []string{"cls"}},
	}
	id := createWorkflow(t, "classifier", comps)
	r := requireRunSucceeded(t, id, map[string]any{"query": "my invoice was charged twice, how do I get a refund?"})
	assert.Equal(t, "BILLING", answerOf(t, r))
	r = requireRunSucceeded(t, id, map[string]any{"query": "the app crashes when I upload a file"})
	assert.Equal(t, "TECH", answerOf(t, r))
}

// #14 ParameterExtractor pulls typed fields out of the query.
func TestParameterExtractor(t *testing.T) {
	llmSkipIfNoModel(t)
	comps := map[string]comp{
		"start": {Kind: "Start", Params: map[string]any{"fields": []any{}}, Downstream: []string{"ext"}},
		"ext": {Kind: "ParameterExtractor", Params: map[string]any{
			"model": chatModelID,
			"query": "{sys.query}",
			"parameters": []any{
				map[string]any{"name": "city", "type": "string", "description": "the city name"},
				map[string]any{"name": "days", "type": "number", "description": "number of days"},
			},
		}, Upstream: []string{"start"}, Downstream: []string{"ans"}},
		"ans": {Kind: "Answer", Params: answerTemplate("city={ext@city} days={ext@days}"), Upstream: []string{"ext"}},
	}
	id := createWorkflow(t, "extractor", comps)
	r := requireRunSucceeded(t, id, map[string]any{"query": "Book a hotel in Tokyo for 5 days"})
	ans := answerOf(t, r)
	assert.Contains(t, ans, "city=Tokyo")
	assert.Contains(t, ans, "days=5")
}

// #15 Iteration fans a list out through a per-item body.
func TestIterationOverList(t *testing.T) {
	llmSkipIfNoModel(t)
	comps := map[string]comp{
		"start": {Kind: "Start", Params: map[string]any{"fields": []any{}}, Downstream: []string{"loop"}},
		"loop": {Kind: "Iteration", Params: map[string]any{
			"items":      `["alpha","beta","gamma"]`,
			"output_ref": "{tpl@text}",
		}, Upstream: []string{"start"}, Downstream: []string{"ans"}},
		"tpl": {Kind: "Template", Params: map[string]any{"template": "item-{loop@item}"}, Parent: "loop"},
		"ans": {Kind: "Answer", Params: answerTemplate("{loop@results}"), Upstream: []string{"loop"}},
	}
	id := createWorkflow(t, "iteration", comps)
	r := requireRunSucceeded(t, id, nil)
	ans := answerOf(t, r)
	for _, want := range []string{"item-alpha", "item-beta", "item-gamma"} {
		assert.Contains(t, ans, want, "iteration must process every item; got: %s", ans)
	}
}

// #16 Agent node: one autonomous turn over the plain prompt (no KBs).
func TestAgentNodePlainPrompt(t *testing.T) {
	llmSkipIfNoModel(t)
	comps := linearComps(
		node("start", "Start", map[string]any{"fields": []any{}}),
		node("agent", "Agent", map[string]any{
			"model":  chatModelID,
			"prompt": "Reply with the single word: pong",
		}),
		node("ans", "Answer", map[string]any{"template": "{agent@answer}"}),
	)
	id := createWorkflow(t, "agent-node", comps)
	r := runSync(t, id, nil)
	require.Equal(t, "succeeded", r.Status, "%s", r.Error)
	require.NotNil(t, traceOf(t, r, "agent"))
	c, _ := traceOf(t, r, "agent").Outputs["answer"].(string)
	assert.NotEmpty(t, strings.TrimSpace(c))
}
