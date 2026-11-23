package dom

import (
	"slices"

	"github.com/gomcpgo/htmltodocx/internal/css"
)

// varBudget bounds the bytes produced by var() substitution in one
// conversion. A stylesheet that defines many long variables on "*" would
// otherwise redo that work on every element. Real documents use a tiny
// fraction of it.
const varBudget = 32 << 20

// varScope holds the custom properties an element defines, already resolved,
// and points at the scope of the nearest ancestor that defines any. Scopes
// are never changed after they are built, so style copies can share them.
type varScope struct {
	parent *varScope
	vals   map[string]varValue
}

// varValue is a resolved custom property. ok is false when its value could
// not be resolved (an undefined reference or a cycle); such a property still
// hides the one it would inherit, and var() treats it as undefined.
type varValue struct {
	val string
	ok  bool
}

func (s *varScope) lookup(name string) (string, bool) {
	for ; s != nil; s = s.parent {
		if v, found := s.vals[name]; found {
			return v.val, v.ok
		}
	}
	return "", false
}

// customProperties returns the variable scope of an element. Declarations
// come in cascade order (later wins), as the caller applies other properties.
// Values are resolved here, where they are defined, so descendants inherit
// the result rather than re-resolving the reference in their own context, as
// the CSS specification requires. When the element defines no custom
// properties the parent's scope is returned unchanged.
func (c *conv) customProperties(parent *varScope, decls []css.Decl) *varScope {
	var raw map[string]string
	for _, d := range decls {
		if css.IsCustomProperty(d.Prop) {
			if raw == nil {
				raw = map[string]string{}
			}
			raw[d.Prop] = d.Value
		}
	}
	if raw == nil {
		return parent
	}
	scope := &varScope{parent: parent, vals: make(map[string]varValue, len(raw))}
	resolving := map[string]bool{}
	var resolve func(name string) (string, bool)
	resolve = func(name string) (string, bool) {
		if v, done := scope.vals[name]; done {
			return v.val, v.ok
		}
		r, own := raw[name]
		if !own {
			return parent.lookup(name)
		}
		if resolving[name] {
			return "", false // a cycle: every variable in it is invalid
		}
		resolving[name] = true
		v, ok := c.substitute(r, resolve)
		delete(resolving, name)
		scope.vals[name] = varValue{v, ok}
		return v, ok
	}
	// Map order is random; resolve in name order so output is reproducible.
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		resolve(name)
	}
	return scope
}

// substitute is css.SubstituteVars with the conversion-wide budget applied.
func (c *conv) substitute(val string, lookup func(string) (string, bool)) (string, bool) {
	if c.varBytes >= varBudget {
		c.warn("css", "too much CSS variable substitution; further var() references were ignored")
		return "", false
	}
	out, ok := css.SubstituteVars(val, lookup)
	c.varBytes += len(out)
	return out, ok
}
