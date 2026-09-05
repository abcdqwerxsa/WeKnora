package nodes

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// Additional single-pass component names (RAGFlow intranet set). Lookup
// stays case-insensitive like the core five.
const (
	ComponentTemplate           = "Template"
	ComponentVariableAggregator = "VariableAggregator"
	ComponentHTTP               = "HTTP"
	ComponentDataOps            = "DataOps"
)

func init() {
	RegisterNodeFactory(ComponentTemplate, newTemplate)
	RegisterNodeFactory(ComponentVariableAggregator, newVariableAggregator)
	RegisterNodeFactory(ComponentHTTP, newHTTP)
	RegisterNodeFactory(ComponentDataOps, newDataOps)
}

// ---- Template ------------------------------------------------------------
//
// Renders params.template against the run state, then applies the optional
// ordered ops list (semantics ported from RAGFlow's string_transform):
//
//	{op: "upper"|"lower"|"trim"}
//	{op: "replace", from: "...", to: "..."}          (literal replace-all)
//	{op: "regex_extract", pattern: "...", group: 1}  (first match; empty
//	                                                 pattern result keeps
//	                                                 the previous text)
type templateOp struct {
	op      string
	from    string
	to      string
	pattern *regexp.Regexp
	group   int
}

type templateNode struct {
	template string
	ops      []templateOp
}

func newTemplate(params map[string]any, deps Deps) (Node, error) {
	tmpl, err := strParam("Template", "template", params, true)
	if err != nil {
		return nil, err
	}
	ops, err := templateOps(params)
	if err != nil {
		return nil, err
	}
	return &templateNode{template: tmpl, ops: ops}, nil
}

func (n *templateNode) Invoke(ctx context.Context, inputs map[string]any) (map[string]any, error) {
	st, err := StateFromInputs(inputs)
	if err != nil {
		return nil, err
	}
	text, err := Render(n.template, st)
	if err != nil {
		return nil, fmt.Errorf("workflow Template: %w", err)
	}
	for i, op := range n.ops {
		next, err := applyTemplateOp(text, op)
		if err != nil {
			return nil, fmt.Errorf("workflow Template: op[%d] %s: %w", i, op.op, err)
		}
		text = next
	}
	return map[string]any{"text": text}, nil
}

func applyTemplateOp(text string, op templateOp) (string, error) {
	switch op.op {
	case "upper":
		return strings.ToUpper(text), nil
	case "lower":
		return strings.ToLower(text), nil
	case "trim":
		return strings.TrimSpace(text), nil
	case "replace":
		return strings.ReplaceAll(text, op.from, op.to), nil
	case "regex_extract":
		m := op.pattern.FindStringSubmatch(text)
		if m == nil {
			return "", fmt.Errorf("pattern %q matched nothing", op.pattern.String())
		}
		if op.group <= 0 || op.group >= len(m) {
			return m[0], nil
		}
		return m[op.group], nil
	default:
		return "", fmt.Errorf("unknown op %q", op.op)
	}
}

func templateOps(params map[string]any) ([]templateOp, error) {
	raw, ok := params["ops"]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("workflow Template: param \"ops\" must be a list, got %T", raw)
	}
	out := make([]templateOp, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("workflow Template: ops[%d] must be an object, got %T", i, item)
		}
		op, _ := m["op"].(string)
		t := templateOp{op: op}
		switch op {
		case "upper", "lower", "trim":
		case "replace":
			t.from, _ = m["from"].(string)
			t.to, _ = m["to"].(string)
		case "regex_extract":
			pat, _ := m["pattern"].(string)
			if pat == "" {
				return nil, fmt.Errorf("workflow Template: ops[%d] regex_extract requires \"pattern\"", i)
			}
			re, err := regexp.Compile(pat)
			if err != nil {
				return nil, fmt.Errorf("workflow Template: ops[%d] bad pattern: %w", i, err)
			}
			t.pattern = re
			if v, ok := m["group"]; ok {
				g, err := toInt("Template", "group", v)
				if err != nil {
					return nil, err
				}
				t.group = g
			}
		default:
			return nil, fmt.Errorf("workflow Template: ops[%d] unknown op %q", i, op)
		}
		out = append(out, t)
	}
	return out, nil
}

// ---- VariableAggregator ---------------------------------------------------
//
// Collects named upstream references into one {values} map. A reference
// whose source has not run (the not-taken branch of a Switch, a skipped
// aggregation member) is SKIPPED silently — this is the semantics that
// lets the node merge the outputs of whatever branch actually executed
// without the graph having to model it as a join.
type aggregatorVar struct {
	name string
	ref  string
}

type aggregatorNode struct {
	vars []aggregatorVar
}

func newVariableAggregator(params map[string]any, deps Deps) (Node, error) {
	raw, ok := params["variables"]
	if !ok || raw == nil {
		return nil, fmt.Errorf("workflow VariableAggregator: missing required param \"variables\"")
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("workflow VariableAggregator: param \"variables\" must be a list, got %T", raw)
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("workflow VariableAggregator: \"variables\" must not be empty")
	}
	vars := make([]aggregatorVar, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("workflow VariableAggregator: variables[%d] must be an object, got %T", i, item)
		}
		name, _ := m["name"].(string)
		ref, _ := m["ref"].(string)
		if name == "" || ref == "" {
			return nil, fmt.Errorf("workflow VariableAggregator: variables[%d] requires non-empty \"name\" and \"ref\"", i)
		}
		vars = append(vars, aggregatorVar{name: name, ref: ref})
	}
	return &aggregatorNode{vars: vars}, nil
}

func (n *aggregatorNode) Invoke(ctx context.Context, inputs map[string]any) (map[string]any, error) {
	st, err := StateFromInputs(inputs)
	if err != nil {
		return nil, err
	}
	values := map[string]any{}
	for _, v := range n.vars {
		val, lerr := lookupRef(v.ref, st)
		if lerr != nil {
			continue // not-executed branch: skip, not fail
		}
		values[v.name] = val
	}
	return map[string]any{"values": values, "collected": len(values)}, nil
}

// ---- HTTP -----------------------------------------------------------------
//
// Renders url / body_template against the run state and executes the call
// through the injected HTTPFunc. The engine stays network-free; the
// intranet-only policy lives in the adapter the service layer injects.

type httpNode struct {
	method  string
	url     string
	headers map[string]string
	body    string
	timeout int
	do      HTTPFunc
}

func newHTTP(params map[string]any, deps Deps) (Node, error) {
	urlTmpl, err := strParam("HTTP", "url", params, true)
	if err != nil {
		return nil, err
	}
	method, _ := params["method"].(string)
	if method == "" {
		method = "GET"
	}
	headers := map[string]string{}
	if raw, ok := params["headers"].(map[string]any); ok {
		for k, v := range raw {
			if s, ok := v.(string); ok {
				headers[k] = s
			}
		}
	}
	body, _ := params["body_template"].(string)
	timeout := 0
	if v, ok := params["timeout_seconds"]; ok {
		if timeout, err = toInt("HTTP", "timeout_seconds", v); err != nil {
			return nil, err
		}
	}
	return &httpNode{method: method, url: urlTmpl, headers: headers, body: body, timeout: timeout, do: deps.HTTPFunc}, nil
}

func (n *httpNode) Invoke(ctx context.Context, inputs map[string]any) (map[string]any, error) {
	if n.do == nil {
		return nil, fmt.Errorf("workflow HTTP: no HTTPFunc injected (compile-time Deps.HTTPFunc is nil)")
	}
	st, err := StateFromInputs(inputs)
	if err != nil {
		return nil, err
	}
	url, err := Render(n.url, st)
	if err != nil {
		return nil, fmt.Errorf("workflow HTTP url: %w", err)
	}
	body, err := Render(n.body, st)
	if err != nil {
		return nil, fmt.Errorf("workflow HTTP body_template: %w", err)
	}
	res, err := n.do(ctx, HTTPRequest{
		Method: strings.ToUpper(n.method), URL: url, Headers: n.headers,
		Body: body, TimeoutSeconds: n.timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("workflow HTTP: %w", err)
	}
	headers := res.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	return map[string]any{"status_code": res.StatusCode, "body": res.Body, "headers": headers}, nil
}

// ---- DataOps --------------------------------------------------------------

type dataOpsVar struct {
	name string
	ref  string
}

type dataOpsNode struct {
	sql        string
	vars       []dataOpsVar
	exec       DataOpsFunc
}

func newDataOps(params map[string]any, deps Deps) (Node, error) {
	sqlTmpl, err := strParam("DataOps", "sql", params, true)
	if err != nil {
		return nil, err
	}
	vars := []dataOpsVar{}
	if raw, ok := params["variables"].([]any); ok {
		for i, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("workflow DataOps: variables[%d] must be an object, got %T", i, item)
			}
			name, _ := m["name"].(string)
			ref, _ := m["ref"].(string)
			if name == "" || ref == "" {
				return nil, fmt.Errorf("workflow DataOps: variables[%d] requires non-empty \"name\" and \"ref\"", i)
			}
			vars = append(vars, dataOpsVar{name: name, ref: ref})
		}
	}
	return &dataOpsNode{sql: sqlTmpl, vars: vars, exec: deps.DataOpsFunc}, nil
}

func (n *dataOpsNode) Invoke(ctx context.Context, inputs map[string]any) (map[string]any, error) {
	if n.exec == nil {
		return nil, fmt.Errorf("workflow DataOps: no DataOpsFunc injected (compile-time Deps.DataOpsFunc is nil)")
	}
	st, err := StateFromInputs(inputs)
	if err != nil {
		return nil, err
	}
	query, err := Render(n.sql, st)
	if err != nil {
		return nil, fmt.Errorf("workflow DataOps sql: %w", err)
	}
	args := map[string]any{}
	for _, v := range n.vars {
		val, lerr := lookupRef(v.ref, st)
		if lerr != nil {
			return nil, fmt.Errorf("workflow DataOps: variable %q ref %s: %w", v.name, v.ref, lerr)
		}
		args[v.name] = val
	}
	res, err := n.exec(ctx, DataOpsRequest{SQL: query, Args: args})
	if err != nil {
		return nil, fmt.Errorf("workflow DataOps: %w", err)
	}
	rows := res.Rows
	if rows == nil {
		rows = []map[string]any{}
	}
	columns := res.Columns
	if columns == nil {
		columns = []string{}
	}
	return map[string]any{"columns": columns, "rows": rows, "row_count": len(rows)}, nil
}
