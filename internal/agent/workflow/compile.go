package workflow

import (
	"context"
	"fmt"
	"strconv"
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
	// Outputs is the node's recorded output map on PhaseFinished frames
	// (nil otherwise). Populated from the CanvasState AFTER recording, so
	// subscribers see exactly what downstream templates will reference.
	Outputs map[string]any `json:"outputs,omitempty"`
	// Replayed marks a finished frame whose outputs came from checkpoint
	// replay (resume) instead of a fresh Invoke — DurationMS is 0.
	Replayed bool `json:"replayed,omitempty"`
}

// Deps are the injected capabilities and callbacks the compiled graph uses.
// LLMFunc / RetrievalFunc may be nil when no node needs them; a node that
// does need a nil dependency fails at Invoke time with a clear error.
type Deps struct {
	LLMFunc       nodes.LLMFunc
	RetrievalFunc nodes.RetrievalFunc
	HTTPFunc      nodes.HTTPFunc
	DataOpsFunc   nodes.DataOpsFunc
	WebSearchFunc nodes.WebSearchFunc
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
	query  string
	files  []string
	inputs map[string]any
	state  *CanvasState
	// resume, when non-nil, seeds the fresh CanvasState from a checkpoint
	// side-car (outputs/path of previously completed nodes). Sys/Env come
	// from the ORIGINAL run via the snapshot, so {sys.query} keeps its
	// first-execution meaning on resume.
	resume *CanvasState
	// persistCheckpoint, when set, snapshots req.state to the checkpoint
	// KV after every node completion (crash-safe resume). Called from
	// nodeClosure on the run's goroutine.
	persistCheckpoint func()
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
				if req.resume != nil {
					// Checkpoint resume: overlay the persisted snapshot (outputs,
					// path and the ORIGINAL sys/env — a resume continues the prior
					// run, it does not restart it with new inputs).
					st.Restore(req.resume.Snapshot())
				}
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
			HTTPFunc:      deps.HTTPFunc,
			DataOpsFunc:   deps.DataOpsFunc,
			WebSearchFunc: deps.WebSearchFunc,
		})
		if err != nil {
			return nil, fmt.Errorf("workflow: node %q: %w", id, err)
		}
		fn := nodeClosure(id, node, deps, nodeErrorPolicy(comp.Obj.Params))
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
		policy := nodeErrorPolicy(comp.Obj.Params)
		targets, err := nodes.RouteTargets(comp.Obj.ComponentName, comp.Obj.Params)
		if err != nil {
			return nil, fmt.Errorf("workflow: node %q: %w", id, err)
		}
		// on_error.route_to joins (or becomes) the branch target set.
		if policy.routeTo != "" {
			if _, ok := norm.Components[policy.routeTo]; !ok {
				return nil, fmt.Errorf("workflow: node %q on_error.route_to targets unknown node %q", id, policy.routeTo)
			}
			targets = append(targets, policy.routeTo)
		}
		if len(targets) > 0 {
			endNodes := make(map[string]bool, len(targets))
			for _, t := range targets {
				if _, ok := norm.Components[t]; !ok {
					return nil, fmt.Errorf("workflow: node %q routes to unknown node %q", id, t)
				}
				endNodes[graphKey(t)] = true
			}
			// Plain nodes with an error branch: success keeps the single
			// NORMAL downstream (route_to target excluded — it is listed in
			// Downstream for topology detection, mirroring the Switch
			// convention); a multi-downstream success fan-out cannot be
			// expressed through one branch — fail compilation with a clear
			// message instead of silently mis-routing.
			routingByParams := nodes.IsRoutingComponent(comp.Obj.ComponentName)
			if !routingByParams {
				normal := make([]string, 0, len(comp.Downstream))
				for _, d := range comp.Downstream {
					if d != policy.routeTo {
						normal = append(normal, d)
					}
				}
				if len(normal) != 1 {
					return nil, fmt.Errorf("workflow: node %q uses on_error.route_to and must keep exactly one normal downstream (found %d)", id, len(normal))
				}
				endNodes[graphKey(normal[0])] = true
			}
			cond := func(ctx context.Context, in map[string]any) (string, error) {
				route, _ := in[nodes.RouteOutputKey].(string)
				if route == "" {
					if routingByParams {
						return "", fmt.Errorf("workflow: branch after %q: empty route (no case matched and no default set)", id)
					}
					// Plain node success path: its single normal downstream.
					for _, d := range comp.Downstream {
						if d != policy.routeTo {
							return graphKey(d), nil
						}
					}
					return "", fmt.Errorf("workflow: branch after %q: no normal downstream", id)
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

// RunOptions carries per-run execution options. Zero value = current
// Run behaviour. CheckpointID, when non-empty AND the workflow was
// compiled with Deps.CheckpointKV, enables checkpoint resume:
//
//   - the engine persists a CanvasState side-car under "<id>#ctx" after
//     every completed node (crash-safe) and once more on failure;
//   - a run with the same CheckpointID seeds its CanvasState from that
//     side-car and short-circuits nodes whose outputs are already
//     recorded — they are NOT re-invoked, their recorded outputs are
//     replayed onto the graph edges instead;
//   - eino's own WithCheckPointID is also passed through: it is a no-op
//     on this engine today (v0.9.x persists graph checkpoints only on
//     node interrupts, which our nodes never raise) but keeps the store
//     consistent if interrupts are ever added.
//
// A missing side-car (fresh id) degrades to a normal run.
type RunOptions struct {
	CheckpointID string
	// Inputs carries the Start-node form values (keyed by declared field
	// name). Materialised into the Start node's outputs; nil = query-only
	// runs (the pre-form DSL behaviour).
	Inputs map[string]any
}

// Run executes the workflow once. query/files are exposed to templates as
// {sys.query} / {sys.files}; DSL variables become {env.*}.
func (w *Workflow) Run(ctx context.Context, query string, files []string) (*RunResult, error) {
	return w.RunWithOptions(ctx, query, files, RunOptions{})
}

// RunWithOptions executes the workflow with per-run options. See
// RunOptions for the checkpoint semantics.
func (w *Workflow) RunWithOptions(ctx context.Context, query string, files []string, opts RunOptions) (*RunResult, error) {
	req := &runRequest{query: query, files: files, inputs: opts.Inputs}

	ckptEnabled := opts.CheckpointID != "" && w.deps.CheckpointKV != nil
	var invokeOpts []compose.Option
	if ckptEnabled {
		invokeOpts = append(invokeOpts, compose.WithCheckPointID(opts.CheckpointID))
		// Seed the run's CanvasState from the side-car so {node@param}
		// template refs resolve for nodes eino is about to skip. A miss is
		// the normal first-run case: req.resume stays nil.
		if data, ok, gerr := w.deps.CheckpointKV.Get(ctx, ctxStateKey(opts.CheckpointID)); gerr == nil && ok {
			if st, ierr := ImportState(data); ierr == nil {
				req.resume = st
			}
		}
	}

	ctx = context.WithValue(ctx, runCtxKey{}, req)
	if ckptEnabled {
		// Crash-safe resume: persist the side-car after every node
		// completion (hook consumed by nodeClosure). Failures degrade the
		// resume to a fresh run, never the run itself.
		req.persistCheckpoint = func() {
			if data, merr := ExportState(req.state); merr == nil {
				_ = w.deps.CheckpointKV.Set(ctx, ctxStateKey(opts.CheckpointID), data, w.deps.CheckpointTTL)
			}
		}
	}
	startInput := map[string]any{"query": query, "files": files}
	if opts.Inputs != nil {
		startInput["inputs"] = opts.Inputs
	}
	_, invokeErr := w.runnable.Invoke(ctx, startInput, invokeOpts...)

	// Persist the terminal CanvasState side-car for failed/interrupted
	// runs (that is exactly what makes the NEXT RunWithOptions resumable).
	// On success both the side-car and eino's raw checkpoint are deleted
	// best-effort — TTL bounds anything the deletes miss. Side-car errors
	// are swallowed deliberately: a failed persistence downgrades the next
	// resume to a fresh run, which is always a correct outcome.
	if ckptEnabled && req.state != nil {
		if invokeErr != nil {
			if data, merr := ExportState(req.state); merr == nil {
				_ = w.deps.CheckpointKV.Set(ctx, ctxStateKey(opts.CheckpointID), data, w.deps.CheckpointTTL)
			}
		} else {
			_ = w.deps.CheckpointKV.Delete(ctx, ctxStateKey(opts.CheckpointID))
			_ = w.deps.CheckpointKV.Delete(ctx, opts.CheckpointID)
		}
	}

	if invokeErr != nil {
		return nil, invokeErr
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
			strings.EqualFold(comp.Obj.ComponentName, "Answer") {
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

// ctxStateKey derives the CanvasState side-car key from a checkpoint id.
// "#ctx" cannot collide with eino's raw checkpoint ids (run UUIDs).
func ctxStateKey(checkpointID string) string { return checkpointID + "#ctx" }

// errorPolicy is the compiled per-node error-handling configuration
// (params.on_error). The zero value keeps fail-fast semantics.
type errorPolicy struct {
	// action: "fail" (default) | "continue" | "route_to".
	action string
	// defaultOutputs are recorded as the node's outputs when action is
	// "continue" (and the RouteOutputKey when "route_to" targets a node).
	defaultOutputs map[string]any
	// routeTo is the downstream node id for action "route_to".
	routeTo string
	// retries: how many times a failed Invoke is retried (0 = none).
	retries int
	// retryDelayMS between retries.
	retryDelayMS int
}

// numAny coerces JSON float64 / Go int / int64 / string numerics.
func numAny(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// nodeErrorPolicy parses params.on_error + params.retry.
// ponytail: one flat policy shape; no per-condition error branches — a
// failing Switch condition can be modelled with the continue default.
func nodeErrorPolicy(params map[string]any) errorPolicy {
	p := errorPolicy{}
	if raw, ok := params["retry"].(map[string]any); ok {
		if v, ok := raw["count"]; ok {
			if f, ok := numAny(v); ok && f > 0 && f <= 5 {
				p.retries = int(f)
			}
		}
		if v, ok := raw["delay_ms"]; ok {
			if f, ok := numAny(v); ok && f > 0 && f <= 60000 {
				p.retryDelayMS = int(f)
			}
		}
	}
	onErr, ok := params["on_error"].(map[string]any)
	if !ok {
		return p
	}
	switch action, _ := onErr["action"].(string); action {
	case "continue", "route_to":
		p.action = action
	case "", "fail":
		// keep fail-fast
	default:
		// Unknown actions degrade to fail-fast: never guess a fallback.
	}
	if outs, ok := onErr["default_outputs"].(map[string]any); ok {
		p.defaultOutputs = outs
	}
	if target, _ := onErr["route_to"].(string); target != "" {
		p.routeTo = target
	}
	if p.action == "route_to" && p.routeTo == "" {
		p.action = "" // route_to without a target is meaningless → fail
	}
	return p
}

// nodeClosure wraps a Node with path tracking, output recording, event
// emission, panic recovery, retry and error-policy handling, and timing. It
// is the single place node lifecycle semantics live.
func nodeClosure(id string, node nodes.Node, deps Deps, policy errorPolicy) func(ctx context.Context, in map[string]any) (map[string]any, error) {
	emit := func(ev NodeEvent) {
		if deps.OnNodeEvent != nil {
			deps.OnNodeEvent(ev)
		}
	}
	return func(ctx context.Context, in map[string]any) (map[string]any, error) {
		// Checkpoint resume: a node whose outputs are already recorded in
		// the restored state completed in a previous attempt — replay its
		// recorded outputs onto the graph edge instead of re-invoking it.
		// (Nodes with no recorded outputs — interrupted mid-flight — re-run
		// normally.) Replay emits a finished frame (Replayed=true) so trace
		// accumulators and SSE subscribers see a complete run picture.
		if req, _ := ctx.Value(runCtxKey{}).(*runRequest); req != nil && req.resume != nil {
			if cached := req.resume.OutputsOf(id); len(cached) > 0 {
				emit(NodeEvent{NodeID: id, Phase: PhaseFinished, Outputs: cached, Replayed: true})
				return cached, nil
			}
		}

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
			for attempt := 0; ; attempt++ {
				out, err = node.Invoke(ctx, in)
				if err == nil || attempt >= policy.retries || ctx.Err() != nil {
					return
				}
				// Retryable failure (context still alive): bounded backoff.
				emit(NodeEvent{NodeID: id, Phase: PhaseFailed, Err: err, DurationMS: msSince(start)})
				if policy.retryDelayMS > 0 {
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(time.Duration(policy.retryDelayMS) * time.Millisecond):
					}
				}
				emit(NodeEvent{NodeID: id, Phase: PhaseStarted})
			}
		}()
		if err != nil {
			wrapped := fmt.Errorf("workflow: node %q: %w", id, err)
			// Error policy: continue / route_to record the configured defaults
			// instead of failing the run (Phase 5, Dify error-branch parity).
			if policy.action == "continue" {
				fallback := make(map[string]any, len(policy.defaultOutputs)+1)
				for k, v := range policy.defaultOutputs {
					fallback[k] = v
				}
				fallback["_error"] = wrapped.Error()
				for k, v := range fallback {
					cs.SetOutput(id, k, v)
				}
				emit(NodeEvent{NodeID: id, Phase: PhaseFinished, DurationMS: msSince(start), Outputs: cs.OutputsOf(id)})
				return fallback, nil
			}
			if policy.action == "route_to" {
				fallback := map[string]any{nodes.RouteOutputKey: policy.routeTo, "_error": wrapped.Error()}
				for k, v := range policy.defaultOutputs {
					fallback[k] = v
				}
				for k, v := range fallback {
					if k != nodes.RouteOutputKey {
						cs.SetOutput(id, k, v)
					}
				}
				emit(NodeEvent{NodeID: id, Phase: PhaseFinished, DurationMS: msSince(start), Outputs: cs.OutputsOf(id)})
				return fallback, nil
			}
			finish(wrapped)
			return nil, wrapped
		}

		for k, v := range out {
			if k == nodes.StateInputKey {
				continue
			}
			cs.SetOutput(id, k, v)
		}
		if req, _ := ctx.Value(runCtxKey{}).(*runRequest); req != nil && req.persistCheckpoint != nil {
			req.persistCheckpoint()
		}
		emit(NodeEvent{NodeID: id, Phase: PhaseFinished, DurationMS: msSince(start), Outputs: cs.OutputsOf(id)})
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

func msSince(start time.Time) int64 { return time.Since(start).Milliseconds() }
