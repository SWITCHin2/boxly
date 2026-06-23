package template

import "sync"

// Registry holds the live set of templates: the shipped builtins plus any
// custom templates an admin defines at runtime. It is safe for concurrent use.
type Registry struct {
	mu    sync.RWMutex
	order []string // stable display order
	byID  map[string]Template
}

// reg is the process-wide registry, seeded with the builtins.
var reg = newRegistry()

func newRegistry() *Registry {
	r := &Registry{byID: map[string]Template{}}
	for _, t := range builtins {
		t.Builtin = true
		r.order = append(r.order, t.ID)
		r.byID[t.ID] = t
	}
	return r
}

// All returns the enabled templates in display order.
func All() []Template { return reg.all(false) }

// AllIncludingDisabled returns every template (admin view).
func AllIncludingDisabled() []Template { return reg.all(true) }

func (r *Registry) all(includeDisabled bool) []Template {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Template, 0, len(r.order))
	for _, id := range r.order {
		t := r.byID[id]
		if t.Disabled && !includeDisabled {
			continue
		}
		out = append(out, t)
	}
	return out
}

// Get returns a template by id, falling back to Default for "", and reports
// whether the id was known.
func Get(id string) (Template, bool) {
	if id == "" {
		id = Default
	}
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	if t, ok := reg.byID[id]; ok {
		return t, true
	}
	return reg.byID[Default], false
}

// Upsert adds or updates a custom template (builtins cannot be replaced wholesale
// but their pool bounds / disabled flag may be overridden). Returns the merged set.
func Upsert(t Template) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	existing, ok := reg.byID[t.ID]
	if ok && existing.Builtin {
		// Only allow overriding mutable fields on builtins.
		existing.WarmMin, existing.WarmMax, existing.Disabled, existing.Manifest = t.WarmMin, t.WarmMax, t.Disabled, t.Manifest
		reg.byID[t.ID] = existing
		return
	}
	if !ok {
		reg.order = append(reg.order, t.ID)
	}
	reg.byID[t.ID] = t
}

// Remove deletes a custom template (builtins are kept).
func Remove(id string) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if t, ok := reg.byID[id]; ok && t.Builtin {
		return
	}
	delete(reg.byID, id)
	for i, x := range reg.order {
		if x == id {
			reg.order = append(reg.order[:i], reg.order[i+1:]...)
			break
		}
	}
}

// LoadCustom resets to the builtins, then applies the given custom templates and
// builtin overrides — used when (re)loading from the admin ConfigMap.
func LoadCustom(custom []Template) {
	reg.mu.Lock()
	defer reg.mu.Unlock()

	reg.order = nil
	reg.byID = map[string]Template{}
	for _, t := range builtins {
		t.Builtin = true
		reg.order = append(reg.order, t.ID)
		reg.byID[t.ID] = t
	}
	for _, t := range custom {
		existing, ok := reg.byID[t.ID]
		if ok && existing.Builtin {
			existing.WarmMin, existing.WarmMax, existing.Disabled, existing.Manifest = t.WarmMin, t.WarmMax, t.Disabled, t.Manifest
			reg.byID[t.ID] = existing
			continue
		}
		if !ok {
			reg.order = append(reg.order, t.ID)
		}
		reg.byID[t.ID] = t
	}
}
