package nodes

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Built-in component names. These are the canonical spellings; lookup is
// case-insensitive so a DSL may carry "start"/"START" as well.
const (
	ComponentStart     = "Start"
	ComponentAnswer    = "Answer"
	ComponentLLM       = "LLM"
	ComponentRetrieval = "Retrieval"
	ComponentSwitch    = "Switch"
)

func init() {
	RegisterNodeFactory(ComponentStart, newStart)
	RegisterNodeFactory(ComponentAnswer, newAnswer)
	RegisterNodeFactory(ComponentLLM, newLLM)
	RegisterNodeFactory(ComponentRetrieval, newRetrieval)
	RegisterNodeFactory(ComponentSwitch, newSwitch)
}

// ---- Start ---------------------------------------------------------------
//
// The graph entry. It echoes the runtime request (query / files) into its
// own outputs so downstream templates can use {start_id@query}. sys.* is
// populated by the runtime itself; Start is intentionally a no-op shell
// that only materialises the request as node output.

type startNode struct{}

func newStart(params map[string]any, deps Deps) (Node, error) { return &startNode{}, nil }

func (n *startNode) Invoke(ctx context.Context, inputs map[string]any) (map[string]any, error) {
	out := map[string]any{}
	if v, ok := inputs["query"]; ok {
		out["query"] = v
	}
	if v, ok := inputs["files"]; ok {
		out["files"] = v
	}
	return out, nil
}

// ---- Answer --------------------------------------------------------------

type answerNode struct{ template string }

func newAnswer(params map[string]any, deps Deps) (Node, error) {
	t, err := strParam("Answer", "template", params, true)
	if err != nil {
		return nil, err
	}
	return &answerNode{template: t}, nil
}

func (n *answerNode) Invoke(ctx context.Context, inputs map[string]any) (map[string]any, error) {
	st, err := StateFromInputs(inputs)
	if err != nil {
		return nil, err
	}
	answer, err := Render(n.template, st)
	if err != nil {
		return nil, fmt.Errorf("workflow Answer: %w", err)
	}
	return map[string]any{"answer": answer}, nil
}

// ---- LLM -----------------------------------------------------------------

type llmNode struct {
	prompt        string
	systemPrompt  string
	model         string
	temperature   float64
	maxTokens     int
	llm           LLMFunc
}

func newLLM(params map[string]any, deps Deps) (Node, error) {
	prompt, err := strParam("LLM", "prompt", params, true)
	if err != nil {
		return nil, err
	}
	systemPrompt, err := strParam("LLM", "system_prompt", params, false)
	if err != nil {
		return nil, err
	}
	model, _ := params["model"].(string)
	temp := 0.0
	if v, ok := params["temperature"]; ok {
		temp, err = toFloat("LLM", "temperature", v)
		if err != nil {
			return nil, err
		}
	}
	maxTokens := 0
	if v, ok := params["max_tokens"]; ok {
		maxTokens, err = toInt("LLM", "max_tokens", v)
		if err != nil {
			return nil, err
		}
	}
	return &llmNode{prompt: prompt, systemPrompt: systemPrompt, model: model, temperature: temp, maxTokens: maxTokens, llm: deps.LLMFunc}, nil
}

func (n *llmNode) Invoke(ctx context.Context, inputs map[string]any) (map[string]any, error) {
	if n.llm == nil {
		return nil, fmt.Errorf("workflow LLM: no LLMFunc injected (compile-time Deps.LLMFunc is nil)")
	}
	st, err := StateFromInputs(inputs)
	if err != nil {
		return nil, err
	}
	prompt, err := Render(n.prompt, st)
	if err != nil {
		return nil, fmt.Errorf("workflow LLM: %w", err)
	}
	system := ""
	if n.systemPrompt != "" {
		if system, err = Render(n.systemPrompt, st); err != nil {
			return nil, fmt.Errorf("workflow LLM system_prompt: %w", err)
		}
	}
	content, err := n.llm(ctx, LLMRequest{
		Prompt:       prompt,
		SystemPrompt: system,
		Model:        n.model,
		Temperature:  n.temperature,
		MaxTokens:    n.maxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("workflow LLM: llm call failed: %w", err)
	}
	return map[string]any{"content": content}, nil
}

// ---- Retrieval -----------------------------------------------------------

type retrievalNode struct {
	query           string
	kbIDs           []string
	topK            int
	vectorThresh    float64
	keywordThresh   float64
	useRerank       bool
	rerankModelID   string
	retr            RetrievalFunc
}

func newRetrieval(params map[string]any, deps Deps) (Node, error) {
	query, err := strParam("Retrieval", "query", params, true)
	if err != nil {
		return nil, err
	}
	kbIDs, err := strSliceParam("Retrieval", "kb_ids", params)
	if err != nil {
		return nil, err
	}
	topK := 0
	if v, ok := params["top_k"]; ok {
		topK, err = toInt("Retrieval", "top_k", v)
		if err != nil {
			return nil, err
		}
	}
	// similarity_threshold is the RAGFlow-compatible alias; an explicit
	// vector_threshold wins when both are present.
	vecThresh := 0.0
	if v, ok := params["similarity_threshold"]; ok {
		if vecThresh, err = toFloat("Retrieval", "similarity_threshold", v); err != nil {
			return nil, err
		}
	}
	if v, ok := params["vector_threshold"]; ok {
		if vecThresh, err = toFloat("Retrieval", "vector_threshold", v); err != nil {
			return nil, err
		}
	}
	kwThresh := 0.0
	if v, ok := params["keyword_threshold"]; ok {
		if kwThresh, err = toFloat("Retrieval", "keyword_threshold", v); err != nil {
			return nil, err
		}
	}
	useRerank := false
	if v, ok := params["use_rerank"]; ok {
		if useRerank, err = toBool("Retrieval", "use_rerank", v); err != nil {
			return nil, err
		}
	}
	rerankModel, _ := params["rerank_model_id"].(string)
	if useRerank && strings.TrimSpace(rerankModel) == "" {
		return nil, fmt.Errorf("workflow Retrieval: use_rerank=true requires rerank_model_id")
	}
	return &retrievalNode{
		query: query, kbIDs: kbIDs, topK: topK,
		vectorThresh: vecThresh, keywordThresh: kwThresh,
		useRerank: useRerank, rerankModelID: rerankModel,
		retr: deps.RetrievalFunc,
	}, nil
}

func (n *retrievalNode) Invoke(ctx context.Context, inputs map[string]any) (map[string]any, error) {
	if n.retr == nil {
		return nil, fmt.Errorf("workflow Retrieval: no RetrievalFunc injected (compile-time Deps.RetrievalFunc is nil)")
	}
	st, err := StateFromInputs(inputs)
	if err != nil {
		return nil, err
	}
	query, err := Render(n.query, st)
	if err != nil {
		return nil, fmt.Errorf("workflow Retrieval: %w", err)
	}
	res, err := n.retr(ctx, RetrievalRequest{
		Query: query, KBIDs: n.kbIDs, TopK: n.topK,
		VectorThreshold:  n.vectorThresh,
		KeywordThreshold: n.keywordThresh,
		UseRerank:       n.useRerank,
		RerankModelID:   n.rerankModelID,
	})
	if err != nil {
		return nil, fmt.Errorf("workflow Retrieval: retrieval failed: %w", err)
	}
	chunks := []map[string]any{}
	docAggs := []map[string]any{}
	if res != nil {
		if res.Chunks != nil {
			chunks = res.Chunks
		}
		if res.DocAggs != nil {
			docAggs = res.DocAggs
		}
	}
	return map[string]any{"chunks": chunks, "doc_aggs": docAggs}, nil
}

// ---- Switch --------------------------------------------------------------

// SwitchCase is one equality rule: when the rendered value equals Value the
// branch routes to node id To.
type SwitchCase struct {
	Value string `json:"value"`
	To    string `json:"to"`
}

type switchNode struct {
	value     string
	cases     []SwitchCase
	defaultTo string
}

func newSwitch(params map[string]any, deps Deps) (Node, error) {
	value, err := strParam("Switch", "value", params, true)
	if err != nil {
		return nil, err
	}
	cases, err := switchCases(params)
	if err != nil {
		return nil, err
	}
	defaultTo, _ := params["default"].(string)
	return &switchNode{value: value, cases: cases, defaultTo: defaultTo}, nil
}

func (n *switchNode) Invoke(ctx context.Context, inputs map[string]any) (map[string]any, error) {
	st, err := StateFromInputs(inputs)
	if err != nil {
		return nil, err
	}
	value, err := Render(n.value, st)
	if err != nil {
		return nil, fmt.Errorf("workflow Switch: %w", err)
	}
	for _, c := range n.cases {
		if c.Value == value {
			return map[string]any{RouteOutputKey: c.To, "matched": value}, nil
		}
	}
	if n.defaultTo == "" {
		return nil, fmt.Errorf("workflow Switch: value %q matched no case and no default target is set", value)
	}
	return map[string]any{RouteOutputKey: n.defaultTo, "matched": value}, nil
}

// RouteTargets returns the set of possible downstream node ids for a
// component, used at compile time to build an eino branch instead of plain
// edges. Non-routing components return nil.
func RouteTargets(componentName string, params map[string]any) ([]string, error) {
	if !isSwitch(componentName) {
		return nil, nil
	}
	cases, err := switchCases(params)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, c := range cases {
		if c.To == "" {
			return nil, fmt.Errorf("workflow Switch: case %q has empty target", c.Value)
		}
		set[c.To] = true
	}
	if d, _ := params["default"].(string); d != "" {
		set[d] = true
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}

func isSwitch(componentName string) bool {
	return canonical(componentName) == canonical(ComponentSwitch)
}

// switchCases accepts the JSON shape ([]any of map[string]any) as well as
// an already-typed []SwitchCase (tests / programmatic DSLs).
func switchCases(params map[string]any) ([]SwitchCase, error) {
	raw, ok := params["cases"]
	if !ok || raw == nil {
		return nil, fmt.Errorf("workflow Switch: missing required param \"cases\"")
	}
	switch list := raw.(type) {
	case []SwitchCase:
		return list, nil
	case []any:
		out := make([]SwitchCase, 0, len(list))
		for i, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("workflow Switch: cases[%d] must be an object, got %T", i, item)
			}
			c := SwitchCase{}
			if v, ok := m["value"]; ok {
				c.Value, _ = v.(string)
			}
			if v, ok := m["to"]; ok {
				c.To, _ = v.(string)
			}
			if c.To == "" {
				return nil, fmt.Errorf("workflow Switch: cases[%d] has empty \"to\"", i)
			}
			out = append(out, c)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("workflow Switch: param \"cases\" must be a list, got %T", raw)
	}
}

// ---- param coercion helpers (JSON gives float64 / []any) ------------------

func strSliceParam(nodeLabel, param string, params map[string]any) ([]string, error) {
	raw, ok := params[param]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		if typed, isStrSlice := raw.([]string); isStrSlice {
			return typed, nil
		}
		return nil, fmt.Errorf("workflow %s: param %q must be a list of strings, got %T", nodeLabel, param, raw)
	}
	out := make([]string, 0, len(list))
	for i, v := range list {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("workflow %s: param %q[%d] must be a string, got %T", nodeLabel, param, i, v)
		}
		out = append(out, s)
	}
	return out, nil
}

func toFloat(nodeLabel, param string, v any) (float64, error) {
	switch t := v.(type) {
	case float64:
		return t, nil
	case int:
		return float64(t), nil
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return 0, fmt.Errorf("workflow %s: param %q must be a number, got %q", nodeLabel, param, t)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("workflow %s: param %q must be a number, got %T", nodeLabel, param, v)
	}
}

func toInt(nodeLabel, param string, v any) (int, error) {
	f, err := toFloat(nodeLabel, param, v)
	if err != nil {
		return 0, err
	}
	return int(f), nil
}

// toBool coerces a JSON boolean-ish param (true/false, or the strings
// "true"/"false") — RAGFlow DSL exports sometimes stringify flags.
func toBool(nodeLabel, param string, v any) (bool, error) {
	switch t := v.(type) {
	case bool:
		return t, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
	}
	return false, fmt.Errorf("workflow %s: param %q must be a boolean, got %T", nodeLabel, param, v)
}
