package nodes

import (
	"fmt"
	"regexp"
	"strings"
)

// VarRefPattern matches the workflow variable-reference syntax:
//
//	{nodeId@param}   — output param of an earlier node (id: [a-zA-Z0-9_:]+)
//	{sys.query}      — runtime request fields (sys.* namespace)
//	{sys.files}
//	{env.x}          — deployment-time constants (env.* namespace)
//
// The shape mirrors RAGFlow's variable_ref_patt (minus loop item/index,
// which this engine does not ship yet). Repeated braces and surrounding
// whitespace are tolerated: "{{ ref }}" resolves the same as "{ref}".
var VarRefPattern = regexp.MustCompile(`\{+\s*([a-zA-Z0-9_:]+@[A-Za-z0-9_.-]+|sys\.[A-Za-z0-9_.]+|env\.[A-Za-z0-9_.]+)\s*\}+`)

// ExtractRefs returns the unique references (without braces) in s, in
// first-occurrence order. Pure regex — no state access.
func ExtractRefs(s string) []string {
	matches := VarRefPattern.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		ref := m[1]
		if !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	return out
}

// Render resolves every reference in s against st. A reference whose source
// has not produced a value yet fails with an error naming the ref, so
// mis-ordered graphs surface at the offending node instead of rendering
// silently-empty text. Non-string values render via %v.
func Render(s string, st StateView) (string, error) {
	if s == "" || st == nil {
		return s, nil
	}
	var unresolved []string
	out := VarRefPattern.ReplaceAllStringFunc(s, func(m string) string {
		sub := VarRefPattern.FindStringSubmatch(m)
		if len(sub) < 2 {
			return m // unreachable: m came from the same pattern
		}
		ref := sub[1]
		v, err := lookupRef(ref, st)
		if err != nil {
			unresolved = append(unresolved, ref)
			return m
		}
		return renderValue(v)
	})
	if len(unresolved) > 0 {
		return "", fmt.Errorf("workflow: unresolved reference(s): %s", strings.Join(unresolved, ", "))
	}
	return out, nil
}

func lookupRef(ref string, st StateView) (any, error) {
	switch {
	case strings.HasPrefix(ref, "sys."):
		if v, ok := st.SysValue(strings.TrimPrefix(ref, "sys.")); ok {
			return v, nil
		}
		return nil, fmt.Errorf("sys.%s not set", strings.TrimPrefix(ref, "sys."))
	case strings.HasPrefix(ref, "env."):
		if v, ok := st.EnvValue(strings.TrimPrefix(ref, "env.")); ok {
			return v, nil
		}
		return nil, fmt.Errorf("env.%s not set", strings.TrimPrefix(ref, "env."))
	default:
		nodeID, param, ok := strings.Cut(ref, "@")
		if !ok || nodeID == "" || param == "" {
			return nil, fmt.Errorf("malformed node reference %q", ref)
		}
		if v, ok := st.GetOutput(nodeID, param); ok {
			return v, nil
		}
		return nil, fmt.Errorf("node %s has no output %q yet", nodeID, param)
	}
}

func renderValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}
