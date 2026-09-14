package nodes

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// VarRefPattern matches the workflow variable-reference syntax:
//
//	{nodeId@param}   — output param of an earlier node (id: [a-zA-Z0-9_:-]+)
//	{sys.query}      — runtime request fields (sys.* namespace)
//	{sys.files}
//	{env.x}          — deployment-time constants (env.* namespace)
//
// The shape mirrors RAGFlow's variable_ref_patt (minus loop item/index,
// which this engine does not ship yet). Repeated braces and surrounding
// whitespace are tolerated: "{{ ref }}" resolves the same as "{ref}".
// Node ids include "-": the frontend's makeNodeId generates "llm-abc123".
var VarRefPattern = regexp.MustCompile(`\{+\s*([a-zA-Z0-9_:-]+@[A-Za-z0-9_.-]+|sys\.[A-Za-z0-9_.]+|env\.[A-Za-z0-9_.]+)\s*\}+`)

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

// indexValue addresses slice[idx] with clear errors; generic over the
// engine's static slice types ([]any, []map[string]any, []string).
func indexValue[S ~[]E, E any](nodeID, out string, slice S, key string) (any, error) {
	idx, err := strconv.Atoi(key)
	if err != nil {
		return nil, fmt.Errorf("node %s output %q is an array, segment %q is not an index", nodeID, out, key)
	}
	if idx < 0 || idx >= len(slice) {
		return nil, fmt.Errorf("node %s output %q index %d out of range (len %d)", nodeID, out, idx, len(slice))
	}
	return any(slice[idx]), nil
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
		// A dotted param addresses a nested value of a structured output,
		// e.g. {agg@values.picked} — the first segment is the output name,
		// the rest walk map keys or array indices ({retr@chunks.2.content}
		// addresses the third chunk). Live-run outputs carry their static Go
		// types ([]map[string]any from Retrieval/DataOps, []string files,
		// map[string]string headers) — only checkpoint/pinned copies are
		// JSON-rounded to []any/map[string]any — so every shape is handled.
		// User-declared names (Start form fields) may contain dots: the exact
		// match is tried first, the segmented walk only on miss.
		if v, ok := st.GetOutput(nodeID, param); ok {
			return v, nil
		}
		segs := strings.Split(param, ".")
		v, ok := st.GetOutput(nodeID, segs[0])
		if !ok {
			return nil, fmt.Errorf("node %s has no output %q yet", nodeID, segs[0])
		}
		for _, key := range segs[1:] {
			var next any
			switch holder := v.(type) {
			case map[string]any:
				var found bool
				next, found = holder[key]
				if !found {
					return nil, fmt.Errorf("node %s output %q has no key %q", nodeID, segs[0], key)
				}
			case map[string]string:
				s, found := holder[key]
				if !found {
					return nil, fmt.Errorf("node %s output %q has no key %q", nodeID, segs[0], key)
				}
				next = s
			case []any:
				n, ierr := indexValue(nodeID, segs[0], holder, key)
				if ierr != nil {
					return nil, ierr
				}
				next = n
			case []map[string]any:
				n, ierr := indexValue(nodeID, segs[0], holder, key)
				if ierr != nil {
					return nil, ierr
				}
				next = n
			case []string:
				n, ierr := indexValue(nodeID, segs[0], holder, key)
				if ierr != nil {
					return nil, ierr
				}
				next = n
			default:
				return nil, fmt.Errorf("node %s output %q is not addressable at %q", nodeID, segs[0], key)
			}
			v = next
		}
		return v, nil
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
