package changeset

import (
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// absVal is what an expression evaluates to, as far as hidden fields go: a record (or a list of records) of some
// models, or a plain value (nil models).
type absVal struct{ models map[string]bool }

func recOf(ms ...string) absVal {
	out := map[string]bool{}
	for _, m := range ms {
		if m != "" {
			out[m] = true
		}
	}
	if len(out) == 0 {
		return absVal{}
	}
	return absVal{models: out}
}

func (v absVal) isRec() bool { return len(v.models) > 0 }

// copyScan follows an expression's reads (audit flow-locked F2, copy-hidden G1-G4): every field read that has
// hidden_for is collected; a record used whole, indexed by a computed key, or an expression that does not parse
// counts as reading all of its hidden fields, so the scan errs towards high.
type copyScan struct {
	env  Env
	refs map[string]hiddenRef
	// self is a model the change set adds or changes and its fields as written: an app or module model the change set
	// creates is not installed yet, so companyModelFields does not know it (audit copy-hidden G4)
	self       string
	selfFields map[string]map[string]any
}

func (c *copyScan) fields(m string) map[string]map[string]any {
	if fs := companyModelFields(c.env, m); fs != nil {
		if m == c.self {
			for k, v := range c.selfFields {
				if _, ok := fs[k]; !ok {
					fs[k] = v
				}
			}
		}
		return fs
	}
	if m == c.self {
		return c.selfFields
	}
	return nil
}

func (c *copyScan) leak(path string, hidden []string) {
	if _, ok := c.refs[path]; !ok {
		c.refs[path] = hiddenRef{path: path, hidden: hidden}
	}
}

// leakAll records every hidden field of the record's models (the record is copied whole, or by a key the scan cannot read).
func (c *copyScan) leakAll(v absVal, why string) {
	for m := range v.models {
		for name, fd := range c.fields(m) {
			if h := meta.StrList(fd["hidden_for"]); len(h) > 0 {
				c.leak(m+"."+name+" ("+why+")", h)
			}
		}
	}
}

func (c *copyScan) field(v absVal, name string) absVal {
	var next []string
	for m := range v.models {
		fd := c.fields(m)[name]
		if fd == nil {
			if name == "record" || name == "before" { // event.record, event.before: the same record
				next = append(next, m)
			}
			continue
		}
		if h := meta.StrList(fd["hidden_for"]); len(h) > 0 {
			c.leak(m+"."+name, h)
			continue
		}
		switch fd["type"] {
		case "link", "dynamic_link":
			next = append(next, meta.Str(fd, "to"))
		case "table":
			next = append(next, meta.Str(fd, "of"))
		}
	}
	return recOf(next...)
}

func (c *copyScan) eval(x cel.Expr, scope map[string]absVal) absVal {
	switch e := x.(type) {
	case nil, *cel.Lit:
		return absVal{}
	case *cel.Ident:
		if v, ok := scope[e.Name]; ok {
			return v
		}
		if self, ok := scope["\x00self"]; ok {
			return c.field(self, e.Name)
		}
		return absVal{}
	case *cel.Select:
		return c.field(c.eval(e.X, scope), e.Field)
	case *cel.Has:
		c.eval(e.X, scope)
		return absVal{}
	case *cel.Index:
		v := c.eval(e.X, scope)
		if !v.isRec() {
			c.eval(e.I, scope)
			return absVal{}
		}
		if lit, ok := e.I.(*cel.Lit); ok {
			switch k := lit.V.(type) {
			case string:
				return c.field(v, k)
			case int64, int, float64:
				return v // an element of a list of records
			}
		}
		c.eval(e.I, scope)
		c.leakAll(v, "read by a computed key")
		return absVal{}
	case *cel.Comp:
		r := c.eval(e.Range, scope)
		inner := make(map[string]absVal, len(scope)+1)
		for k, v := range scope {
			inner[k] = v
		}
		inner[e.Var] = r
		if e.Filter != nil {
			c.use(c.eval(e.Filter, inner))
		}
		b := c.eval(e.Body, inner)
		if e.Kind == "map" {
			return b
		}
		if e.Kind == "filter" {
			return r
		}
		c.use(b)
		return absVal{}
	case *cel.Call:
		if e.Target != nil {
			c.use(c.eval(e.Target, scope))
		}
		for _, a := range e.Args {
			c.use(c.eval(a, scope))
		}
		return absVal{}
	case *cel.Unary:
		c.use(c.eval(e.X, scope))
		return absVal{}
	case *cel.Binary:
		c.use(c.eval(e.L, scope))
		c.use(c.eval(e.R, scope))
		return absVal{}
	case *cel.Cond:
		c.use(c.eval(e.C, scope))
		t, f := c.eval(e.T, scope), c.eval(e.F, scope)
		var ms []string
		for m := range t.models {
			ms = append(ms, m)
		}
		for m := range f.models {
			ms = append(ms, m)
		}
		return recOf(ms...)
	case *cel.List:
		for _, el := range e.Elems {
			c.use(c.eval(el, scope))
		}
		return absVal{}
	case *cel.MapLit:
		for i := range e.Keys {
			c.use(c.eval(e.Keys[i], scope))
			c.use(c.eval(e.Vals[i], scope))
		}
		return absVal{}
	}
	return absVal{}
}

// use marks a record that becomes part of a plain value (a function argument, an operand, a list element): its
// hidden fields go with it.
func (c *copyScan) use(v absVal) {
	if v.isRec() {
		c.leakAll(v, "used whole")
	}
}

// hiddenReads lists the fields with hidden_for an expression reads. A name is a flow variable (vars: the models it
// may hold; event is the flow's record) or, with self set, a field of that model; link and table fields are followed.
// An expression that does not parse is taken to read every hidden field it could reach.
func hiddenReads(expr string, vars map[string]map[string]bool, self string, env Env) []hiddenRef {
	return hiddenReadsIn(expr, vars, self, nil, env)
}

// hiddenReadsIn is hiddenReads with the fields of the model self as the change set writes them.
func hiddenReadsIn(expr string, vars map[string]map[string]bool, self string, selfFields map[string]map[string]any, env Env) []hiddenRef {
	c := &copyScan{env: env, refs: map[string]hiddenRef{}, self: self, selfFields: selfFields}
	scope := map[string]absVal{}
	for name, ms := range vars {
		scope[name] = recOf(sortedSet(ms)...)
	}
	if self != "" {
		scope["\x00self"] = recOf(self)
	}
	x, err := cel.Parse(expr)
	if err != nil {
		for _, v := range scope {
			c.leakAll(v, "expression that does not parse")
		}
	} else {
		c.use(c.eval(x, scope)) // a whole record as the value copies it whole
	}
	out := make([]hiddenRef, 0, len(c.refs))
	for _, r := range c.refs {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// modelsOf is the models an expression's value may be a record (or list of records) of: the source of a for_each.
func modelsOf(expr string, vars map[string]map[string]bool, env Env) map[string]bool {
	c := &copyScan{env: env, refs: map[string]hiddenRef{}}
	scope := map[string]absVal{}
	for name, ms := range vars {
		scope[name] = recOf(sortedSet(ms)...)
	}
	x, err := cel.Parse(strings.TrimSpace(expr))
	if err != nil {
		return nil
	}
	return c.eval(x, scope).models
}
