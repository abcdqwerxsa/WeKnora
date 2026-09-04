// Package nodes holds the workflow node contract, the factory registry and
// the built-in node shells. It is the lowest layer of the workflow engine:
// it must not import any other package under internal/agent/workflow (the
// root package imports nodes, so a back-import would create a cycle — the
// same split RAGFlow uses between its canvas and runtime packages).
//
// Business capabilities (LLM calls, knowledge-base retrieval) are injected
// as functions by the compiler — nodes never imports WeKnora services.
package nodes

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// StateInputKey is the reserved key under which the compiled graph injects
// the per-run *CanvasState into every node's input map (see compile.go:
// WithStatePreHandler). Nodes unwrap it with StateFromInputs.
const StateInputKey = "_canvas_state"

// RouteOutputKey is the reserved output param a Switch node sets to the id
// of the downstream node the branch should take. The graph branch condition
// (compile.go) reads it after the node ran.
const RouteOutputKey = "__route__"

// StateView is the read-only slice of CanvasState the nodes package needs
// for template resolution. The root package's *CanvasState implements it,
// keeping the dependency direction one-way (root -> nodes).
type StateView interface {
	GetOutput(nodeID, param string) (any, bool)
	SysValue(key string) (any, bool)
	EnvValue(key string) (any, bool)
}

// Node is the minimal executable contract of a workflow node. inputs is the
// merged output of the upstream node(s) plus the reserved StateInputKey.
// Implementations must not mutate inputs and must return a fresh map.
type Node interface {
	Invoke(ctx context.Context, inputs map[string]any) (map[string]any, error)
}

// Factory builds a Node from DSL params. Returning an error fails compilation.
type Factory func(params map[string]any, deps Deps) (Node, error)

// Deps carries the injected business capabilities. A nil function is legal
// at build time; shells return a clear error at Invoke time instead, so a
// workflow that never runs an LLM node compiles fine without an LLMFunc.
type Deps struct {
	LLMFunc       LLMFunc
	RetrievalFunc RetrievalFunc
}

// LLMRequest is the rendered input handed to an injected LLMFunc.
type LLMRequest struct {
	Prompt      string
	Model       string
	Temperature float64
}

// LLMFunc renders-and-calls one LLM turn. Injected by the compiler.
type LLMFunc func(ctx context.Context, req LLMRequest) (string, error)

// RetrievalRequest is the rendered input handed to an injected RetrievalFunc.
type RetrievalRequest struct {
	Query string
	KBIDs []string
	TopK  int
}

// RetrievalResult is the retrieval payload stored into node outputs.
type RetrievalResult struct {
	Chunks  []map[string]any `json:"chunks"`
	DocAggs []map[string]any `json:"doc_aggs"`
}

// RetrievalFunc runs one knowledge-base search. Injected by the compiler.
type RetrievalFunc func(ctx context.Context, req RetrievalRequest) (*RetrievalResult, error)

// registry

var (
	regMu    sync.RWMutex
	registry = map[string]Factory{}
)

// RegisterNodeFactory registers a factory under a canonical component name.
// Re-registration with the same (lower-cased) name panics — it is a startup
// wiring bug, not a runtime condition.
func RegisterNodeFactory(componentName string, f Factory) {
	key := canonical(componentName)
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[key]; dup {
		panic("workflow nodes: duplicate factory for " + componentName)
	}
	registry[key] = f
}

// New resolves a component name (case-insensitive) to a Node.
func New(componentName string, params map[string]any, deps Deps) (Node, error) {
	regMu.RLock()
	f, ok := registry[canonical(componentName)]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("workflow nodes: unknown component %q (known: %s)",
			componentName, strings.Join(Known(), ", "))
	}
	if params == nil {
		params = map[string]any{}
	}
	return f(params, deps)
}

// Known returns the sorted canonical component names.
func Known() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// IsKnown reports whether componentName resolves to a registered factory.
func IsKnown(componentName string) bool {
	regMu.RLock()
	_, ok := registry[canonical(componentName)]
	regMu.RUnlock()
	return ok
}

func canonical(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// StateFromInputs unwraps the injected CanvasState from a node's input map.
func StateFromInputs(inputs map[string]any) (StateView, error) {
	v, ok := inputs[StateInputKey]
	if !ok || v == nil {
		return nil, fmt.Errorf("workflow nodes: internal error: %s missing from inputs", StateInputKey)
	}
	sv, ok := v.(StateView)
	if !ok {
		return nil, fmt.Errorf("workflow nodes: internal error: %s holds %T, want StateView", StateInputKey, v)
	}
	return sv, nil
}

// strParam reads a string param, erroring with node context when missing.
func strParam(nodeLabel, param string, params map[string]any, required bool) (string, error) {
	v, ok := params[param]
	if !ok || v == nil {
		if required {
			return "", fmt.Errorf("workflow %s: missing required param %q", nodeLabel, param)
		}
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("workflow %s: param %q must be a string, got %T", nodeLabel, param, v)
	}
	return s, nil
}
