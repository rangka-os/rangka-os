package changeset

import (
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// absVal is what an expression evaluates to, as far as hidden fields go: a record (or a list of records) of some
// models, or a plain value (nil models). link marks the value of a link field: at run time it is only the linked
// record's name, so copying it or comparing it copies nothing hidden; reading a member of it follows the link
// (audit copy-hidden H1). keyed marks a map literal holding links: any member of it is one of those links.
type absVal struct {
	models map[string]bool
	link   bool
	keyed  bool
}

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

// whole is a value that carries a record's hidden fields when it is copied as is: a record, not a link's name.
func (v absVal) whole() bool { return v.isRec() && !v.link }

func linkOf(v absVal) absVal {
	if !v.isRec() {
		return absVal{}
	}
	return absVal{models: v.models, link: true}
}

// join is either of two values (the branches of a condition, the elements of a list): a record if either is one.
func join(a, b absVal) absVal {
	if !a.isRec() {
		return b
	}
	if !b.isRec() {
		return a
	}
	out := map[string]bool{}
	for m := range a.models {
		out[m] = true
	}
	for m := range b.models {
		out[m] = true
	}
	return absVal{models: out, link: a.link && b.link, keyed: a.keyed || b.keyed}
}

// linkPart is the links a value carries on (a link, a list or map of links), dropped when it is a plain value or a
// record (a record is counted where it is used).
func linkPart(v absVal) absVal {
	if v.link {
		return v
	}
	return absVal{}
}

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
	var links []string
	// a member that is no field of the linked model is a key of a map literal (or a list) holding the link: the link
	// itself (audit copy-hidden H2); a keyed value may be either, so both are followed
	carry := v.keyed
	for m := range v.models {
		fd := c.fields(m)[name]
		if fd == nil {
			if name == "record" || name == "before" { // event.record, event.before: the same record
				next = append(next, m)
			} else if v.link {
				carry = true
			}
			continue
		}
		if h := meta.StrList(fd["hidden_for"]); len(h) > 0 {
			c.leak(m+"."+name, h)
			continue
		}
		switch fd["type"] {
		case "link", "dynamic_link":
			links = append(links, meta.Str(fd, "to"))
		case "table":
			next = append(next, meta.Str(fd, "of"))
		}
	}
	out := join(recOf(next...), linkOf(recOf(links...)))
	if carry {
		out = join(out, linkOf(v))
	}
	return out
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
				return v // an element of a list of records or links
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
		// size counts and a conversion turns a link into its name: neither carries a link on; any other function may
		// return one of its arguments (max, min, a list function), so their links stay followable (audit H2)
		var out absVal
		scalar := false
		switch e.Fn {
		case "size", "string", "int", "uint", "double", "bool":
			scalar = true
		}
		args := e.Args
		if e.Target != nil {
			args = append([]cel.Expr{e.Target}, args...)
		}
		for _, a := range args {
			v := c.eval(a, scope)
			if e.Fn != "size" {
				c.use(v)
			}
			if !scalar {
				out = join(out, linkPart(v))
			}
		}
		return out
	case *cel.Unary:
		c.use(c.eval(e.X, scope))
		return absVal{}
	case *cel.Binary:
		l, r := c.eval(e.L, scope), c.eval(e.R, scope)
		switch e.Op {
		case "==", "!=", "<", "<=", ">", ">=", "in", "&&", "||":
			// a comparison yields a bool; the fields it reads were already counted by field
		default:
			c.use(l)
			c.use(r)
			return join(linkPart(l), linkPart(r)) // list + list keeps the links of both
		}
		return absVal{}
	case *cel.Cond:
		c.eval(e.C, scope)
		return join(c.eval(e.T, scope), c.eval(e.F, scope))
	case *cel.List:
		// a list of links stays one (its elements can be followed); a record in a list is copied whole
		var out absVal
		for _, el := range e.Elems {
			v := c.eval(el, scope)
			c.use(v)
			out = join(out, linkPart(v))
		}
		return out
	case *cel.MapLit:
		var out absVal
		for i := range e.Keys {
			c.use(c.eval(e.Keys[i], scope))
			v := c.eval(e.Vals[i], scope)
			c.use(v)
			if v.link {
				out = join(out, absVal{models: v.models, link: true, keyed: true})
			}
		}
		return out
	}
	return absVal{}
}

// use marks a record that becomes part of a plain value (a function argument, an operand, a list element): its
// hidden fields go with it.
func (c *copyScan) use(v absVal) {
	if v.whole() {
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
