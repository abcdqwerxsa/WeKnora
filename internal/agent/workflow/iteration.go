package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/workflow/nodes"
)

// The Iteration node: loops a body sub-graph over an array. An eino graph
// cannot contain a cycle, so the loop is a nested runnable — the body is
// compiled by the same Compile (recursively) and invoked once per item with
// a child CanvasState seeded from the outer run's snapshot plus the current
// item/index. Items are independent (no cross-item carry-over); execution is
// sequential.
// ponytail: sequential items, atomic from the outer checkpoint's perspective
// — a crash mid-iteration re-runs the whole loop on resume; per-item
// checkpointing needs child checkpoint ids if ever required.

func isIteration(componentName string) bool {
	return strings.EqualFold(componentName, nodes.ComponentIteration)
}

func firstBodyID(body map[string]*Component) string {
	for id := range body {
		return id
	}
	return ""
}

// iterationParams are the loop parameters (snake_case, engine contract).
type iterationParams struct {
	items     string // template rendering to a JSON array
	itemVar   string // exposed as {iterID@itemVar} inside the body (default "item")
	indexVar  string // exposed as {iterID@indexVar} (default "index")
	outputRef string // single {node@param} ref or template rendered per item
	outputVar string // collected array key (default "results")
}

func parseIterationParams(params map[string]any) (iterationParams, error) {
	p := iterationParams{itemVar: "item", indexVar: "index", outputVar: "results"}
	var err error
	if p.items, err = strParamIter("items", params); err != nil {
		return p, err
	}
	if p.outputRef, err = strParamIter("output_ref", params); err != nil {
		return p, err
	}
	if v, _ := params["item_var"].(string); v != "" {
		p.itemVar = v
	}
	if v, _ := params["index_var"].(string); v != "" {
		p.indexVar = v
	}
	if v, _ := params["output_var"].(string); v != "" {
		p.outputVar = v
	}
	return p, nil
}

func strParamIter(key string, params map[string]any) (string, error) {
	v, _ := params[key].(string)
	if strings.TrimSpace(v) == "" {
		return "", fmt.Errorf("workflow Iteration: missing required param %q", key)
	}
	return v, nil
}

type iterationNode struct {
	id     string
	params iterationParams
	body   *Workflow
}

func newIterationNode(id string, params map[string]any, body *Workflow) (nodes.Node, error) {
	p, err := parseIterationParams(params)
	if err != nil {
		return nil, err
	}
	return &iterationNode{id: id, params: p, body: body}, nil
}

// Invoke loops the body over the rendered items array. The outer state is
// snapshotted once; each item restores it into a child state (plus
// item/index under the iteration node's id) so body templates see both the
// outer context ({start@query} etc.) and the loop variables.
func (n *iterationNode) Invoke(ctx context.Context, inputs map[string]any) (map[string]any, error) {
	st, err := nodes.StateFromInputs(inputs)
	if err != nil {
		return nil, err
	}
	parent := st.(*CanvasState)
	rendered, err := nodes.Render(n.params.items, parent)
	if err != nil {
		return nil, fmt.Errorf("workflow Iteration: items: %w", err)
	}
	var items []any
	if err := json.Unmarshal([]byte(rendered), &items); err != nil {
		return nil, fmt.Errorf("workflow Iteration: items must render to a JSON array, got %.80q", rendered)
	}
	snapshot := parent.Snapshot()
	results := make([]any, 0, len(items))
	for i, item := range items {
		seed := NewCanvasState(nil, nil)
		seed.Restore(snapshot)
		seed.SetOutput(n.id, n.params.itemVar, item)
		seed.SetOutput(n.id, n.params.indexVar, float64(i))
		child, runErr := n.body.runSeeded(ctx, seed)
		if runErr != nil {
			return nil, fmt.Errorf("workflow Iteration: item %d: %w", i, runErr)
		}
		results = append(results, n.collectOutput(child))
	}
	return map[string]any{n.params.outputVar: results, "count": len(results)}, nil
}

// collectOutput resolves output_ref against the finished child state: a
// single {node@param} ref returns the raw value (any type); anything else
// renders as a template (string).
func (n *iterationNode) collectOutput(child *CanvasState) any {
	if m := nodes.VarRefPattern.FindStringSubmatch(strings.TrimSpace(n.params.outputRef)); len(m) == 2 {
		if nodeID, param, ok := strings.Cut(m[1], "@"); ok {
			if v, found := child.GetOutput(nodeID, param); found {
				return v
			}
		}
		return nil
	}
	rendered, err := nodes.Render(n.params.outputRef, child)
	if err != nil {
		return nil // body gap for this item → null slot keeps the array aligned
	}
	return rendered
}

// runSeeded executes the workflow once with a caller-built starting state:
// GenLocalState restores sys/env/outputs from the seed snapshot (the same
// mechanism checkpoint resume uses). Checkpoint stores are NOT engaged —
// the caller (iteration) owns retry granularity.
func (w *Workflow) runSeeded(ctx context.Context, seed *CanvasState) (*CanvasState, error) {
	req := &runRequest{resume: seed}
	ctx = context.WithValue(ctx, runCtxKey{}, req)
	if _, err := w.runnable.Invoke(ctx, map[string]any{}); err != nil {
		return nil, err
	}
	if req.state == nil {
		return nil, fmt.Errorf("workflow: internal error: seeded run produced no state")
	}
	return req.state, nil
}
