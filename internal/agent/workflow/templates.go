package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Built-in workflow templates: read-only preset DSLs loaded once at
// startup from configDir/workflow_templates/*.json (the same path-based
// config loading builtin_agents.yaml uses). Templates are never
// user-mutable — the service layer copies one into a tenant as a
// published workflow (InstantiateWorkflowTemplate), binding its KB
// placeholders on the way in.

// TemplateI18n is one locale's display strings.
type TemplateI18n struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// WorkflowTemplate is one preset workflow file. KBPlaceholders lists the
// KB_* sentinel ids appearing in kb_ids params; every one must be bound at
// instantiation time.
type WorkflowTemplate struct {
	ID             string                  `json:"id"`
	Category       string                  `json:"category,omitempty"`
	KBPlaceholders []string                `json:"kb_placeholders,omitempty"`
	I18n           map[string]TemplateI18n `json:"i18n"`
	DSL            *DSL                    `json:"dsl"`

	nodeCount int
}

// NodeCount returns the number of components (derived at load time).
func (t *WorkflowTemplate) NodeCount() int { return t.nodeCount }

// Localized resolves the display strings for a locale: exact match, then
// the first entry sharing the locale's base language, then "default".
func (t *WorkflowTemplate) Localized(locale string) TemplateI18n {
	if v, ok := t.I18n[locale]; ok && v.Name != "" {
		return v
	}
	if base, _, found := strings.Cut(locale, "-"); found {
		for k, v := range t.I18n {
			if k != "default" && strings.HasPrefix(k, base) && v.Name != "" {
				return v
			}
		}
	}
	return t.I18n["default"]
}

var (
	templatesMu    sync.RWMutex
	templatesByID  = map[string]*WorkflowTemplate{}
	templatesOrder []string
)

// kbPlaceholderPattern names the KB_* placeholder convention: every
// kb_ids entry matching it must be declared in kb_placeholders.
var kbPlaceholderPattern = regexp.MustCompile(`^KB_[A-Z0-9_]+$`)

// LoadWorkflowTemplates reads every *.json under
// configDir/workflow_templates, validates each (compile + KB-placeholder
// consistency) and installs them as the global registry. A missing
// directory loads an empty registry (minimal deployments); an unreadable
// or invalid file returns an error naming the file — a broken template
// must fail startup, not a production run.
func LoadWorkflowTemplates(configDir string) error {
	byID := map[string]*WorkflowTemplate{}
	var order []string
	dir := filepath.Join(configDir, "workflow_templates")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			installTemplates(byID, order)
			return nil
		}
		return fmt.Errorf("workflow templates: read %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		tpl, lerr := loadTemplateFile(path)
		if lerr != nil {
			return lerr
		}
		if _, dup := byID[tpl.ID]; dup {
			return fmt.Errorf("workflow templates: duplicate id %q (%s)", tpl.ID, path)
		}
		byID[tpl.ID] = tpl
		order = append(order, tpl.ID)
	}
	installTemplates(byID, order)
	return nil
}

func loadTemplateFile(path string) (*WorkflowTemplate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("workflow templates: read %s: %w", path, err)
	}
	var tpl WorkflowTemplate
	if err := json.Unmarshal(raw, &tpl); err != nil {
		return nil, fmt.Errorf("workflow templates: parse %s: %w", path, err)
	}
	if tpl.ID == "" {
		return nil, fmt.Errorf("workflow templates: %s: missing \"id\"", path)
	}
	if tpl.I18n["default"].Name == "" {
		return nil, fmt.Errorf("workflow templates: %s: i18n needs a non-empty default.name", path)
	}
	if tpl.DSL == nil {
		return nil, fmt.Errorf("workflow templates: %s: missing \"dsl\"", path)
	}
	// Compile with zero Deps: factories validate params at build time,
	// injected funcs are only needed at Invoke time.
	if _, err := Compile(tpl.DSL, Deps{}); err != nil {
		return nil, fmt.Errorf("workflow templates: %s does not compile: %w", path, err)
	}
	norm, err := Normalize(tpl.DSL)
	if err != nil {
		return nil, fmt.Errorf("workflow templates: %s: %w", path, err)
	}
	tpl.nodeCount = len(norm.Components)
	if err := checkKBPlaceholders(&tpl, norm); err != nil {
		return nil, fmt.Errorf("workflow templates: %s: %w", path, err)
	}
	return &tpl, nil
}

// checkKBPlaceholders cross-checks the declared kb_placeholders against
// the KB_* strings actually referenced in kb_ids params: every declared
// placeholder must be used, and every KB_* kb_ids entry must be declared.
func checkKBPlaceholders(t *WorkflowTemplate, norm *DSL) error {
	used := map[string]bool{}
	for _, comp := range norm.Components {
		if comp == nil {
			continue
		}
		for _, kbID := range KBIDsOf(comp.Obj.Params) {
			if kbPlaceholderPattern.MatchString(kbID) {
				used[kbID] = true
			}
		}
	}
	declared := map[string]bool{}
	for _, p := range t.KBPlaceholders {
		if !used[p] {
			return fmt.Errorf("kb placeholder %q is declared but referenced by no kb_ids", p)
		}
		declared[p] = true
	}
	for kb := range used {
		if !declared[kb] {
			return fmt.Errorf("kb_ids references undeclared placeholder %q", kb)
		}
	}
	return nil
}

// KBIDsOf extracts params.kb_ids (Retrieval and Agent both carry the key).
func KBIDsOf(params map[string]any) []string {
	raw, ok := params["kb_ids"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// BindKBPlaceholders returns a defensive copy of the template DSL with
// every kb_ids placeholder replaced by its binding (placeholder → real KB
// id). Unbound placeholders are left in place — the service layer rejects
// incomplete bindings before calling this.
func (t *WorkflowTemplate) BindKBPlaceholders(bindings map[string]string) (*DSL, error) {
	norm, err := Normalize(t.DSL)
	if err != nil {
		return nil, err
	}
	for _, comp := range norm.Components {
		if comp == nil {
			continue
		}
		ids := KBIDsOf(comp.Obj.Params)
		if len(ids) == 0 {
			continue
		}
		next := make([]any, len(ids))
		changed := false
		for i, id := range ids {
			if bound, ok := bindings[id]; ok && bound != "" {
				next[i] = bound
				changed = true
			} else {
				next[i] = id
			}
		}
		if changed {
			// Normalize hands back fresh per-component params maps, so
			// replacing one key cannot touch the shared template.
			comp.Obj.Params["kb_ids"] = next
		}
	}
	return norm, nil
}

func installTemplates(byID map[string]*WorkflowTemplate, order []string) {
	templatesMu.Lock()
	defer templatesMu.Unlock()
	templatesByID = byID
	templatesOrder = order
}

// ListWorkflowTemplates returns all loaded templates in load order.
func ListWorkflowTemplates() []*WorkflowTemplate {
	templatesMu.RLock()
	defer templatesMu.RUnlock()
	out := make([]*WorkflowTemplate, len(templatesOrder))
	for i, id := range templatesOrder {
		out[i] = templatesByID[id]
	}
	return out
}

// GetWorkflowTemplate returns the loaded template by id (nil when absent).
func GetWorkflowTemplate(id string) *WorkflowTemplate {
	templatesMu.RLock()
	defer templatesMu.RUnlock()
	return templatesByID[id]
}
