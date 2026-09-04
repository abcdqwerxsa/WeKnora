package workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/workflow/nodes"
	"github.com/cloudwego/eino/compose"
)

// NodePhase marks where in its lifecycle a node is.
type NodePhase string

const (
	PhaseStarted  NodePhase = "started"
	PhaseFinished NodePhase = "finished"
	PhaseFailed   NodePhase = "failed"
)

// NodeEvent is emitted (via Deps.OnNodeEvent) around every node execution.
// The engine stays pure: it calls the injected callback and never imports
// the platform EventBus — the integration layer forwards these events.
type NodeEvent struct {
	NodeID     string    `json:"node_id"`
	Phase      NodePhase `json:"phase"`
	Err        error     `json:"-"`
	DurationMS int64     `json:"duration_ms"`
}

// Deps are the injected capabilities and callbacks the compiled graph uses.
// LLMFunc / RetrievalFunc may be nil when no node needs them; a node that
// does need a nil dependency fails at Invoke time with a clear error.
type Deps struct {
	LLMFunc       nodes.LLMFunc
	RetrievalFunc nodes.RetrievalFunc
	OnNodeEvent   func(NodeEvent)

	// CheckpointKV (optional) enables eino checkpoint persistence through
	// KVCheckPointStore. CheckpointTTL is applied on every write; zero
	// means no expiry.
	CheckpointKV  KVStore
	CheckpointTTL time.Duration
}

// RunResult is the outcome of one Workflow.Run.
type RunResult struct {
	// Outputs is the final snapshot of every node's recorded outputs.
	Outputs *StateSnapshot
	// Path is the execution order of node ids.
	Path []string
	// Answer is the output of the (single) Answer node when the run
	// reached one; empty otherwise.
	Answer string
}

// Workflow is a compiled, reusable workflow. It is safe for concurrent Run
// calls: eino generates a fresh CanvasState per run.
type Workflow struct {
	dsl      *DSL
	deps     Deps
	runnable compose.Runnable[map[string]any, map[string]any]
}

// runRequest is the per-run payload carried on the context so eino's
// GenLocalState closure (created once at compile time) can seed the state.
type runRequest struct {
	query string
	files []string
	state *CanvasState
}

type runCtxKey struct{}

// Compile normalizes and validates the DSL, then builds an eino graph.
//
// MVP topology constraints (surfaced as errors here):
//   - exactly one entry node (no upstream, not targeted by any edge) — eino
//     feeds the graph input to a single start;
//   - exactly one terminal node (no downstream) — the graph output type is
//     a single map, so multiple ends would need merge semantics this engine
//     does not define yet.
//
// Cycles are rejected by eino at compile time and passed through wrapped.
func Compile(dsl *DSL, deps Deps) (*Workflow, error) {
	norm, err := Normalize(dsl)
	if err != nil {
		return nil, err
	}

	entries := entryIDs(norm.Components)
	if len(entries) != 1 {
		return nil, fmt.Errorf("workflow: compile requires exactly one entry node, found %d (%v)", len(entries), entries)
	}
	terminals := terminalIDs(norm.Components)
	if len(terminals) != 1 {
		return nil, fmt.Errorf("workflow: compile requires exactly one terminal node, found %d (%v) — MVP graphs must converge on a single end node", len(terminals), terminals)
	}

	g := compose.NewGraph[map[string]any, map[string]any](
		compose.WithGenLocalState(func(ctx context.Context) *CanvasState {
			req, _ := ctx.Value(runCtxKey{}).(*runRequest)
			sys := map[string]any{}
			env := map[string]any{}
			if req != nil {
				sys["query"] = req.query
				sys["files"] = req.files
				for k, v := range norm.Variables {
					env[k] = v
				}
			}
			st := NewCanvasState(sys, env)
			if req != nil {
				req.state = st
			}
			return st
		}),
	)

	// nodes
	for id, comp := range norm.Components {
		node, err := nodes.New(comp.Obj.ComponentName, comp.Obj.Params, nodes.Deps{
			LLMFunc:       deps.LLMFunc,
			RetrievalFunc: deps.RetrievalFunc,
		})
		if err != nil {
			return nil, fmt.Errorf("workflow: node %q: %w", id, err)
		}
		fn := nodeClosure(id, node, deps)
		err = g.AddLambdaNode(graphKey(id), compose.InvokableLambda(fn), compose.WithStatePreHandler(
			func(ctx context.Context, in map[string]any, st *CanvasState) (map[string]any, error) {
				// Clone before writing: parallel branches may receive the
				// same upstream map, and mutating it here would race.
				cloned := make(map[string]any, len(in)+1)
				for k, v := range in {
					cloned[k] = v
				}
				cloned[nodes.StateInputKey] = st
				return cloned, nil
			}))
		if err != nil {
			return nil, fmt.Errorf("workflow: add node %q: %w", id, err)
		}
	}

	// edges / branches
	for id, comp := range norm.Components {
		targets, err := nodes.RouteTargets(comp.Obj.ComponentName, comp.Obj.Params)
		if err != nil {
			return nil, fmt.Errorf("workflow: node %q: %w", id, err)
		}
		if len(targets) > 0 {
			endNodes := make(map[string]bool, len(targets))
			for _, t := range targets {
				if _, ok := norm.Components[t]; !ok {
					return nil, fmt.Errorf("workflow: node %q routes to unknown node %q", id, t)
				}
				endNodes[graphKey(t)] = true
			}
			cond := func(ctx context.Context, in map[string]any) (string, error) {
				route, _ := in[nodes.RouteOutputKey].(string)
				if route == "" {
					return "", fmt.Errorf("workflow: branch after %q: empty route (no case matched and no default set)", id)
				}
				// route holds a DSL node id; endNodes are keyed by graph key.
				return graphKey(route), nil
			}
			if err := g.AddBranch(graphKey(id), compose.NewGraphBranch(cond, endNodes)); err != nil {
				return nil, fmt.Errorf("workflow: add branch after %q: %w", id, err)
			}
			continue
		}
		for _, d := range comp.Downstream {
			if _, ok := norm.Components[d]; !ok {
				return nil, fmt.Errorf("workflow: node %q has unknown downstream %q", id, d)
			}
			if err := g.AddEdge(graphKey(id), graphKey(d)); err != nil {
				return nil, fmt.Errorf("workflow: add edge %q->%q: %w", id, d, err)
			}
		}
	}

	// Wire the single entry to START and the single terminal to END.
	// (entries/terminals were validated to be exactly one each above.)
	if err := g.AddEdge(compose.START, graphKey(entries[0])); err != nil {
		return nil, fmt.Errorf("workflow: wire start: %w", err)
	}
	if err := g.AddEdge(graphKey(terminals[0]), compose.END); err != nil {
		return nil, fmt.Errorf("workflow: wire end: %w", err)
	}

	compileOpts := []compose.GraphCompileOption{}
	if deps.CheckpointKV != nil {
		compileOpts = append(compileOpts, compose.WithCheckPointStore(
			&KVCheckPointStore{KV: deps.CheckpointKV, TTL: deps.CheckpointTTL}))
	}

	runnable, err := g.Compile(context.Background(), compileOpts...)
	if err != nil {
		return nil, fmt.Errorf("workflow: eino compile: %w", err)
	}
	return &Workflow{dsl: norm, deps: deps, runnable: runnable}, nil
}

// Run executes the workflow once. query/files are exposed to templates as
// {sys.query} / {sys.files}; DSL variables become {env.*}.
func (w *Workflow) Run(ctx context.Context, query string, files []string) (*RunResult, error) {
	req := &runRequest{query: query, files: files}
	ctx = context.WithValue(ctx, runCtxKey{}, req)
	if _, err := w.runnable.Invoke(ctx, map[string]any{"query": query, "files": files}); err != nil {
		return nil, err
	}

	state := req.state
	if state == nil {
		return nil, fmt.Errorf("workflow: internal error: run produced no state")
	}
	res := &RunResult{
		Outputs: state.Snapshot(),
		Path:    state.PathCopy(),
	}
	for _, id := range res.Path {
		if comp, ok := w.dsl.Components[id]; ok &&
			equalFold(comp.Obj.ComponentName, "Answer") {
			if v, ok := state.GetOutput(id, "answer"); ok {
				if s, ok := v.(string); ok {
					res.Answer = s
					break
				}
			}
		}
	}
	return res, nil
}

// nodeClosure wraps a Node with path tracking, output recording, event
// emission, panic recovery and timing. It is the single place node
// lifecycle semantics live.
func nodeClosure(id string, node nodes.Node, deps Deps) func(ctx context.Context, in map[string]any) (map[string]any, error) {
	emit := func(ev NodeEvent) {
		if deps.OnNodeEvent != nil {
			deps.OnNodeEvent(ev)
		}
	}
	return func(ctx context.Context, in map[string]any) (map[string]any, error) {
		emit(NodeEvent{NodeID: id, Phase: PhaseStarted})
		start := time.Now()

		finish := func(err error) {
			emit(NodeEvent{NodeID: id, Phase: PhaseFailed, Err: err, DurationMS: msSince(start)})
		}

		state, err := nodes.StateFromInputs(in)
		if err != nil {
			finish(err)
			return nil, fmt.Errorf("workflow: node %q: %w", id, err)
		}
		cs, ok := state.(*CanvasState)
		if !ok {
			err := fmt.Errorf("workflow: node %q: state is %T, want *workflow.CanvasState", id, state)
			finish(err)
			return nil, err
		}
		cs.AppendPath(id)

		out, err := func() (out map[string]any, err error) {
			defer func() {
				if r := recover(); r != nil {
					out = nil
					err = fmt.Errorf("workflow: node %q panicked: %v", id, r)
				}
			}()
			return node.Invoke(ctx, in)
		}()
		if err != nil {
			wrapped := fmt.Errorf("workflow: node %q: %w", id, err)
			finish(wrapped)
			return nil, wrapped
		}

		for k, v := range out {
			if k == nodes.StateInputKey {
				continue
			}
			cs.SetOutput(id, k, v)
		}
		emit(NodeEvent{NodeID: id, Phase: PhaseFinished, DurationMS: msSince(start)})
		return out, nil
	}
}

// graphKey maps a DSL node id onto an eino graph node key. eino reserves
// the bare keys "start" and "end"; prefixing keeps user-chosen ids (which
// will inevitably include "start") out of eino's reserved namespace. The
// mapping is injective and only used at the eino boundary — events, Path
// and outputs keep the original DSL id.
func graphKey(id string) string { return "wf:" + id }

func entryIDs(comps map[string]*Component) []string {
	targeted := map[string]bool{}
	for _, c := range comps {
		for _, d := range c.Downstream {
			targeted[d] = true
		}
	}
	var out []string
	for id, c := range comps {
		if len(c.Upstream) == 0 && !targeted[id] {
			out = append(out, id)
		}
	}
	return out
}

func terminalIDs(comps map[string]*Component) []string {
	var out []string
	for id, c := range comps {
		if len(c.Downstream) == 0 {
			out = append(out, id)
		}
	}
	return out
}

func equalFold(a, b string) bool { return strings.EqualFold(a, b) }

func msSince(start time.Time) int64 { return time.Since(start).Milliseconds() }
