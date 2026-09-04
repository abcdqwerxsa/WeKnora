package workflow

import (
	"github.com/Tencent/WeKnora/internal/agent/workflow/nodes"
)

// VarRefPattern is the variable-reference syntax accepted in templates:
// {nodeId@param}, {sys.query}, {sys.files}, {env.x}. See nodes.VarRefPattern.
var VarRefPattern = nodes.VarRefPattern

// ExtractRefs returns the unique references (without braces) in s, in
// first-occurrence order.
func ExtractRefs(s string) []string { return nodes.ExtractRefs(s) }

// ResolveTemplate renders s against the run state. A reference that has no
// value yet returns an error naming the reference.
func ResolveTemplate(s string, state *CanvasState) (string, error) {
	return nodes.Render(s, state)
}
