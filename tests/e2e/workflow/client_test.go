// Package e2e drives the workflow REST surface of a live WeKnora instance.
//
// Configuration (env):
//
//	E2E_BASE_URL   (default http://192.168.20.226:8091)
//	E2E_EMAIL      login email    (required)
//	E2E_PASSWORD   login password (required)
//	E2E_KB_NAME    substring to pick the retrieval KB (optional; default "wiki")
//
// Every workflow is created with the zz-e2e- name prefix; TestMain deletes
// all prefixed workflows before and after the run so the suite is
// idempotent and leaves nothing behind.
package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const namePrefix = "zz-e2e-"

var (
	baseURL string
	token   string
	httpCli = &http.Client{Timeout: 120 * time.Second}
)

func optMain(t *testing.T) {
	baseURL = strings.TrimRight(envOr("E2E_BASE_URL", "http://192.168.20.226:8091"), "/")
	email := os.Getenv("E2E_EMAIL")
	pass := os.Getenv("E2E_PASSWORD")
	if email == "" || pass == "" {
		fmt.Println("E2E_EMAIL / E2E_PASSWORD not set — skipping the suite")
		os.Exit(0)
	}
	login(t, email, pass)
	cleanupWorkflows(t)
}

// ---- raw HTTP ---------------------------------------------------------------

type apiResp struct {
	Status  int
	Body    []byte
	Success bool
	Message string
	Data    json.RawMessage
	Run     json.RawMessage
}

func call(t *testing.T, method, path string, body any) *apiResp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, baseURL+path, rd)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpCli.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	out := &apiResp{Status: resp.StatusCode, Body: raw}
	var env struct {
		Success bool            `json:"success"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
		Run     json.RawMessage `json:"run"`
	}
	if json.Unmarshal(raw, &env) == nil {
		out.Success, out.Message, out.Data, out.Run = env.Success, env.Message, env.Data, env.Run
	}
	return out
}

func decodeInto(t *testing.T, raw json.RawMessage, v any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(raw, v), "raw: %s", string(raw))
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// ---- auth & lifecycle ---------------------------------------------------------

func login(t *testing.T, email, pass string) {
	t.Helper()
	body := map[string]string{"email": email, "password": pass}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	resp, err := http.Post(baseURL+"/api/v1/auth/login", "application/json", bytes.NewReader(raw))
	require.NoError(t, err)
	defer resp.Body.Close()
	rd, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "login failed: %s", string(rd))
	var lr struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(rd, &lr))
	require.NotEmpty(t, lr.Token, "login returned no token: %s", string(rd))
	token = lr.Token
}

// wf mirrors the parts of types.Workflow the suite reads.
type wf struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Status string          `json:"status"`
	DSL    json.RawMessage `json:"dsl"`
}

func listWorkflows(t *testing.T) []wf {
	t.Helper()
	r := call(t, "GET", "/api/v1/workflows?page=1&page_size=100", nil)
	require.Equal(t, http.StatusOK, r.Status, "%s", r.Body)
	var page struct {
		Workflows []wf `json:"workflows"`
	}
	decodeInto(t, r.Data, &page)
	return page.Workflows
}

func cleanupWorkflows(t *testing.T) {
	t.Helper()
	for _, w := range listWorkflows(t) {
		if !strings.HasPrefix(w.Name, namePrefix) {
			continue
		}
		r := call(t, "DELETE", "/api/v1/workflows/"+w.ID, nil)
		if r.Status != http.StatusOK {
			t.Logf("cleanup: delete %s (%s) -> %d %s", w.ID, w.Name, r.Status, r.Body)
		}
	}
}

// createWorkflow builds a workflow from the components map (id -> comp) and
// returns its id. comps edges are derived from upstream/downstream lists.
func createWorkflow(t *testing.T, name string, comps map[string]comp) string {
	t.Helper()
	dsl := buildDSL(comps)
	r := call(t, "POST", "/api/v1/workflows", map[string]any{
		"name":        namePrefix + name,
		"description": "e2e",
		"dsl":         dsl,
	})
	require.Equal(t, http.StatusCreated, r.Status, "create %s: %s", name, r.Body)
	var created wf
	decodeInto(t, r.Data, &created)
	require.NotEmpty(t, created.ID)
	return created.ID
}

// comp is one node's semantic view.
type comp struct {
	Kind       string
	Params     map[string]any
	Upstream   []string
	Downstream []string
	Parent     string // Iteration body membership (parent = iteration node id)
}

func buildDSL(comps map[string]comp) map[string]any {
	out := map[string]any{"version": 1, "components": map[string]any{}}
	cm := out["components"].(map[string]any)
	for id, c := range comps {
		entry := map[string]any{
			"obj":        map[string]any{"component_name": c.Kind, "params": c.Params},
			"upstream":   c.Upstream,
			"downstream": c.Downstream,
		}
		if c.Parent != "" {
			entry["parent"] = c.Parent
		}
		cm[id] = entry
	}
	return out
}

func publishWorkflow(t *testing.T, id string) {
	t.Helper()
	r := call(t, "POST", "/api/v1/workflows/"+id+"/publish", nil)
	require.Equal(t, http.StatusOK, r.Status, "publish: %s", r.Body)
}

func updateWorkflowDSL(t *testing.T, id string, comps map[string]comp) {
	t.Helper()
	// PUT is a full replace of mutable fields; fetch current name first.
	cur := listWorkflows(t)
	var name string
	for _, w := range cur {
		if w.ID == id {
			name = w.Name
		}
	}
	r := call(t, "PUT", "/api/v1/workflows/"+id, map[string]any{
		"name": name, "description": "e2e", "dsl": buildDSL(comps),
	})
	require.Equal(t, http.StatusOK, r.Status, "update: %s", r.Body)
}

// ---- runs ---------------------------------------------------------------------

// run mirrors the parts of types.WorkflowRun the suite reads.
type run struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Output json.RawMessage `json:"output"`
	Error  string          `json:"error"`
	Trace  []traceEntry    `json:"trace"`
}

type traceEntry struct {
	NodeID   string         `json:"node_id"`
	Kind     string         `json:"kind"`
	Phase    string         `json:"phase"`
	Outputs  map[string]any `json:"outputs"`
	Err      string         `json:"error"`
	Replayed bool           `json:"replayed"`
	Duration int64          `json:"duration_ms"`
}

type runOutput struct {
	Answer string `json:"answer"`
}

func startRun(t *testing.T, id string, body map[string]any) run {
	t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	if _, ok := body["query"]; !ok {
		body["query"] = "e2e query"
	}
	r := call(t, "POST", "/api/v1/workflows/"+id+"/runs", body)
	require.Contains(t, []int{http.StatusOK, http.StatusAccepted}, r.Status,
		"run start: %s", r.Body)
	var out run
	decodeInto(t, r.Run, &out)
	require.NotEmpty(t, out.ID)
	return out
}

// runSync runs and returns the terminal run (async=false path).
func runSync(t *testing.T, id string, body map[string]any) run {
	t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	body["async"] = false
	return startRun(t, id, body)
}

// runAsyncAwait starts an async run and polls to terminal state.
func runAsyncAwait(t *testing.T, id string, body map[string]any, timeout time.Duration) run {
	t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	body["async"] = true
	out := startRun(t, id, body)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cur := getRun(t, id, out.ID)
		if isTerminal(cur.Status) {
			return cur
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("async run %s did not reach terminal state within %s (last: %s)", out.ID, timeout, getRun(t, id, out.ID).Status)
	return run{}
}

func getRun(t *testing.T, id, runID string) run {
	t.Helper()
	r := call(t, "GET", "/api/v1/workflows/"+id+"/runs/"+runID, nil)
	require.Equal(t, http.StatusOK, r.Status, "get run: %s", r.Body)
	var out run
	decodeInto(t, r.Data, &out)
	return out
}

func listRuns(t *testing.T, id string) []run {
	t.Helper()
	r := call(t, "GET", "/api/v1/workflows/"+id+"/runs", nil)
	require.Equal(t, http.StatusOK, r.Status, "%s", r.Body)
	var page struct {
		Runs []run `json:"runs"`
	}
	decodeInto(t, r.Data, &page)
	return page.Runs
}

func isTerminal(status string) bool {
	switch status {
	case "succeeded", "failed", "cancelled":
		return true
	}
	return false
}

func answerOf(t *testing.T, r run) string {
	t.Helper()
	var o runOutput
	require.NoError(t, json.Unmarshal(r.Output, &o), "output: %s", string(r.Output))
	return o.Answer
}

func traceOf(t *testing.T, r run, nodeID string) *traceEntry {
	t.Helper()
	for i := range r.Trace {
		if r.Trace[i].NodeID == nodeID {
			return &r.Trace[i]
		}
	}
	return nil
}

// collectSSE subscribes to the run event stream until the terminal frame or
// timeout, returning every parsed data frame.
func collectSSE(t *testing.T, workflowID, runID string, timeout time.Duration) []map[string]any {
	t.Helper()
	req, err := http.NewRequest("GET", baseURL+"/api/v1/workflows/"+workflowID+"/runs/"+runID+"/events", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	cli := &http.Client{} // no global timeout; the context bounds the stream
	ctx, cancel := context.WithTimeout(req.Context(), timeout)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := cli.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var frames []map[string]any
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var f map[string]any
		if json.Unmarshal([]byte(payload), &f) == nil {
			frames = append(frames, f)
			if kind, _ := f["kind"].(string); kind == "run" {
				return frames
			}
		}
	}
	return frames
}

// ---- misc shared helpers -------------------------------------------------------

// nodeSpec is the terse tuple for linearComps.
type nodeSpec struct {
	id     string
	kind   string
	params map[string]any
}

func node(id, kind string, params map[string]any) nodeSpec {
	return nodeSpec{id: id, kind: kind, params: params}
}

// linearComps wires ids into a straight line start -> ... -> end.
func linearComps(entries ...nodeSpec) map[string]comp {
	comps := map[string]comp{}
	var prev string
	for i, e := range entries {
		c := comp{Kind: e.kind, Params: e.params}
		if i > 0 {
			c.Upstream = []string{prev}
			p := comps[prev]
			p.Downstream = append(p.Downstream, e.id)
			comps[prev] = p
		}
		comps[e.id] = c
		prev = e.id
	}
	return comps
}

// startComp is the standard Start node with no form fields.
func startComp() comp { return comp{Kind: "Start", Params: map[string]any{"fields": []any{}}} }

func answerTemplate(tmpl string) map[string]any {
	return map[string]any{"template": tmpl}
}

// requireRunSucceeded asserts terminal success and returns the run.
func requireRunSucceeded(t *testing.T, id string, body map[string]any) run {
	t.Helper()
	r := runSync(t, id, body)
	require.Equal(t, "succeeded", r.Status, "run failed: %s", r.Error)
	return r
}

var _ = assert.Equal // keep import when a file uses only require

func TestMain(m *testing.M) {
	// Wrap the optMain contract: login + pre-cleanup, run, post-cleanup.
	fake := &testing.T{}
	optMain(fake)
	if !fake.Failed() {
		resolveChatModel(fake)
	}
	code := m.Run()
	cleanupWorkflows(fake)
	os.Exit(code)
}

// chatModelID is the first available KnowledgeQA model (lazy, once per run).
// The workflow LLM node falls back to the tenant's default chat model, but a
// workspace may not have one flagged — the suite picks explicitly instead.
var chatModelID string

func resolveChatModel(t *testing.T) {
	t.Helper()
	r := call(t, "GET", "/api/v1/models?type=KnowledgeQA", nil)
	require.Equal(t, http.StatusOK, r.Status, "%s", r.Body)
	var models []struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Data, &models), "data: %s", string(r.Data))
	for _, m := range models {
		if m.ID != "" {
			chatModelID = m.ID
			return
		}
	}
}
