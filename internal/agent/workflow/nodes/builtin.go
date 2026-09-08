package nodes

import (
	"context"
	"fmt"
	"regexp"
	"slices"
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
//
// Input form (Dify-style): params.fields declares typed form fields
// (name / label / type / required / default / options). The run request's
// `inputs` map is materialised into outputs by field name — missing values
// fall back to the declared default. Required enforcement lives at the API
// layer (the service inspects the compiled Start params), not here: the
// engine stays a pure executor.

type StartField struct {
	Name     string   `json:"name"`
	Label    string   `json:"label,omitempty"`
	Type     string   `json:"type"` // text | paragraph | number | select
	Required bool     `json:"required,omitempty"`
	Default  string   `json:"default,omitempty"`
	Options  []string `json:"options,omitempty"` // select choices
}

type startNode struct {
	fields []StartField
}

func newStart(params map[string]any, deps Deps) (Node, error) {
	fields, err := startFields(params)
	if err != nil {
		return nil, err
	}
	return &startNode{fields: fields}, nil
}

func (n *startNode) Invoke(ctx context.Context, inputs map[string]any) (map[string]any, error) {
	out := map[string]any{}
	if v, ok := inputs["query"]; ok {
		out["query"] = v
	}
	if v, ok := inputs["files"]; ok {
		out["files"] = v
	}
	form, _ := inputs["inputs"].(map[string]any)
	for _, f := range n.fields {
		if f.Name == "" {
			continue
		}
		if v, ok := form[f.Name]; ok && v != nil {
			out[f.Name] = v
			continue
		}
		if f.Default != "" {
			out[f.Name] = f.Default
		}
	}
	return out, nil
}

// startFields parses params.fields, accepting the JSON shape ([]any of
// maps) and tolerant of an absent/empty list.
func startFields(params map[string]any) ([]StartField, error) {
	raw, ok := params["fields"]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("workflow Start: param \"fields\" must be a list, got %T", raw)
	}
	out := make([]StartField, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("workflow Start: fields[%d] must be an object, got %T", i, item)
		}
		f := StartField{}
		if v, ok := m["name"]; ok {
			f.Name, _ = v.(string)
		}
		if v, ok := m["label"]; ok {
			f.Label, _ = v.(string)
		}
		if v, ok := m["type"]; ok {
			f.Type, _ = v.(string)
		}
		f.Type = strings.ToLower(strings.TrimSpace(f.Type))
		if f.Type == "" {
			f.Type = "text"
		}
		switch f.Type {
		case "text", "paragraph", "number", "select":
		default:
			return nil, fmt.Errorf("workflow Start: fields[%d] has unknown type %q (text|paragraph|number|select)", i, f.Type)
		}
		if v, ok := m["required"]; ok {
			f.Required, _ = v.(bool)
		}
		if v, ok := m["default"]; ok {
			f.Default, _ = v.(string)
		}
		if rawOpts, ok := m["options"]; ok && rawOpts != nil {
			opts, ok := rawOpts.([]any)
			if !ok {
				return nil, fmt.Errorf("workflow Start: fields[%d].options must be a list, got %T", i, rawOpts)
			}
			for _, o := range opts {
				if s, ok := o.(string); ok {
					f.Options = append(f.Options, s)
				}
			}
		}
		if f.Name == "" {
			return nil, fmt.Errorf("workflow Start: fields[%d] has an empty \"name\"", i)
		}
		out = append(out, f)
	}
	return out, nil
}

// StartFieldsOf parses params.fields via startFields; exported for the
// service layer's required-field validation (same coercion rules as the
// engine, one source of truth).
func StartFieldsOf(params map[string]any) ([]StartField, error) {
	return startFields(params)
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
	prompt         string
	systemPrompt   string
	model          string
	temperature    float64
	temperatureSet bool
	maxTokens      int
	thinking       *bool
	llm            LLMFunc
	llmStream      LLMStreamFunc
	onDelta        func(string)
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
	tempSet := false
	if v, ok := params["temperature"]; ok && v != nil {
		if temp, err = toFloat("LLM", "temperature", v); err != nil {
			return nil, err
		}
		tempSet = true
	}
	maxTokens := 0
	if v, ok := params["max_tokens"]; ok {
		maxTokens, err = toInt("LLM", "max_tokens", v)
		if err != nil {
			return nil, err
		}
	}
	// thinking: tri-state — absent = provider/model default, false = disable
	// extended thinking, true = request it explicitly.
	var thinking *bool
	if v, ok := params["thinking"]; ok && v != nil {
		b, terr := toBool("LLM", "thinking", v)
		if terr != nil {
			return nil, terr
		}
		thinking = &b
	}
	return &llmNode{prompt: prompt, systemPrompt: systemPrompt, model: model, temperature: temp, temperatureSet: tempSet, maxTokens: maxTokens, thinking: thinking, llm: deps.LLMFunc, llmStream: deps.LLMStreamFunc, onDelta: deps.OnDelta}, nil
}

func (n *llmNode) Invoke(ctx context.Context, inputs map[string]any) (map[string]any, error) {
	if n.llm == nil && n.llmStream == nil {
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
	req := LLMRequest{
		Prompt:         prompt,
		SystemPrompt:   system,
		Model:          n.model,
		Temperature:    n.temperature,
		TemperatureSet: n.temperatureSet,
		MaxTokens:      n.maxTokens,
		Thinking:       n.thinking,
	}
	// Streaming path: tokens flow to the delta sink while the full text
	// still returns as the recorded output (single source of truth).
	var content string
	if n.llmStream != nil {
		onDelta := n.onDelta // nil sink = stream without delta emission
		content, err = n.llmStream(ctx, req, onDelta)
	} else {
		content, err = n.llm(ctx, req)
	}
	if err != nil {
		return nil, fmt.Errorf("workflow LLM: llm call failed: %w", err)
	}
	return map[string]any{"content": content}, nil
}

// ---- Retrieval -----------------------------------------------------------

type retrievalNode struct {
	query         string
	kbIDs         []string
	topK          int
	vectorThresh  float64
	keywordThresh float64
	useRerank     bool
	rerankModelID string
	retr          RetrievalFunc
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
		UseRerank:        n.useRerank,
		RerankModelID:    n.rerankModelID,
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

// SwitchCondition is one comparison inside a case group. Ref and Value are
// templates ({node@param} / {sys.*} / {env.*} render normally), so a case
// can compare a node output against a literal or another output.
type SwitchCondition struct {
	Ref   string `json:"ref"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

// SwitchCase is one routing rule: a group of conditions joined by Logic,
// routing to node id To when the group evaluates true. The legacy shape
// ({value, to} + the node-level `value` template, pure string equality)
// migrates to Conditions=[{Ref: <node value>, Op: "eq", Value: <case value>}]
// at build time — old DSLs keep running unchanged.
type SwitchCase struct {
	Conditions []SwitchCondition `json:"conditions,omitempty"`
	Logic      string            `json:"logic,omitempty"` // "and" (default) | "or"
	To         string            `json:"to"`
	// Legacy equality form (pre-conditions DSLs). Ignored when Conditions
	// is non-empty.
	Value string `json:"value,omitempty"`
}

// Condition operators (Dify IF/ELSE subset). Numeric ops fail the node on
// non-numeric operands (loud, not silently-false).
const (
	OpEq          = "eq"
	OpNe          = "ne"
	OpContains    = "contains"
	OpNotContains = "not_contains"
	OpStartsWith  = "starts_with"
	OpEndsWith    = "ends_with"
	OpEmpty       = "empty"
	OpNotEmpty    = "not_empty"
	OpGt          = "gt"
	OpGte         = "gte"
	OpLt          = "lt"
	OpLte         = "lte"
	OpRegex       = "regex"
	OpIn          = "in"
	OpNotIn       = "not_in"
)

var conditionOps = map[string]bool{
	OpEq: true, OpNe: true, OpContains: true, OpNotContains: true,
	OpStartsWith: true, OpEndsWith: true, OpEmpty: true, OpNotEmpty: true,
	OpGt: true, OpGte: true, OpLt: true, OpLte: true,
	OpRegex: true, OpIn: true, OpNotIn: true,
}

// opsNeedingNoValue: operators that ignore the right-hand Value.
var opsNeedingNoValue = map[string]bool{OpEmpty: true, OpNotEmpty: true}

type switchNode struct {
	cases     []SwitchCase
	defaultTo string
	regexes   map[int]*regexp.Regexp // case index → compiled pattern (Op regex)
}

func newSwitch(params map[string]any, deps Deps) (Node, error) {
	cases, err := switchCases(params)
	if err != nil {
		return nil, err
	}
	// Legacy migration: cases carrying the old {value, to} shape compare
	// against the node-level `value` template with eq.
	legacyValue, _ := params["value"].(string)
	for i := range cases {
		if len(cases[i].Conditions) == 0 {
			if legacyValue == "" {
				return nil, fmt.Errorf("workflow Switch: cases[%d] uses the legacy equality form which requires the node-level \"value\" param", i)
			}
			cases[i].Conditions = []SwitchCondition{{Ref: legacyValue, Op: OpEq, Value: cases[i].Value}}
		}
	}
	// Validate operators / compile regexes up front so bad params fail at
	// compile time, not mid-run.
	regexes := map[int]*regexp.Regexp{}
	for i, c := range cases {
		if len(c.Conditions) == 0 {
			return nil, fmt.Errorf("workflow Switch: cases[%d] has no conditions", i)
		}
		if c.Logic != "" && c.Logic != "and" && c.Logic != "or" {
			return nil, fmt.Errorf("workflow Switch: cases[%d] logic must be \"and\" or \"or\", got %q", i, c.Logic)
		}
		for j, cond := range c.Conditions {
			if cond.Ref == "" {
				return nil, fmt.Errorf("workflow Switch: cases[%d].conditions[%d] has an empty \"ref\"", i, j)
			}
			if !conditionOps[cond.Op] {
				return nil, fmt.Errorf("workflow Switch: cases[%d].conditions[%d] has unknown operator %q", i, j, cond.Op)
			}
			if cond.Op == OpRegex {
				re, rerr := regexp.Compile(cond.Value)
				if rerr != nil {
					return nil, fmt.Errorf("workflow Switch: cases[%d].conditions[%d] regex: %w", i, j, rerr)
				}
				regexes[i] = re
			}
		}
	}
	defaultTo, _ := params["default"].(string)
	return &switchNode{cases: cases, defaultTo: defaultTo, regexes: regexes}, nil
}

func (n *switchNode) Invoke(ctx context.Context, inputs map[string]any) (map[string]any, error) {
	st, err := StateFromInputs(inputs)
	if err != nil {
		return nil, err
	}
	for i, c := range n.cases {
		matched, left, err := n.evalCase(st, i, c)
		if err != nil {
			return nil, fmt.Errorf("workflow Switch: case %d: %w", i, err)
		}
		if matched {
			return map[string]any{RouteOutputKey: c.To, "matched": left}, nil
		}
	}
	if n.defaultTo == "" {
		return nil, fmt.Errorf("workflow Switch: no case matched and no default target is set")
	}
	return map[string]any{RouteOutputKey: n.defaultTo, "matched": ""}, nil
}

// evalCase renders and evaluates one case group. Returns whether the group
// holds plus the rendered left operand of its first condition (the
// "matched" output for traces). A ref that cannot resolve evaluates to
// false (not an error): cases are ordered alternatives, and a later case
// legitimately references vars that only exist on other execution paths.
// Type errors inside an operator (e.g. numeric compare on text) stay loud.
// AND short-circuits on the first false condition, OR on the first true.
func (n *switchNode) evalCase(st StateView, index int, c SwitchCase) (bool, string, error) {
	logicIsOr := c.Logic == "or"
	firstLeft := ""
	for j, cond := range c.Conditions {
		left, lerr := Render(cond.Ref, st)
		if lerr != nil {
			if logicIsOr {
				continue // unresolved ref cannot satisfy an OR member
			}
			return false, firstLeft, nil
		}
		if j == 0 {
			firstLeft = left
		}
		right := cond.Value
		if !opsNeedingNoValue[cond.Op] {
			var rerr error
			right, rerr = Render(cond.Value, st)
			if rerr != nil {
				if logicIsOr {
					continue
				}
				return false, firstLeft, nil
			}
		}
		holds, err := evalCondition(left, cond.Op, right, n.regexes[index])
		if err != nil {
			return false, "", fmt.Errorf("conditions[%d]: %w", j, err)
		}
		if logicIsOr && holds {
			return true, firstLeft, nil
		}
		if !logicIsOr && !holds {
			return false, firstLeft, nil
		}
	}
	return !logicIsOr, firstLeft, nil
}

func evalCondition(left, op, right string, re *regexp.Regexp) (bool, error) {
	switch op {
	case OpEq:
		return left == right, nil
	case OpNe:
		return left != right, nil
	case OpContains:
		return strings.Contains(left, right), nil
	case OpNotContains:
		return !strings.Contains(left, right), nil
	case OpStartsWith:
		return strings.HasPrefix(left, right), nil
	case OpEndsWith:
		return strings.HasSuffix(left, right), nil
	case OpEmpty:
		return strings.TrimSpace(left) == "", nil
	case OpNotEmpty:
		return strings.TrimSpace(left) != "", nil
	case OpGt, OpGte, OpLt, OpLte:
		ln, lerr := strconv.ParseFloat(strings.TrimSpace(left), 64)
		rn, rerr := strconv.ParseFloat(strings.TrimSpace(right), 64)
		if lerr != nil || rerr != nil {
			return false, fmt.Errorf("operator %q needs numeric operands, got %q and %q", op, left, right)
		}
		switch op {
		case OpGt:
			return ln > rn, nil
		case OpGte:
			return ln >= rn, nil
		case OpLt:
			return ln < rn, nil
		default:
			return ln <= rn, nil
		}
	case OpRegex:
		if re == nil {
			return false, fmt.Errorf("regex operator lost its compiled pattern")
		}
		return re.MatchString(left), nil
	case OpIn:
		return slices.Contains(splitList(right), left), nil
	case OpNotIn:
		return !slices.Contains(splitList(right), left), nil
	default:
		return false, fmt.Errorf("unknown operator %q", op)
	}
}

// splitList splits a comma-separated literal into trimmed members.
func splitList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// RouteTargets returns the set of possible downstream node ids for a
// routing component (Switch, QuestionClassifier), used at compile time to
// build an eino branch instead of plain edges. Non-routing components
// return nil.
func RouteTargets(componentName string, params map[string]any) ([]string, error) {
	if isSwitch(componentName) {
		return switchTargets(params)
	}
	if canonical(componentName) == canonical(ComponentQuestionClassifier) {
		return classifierTargets(params)
	}
	return nil, nil
}

func switchTargets(params map[string]any) ([]string, error) {
	cases, err := switchCases(params)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for i, c := range cases {
		if c.To == "" {
			return nil, fmt.Errorf("workflow Switch: cases[%d] has empty target", i)
		}
		set[c.To] = true
	}
	if d, _ := params["default"].(string); d != "" {
		set[d] = true
	}
	return sortedTargets(set), nil
}

func classifierTargets(params map[string]any) ([]string, error) {
	classes, err := classifierClasses(params)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, c := range classes {
		set[c.To] = true
	}
	if d, _ := params["default"].(string); d != "" {
		set[d] = true
	}
	return sortedTargets(set), nil
}

func sortedTargets(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// IsRoutingComponent reports whether the component routes by params
// (Switch, QuestionClassifier) — i.e. RouteTargets derives from its params
// rather than from an on_error policy.
func IsRoutingComponent(componentName string) bool {
	if isSwitch(componentName) {
		return true
	}
	return canonical(componentName) == canonical(ComponentQuestionClassifier)
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
			if v, ok := m["logic"]; ok {
				c.Logic, _ = v.(string)
			}
			if rawConds, ok := m["conditions"]; ok && rawConds != nil {
				condList, ok := rawConds.([]any)
				if !ok {
					return nil, fmt.Errorf("workflow Switch: cases[%d].conditions must be a list, got %T", i, rawConds)
				}
				for j, rawCond := range condList {
					cm, ok := rawCond.(map[string]any)
					if !ok {
						return nil, fmt.Errorf("workflow Switch: cases[%d].conditions[%d] must be an object, got %T", i, j, rawCond)
					}
					cond := SwitchCondition{}
					if v, ok := cm["ref"]; ok {
						cond.Ref, _ = v.(string)
					}
					if v, ok := cm["op"]; ok {
						cond.Op, _ = v.(string)
					}
					if v, ok := cm["value"]; ok {
						cond.Value, _ = v.(string)
					}
					c.Conditions = append(c.Conditions, cond)
				}
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
