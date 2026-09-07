package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/workflow/nodes"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSandboxResolver + fakeSandboxManager let the Code adapter run without
// a real backend: the manager records the ExecuteConfig and answers with a
// canned ExecuteResult.
type fakeSandboxResolver struct{ manager sandbox.Manager }

func (r *fakeSandboxResolver) Resolve(_ context.Context, _ uint64, _ string) (sandbox.Manager, error) {
	return r.manager, nil
}

type fakeSandboxManager struct {
	called  *sandbox.ExecuteConfig
	stdout  string
	stderr  string
	exit    int
	execErr error
}

func (m *fakeSandboxManager) Execute(_ context.Context, cfg *sandbox.ExecuteConfig) (*sandbox.ExecuteResult, error) {
	m.called = cfg
	if m.execErr != nil {
		return nil, m.execErr
	}
	return &sandbox.ExecuteResult{Stdout: m.stdout, Stderr: m.stderr, ExitCode: m.exit}, nil
}
func (m *fakeSandboxManager) Cleanup(context.Context) error { return nil }
func (m *fakeSandboxManager) GetSandbox() sandbox.Sandbox   { return nil }
func (m *fakeSandboxManager) GetType() sandbox.SandboxType  { return "" }

func codeSvc(t *testing.T, resolver sandbox.TenantSandboxResolver) *workflowService {
	t.Helper()
	svc := NewWorkflowService(newRunRepoStub(nil), nil, nil, nil, nil, nil, nil, resolver)
	return svc.(*workflowService)
}

func codeCtx() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10001))
}

func TestRunCodeParsesLastJSONObject(t *testing.T) {
	mgr := &fakeSandboxManager{stdout: "progress: step 1\n{\"answer\": 42, \"nested\": {\"k\": \"v\"}}\n"}
	svc := codeSvc(t, &fakeSandboxResolver{manager: mgr})
	out, err := svc.runCode(codeCtx(), nodes.CodeRequest{
		Language: "python3", Code: "print(json.dumps({'answer': 42}))",
		InputJSON: `{"q":"hi"}`, TimeoutSeconds: 10,
	})
	require.NoError(t, err)
	assert.Equal(t, float64(42), out["answer"])
	// Ephemeral execution: empty SessionID, env-carried input, .py interpreter.
	assert.Equal(t, "", mgr.called.SessionID)
	assert.Equal(t, `{"q":"hi"}`, mgr.called.Env[codeInputEnvVar])
	assert.Equal(t, "workflow_code.py", mgr.called.Script)
	assert.Equal(t, "print(json.dumps({'answer': 42}))", mgr.called.ScriptContent)
}

func TestRunCodeNodeInterpreter(t *testing.T) {
	mgr := &fakeSandboxManager{stdout: `{"ok":true}`}
	svc := codeSvc(t, &fakeSandboxResolver{manager: mgr})
	_, err := svc.runCode(codeCtx(), nodes.CodeRequest{Language: "node", Code: "console.log(1)"})
	require.NoError(t, err)
	assert.Equal(t, "workflow_code.js", mgr.called.Script)
}

func TestRunCodeFailurePaths(t *testing.T) {
	// Script failure carries stderr tail; no-JSON stdout is an error.
	svc := codeSvc(t, &fakeSandboxResolver{manager: &fakeSandboxManager{exit: 1, stderr: "Traceback ... boom"}})
	_, err := svc.runCode(codeCtx(), nodes.CodeRequest{Language: "python3", Code: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")

	svc2 := codeSvc(t, &fakeSandboxResolver{manager: &fakeSandboxManager{stdout: "no json here"}})
	_, err = svc2.runCode(codeCtx(), nodes.CodeRequest{Language: "python3", Code: "print('hi')"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no JSON object")

	// Resolver without backend (Lite mode) errors clearly.
	svc3 := codeSvc(t, nil)
	_, err = svc3.runCode(codeCtx(), nodes.CodeRequest{Language: "python3", Code: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no sandbox backend")

	// Resolver failure surfaces wrapped.
	svc4 := codeSvc(t, errResolver{})
	_, err = svc4.runCode(codeCtx(), nodes.CodeRequest{Language: "python3", Code: "x"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, errResolveFailed))
}

type errResolver struct{}

func (errResolver) Resolve(context.Context, uint64, string) (sandbox.Manager, error) {
	return nil, errResolveFailed
}

var errResolveFailed = errors.New("resolve failed")

func TestLastJSONObject(t *testing.T) {
	cases := []struct {
		name, in string
		want     map[string]any
	}{
		{"plain", `{"a":1}`, map[string]any{"a": float64(1)}},
		{"progress then object", "line\n{\"a\": {\"b\": 2}}", map[string]any{"a": map[string]any{"b": float64(2)}}},
		{"two objects, last wins", `{"first":1} {"second":2}`, map[string]any{"second": float64(2)}},
		{"braces inside strings", `{"text": "a } b { c"}`, map[string]any{"text": "a } b { c"}},
		{"escaped quote in string", `{"t": "x\"}"}`, map[string]any{"t": `x"}`}},
		{"no object", "hello world", nil},
		{"unterminated", `{"a":1`, nil},
	}
	for _, tc := range cases {
		got := lastJSONObject(tc.in)
		if tc.want == nil {
			if got != nil {
				t.Errorf("%s: got %v, want nil", tc.name, got)
			}
			continue
		}
		if got == nil {
			t.Errorf("%s: got nil, want %v", tc.name, tc.want)
			continue
		}
		assert.Equal(t, tc.want, got, tc.name)
	}
}
