package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// The Code node: executes a user script in an ephemeral sandbox (allocated
// per run, torn down after) through the injected CodeFunc. The engine stays
// execution-free — the adapter resolves the tenant's sandbox backend.
const ComponentCode = "Code"

func init() {
	RegisterNodeFactory(ComponentCode, newCode)
}

// CodeRequest is the rendered input handed to an injected CodeFunc.
type CodeRequest struct {
	// Language: "python3" or "node" (maps onto the sandbox interpreter).
	Language string
	// Code is the verbatim script body.
	Code string
	// InputJSON is the rendered variables object the script reads from the
	// WEKNORA_WORKFLOW_INPUT environment variable.
	InputJSON string
	// TimeoutSeconds bounds one execution (adapter clamps to the run cap).
	TimeoutSeconds int
}

// CodeFunc executes one script and returns its parsed JSON output object.
// Injected by the compiler.
type CodeFunc func(ctx context.Context, req CodeRequest) (map[string]any, error)

type codeVar struct {
	name string
	ref  string
}

type codeNode struct {
	code       string
	language   string
	vars       []codeVar
	timeoutSec int
	exec       CodeFunc
}

func newCode(params map[string]any, deps Deps) (Node, error) {
	code, err := strParam(ComponentCode, "code", params, true)
	if err != nil {
		return nil, err
	}
	language, _ := params["language"].(string)
	switch strings.TrimSpace(language) {
	case "python3", "":
		language = "python3"
	case "node":
	default:
		return nil, fmt.Errorf("workflow Code: language must be \"python3\" or \"node\", got %q", language)
	}
	rawVars, _ := params["variables"].([]any)
	vars := make([]codeVar, 0, len(rawVars))
	for i, item := range rawVars {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("workflow Code: variables[%d] must be an object, got %T", i, item)
		}
		v := codeVar{}
		if val, ok := m["name"]; ok {
			v.name, _ = val.(string)
		}
		if val, ok := m["ref"]; ok {
			v.ref, _ = val.(string)
		}
		if v.name == "" {
			return nil, fmt.Errorf("workflow Code: variables[%d] has an empty \"name\"", i)
		}
		vars = append(vars, v)
	}
	timeoutSec := 30
	if v, ok := params["timeout_seconds"]; ok && v != nil {
		if timeoutSec, err = toInt(ComponentCode, "timeout_seconds", v); err != nil {
			return nil, err
		}
	}
	return &codeNode{code: code, language: language, vars: vars, timeoutSec: timeoutSec, exec: deps.CodeFunc}, nil
}

func (n *codeNode) Invoke(ctx context.Context, inputs map[string]any) (map[string]any, error) {
	if n.exec == nil {
		return nil, fmt.Errorf("workflow Code: no CodeFunc injected (no sandbox backend configured)")
	}
	st, err := StateFromInputs(inputs)
	if err != nil {
		return nil, err
	}
	input := make(map[string]any, len(n.vars))
	for _, v := range n.vars {
		rendered, rerr := Render(v.ref, st)
		if rerr != nil {
			return nil, fmt.Errorf("workflow Code: variable %q: %w", v.name, rerr)
		}
		input[v.name] = rendered
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("workflow Code: input marshal: %w", err)
	}
	out, err := n.exec(ctx, CodeRequest{
		Language: n.language, Code: n.code, InputJSON: string(inputJSON), TimeoutSeconds: n.timeoutSec,
	})
	if err != nil {
		return nil, fmt.Errorf("workflow Code: %w", err)
	}
	return out, nil
}
