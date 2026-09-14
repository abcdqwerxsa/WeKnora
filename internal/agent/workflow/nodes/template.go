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
// engine's static slice types ([]any, []map[string]any, []string). src
// names the value being addressed (e.g. `node llm-1 output "chunks"`).
func indexValue[S ~[]E, E any](src string, slice S, key string) (any, error) {
	idx, err := strconv.Atoi(key)
	if err != nil {
		return nil, fmt.Errorf("%s: segment %q is not an index", src, key)
	}
	if idx < 0 || idx >= len(slice) {
		return nil, fmt.Errorf("%s: index %d out of range (len %d)", src, idx, len(slice))
	}
	return any(slice[idx]), nil
}

// walkRefPath descends v by dotted segments (map keys or array indices).
// src names the root value in error messages, e.g. `node llm-1 output
// "chunks"` or `sys.files`.
func walkRefPath(src string, v any, segs []string) (any, error) {
	for _, key := range segs {
		var next any
		switch holder := v.(type) {
		case map[string]any:
			var found bool
			next, found = holder[key]
			if !found {
				return nil, fmt.Errorf("%s has no key %q", src, key)
			}
		case map[string]string:
			s, found := holder[key]
			if !found {
				return nil, fmt.Errorf("%s has no key %q", src, key)
			}
			next = s
		case []any:
			n, ierr := indexValue(src, holder, key)
			if ierr != nil {
				return nil, ierr
			}
			next = n
		case []map[string]any:
			n, ierr := indexValue(src, holder, key)
			if ierr != nil {
				return nil, ierr
			}
			next = n
		case []string:
			n, ierr := indexValue(src, holder, key)
			if ierr != nil {
				return nil, ierr
			}
			next = n
		default:
			return nil, fmt.Errorf("%s is not addressable at %q", src, key)
		}
		v = next
	}
	return v, nil
}

func lookupRef(ref string, st StateView) (any, error) {
	// Tolerate braced refs ("{node@out}") from params that carry raw
	// references (VariableAggregator / DataOps variables[].ref): extract the
	// canonical stripped form; already-stripped refs pass through unchanged.
	// Multiple refs in one string are rejected — the contract is a single
	// reference, and silently binding only the first would mask the error.
	trimmed := strings.TrimSpace(ref)
	if m := VarRefPattern.FindStringSubmatch(trimmed); m != nil {
		if all := VarRefPattern.FindAllStringSubmatch(trimmed, -1); len(all) > 1 {
			return nil, fmt.Errorf("expected a single reference, found %d in %q", len(all), ref)
		}
		ref = m[1]
	}
	switch {
	case strings.HasPrefix(ref, "sys."):
		key := strings.TrimPrefix(ref, "sys.")
		if v, ok := st.SysValue(key); ok {
			return v, nil
		}
		// Dotted sys refs walk the same path as node outputs: {sys.files.0}
		// addresses the first uploaded file (sys.files materializes as
		// []string). Exact key first, segmented walk on miss — mirroring
		// user-declared Start names that may themselves contain dots.
		if segs := strings.Split(key, "."); len(segs) > 1 {
			if v, ok := st.SysValue(segs[0]); ok {
				return walkRefPath("sys."+segs[0], v, segs[1:])
			}
		}
		return nil, fmt.Errorf("sys.%s not set", key)
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
		return walkRefPath(fmt.Sprintf("node %s output %q", nodeID, segs[0]), v, segs[1:])
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
