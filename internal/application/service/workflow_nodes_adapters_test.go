package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/workflow/nodes"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunHTTPIntranetRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "t1", r.Header.Get("X-Trace"))
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		fmt.Fprint(w, "pong")
	}))
	defer srv.Close()

	svc := &workflowService{}
	res, err := svc.runHTTP(context.Background(), mustHTTPReq("POST", srv.URL))
	require.NoError(t, err)
	assert.Equal(t, 200, res.StatusCode)
	assert.Equal(t, "pong", res.Body)
	assert.Equal(t, "text/plain", res.Headers["Content-Type"])
}

func mustHTTPReq(method, url string) nodes.HTTPRequest {
	return nodes.HTTPRequest{Method: method, URL: url, Headers: map[string]string{"X-Trace": "t1"}}
}

func TestRunHTTPRejectsPublicHost(t *testing.T) {
	svc := &workflowService{}
	_, err := svc.runHTTP(context.Background(), mustHTTPReq("GET", "http://8.8.8.8/"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "intranet")
}

func TestRunHTTPRejectsPublicRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://93.184.216.34/", http.StatusFound)
	}))
	defer srv.Close()

	svc := &workflowService{}
	_, err := svc.runHTTP(context.Background(), mustHTTPReq("GET", srv.URL))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "intranet")
}

func TestRunDataOpsQueryWithPlaceholders(t *testing.T) {
	svc := &workflowService{}
	res, err := svc.runDataOps(context.Background(), nodes.DataOpsRequest{
		SQL:  "SELECT $x + 1 AS v, $name AS n",
		Args: map[string]any{"x": 41, "name": "unit"},
	})
	require.NoError(t, err)
	require.Len(t, res.Rows, 1)
	// DuckDB returns DECIMAL/numeric for arithmetic; compare loosely.
	assert.Contains(t, fmt.Sprint(res.Rows[0]["v"]), "42")
	assert.Equal(t, "unit", res.Rows[0]["n"])
}

func TestRunDataOpsGuards(t *testing.T) {
	svc := &workflowService{}
	cases := []struct {
		name string
		sql  string
	}{
		{"multi-statement", "select 1; drop table x"},
		{"write", "delete from t"},
		{"short", "se"},
	}
	for _, tc := range cases {
		_, err := svc.runDataOps(context.Background(), nodes.DataOpsRequest{SQL: tc.sql})
		require.Error(t, err, tc.name)
	}
	// unreferenced variable is a config error
	_, err := svc.runDataOps(context.Background(), nodes.DataOpsRequest{
		SQL: "select 1", Args: map[string]any{"unused": 1},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not referenced")
}

// ---- full-chain RunWorkflow through the real adapters ----------------------

const httpChainDSL = `{"version":1,"components":{
	"start":{"obj":{"component_name":"Start","params":{}},"upstream":[],"downstream":["call"]},
	"call":{"obj":{"component_name":"HTTP","params":{"method":"GET","url":"%s","headers":{"X-Chain":"1"}}},"upstream":["start"],"downstream":["ans"]},
	"ans":{"obj":{"component_name":"Answer","params":{"template":"{call@status_code}|{call@body}"}},"upstream":["call"],"downstream":[]}}}`

func TestRunWorkflowHTTPChain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "1", r.Header.Get("X-Chain"))
		fmt.Fprint(w, "chain-ok")
	}))
	defer srv.Close()

	run := runFullChain(t, fmt.Sprintf(httpChainDSL, srv.URL))
	assert.Equal(t, types.WorkflowRunStatusSucceeded, run.Status)
	assert.Contains(t, string(run.Output), "200|chain-ok")
}

const dataOpsChainDSL = `{"version":1,"components":{
	"start":{"obj":{"component_name":"Start","params":{}},"upstream":[],"downstream":["sql"]},
	"sql":{"obj":{"component_name":"DataOps","params":{
		"sql":"SELECT $q AS echoed, count(*) AS n",
		"variables":[{"name":"q","ref":"start@query"}]}},"upstream":["start"],"downstream":["ans"]},
	"ans":{"obj":{"component_name":"Answer","params":{"template":"rows={sql@row_count} first={sql@rows}"}},"upstream":["sql"],"downstream":[]}}}`

func TestRunWorkflowDataOpsChain(t *testing.T) {
	run := runFullChain(t, dataOpsChainDSL)
	assert.Equal(t, types.WorkflowRunStatusSucceeded, run.Status)
	assert.Contains(t, string(run.Output), "rows=1")
	assert.Contains(t, string(run.Output), "chain-sql")
}

func runFullChain(t *testing.T, dsl string) *types.WorkflowRun {
	t.Helper()
	wf := &types.Workflow{ID: "wf-ext", TenantID: 42, Name: "wf", DSL: types.JSON(dsl)}
	repo := newRunRepoStub(wf)
	svc := NewWorkflowService(repo, nil, nil, nil, nil)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(42))
	run, err := svc.RunWorkflow(ctx, "wf-ext", &types.RunWorkflowRequest{Query: "chain-sql"})
	require.NoError(t, err)
	require.NotNil(t, run)
	if run.Status != types.WorkflowRunStatusSucceeded {
		t.Fatalf("run failed: %s", run.Error)
	}
	return run
}

// The engine node types are re-declared as tiny aliases so this test file
// stays independent of the exact import set of the other workflow tests.
