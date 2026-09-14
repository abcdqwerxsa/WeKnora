package nodes

import (
	"testing"
)

// Array indexing: {node@items.2.name} addresses a []any element (n8n-style
// $json parity for reference reachability); map keys keep exact matching.
func TestRenderArrayIndexAddressing(t *testing.T) {
	st := &fakeState{outputs: map[string]map[string]any{
		"retr": {"chunks": []any{
			map[string]any{"content": "c0"},
			map[string]any{"content": "c1"},
			map[string]any{"content": "c2"},
		}},
	}}
	out, err := Render("third: {retr@chunks.2.content}", st)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != "third: c2" {
		t.Errorf("out = %q", out)
	}
	// Out-of-range and non-numeric segments must fail with clear errors.
	if _, err := Render("{retr@chunks.5.content}", st); err == nil {
		t.Error("out-of-range index must fail")
	}
	if _, err := Render("{retr@chunks.first.content}", st); err == nil {
		t.Error("non-numeric segment on an array must fail")
	}

	// Live-run static types (pre-JSON-round-trip): Retrieval emits
	// []map[string]any, DataOps []map[string]any rows / []string columns,
	// HTTP map[string]string headers — all must be addressable.
	st2 := &fakeState{outputs: map[string]map[string]any{
		"retr": {"chunks": []map[string]any{{"content": "live"}}},
		"ops":  {"columns": []string{"a", "b"}},
		"http": {"headers": map[string]string{"content-type": "application/json"}},
	}}
	for ref, want := range map[string]string{
		"{retr@chunks.0.content}":     "live",
		"{ops@columns.1}":             "b",
		"{http@headers.content-type}": "application/json",
	} {
		out, err := Render(ref, st2)
		if err != nil {
			t.Errorf("%s: %v", ref, err)
			continue
		}
		if out != want {
			t.Errorf("%s = %q, want %q", ref, out, want)
		}
	}

	// User-declared names containing dots (Start form fields) keep the
	// exact-match behaviour.
	st3 := &fakeState{outputs: map[string]map[string]any{
		"start": {"user.name": "Ada"},
	}}
	if out, err := Render("hi {start@user.name}", st3); err != nil || out != "hi Ada" {
		t.Errorf("dotted exact match = %q, %v", out, err)
	}
}
