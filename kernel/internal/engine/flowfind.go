package engine

import (
	"fmt"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// flowFindSQL turns the plain conditions of a flow find's where: into SQL over the model's table: each top-level &&
// operand of the form field == value, value == field, field in list, docstatus == n or docstatus != n, where the
// value side reads no field of the record (event, flow variables, literals) and evaluates now. Only conditions whose
// SQL keeps every row the CEL operand keeps are used: text columns (data, link, select...) without compute: compared
// to a non-empty text, name, and docstatus of a submittable model. Anything else stays CEL only. The caller still
// evaluates the whole where: on each row, so the SQL only narrows what is read (contract 0025 lock unchanged).
// conds use $2.. ($1 is the org).
func flowFindSQL(o *op, m *meta.Model, where string, vars map[string]any) ([]string, []any) {
	if strings.TrimSpace(where) == "" {
		return nil, nil
	}
	p, err := cel.Compile(where)
	if err != nil {
		return nil, nil
	}
	var conds []string
	var args []any
	sc := o.scope(m, store.Record{})
	sc.extra = vars
	env := sc.env()
	value := func(x cel.Expr) (any, bool) {
		if flowReadsRecord(m, x, vars) {
			return nil, false
		}
		v, err := (&cel.Program{Root: x}).Eval(env)
		if err != nil {
			return nil, false
		}
		return cel.Unwrap(v), true
	}
	for _, c := range conjuncts(p.Root) {
		b, ok := c.(*cel.Binary)
		if !ok {
			continue
		}
		col, other := "", cel.Expr(nil)
		if id, ok := b.L.(*cel.Ident); ok {
			col, other = id.Name, b.R
		}
		if col == "" || pushColumn(m, col, vars) == "" {
			if id, ok := b.R.(*cel.Ident); ok && b.Op == "==" {
				col, other = id.Name, b.L
			}
		}
		kind := pushColumn(m, col, vars)
		if kind == "" {
			continue
		}
		v, ok := value(other)
		if !ok {
			continue
		}
		switch {
		case kind == "docstatus" && (b.Op == "==" || b.Op == "!="):
			n, ok := findWhole(v)
			if !ok {
				continue
			}
			args = append(args, n)
			op := "="
			if b.Op == "!=" {
				op = "<>"
			}
			conds = append(conds, fmt.Sprintf("docstatus %s $%d", op, len(args)+1))
		case kind == "text" && b.Op == "==":
			sv, ok := v.(string)
			if !ok || sv == "" {
				continue
			}
			args = append(args, sv)
			conds = append(conds, fmt.Sprintf("%s = $%d", pg.QuoteIdent(col), len(args)+1))
		case kind == "text" && b.Op == "in" && b.L != other:
			list, ok := v.([]any)
			if !ok {
				continue
			}
			names := make([]string, 0, len(list))
			for _, x := range list {
				sx, ok := cel.Unwrap(x).(string)
				if !ok {
					names = nil
					break
				}
				names = append(names, sx)
			}
			if names == nil {
				continue // a non-text member could match through CEL's heterogeneous equality; leave it to CEL
			}
			args = append(args, pg.Array(names))
			conds = append(conds, fmt.Sprintf("%s = any($%d)", pg.QuoteIdent(col), len(args)+1))
		}
	}
	return conds, args
}

// conjuncts flattens a && b && c into its operands.
func conjuncts(x cel.Expr) []cel.Expr {
	if b, ok := x.(*cel.Binary); ok && b.Op == "&&" {
		return append(conjuncts(b.L), conjuncts(b.R)...)
	}
	return []cel.Expr{x}
}

// pushColumn says how an identifier of a where: maps to a column: "text" (a text column a flow scope reads as it is
// stored, or name), "docstatus", or "" (not pushed: a flow variable, a computed or non-text field, status).
func pushColumn(m *meta.Model, name string, vars map[string]any) string {
	if name == "" {
		return ""
	}
	if f := m.Field(name); f != nil {
		if f.Name == "status" || f.Compute != "" || !store.IsColumn(f) || store.SQLType(f.Type) != "text" {
			return ""
		}
		return "text"
	}
	if _, shadow := vars[name]; shadow {
		return ""
	}
	switch name {
	case "name":
		return "text"
	case "docstatus":
		if m.IsSubmittable {
			return "docstatus"
		}
	}
	return ""
}

// flowReadsRecord reports whether x may read the record being tested (a field, a base column, status), or uses a macro.
func flowReadsRecord(m *meta.Model, x cel.Expr, vars map[string]any) bool {
	switch n := x.(type) {
	case nil, *cel.Lit:
		return false
	case *cel.Ident:
		if m.Field(n.Name) != nil {
			return true
		}
		if _, ok := vars[n.Name]; ok {
			return false
		}
		switch n.Name {
		case "name", "created_by", "created_at", "modified_at", "amended_from", "docstatus", "status":
			return true
		}
		return false
	case *cel.Select:
		return flowReadsRecord(m, n.X, vars)
	case *cel.Has:
		return flowReadsRecord(m, n.X, vars)
	case *cel.Call:
		if n.Fn == "option" {
			return true // option() reads a field of the record
		}
		if flowReadsRecord(m, n.Target, vars) {
			return true
		}
		for _, a := range n.Args {
			if flowReadsRecord(m, a, vars) {
				return true
			}
		}
		return false
	case *cel.Index:
		return flowReadsRecord(m, n.X, vars) || flowReadsRecord(m, n.I, vars)
	case *cel.List:
		for _, a := range n.Elems {
			if flowReadsRecord(m, a, vars) {
				return true
			}
		}
		return false
	case *cel.MapLit:
		for i := range n.Keys {
			if flowReadsRecord(m, n.Keys[i], vars) || flowReadsRecord(m, n.Vals[i], vars) {
				return true
			}
		}
		return false
	case *cel.Unary:
		return flowReadsRecord(m, n.X, vars)
	case *cel.Binary:
		return flowReadsRecord(m, n.L, vars) || flowReadsRecord(m, n.R, vars)
	case *cel.Cond:
		return flowReadsRecord(m, n.C, vars) || flowReadsRecord(m, n.T, vars) || flowReadsRecord(m, n.F, vars)
	}
	return true // a macro (Comp) or anything new: not pushed
}

func findWhole(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case int:
		return int64(x), true
	case float64:
		if x == float64(int64(x)) {
			return int64(x), true
		}
	}
	return 0, false
}
