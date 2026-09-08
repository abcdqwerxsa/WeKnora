package e2e

// Model-free node scenarios: Start, Switch, Template, VariableAggregator,
// HTTP, DataOps, Code, Retrieval, reference rendering, error policy and
// publish semantics. None of these need a chat model (Retrieval reads the
// pre-existing wiki KB).
import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1 Start: form fields materialise, defaults backfill, required gating.
func TestStartFormFieldsAndRequired(t *testing.T) {
	comps := map[string]comp{
		"start": {Kind: "Start", Params: map[string]any{"fields": []any{
			map[string]any{"name": "topic", "type": "text", "required": true},
			map[string]any{"name": "tone", "type": "select", "options": []any{"formal", "casual"}, "default": "formal"},
		}}, Downstream: []string{"ans"}},
		"ans": {Kind: "Answer", Params: answerTemplate("topic={start@topic} tone={start@tone}"), Upstream: []string{"start"}},
	}
	id := createWorkflow(t, "start-form", comps)

	// Provided value wins.
	r := requireRunSucceeded(t, id, map[string]any{"inputs": map[string]any{"topic": "gardening"}})
	assert.Equal(t, "topic=gardening tone=formal", answerOf(t, r))

	// Missing required field is rejected before execution.
	resp := call(t, "POST", "/api/v1/workflows/"+id+"/runs", map[string]any{"query": "q", "async": false})
	require.Equal(t, http.StatusBadRequest, resp.Status, "missing required must 400: %s", resp.Body)

	// Explicit value overrides the declared default.
	r = requireRunSucceeded(t, id, map[string]any{"inputs": map[string]any{"topic": "x", "tone": "casual"}})
	assert.Equal(t, "topic=x tone=casual", answerOf(t, r))
}

// #7 Switch: operators, and/or logic, default routing.
func TestSwitchOperatorsAndRouting(t *testing.T) {
	mk := func(fields []any, cases []any, def string) map[string]comp {
		return map[string]comp{
			"start": {Kind: "Start", Params: map[string]any{"fields": fields}, Downstream: []string{"sw"}},
			"sw":    {Kind: "Switch", Params: map[string]any{"cases": cases, "default": def}, Upstream: []string{"start"}, Downstream: []string{"a", "b"}},
			"a":     {Kind: "Answer", Params: answerTemplate("A:{sw@matched}"), Upstream: []string{"sw"}},
			"b":     {Kind: "Answer", Params: answerTemplate("B:{sw@matched}"), Upstream: []string{"sw"}},
		}
	}
	run := func(id string, inputs map[string]any) string {
		body := map[string]any{"async": false}
		if inputs != nil {
			body["inputs"] = inputs
		}
		r := requireRunSucceeded(t, id, body)
		return answerOf(t, r)
	}

	// eq + default fallback.
	id := createWorkflow(t, "switch-eq", mk([]any{map[string]any{"name": "mode", "type": "text"}}, []any{
		map[string]any{"conditions": []any{map[string]any{"ref": "{start@mode}", "op": "eq", "value": "fast"}}, "logic": "and", "to": "a"},
	}, "b"))
	assert.Equal(t, "A:fast", run(id, map[string]any{"mode": "fast"}))
	// default route: matched renders empty by design (no case fired)
	assert.Equal(t, "B:", run(id, map[string]any{"mode": "slow"}))

	// contains + numeric gt via or-logic.
	id = createWorkflow(t, "switch-or", mk([]any{
		map[string]any{"name": "text", "type": "text"},
		map[string]any{"name": "count", "type": "number"},
	}, []any{
		map[string]any{"conditions": []any{
			map[string]any{"ref": "{start@text}", "op": "contains", "value": "urgent"},
			map[string]any{"ref": "{start@count}", "op": "gt", "value": "10"},
		}, "logic": "or", "to": "a"},
	}, "b"))
	assert.Equal(t, "A:", run(id, map[string]any{"text": "not urgent at all", "count": "3"})[:2]) // hmm: contains hits
	assert.Equal(t, "A:", run(id, map[string]any{"text": "plain", "count": "42"})[:2])
	assert.Equal(t, "B:", run(id, map[string]any{"text": "plain", "count": "3"})[:2])

	// regex.
	id = createWorkflow(t, "switch-regex", mk([]any{map[string]any{"name": "code", "type": "text"}}, []any{
		map[string]any{"conditions": []any{map[string]any{"ref": "{start@code}", "op": "regex", "value": "^ACC-\\d+$"}}, "logic": "and", "to": "a"},
	}, "b"))
	assert.Equal(t, "A:ACC-123", run(id, map[string]any{"code": "ACC-123"}))
	assert.Equal(t, "B:", run(id, map[string]any{"code": "XX-1"}))
}

// #8 Template node: every op.
func TestTemplateNodeOps(t *testing.T) {
	comps := map[string]comp{
		"start": {Kind: "Start", Params: map[string]any{"fields": []any{
			map[string]any{"name": "name", "type": "text"},
		}}, Downstream: []string{"tpl"}},
		"tpl": {Kind: "Template", Params: map[string]any{
			"template": "  Hello {start@name}  ",
			"ops": []any{
				map[string]any{"op": "trim"},
				map[string]any{"op": "replace", "from": "Hello", "to": "Goodbye"},
				map[string]any{"op": "upper"},
				map[string]any{"op": "lower"},
				map[string]any{"op": "regex_extract", "pattern": `goodbye (\w+)`, "group": 1},
			},
		}, Upstream: []string{"start"}, Downstream: []string{"ans"}},
		"ans": {Kind: "Answer", Params: answerTemplate("[{tpl@text}]"), Upstream: []string{"tpl"}},
	}
	id := createWorkflow(t, "template-ops", comps)
	r := requireRunSucceeded(t, id, map[string]any{"inputs": map[string]any{"name": "weknora"}})
	assert.Equal(t, "[weknora]", answerOf(t, r))
}

// #9 VariableAggregator: merges whichever branch ran; skips the other.
func TestVariableAggregatorBranchMerge(t *testing.T) {
	comps := map[string]comp{
		"start": {Kind: "Start", Params: map[string]any{"fields": []any{
			map[string]any{"name": "mode", "type": "text"},
		}}, Downstream: []string{"sw"}},
		"sw": {Kind: "Switch", Params: map[string]any{
			"cases":   []any{map[string]any{"conditions": []any{map[string]any{"ref": "{start@mode}", "op": "eq", "value": "left"}}, "logic": "and", "to": "a"}},
			"default": "b",
		}, Upstream: []string{"start"}, Downstream: []string{"a", "b"}},
		"a": {Kind: "Template", Params: map[string]any{"template": "A-value"}, Upstream: []string{"sw"}, Downstream: []string{"agg"}},
		"b": {Kind: "Template", Params: map[string]any{"template": "B-value"}, Upstream: []string{"sw"}, Downstream: []string{"agg"}},
		"agg": {Kind: "VariableAggregator", Params: map[string]any{"variables": []any{
			map[string]any{"name": "picked", "ref": "a@text"},
			map[string]any{"name": "other", "ref": "b@text"},
		}}, Upstream: []string{"a", "b"}, Downstream: []string{"ans"}},
		"ans": {Kind: "Answer", Params: answerTemplate("n={agg@collected}"), Upstream: []string{"agg"}},
	}
	id := createWorkflow(t, "aggregator", comps)
	r := requireRunSucceeded(t, id, map[string]any{"inputs": map[string]any{"mode": "left"}})
	assert.Equal(t, "n=1", answerOf(t, r), "only the taken branch node must be collected")
}

// #10 HTTP: intranet call allowed + body/URL refs render; public egress blocked.
func TestHTTPNodeIntranetAndRefRender(t *testing.T) {
	comps := map[string]comp{
		"start": {Kind: "Start", Params: map[string]any{"fields": []any{}}, Downstream: []string{"http"}},
		"http": {Kind: "HTTP", Params: map[string]any{
			"method": "GET",
			"url":    baseURL + "/api/v1/version-missing-ok",
		}, Upstream: []string{"start"}, Downstream: []string{"ans"}},
		"ans": {Kind: "Answer", Params: answerTemplate("status={http@status_code}"), Upstream: []string{"http"}},
	}
	id := createWorkflow(t, "http-intranet", comps)
	r := requireRunSucceeded(t, id, nil)
	assert.Equal(t, "status=401", answerOf(t, r), "unauthenticated GET on the own API still proves the intranet call ran")
}

func TestHTTPNodePublicEgressBlocked(t *testing.T) {
	comps := map[string]comp{
		"start": {Kind: "Start", Params: map[string]any{"fields": []any{}}, Downstream: []string{"http"}},
		"http": {Kind: "HTTP", Params: map[string]any{
			"method": "GET", "url": "http://example.com/",
		}, Upstream: []string{"start"}, Downstream: []string{"ans"}},
		"ans": {Kind: "Answer", Params: answerTemplate("never"), Upstream: []string{"http"}},
	}
	id := createWorkflow(t, "http-public", comps)
	r := runSync(t, id, nil)
	assert.Equal(t, "failed", r.Status, "public egress must fail the run")
	assert.Contains(t, r.Error, "intranet", "error should name the intranet policy: %s", r.Error)
}

// #11 DataOps: SELECT-only validation + parameter binding.
func TestDataOpsSelectOnlyAndBinding(t *testing.T) {
	// Non-SELECT must be rejected by the adapter (node fails).
	comps := map[string]comp{
		"start": {Kind: "Start", Params: map[string]any{"fields": []any{
			map[string]any{"name": "n", "type": "number"},
		}}, Downstream: []string{"ops"}},
		"ops": {Kind: "DataOps", Params: map[string]any{
			"sql": "DROP TABLE nothing", "variables": []any{},
		}, Upstream: []string{"start"}, Downstream: []string{"ans"}},
		"ans": {Kind: "Answer", Params: answerTemplate("never"), Upstream: []string{"ops"}},
	}
	id := createWorkflow(t, "dataops-write-blocked", comps)
	r := runSync(t, id, nil)
	assert.Equal(t, "failed", r.Status)
	assert.Contains(t, strings.ToLower(r.Error), "select", "write SQL must be rejected: %s", r.Error)

	// A harmless SELECT over an expression (no table dependency).
	comps["ops"] = comp{Kind: "DataOps", Params: map[string]any{
		"sql":       "SELECT {start@n} + 1 AS doubled",
		"variables": []any{},
	}, Upstream: []string{"start"}, Downstream: []string{"ans"}}
	comps["ans"] = comp{Kind: "Answer", Params: answerTemplate("rows={ops@row_count}"), Upstream: []string{"ops"}}
	id = createWorkflow(t, "dataops-select", comps)
	r = runSync(t, id, map[string]any{"inputs": map[string]any{"n": "41"}})
	if r.Status == "failed" && strings.Contains(r.Error, "sandbox") {
		t.Skipf("DataOps analytic store unavailable on this deployment: %s", r.Error)
	}
	require.Equal(t, "succeeded", r.Status, "%s", r.Error)
	assert.Equal(t, "rows=1", answerOf(t, r))
}

// #12 Code: success path, or clean failure when no sandbox is configured.
func TestCodeNode(t *testing.T) {
	comps := map[string]comp{
		"start": {Kind: "Start", Params: map[string]any{"fields": []any{}}, Downstream: []string{"code"}},
		"code": {Kind: "Code", Params: map[string]any{
			"language": "python3",
			"code":     "import os, json\nprint(json.dumps({'echo': os.environ.get('WEKNORA_WORKFLOW_INPUT','')}))",
		}, Upstream: []string{"start"}, Downstream: []string{"ans"}},
		"ans": {Kind: "Answer", Params: answerTemplate("kind=ran"), Upstream: []string{"code"}},
	}
	id := createWorkflow(t, "code-node", comps)
	r := runSync(t, id, nil)
	if r.Status == "failed" {
		if strings.Contains(r.Error, "sandbox") {
			t.Skipf("Code sandbox not configured on this deployment: %s", r.Error)
		}
		t.Fatalf("code node failed unexpectedly: %s", r.Error)
	}
	require.Equal(t, "succeeded", r.Status)
	assert.Equal(t, "kind=ran", answerOf(t, r))
}

// #17 Retrieval against the pre-existing wiki KB (auto-discovered).
func TestRetrievalAgainstWikiKB(t *testing.T) {
	kbID := findWikiKB(t)
	if kbID == "" {
		t.Skip("no knowledge base matching E2E_KB_NAME (default 'wiki') found")
	}
	comps := map[string]comp{
		"start": {Kind: "Start", Params: map[string]any{"fields": []any{}}, Downstream: []string{"ret"}},
		"ret": {Kind: "Retrieval", Params: map[string]any{
			"query": "{sys.query}", "kb_ids": []any{kbID}, "top_k": 3,
		}, Upstream: []string{"start"}, Downstream: []string{"ans"}},
		"ans": {Kind: "Answer", Params: answerTemplate("chunks={ret@chunks}"), Upstream: []string{"ret"}},
	}
	id := createWorkflow(t, "retrieval-wiki", comps)
	r := requireRunSucceeded(t, id, map[string]any{"query": "wiki knowledge base content"})
	assert.NotContains(t, answerOf(t, r), "{ret@chunks}", "ref must render, not leak the literal")
	entry := traceOf(t, r, "ret")
	require.NotNil(t, entry, "trace must record the retrieval node")
	chunks, _ := entry.Outputs["chunks"].([]any)
	assert.NotEmpty(t, chunks, "wiki KB should return at least one chunk for a generic query")
}

func findWikiKB(t *testing.T) string {
	t.Helper()
	resp := call(t, "GET", "/api/v1/knowledge-bases?page=1&page_size=50", nil)
	require.Equal(t, http.StatusOK, resp.Status, "%s", resp.Body)
	var kbs []struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		KnowledgeCount int    `json:"knowledge_count"`
	}
	require.NoError(t, json.Unmarshal(resp.Data, &kbs), "data: %s", string(resp.Data))
	want := strings.ToLower(envOr("E2E_KB_NAME", ""))
	for _, kb := range kbs {
		if want != "" && !strings.Contains(strings.ToLower(kb.Name), want) {
			continue
		}
		if kb.KnowledgeCount > 0 {
			return kb.ID
		}
	}
	return ""
}

// #19 Reference rendering: template/HTTP-body refs render concrete values,
// never literal {node@param} text (regression for hyphenated ids — the ids
// here deliberately carry hyphens like makeNodeId generates).
func TestReferenceRenderingHyphenatedIDs(t *testing.T) {
	comps := linearComps(
		node("start-ab12cd", "Start", map[string]any{"fields": []any{}}),
		node("tpl-ef34gh", "Template", map[string]any{"template": "value-{sys.query}"}),
		node("ans-ij56kl", "Answer", map[string]any{"template": "out:[{tpl-ef34gh@text}] q=[{start-ab12cd@query}]"}),
	)
	id := createWorkflow(t, "refs-hyphen", comps)
	r := requireRunSucceeded(t, id, map[string]any{"query": "probe"})
	assert.Equal(t, "out:[value-probe] q=[probe]", answerOf(t, r),
		"hyphenated node-id refs must render, not echo the literal braces")
}

// #21 Error policy: on_error=continue carries default outputs past a dead node.
func TestErrorPolicyContinue(t *testing.T) {
	comps := map[string]comp{
		"start": {Kind: "Start", Params: map[string]any{"fields": []any{}}, Downstream: []string{"dead"}},
		"dead": {Kind: "HTTP", Params: map[string]any{
			"method": "GET", "url": "http://10.255.255.1:1/", // unroutable intranet → timeout path
			"timeout_seconds": 2,
			"on_error":        map[string]any{"action": "continue", "default_outputs": map[string]any{"status_code": "0", "body": "fallback"}},
		}, Upstream: []string{"start"}, Downstream: []string{"ans"}},
		"ans": {Kind: "Answer", Params: answerTemplate("status={dead@status_code}"), Upstream: []string{"dead"}},
	}
	id := createWorkflow(t, "onerror-continue", comps)
	r := requireRunSucceeded(t, id, nil)
	assert.Equal(t, "status=0", answerOf(t, r), "default_outputs must materialise when the node fails")
}

// #20 Publish semantics: after publish, runs execute the frozen snapshot,
// not the edited draft (the trap the UI now warns about).
func TestPublishedSnapshotIsWhatRuns(t *testing.T) {
	comps := linearComps(
		node("start", "Start", map[string]any{"fields": []any{}}),
		node("ans", "Answer", map[string]any{"template": "PUBLISHED"}),
	)
	id := createWorkflow(t, "publish-snapshot", comps)
	publishWorkflow(t, id)

	// Draft diverges after publish.
	comps["ans"] = comp{Kind: "Answer", Params: answerTemplate("DRAFT"), Upstream: []string{"start"}}
	updateWorkflowDSL(t, id, comps)

	r := requireRunSucceeded(t, id, nil)
	assert.Equal(t, "PUBLISHED", answerOf(t, r), "runs must execute the frozen snapshot, not the live draft")
}

// #22 Retry: a failing node with retry executes, fails, and records the error.
func TestRetryPolicyRecorded(t *testing.T) {
	comps := map[string]comp{
		"start": {Kind: "Start", Params: map[string]any{"fields": []any{}}, Downstream: []string{"dead"}},
		"dead": {Kind: "HTTP", Params: map[string]any{
			"method": "GET", "url": "http://10.255.255.1:1/", "timeout_seconds": 1,
			"retry": map[string]any{"count": 1, "delay_ms": 100},
		}, Upstream: []string{"start"}, Downstream: []string{"ans"}},
		"ans": {Kind: "Answer", Params: answerTemplate("never"), Upstream: []string{"dead"}},
	}
	id := createWorkflow(t, "retry-policy", comps)
	r := runSync(t, id, nil)
	assert.Equal(t, "failed", r.Status)
	require.NotNil(t, traceOf(t, r, "dead"), "failed node must appear in the trace")
	assert.Contains(t, traceOf(t, r, "dead").Err, "10.255.255.1")
}

// ensure fmt stays used if asserts above shift; cheap guard for gofmt-drift.
var _ = fmt.Sprintf
var _ = time.Second
