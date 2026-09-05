package workflow

import (
	"fmt"
	"sort"

	"github.com/Tencent/WeKnora/internal/agent/workflow/nodes"
)

// DSLVersion is the schema version this engine understands.
const DSLVersion = 1

// DSL is the workflow definition persisted as one JSON document. It carries
// two projections of the same graph (mirroring RAGFlow's canvas DSL):
//
//   - Graph: the canvas view (nodes with positions, edges) used by the
//     frontend to render and edit the workflow. Purely presentational for
//     the engine.
//   - Components: the execution topology (node id -> params + upstream /
//     downstream lists) used by Compile. Purely semantic.
//
// Either side may be missing (e.g. a DSL authored programmatically has no
// canvas layout); Normalize repairs the missing side.
type DSL struct {
	Version    int                   `json:"version"`
	Graph      *GraphView            `json:"graph,omitempty"`
	Components map[string]*Component `json:"components"`
	Variables  map[string]any        `json:"variables,omitempty"`
}

// GraphView is the canvas (React-Flow / vue-flow style) projection.
type GraphView struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// GraphNode is one canvas node. Type is the component name ("Start", "LLM",
// ...); Data may carry UI-only state and, when built from a component, the
// node's params under key "params".
type GraphNode struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Position GraphPosition  `json:"position"`
	Data     map[string]any `json:"data,omitempty"`
}

// GraphPosition is the canvas coordinate of a node.
type GraphPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// GraphEdge is one canvas edge. SourceHandle distinguishes branch outlets
// on multi-outlet nodes (e.g. Switch cases); the engine ignores it and
// derives routing from params.
type GraphEdge struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	Target       string `json:"target"`
	SourceHandle string `json:"sourceHandle,omitempty"`
}

// Component is one node in the execution topology.
type Component struct {
	Obj        ComponentObj `json:"obj"`
	Upstream   []string     `json:"upstream"`
	Downstream []string     `json:"downstream"`
}

// ComponentObj names the component type and carries its params.
type ComponentObj struct {
	ComponentName string         `json:"component_name"`
	Params        map[string]any `json:"params"`
}

// Normalize validates the DSL and repairs a missing projection:
//
//   - Graph present, Components missing -> derive topology from edges
//     (component_name = node.Type, params from node.Data["params"]).
//   - Components present, Graph missing -> derive a deterministic default
//     layout (column spacing 200px, rows 120px, layering by longest
//     distance from an entry node).
//   - Both present -> kept as-is.
//
// Malformed entries (edges referencing unknown nodes, self-edges, nil
// components, non-object params) are skipped, not fatal. The input is never
// mutated — a defensive deep copy is returned. Validation errors (no entry
// node; unknown component_name) do fail.
func Normalize(dsl *DSL) (*DSL, error) {
	if dsl == nil {
		return nil, fmt.Errorf("workflow: DSL is nil")
	}
	out := &DSL{
		Version:   dsl.Version,
		Variables: copyVars(dsl.Variables),
	}
	if out.Version == 0 {
		out.Version = DSLVersion
	}

	hasComponents := len(dsl.Components) > 0
	hasGraph := dsl.Graph != nil && (len(dsl.Graph.Nodes) > 0 || len(dsl.Graph.Edges) > 0)

	switch {
	case hasComponents:
		out.Components = copyComponents(dsl.Components)
		if !hasGraph {
			graph, err := defaultLayout(out.Components)
			if err != nil {
				return nil, err
			}
			out.Graph = graph
		} else {
			out.Graph = copyGraph(dsl.Graph)
		}
	case hasGraph:
		comps, err := componentsFromGraph(dsl.Graph)
		if err != nil {
			return nil, err
		}
		out.Components = comps
		out.Graph = copyGraph(dsl.Graph)
	default:
		return nil, fmt.Errorf("workflow: DSL has neither graph nor components")
	}

	if err := validate(out.Components); err != nil {
		return nil, err
	}
	return out, nil
}

// validate enforces: non-empty params maps, known component names, at least
// one entry node (no upstream, not targeted by any edge).
func validate(comps map[string]*Component) error {
	if len(comps) == 0 {
		return fmt.Errorf("workflow: DSL has no components")
	}
	targeted := map[string]bool{}
	for id, c := range comps {
		if c == nil {
			return fmt.Errorf("workflow: component %q is null", id)
		}
		if c.Obj.Params == nil {
			c.Obj.Params = map[string]any{}
		}
		if !nodes.IsKnown(c.Obj.ComponentName) {
			return fmt.Errorf("workflow: component %q has unknown component_name %q (known: %v)",
				id, c.Obj.ComponentName, nodes.Known())
		}
		for _, d := range c.Downstream {
			targeted[d] = true
		}
	}
	entries := 0
	for id, c := range comps {
		if len(c.Upstream) == 0 && !targeted[id] {
			entries++
		}
	}
	if entries == 0 {
		return fmt.Errorf("workflow: DSL has no entry node (a node with no upstream that is not targeted by any edge)")
	}
	return nil
}

// componentsFromGraph derives the execution topology from the canvas view.
func componentsFromGraph(g *GraphView) (map[string]*Component, error) {
	comps := map[string]*Component{}
	for _, n := range g.Nodes {
		if n.ID == "" {
			continue
		}
		params := map[string]any{}
		if n.Data != nil {
			if p, ok := n.Data["params"].(map[string]any); ok {
				params = p
			}
		}
		comps[n.ID] = &Component{
			Obj: ComponentObj{ComponentName: n.Type, Params: params},
		}
	}
	for _, e := range g.Edges {
		src, srcOK := comps[e.Source]
		dst, dstOK := comps[e.Target]
		if !srcOK || !dstOK || e.Source == e.Target {
			continue // malformed edge: skip, don't panic
		}
		src.Downstream = appendUnique(src.Downstream, e.Target)
		dst.Upstream = appendUnique(dst.Upstream, e.Source)
	}
	return comps, nil
}

// defaultLayout builds a deterministic canvas view from the topology:
// x = 200 * layer (longest distance from an entry), y = 120 * index within
// the layer. Node iteration order is by sorted id, so the layout is stable.
func defaultLayout(comps map[string]*Component) (*GraphView, error) {
	layer := map[string]int{}
	visited := map[string]int{} // 0=white 1=in-progress 2=done
	var longest func(id string) int
	longest = func(id string) int {
		switch visited[id] {
		case 2:
			return layer[id]
		case 1:
			return 0 // cycle: clamp; cycle rejection is Compile's job
		}
		visited[id] = 1
		max := 0
		if c := comps[id]; c != nil {
			for _, up := range c.Upstream {
				if l := longest(up); l+1 > max && l >= 0 {
					max = l + 1
				}
			}
		}
		visited[id] = 2
		layer[id] = max
		return max
	}

	ids := make([]string, 0, len(comps))
	for id := range comps {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	layerIndex := map[int]int{}
	nodesOut := make([]GraphNode, 0, len(ids))
	for _, id := range ids {
		l := longest(id)
		y := layerIndex[l]
		layerIndex[l]++
		nodesOut = append(nodesOut, GraphNode{
			ID:       id,
			Type:     comps[id].Obj.ComponentName,
			Position: GraphPosition{X: float64(l * 200), Y: float64(y * 120)},
			Data:     map[string]any{"params": comps[id].Obj.Params},
		})
	}

	edgesOut := make([]GraphEdge, 0)
	for _, id := range ids {
		for _, d := range comps[id].Downstream {
			edgesOut = append(edgesOut, GraphEdge{ID: id + "->" + d, Source: id, Target: d})
		}
	}
	return &GraphView{Nodes: nodesOut, Edges: edgesOut}, nil
}

// ---- defensive copy helpers -------------------------------------------------

func copyComponents(in map[string]*Component) map[string]*Component {
	out := make(map[string]*Component, len(in))
	for id, c := range in {
		if c == nil {
			continue
		}
		params := map[string]any{}
		for k, v := range c.Obj.Params {
			params[k] = v
		}
		out[id] = &Component{
			Obj: ComponentObj{
				ComponentName: c.Obj.ComponentName,
				Params:        params,
			},
			Upstream:   append([]string(nil), c.Upstream...),
			Downstream: append([]string(nil), c.Downstream...),
		}
	}
	return out
}

func copyGraph(in *GraphView) *GraphView {
	if in == nil {
		return nil
	}
	out := &GraphView{
		Nodes: make([]GraphNode, len(in.Nodes)),
		Edges: make([]GraphEdge, len(in.Edges)),
	}
	for i, n := range in.Nodes {
		data := map[string]any{}
		for k, v := range n.Data {
			data[k] = v
		}
		out.Nodes[i] = GraphNode{ID: n.ID, Type: n.Type, Position: n.Position, Data: data}
	}
	copy(out.Edges, in.Edges)
	return out
}

func copyVars(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func appendUnique(list []string, v string) []string {
	for _, existing := range list {
		if existing == v {
			return list
		}
	}
	return append(list, v)
}
