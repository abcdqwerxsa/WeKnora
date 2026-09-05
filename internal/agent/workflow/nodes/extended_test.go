package nodes

import (
	"context"
	"strings"
	"testing"
)

func TestTemplateRenderAndOps(t *testing.T) {
	n, err := New("Template", map[string]any{
		"template": "  {s@q}  ",
		"ops": []any{
			map[string]any{"op": "trim"},
			map[string]any{"op": "replace", "from": " ", "to": "_"},
			map[string]any{"op": "upper"},
		},
	}, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := n.Invoke(context.Background(), map[string]any{
		StateInputKey: &fakeState{outputs: map[string]map[string]any{"s": {"q": "hello world"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["text"] != "HELLO_WORLD" {
		t.Errorf("text = %q, want HELLO_WORLD", out["text"])
	}
}

func TestTemplateRegexExtractGroup(t *testing.T) {
	n, err := New("Template", map[string]any{
		"template": "ticket-12345 done",
		"ops":      []any{map[string]any{"op": "regex_extract", "pattern": `ticket-(\d+)`, "group": 1}},
	}, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := n.Invoke(context.Background(), map[string]any{StateInputKey: &fakeState{}})
	if err != nil {
		t.Fatal(err)
	}
	if out["text"] != "12345" {
		t.Errorf("text = %q, want 12345", out["text"])
	}
}

func TestTemplateUnknownOpFailsCompile(t *testing.T) {
	_, err := New("Template", map[string]any{
		"template": "x", "ops": []any{map[string]any{"op": "explode"}},
	}, Deps{})
	if err == nil || !strings.Contains(err.Error(), "unknown op") {
		t.Errorf("err = %v, want unknown op", err)
	}
}

func TestVariableAggregatorSkipsMissingRefs(t *testing.T) {
	n, err := New("VariableAggregator", map[string]any{
		"variables": []any{
			map[string]any{"name": "taken", "ref": "a@out"},
			map[string]any{"name": "skipped", "ref": "b@never"}, // not-executed branch
			map[string]any{"name": "q", "ref": "sys.query"},
		},
	}, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := n.Invoke(context.Background(), map[string]any{
		"query":       "hi",
		StateInputKey: &fakeState{sys: map[string]any{"query": "hi"}, outputs: map[string]map[string]any{"a": {"out": 7}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	values, ok := out["values"].(map[string]any)
	if !ok {
		t.Fatalf("values type %T", out["values"])
	}
	if len(values) != 2 || values["taken"] != 7 || values["q"] != "hi" {
		t.Errorf("values = %v, want taken+q only", values)
	}
	if _, present := values["skipped"]; present {
		t.Errorf("missing-ref entry must be skipped, got %v", values)
	}
}

func TestVariableAggregatorRequiresVariables(t *testing.T) {
	if _, err := New("VariableAggregator", map[string]any{}, Deps{}); err == nil {
		t.Error("want error for missing variables param")
	}
}

func TestHTTPNodeRendersAndCalls(t *testing.T) {
	var got HTTPRequest
	n, err := New("HTTP", map[string]any{
		"method":        "post",
		"url":           "http://svc.internal/api/{s@path}",
		"body_template": `{"q":"{sys.query}"}`,
		"headers":       map[string]any{"X-Trace": "t1"},
	}, Deps{HTTPFunc: func(_ context.Context, req HTTPRequest) (*HTTPResult, error) {
		got = req
		return &HTTPResult{StatusCode: 200, Body: "pong", Headers: map[string]string{"Content-Type": "text/plain"}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := n.Invoke(context.Background(), map[string]any{
		"query":       "hello",
		StateInputKey: &fakeState{sys: map[string]any{"query": "hello"}, outputs: map[string]map[string]any{"s": {"path": "x"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != "POST" || got.URL != "http://svc.internal/api/x" {
		t.Errorf("request = %+v", got)
	}
	if got.Body != `{"q":"hello"}` || got.Headers["X-Trace"] != "t1" {
		t.Errorf("body/headers = %+v", got)
	}
	if out["status_code"] != 200 || out["body"] != "pong" {
		t.Errorf("out = %v", out)
	}
}

func TestHTTPNodeWithoutFuncErrors(t *testing.T) {
	n, err := New("HTTP", map[string]any{"url": "http://x"}, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.Invoke(context.Background(), map[string]any{StateInputKey: &fakeState{}}); err == nil || !strings.Contains(err.Error(), "HTTPFunc") {
		t.Errorf("err = %v, want HTTPFunc-missing", err)
	}
}

func TestDataOpsNodeResolvesVariables(t *testing.T) {
	var got DataOpsRequest
	n, err := New("DataOps", map[string]any{
		"sql": "SELECT {s@col} AS c FROM t",
		"variables": []any{
			map[string]any{"name": "limit", "ref": "sys.query"},
		},
	}, Deps{DataOpsFunc: func(_ context.Context, req DataOpsRequest) (*DataOpsResult, error) {
		got = req
		return &DataOpsResult{Columns: []string{"c"}, Rows: []map[string]any{{"c": 1}}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := n.Invoke(context.Background(), map[string]any{
		"query":       "5",
		StateInputKey: &fakeState{sys: map[string]any{"query": "5"}, outputs: map[string]map[string]any{"s": {"col": "id"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.SQL != "SELECT id AS c FROM t" || got.Args["limit"] != "5" {
		t.Errorf("request = %+v", got)
	}
	if out["row_count"] != 1 {
		t.Errorf("out = %v", out)
	}
}

func TestDataOpsNodeMissingVariableRefFails(t *testing.T) {
	n, err := New("DataOps", map[string]any{
		"sql":       "SELECT 1",
		"variables": []any{map[string]any{"name": "v", "ref": "ghost@x"}},
	}, Deps{DataOpsFunc: func(_ context.Context, _ DataOpsRequest) (*DataOpsResult, error) {
		return &DataOpsResult{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = n.Invoke(context.Background(), map[string]any{StateInputKey: &fakeState{}})
	if err == nil || !strings.Contains(err.Error(), "variable \"v\"") {
		t.Errorf("err = %v, want missing variable error", err)
	}
}
