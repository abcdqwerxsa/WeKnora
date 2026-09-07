package nodes

import (
	"context"
	"strings"
	"testing"
)

func TestQuestionClassifierRoutesByClassName(t *testing.T) {
	llm := func(_ context.Context, req LLMRequest) (string, error) {
		p := strings.ToLower(req.Prompt)
		if !strings.Contains(p, "billing") || !strings.Contains(p, "question: refund my order") {
			t.Errorf("prompt missing classes/question: %.200s", req.Prompt)
		}
		return "The class is Billing.", nil // tolerant match target
	}
	n, err := New(ComponentQuestionClassifier, map[string]any{
		"query": "{start@query}",
		"classes": []any{
			map[string]any{"name": "Billing", "description": "payment and refunds", "to": "bill_flow"},
			map[string]any{"name": "Tech", "description": "bugs and errors", "to": "tech_flow"},
		},
	}, Deps{LLMFunc: llm})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := n.Invoke(context.Background(), withState(map[string]any{"query": "refund my order"}, &fakeState{
		outputs: map[string]map[string]any{"start": {"query": "refund my order"}},
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out[RouteOutputKey] != "bill_flow" {
		t.Errorf("route=%v want bill_flow", out[RouteOutputKey])
	}
	if out["class"] != "Billing" {
		t.Errorf("class=%v want Billing", out["class"])
	}
}

func TestQuestionClassifierDefaultOnNoMatch(t *testing.T) {
	n, err := New(ComponentQuestionClassifier, map[string]any{
		"query": "q",
		"classes": []any{
			map[string]any{"name": "A", "to": "a"},
			map[string]any{"name": "B", "to": "b"},
		},
		"default": "fallback",
	}, Deps{LLMFunc: func(context.Context, LLMRequest) (string, error) {
		return "totally unrelated", nil
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := n.Invoke(context.Background(), withState(nil, &fakeState{}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out[RouteOutputKey] != "fallback" {
		t.Errorf("route=%v want fallback", out[RouteOutputKey])
	}
}

func TestQuestionClassifierRejectsBadShape(t *testing.T) {
	if _, err := New(ComponentQuestionClassifier, map[string]any{
		"query":   "q",
		"classes": []any{map[string]any{"name": "A", "to": "a"}},
	}, Deps{}); err == nil {
		t.Error("single class must fail compilation")
	}
	targets, err := RouteTargets("QuestionClassifier", map[string]any{
		"classes": []any{map[string]any{"name": "A", "to": "x"}, map[string]any{"name": "B", "to": "y"}},
		"default": "z",
	})
	if err != nil || strings.Join(targets, ",") != "x,y,z" {
		t.Errorf("RouteTargets=%v err=%v want [x y z]", targets, err)
	}
}

func TestParameterExtractorParsesAndCoerces(t *testing.T) {
	reply := `Sure! Here is the JSON: {"city": "Shenzhen", "days": 3, "vip": "true", "note": null}`
	n, err := New(ComponentParameterExtractor, map[string]any{
		"query": "{start@query}",
		"parameters": []any{
			map[string]any{"name": "city", "type": "string", "required": true},
			map[string]any{"name": "days", "type": "number"},
			map[string]any{"name": "vip", "type": "boolean"},
			map[string]any{"name": "note", "type": "string"},
		},
	}, Deps{LLMFunc: func(context.Context, LLMRequest) (string, error) {
		return reply, nil
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := n.Invoke(context.Background(), withState(nil, &fakeState{
		outputs: map[string]map[string]any{"start": {"query": "book hotel"}},
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out["city"] != "Shenzhen" {
		t.Errorf("city=%v", out["city"])
	}
	if out["days"] != float64(3) {
		t.Errorf("days=%v (%T) want 3", out["days"], out["days"])
	}
	if out["vip"] != true {
		t.Errorf("vip=%v want true (string coerced)", out["vip"])
	}
	if v, ok := out["note"]; !ok || v != nil {
		t.Errorf("note=%v want present nil", out["note"])
	}
}

func TestParameterExtractorMissingRequiredFails(t *testing.T) {
	n, err := New(ComponentParameterExtractor, map[string]any{
		"query":      "q",
		"parameters": []any{map[string]any{"name": "city", "type": "string", "required": true}},
	}, Deps{LLMFunc: func(context.Context, LLMRequest) (string, error) {
		return "```json\n{}\n```", nil
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := n.Invoke(context.Background(), withState(nil, &fakeState{})); err == nil {
		t.Error("missing required parameter must fail")
	}
}

func TestWebSearchNodeMapsResults(t *testing.T) {
	called := ""
	n, err := New(ComponentWebSearch, map[string]any{
		"query":       "{start@query}",
		"provider_id": "prov-1",
		"max_results": 3,
	}, Deps{WebSearchFunc: func(_ context.Context, req WebSearchRequest) ([]WebSearchResultItem, error) {
		called = req.ProviderID
		return []WebSearchResultItem{{Title: "t", URL: "http://i", Snippet: "s"}}, nil
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := n.Invoke(context.Background(), withState(nil, &fakeState{
		outputs: map[string]map[string]any{"start": {"query": "intranet docs"}},
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if called != "prov-1" {
		t.Errorf("providerID=%q want prov-1", called)
	}
	if out["result_count"] != 1 {
		t.Errorf("result_count=%v", out["result_count"])
	}
}
