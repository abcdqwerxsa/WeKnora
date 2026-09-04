// Package workflow is WeKnora's self-contained workflow engine: a DSL
// dual-view (canvas graph + execution topology) compiled onto
// cloudwego/eino with injected business capabilities. It deliberately has
// zero dependencies on WeKnora business packages — LLM calls and knowledge
// retrieval arrive as functions via Deps, and events leave via the
// OnNodeEvent callback. Wiring to internal/event / model services happens
// in the integration layer outside this package.
package workflow

import (
	"sync"
)

// CanvasState is the per-run shared state bag every node reads (template
// refs) and writes (its own outputs) through. It plays the same role as
// RAGFlow's runtime.CanvasState.
//
// Concurrency: one sync.RWMutex guards every field — the deliberate
// "start simple" choice (parallel branches share one lock; contention is
// bounded by node count, and MVP graphs have few nodes). If profiling ever
// shows lock contention on wide parallel graphs, split Outputs / Sys / Env
// into separate locks — the accessor surface below is already granular.
type CanvasState struct {
	mu      sync.RWMutex
	Outputs map[string]map[string]any // nodeID -> param -> value
	Sys     map[string]any            // sys.* namespace: query, files, ...
	Env     map[string]any            // env.* namespace: deployment constants
	Path    []string                  // execution order of node ids
}

// NewCanvasState builds an empty state with Sys/Env prefilled from the
// given maps (nil maps are replaced with empty ones).
func NewCanvasState(sys, env map[string]any) *CanvasState {
	return &CanvasState{
		Outputs: map[string]map[string]any{},
		Sys:     nonNil(sys),
		Env:     nonNil(env),
	}
}

// SetOutput records one output param of nodeID.
func (s *CanvasState) SetOutput(nodeID, param string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Outputs == nil {
		s.Outputs = map[string]map[string]any{}
	}
	if s.Outputs[nodeID] == nil {
		s.Outputs[nodeID] = map[string]any{}
	}
	s.Outputs[nodeID][param] = value
}

// GetOutput reads one output param of nodeID.
func (s *CanvasState) GetOutput(nodeID, param string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	params, ok := s.Outputs[nodeID]
	if !ok {
		return nil, false
	}
	v, ok := params[param]
	return v, ok
}

// SysValue reads sys.<key>.
func (s *CanvasState) SysValue(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.Sys[key]
	return v, ok
}

// EnvValue reads env.<key>.
func (s *CanvasState) EnvValue(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.Env[key]
	return v, ok
}

// AppendPath records nodeID entering execution.
func (s *CanvasState) AppendPath(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Path = append(s.Path, nodeID)
}

// PathCopy returns a copy of the execution path.
func (s *CanvasState) PathCopy() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.Path))
	copy(out, s.Path)
	return out
}

// StateSnapshot is the serialisable image of a CanvasState (shallow copies
// of the maps; param values are shared references and must be treated as
// read-only by consumers).
type StateSnapshot struct {
	Outputs map[string]map[string]any `json:"outputs"`
	Sys     map[string]any            `json:"sys"`
	Env     map[string]any            `json:"env"`
	Path    []string                  `json:"path"`
}

// Snapshot returns a copy of the current state. The copy is safe to
// serialise or inspect concurrently with a running graph; mutating the
// original afterwards does not affect an already-taken snapshot.
func (s *CanvasState) Snapshot() *StateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := &StateSnapshot{
		Sys:  copyAnyMap(s.Sys),
		Env:  copyAnyMap(s.Env),
		Path: append([]string(nil), s.Path...),
	}
	if s.Outputs != nil {
		snap.Outputs = make(map[string]map[string]any, len(s.Outputs))
		for id, params := range s.Outputs {
			snap.Outputs[id] = copyAnyMap(params)
		}
	}
	return snap
}

// Restore replaces the state's contents with the snapshot's.
func (s *CanvasState) Restore(snap *StateSnapshot) {
	if snap == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Outputs = copyNestedMap(snap.Outputs)
	s.Sys = copyAnyMap(snap.Sys)
	s.Env = copyAnyMap(snap.Env)
	s.Path = append([]string(nil), snap.Path...)
}

func nonNil(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func copyAnyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func copyNestedMap(m map[string]map[string]any) map[string]map[string]any {
	if m == nil {
		return map[string]map[string]any{}
	}
	out := make(map[string]map[string]any, len(m))
	for k, v := range m {
		out[k] = copyAnyMap(v)
	}
	return out
}
