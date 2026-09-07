package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Phase-3 advanced components. QuestionClassifier and ParameterExtractor
// are pure LLMFunc consumers (prompt engineering + strict reply parsing —
// no new platform adapters); WebSearch takes an injected SearchFunc so the
// engine stays network-free.
const (
	ComponentWebSearch          = "WebSearch"
	ComponentQuestionClassifier = "QuestionClassifier"
	ComponentParameterExtractor = "ParameterExtractor"
	// ComponentIteration names the loop node. Unlike the others it has NO
	// factory here: the engine compiler builds it (it needs the recursively
	// compiled loop body, which only the root package can produce).
	ComponentIteration = "Iteration"
)

func init() {
	RegisterNodeFactory(ComponentWebSearch, newWebSearch)
	RegisterNodeFactory(ComponentQuestionClassifier, newQuestionClassifier)
	RegisterNodeFactory(ComponentParameterExtractor, newParameterExtractor)
	// Sentinel only: makes the name known to DSL validation. The engine
	// compiler intercepts Iteration before nodes.New (it needs the
	// recursively compiled loop body); reaching this factory means the
	// compiler special case was bypassed.
	RegisterNodeFactory(ComponentIteration, func(_ map[string]any, _ Deps) (Node, error) {
		return nil, fmt.Errorf("workflow Iteration: constructed by the compiler — factory unreachable")
	})
}

// ---- WebSearch ------------------------------------------------------------

// WebSearchRequest is the rendered input handed to an injected SearchFunc.
type WebSearchRequest struct {
	Query      string
	ProviderID string // empty = tenant default (resolved by the adapter)
	MaxResults int    // 0 = adapter default
}

// WebSearchResultItem is one search hit stored into node outputs.
type WebSearchResultItem struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

// WebSearchFunc executes one search through the platform's configured
// (admin-managed, intranet-capable) providers. Injected by the compiler.
type WebSearchFunc func(ctx context.Context, req WebSearchRequest) ([]WebSearchResultItem, error)

type webSearchNode struct {
	query      string
	providerID string
	maxResults int
	search     WebSearchFunc
}

func newWebSearch(params map[string]any, deps Deps) (Node, error) {
	query, err := strParam(ComponentWebSearch, "query", params, true)
	if err != nil {
		return nil, err
	}
	providerID, _ := params["provider_id"].(string)
	maxResults := 0
	if v, ok := params["max_results"]; ok && v != nil {
		if maxResults, err = toInt(ComponentWebSearch, "max_results", v); err != nil {
			return nil, err
		}
	}
	return &webSearchNode{query: query, providerID: providerID, maxResults: maxResults, search: deps.WebSearchFunc}, nil
}

func (n *webSearchNode) Invoke(ctx context.Context, inputs map[string]any) (map[string]any, error) {
	if n.search == nil {
		return nil, fmt.Errorf("workflow WebSearch: no SearchFunc injected")
	}
	st, err := StateFromInputs(inputs)
	if err != nil {
		return nil, err
	}
	query, err := Render(n.query, st)
	if err != nil {
		return nil, fmt.Errorf("workflow WebSearch: %w", err)
	}
	items, err := n.search(ctx, WebSearchRequest{Query: query, ProviderID: n.providerID, MaxResults: n.maxResults})
	if err != nil {
		return nil, fmt.Errorf("workflow WebSearch: search failed: %w", err)
	}
	rows := make([]map[string]any, 0, len(items))
	for _, item := range items {
		rows = append(rows, map[string]any{"title": item.Title, "url": item.URL, "snippet": item.Snippet})
	}
	return map[string]any{"results": rows, "result_count": len(rows)}, nil
}

// ---- QuestionClassifier ---------------------------------------------------

// ClassifierClass is one routing class: the LLM picks a class, the branch
// routes to its target node.
type ClassifierClass struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	To          string `json:"to"`
}

type questionClassifierNode struct {
	query       string
	classes     []ClassifierClass
	defaultTo   string
	model       string
	temperature float64
	llm         LLMFunc
}

func newQuestionClassifier(params map[string]any, deps Deps) (Node, error) {
	query, err := strParam(ComponentQuestionClassifier, "query", params, true)
	if err != nil {
		return nil, err
	}
	classes, err := classifierClasses(params)
	if err != nil {
		return nil, err
	}
	if len(classes) < 2 {
		return nil, fmt.Errorf("workflow QuestionClassifier: needs at least 2 classes, got %d", len(classes))
	}
	defaultTo, _ := params["default"].(string)
	model, _ := params["model"].(string)
	temperature, err := temperatureParam(ComponentQuestionClassifier, params)
	if err != nil {
		return nil, err
	}
	return &questionClassifierNode{
		query: query, classes: classes, defaultTo: defaultTo,
		model: model, temperature: temperature, llm: deps.LLMFunc,
	}, nil
}

func (n *questionClassifierNode) Invoke(ctx context.Context, inputs map[string]any) (map[string]any, error) {
	if n.llm == nil {
		return nil, fmt.Errorf("workflow QuestionClassifier: no LLMFunc injected")
	}
	st, err := StateFromInputs(inputs)
	if err != nil {
		return nil, err
	}
	query, err := Render(n.query, st)
	if err != nil {
		return nil, fmt.Errorf("workflow QuestionClassifier: %w", err)
	}

	var b strings.Builder
	b.WriteString("Classify the user question into exactly ONE of the following classes. ")
	b.WriteString("Answer with the class name ONLY, no explanation, no punctuation.\n\nClasses:\n")
	for _, c := range n.classes {
		b.WriteString(fmt.Sprintf("- %s", c.Name))
		if c.Description != "" {
			b.WriteString(fmt.Sprintf(": %s", c.Description))
		}
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf("\nQuestion: %s\n\nClass name:", query))

	reply, err := n.llm(ctx, LLMRequest{Prompt: b.String(), Model: n.model, Temperature: n.temperature})
	if err != nil {
		return nil, fmt.Errorf("workflow QuestionClassifier: %w", err)
	}
	matched := matchClassName(reply, n.classes)
	if matched == nil {
		if n.defaultTo == "" {
			return nil, fmt.Errorf("workflow QuestionClassifier: model reply %q matched no class and no default target is set", strings.TrimSpace(reply))
		}
		return map[string]any{RouteOutputKey: n.defaultTo, "class": ""}, nil
	}
	return map[string]any{RouteOutputKey: matched.To, "class": matched.Name}, nil
}

// wordBoundary builds a case-insensitive word-boundary matcher for a
// class name (second-chance matching after exact equality).
func wordBoundary(name string) *regexp.Regexp {
	escaped := regexp.QuoteMeta(strings.TrimSpace(name))
	re, err := regexp.Compile(`(?i)(^|[^a-z0-9_])` + escaped + `([^a-z0-9_]|$)`)
	if err != nil {
		return nil
	}
	return re
}

// matchClassName finds the class the reply names. Tolerant of wrapping
// prose ("The class is X.") and case drift, but word-boundary anchored so
// short names cannot substring-match unrelated prose.
func matchClassName(reply string, classes []ClassifierClass) *ClassifierClass {
	trimmed := strings.ToLower(strings.TrimSpace(reply))
	for i := range classes {
		name := strings.ToLower(strings.TrimSpace(classes[i].Name))
		if name != "" && trimmed == name {
			return &classes[i]
		}
	}
	for i := range classes {
		re := wordBoundary(classes[i].Name)
		if re != nil && re.MatchString(trimmed) {
			return &classes[i]
		}
	}
	return nil
}

func classifierClasses(params map[string]any) ([]ClassifierClass, error) {
	raw, ok := params["classes"]
	if !ok || raw == nil {
		return nil, fmt.Errorf("workflow QuestionClassifier: missing required param \"classes\"")
	}
	list, ok := raw.([]any)
	if !ok {
		if typed, isTyped := raw.([]ClassifierClass); isTyped {
			return typed, nil
		}
		return nil, fmt.Errorf("workflow QuestionClassifier: param \"classes\" must be a list, got %T", raw)
	}
	out := make([]ClassifierClass, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("workflow QuestionClassifier: classes[%d] must be an object, got %T", i, item)
		}
		c := ClassifierClass{}
		if v, ok := m["name"]; ok {
			c.Name, _ = v.(string)
		}
		if v, ok := m["description"]; ok {
			c.Description, _ = v.(string)
		}
		if v, ok := m["to"]; ok {
			c.To, _ = v.(string)
		}
		if c.Name == "" || c.To == "" {
			return nil, fmt.Errorf("workflow QuestionClassifier: classes[%d] needs non-empty \"name\" and \"to\"", i)
		}
		out = append(out, c)
	}
	return out, nil
}

// ---- ParameterExtractor ---------------------------------------------------

// ExtractorParam declares one structured parameter the LLM must extract.
type ExtractorParam struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type"` // string | number | boolean
	Required    bool   `json:"required,omitempty"`
}

type parameterExtractorNode struct {
	query       string
	params      []ExtractorParam
	model       string
	temperature float64
	llm         LLMFunc
}

func newParameterExtractor(params map[string]any, deps Deps) (Node, error) {
	query, err := strParam(ComponentParameterExtractor, "query", params, true)
	if err != nil {
		return nil, err
	}
	decls, err := extractorParams(params)
	if err != nil {
		return nil, err
	}
	if len(decls) == 0 {
		return nil, fmt.Errorf("workflow ParameterExtractor: needs at least one parameter")
	}
	model, _ := params["model"].(string)
	temperature, err := temperatureParam(ComponentParameterExtractor, params)
	if err != nil {
		return nil, err
	}
	return &parameterExtractorNode{query: query, params: decls, model: model, temperature: temperature, llm: deps.LLMFunc}, nil
}

// jsonishTail grabs the last {...} block of the reply (models wrap JSON in
// prose despite instructions; code fences are stripped first).
var jsonishTail = regexp.MustCompile(`(?s)\{.*\}`)

func (n *parameterExtractorNode) Invoke(ctx context.Context, inputs map[string]any) (map[string]any, error) {
	if n.llm == nil {
		return nil, fmt.Errorf("workflow ParameterExtractor: no LLMFunc injected")
	}
	st, err := StateFromInputs(inputs)
	if err != nil {
		return nil, err
	}
	text, err := Render(n.query, st)
	if err != nil {
		return nil, fmt.Errorf("workflow ParameterExtractor: %w", err)
	}

	var b strings.Builder
	b.WriteString("Extract structured parameters from the input text. ")
	b.WriteString("Reply with ONLY a JSON object keyed by parameter name. ")
	b.WriteString("Use null for values not present. No markdown, no explanation.\n\nParameters:\n")
	for _, p := range n.params {
		b.WriteString(fmt.Sprintf("- %s (%s)", p.Name, p.Type))
		if p.Description != "" {
			b.WriteString(fmt.Sprintf(": %s", p.Description))
		}
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf("\nInput text: %s\n\nJSON:", text))

	reply, err := n.llm(ctx, LLMRequest{Prompt: b.String(), Model: n.model, Temperature: n.temperature})
	if err != nil {
		return nil, fmt.Errorf("workflow ParameterExtractor: %w", err)
	}

	raw := extractJSON(reply)
	if raw == nil {
		return nil, fmt.Errorf("workflow ParameterExtractor: model reply contained no JSON object: %.120s", strings.TrimSpace(reply))
	}
	out := map[string]any{}
	for _, p := range n.params {
		v, present := raw[p.Name]
		if !present || v == nil {
			if p.Required {
				return nil, fmt.Errorf("workflow ParameterExtractor: required parameter %q missing from model reply", p.Name)
			}
			out[p.Name] = nil
			continue
		}
		coerced, cerr := coerceExtracted(p.Name, p.Type, v)
		if cerr != nil {
			return nil, fmt.Errorf("workflow ParameterExtractor: %w", cerr)
		}
		out[p.Name] = coerced
	}
	return out, nil
}

func extractJSON(reply string) map[string]any {
	cleaned := strings.TrimSpace(reply)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	if strings.HasPrefix(cleaned, "{") {
		var m map[string]any
		if json.Unmarshal([]byte(cleaned), &m) == nil {
			return m
		}
	}
	if tail := jsonishTail.FindString(cleaned); tail != "" {
		var m map[string]any
		if json.Unmarshal([]byte(tail), &m) == nil {
			return m
		}
	}
	return nil
}

func coerceExtracted(name, typ string, v any) (any, error) {
	switch typ {
	case "number":
		switch t := v.(type) {
		case float64:
			return t, nil
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
			if err != nil {
				return nil, fmt.Errorf("parameter %q is not a number: %q", name, t)
			}
			return f, nil
		}
		return nil, fmt.Errorf("parameter %q is not a number: %v", name, v)
	case "boolean":
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
		return nil, fmt.Errorf("parameter %q is not a boolean: %v", name, v)
	default: // string
		switch t := v.(type) {
		case string:
			return t, nil
		default:
			return fmt.Sprintf("%v", t), nil
		}
	}
}

func extractorParams(params map[string]any) ([]ExtractorParam, error) {
	raw, ok := params["parameters"]
	if !ok || raw == nil {
		return nil, fmt.Errorf("workflow ParameterExtractor: missing required param \"parameters\"")
	}
	list, ok := raw.([]any)
	if !ok {
		if typed, isTyped := raw.([]ExtractorParam); isTyped {
			return typed, nil
		}
		return nil, fmt.Errorf("workflow ParameterExtractor: param \"parameters\" must be a list, got %T", raw)
	}
	out := make([]ExtractorParam, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("workflow ParameterExtractor: parameters[%d] must be an object, got %T", i, item)
		}
		p := ExtractorParam{}
		if v, ok := m["name"]; ok {
			p.Name, _ = v.(string)
		}
		if v, ok := m["description"]; ok {
			p.Description, _ = v.(string)
		}
		if v, ok := m["type"]; ok {
			p.Type, _ = v.(string)
		}
		switch strings.ToLower(strings.TrimSpace(p.Type)) {
		case "string", "number", "boolean":
			p.Type = strings.ToLower(strings.TrimSpace(p.Type))
		case "":
			p.Type = "string"
		default:
			return nil, fmt.Errorf("workflow ParameterExtractor: parameters[%d] has unknown type %q (string|number|boolean)", i, p.Type)
		}
		if v, ok := m["required"]; ok {
			p.Required, _ = v.(bool)
		}
		if p.Name == "" {
			return nil, fmt.Errorf("workflow ParameterExtractor: parameters[%d] has an empty \"name\"", i)
		}
		out = append(out, p)
	}
	return out, nil
}

// temperatureParam reads params.temperature with the 0-2 clamp shared by
// the LLM node; absent = 0.2 (extraction/classification wants low variance).
func temperatureParam(nodeLabel string, params map[string]any) (float64, error) {
	raw, ok := params["temperature"]
	if !ok || raw == nil {
		return 0.2, nil
	}
	f, err := toFloat(nodeLabel, "temperature", raw)
	if err != nil {
		return 0, err
	}
	if f < 0 {
		return 0, nil
	}
	if f > 2 {
		return 2, nil
	}
	return f, nil
}
