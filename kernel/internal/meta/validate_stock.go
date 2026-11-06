package meta

import (
	"fmt"
	"strings"
)

// legacyStock are the documents stock.post_moves has a built-in rule for; they may not declare stock: (contract
// 0021). Keep in step with engine.LegacyStockModels and LEGACY_STOCK in rangka_validate.py.
var legacyStock = map[string]bool{"goods_receipt": true, "stock_transfer": true, "stock_count": true,
	"stock_adjustment": true, "stock_transfer_receipt": true, "outlet_sales": true}

// stock checks a model's stock: block (contract 0021 §4); ports check_stock, in the same order.
func (v *validator) stock(p string, m *Model, fields map[string]map[string]any, visible map[string]vis) {
	st := asMap(m.Raw["stock"])
	if st == nil {
		return
	}
	onSubmit := false
	for _, h := range asList(m.Raw["on_submit"]) {
		onSubmit = onSubmit || h == "stock.post_moves"
	}
	if !boolean(m.Raw, "is_submittable") || !onSubmit {
		v.rep.add("error", p, "stock", "stock_hook", "stock: needs is_submittable: true and stock.post_moves in on_submit")
	}
	if legacyStock[m.Name] {
		v.rep.add("error", p, "stock", "stock_legacy", m.Name+" has a built-in stock rule and cannot declare stock:")
	}
	fieldKind := func(where string, src map[string]map[string]any, name, label string, ok func(map[string]any) bool, want string) {
		f, found := src[name]
		if !found {
			v.rep.add("error", p, where, "unknown_field", fmt.Sprintf(`"%s" is not a field%s`, name, label))
		} else if !ok(f) {
			v.rep.add("error", p, where, "bad_stock_field", fmt.Sprintf(`"%s" %s`, name, want))
		}
	}
	readOnlyCurrency := func(f map[string]any) bool {
		t := str(f, "type")
		return (t == "currency" || t == "money") && boolean(f, "read_only")
	}
	linkTo := func(target string) func(map[string]any) bool {
		return func(f map[string]any) bool { return str(f, "type") == "link" && str(f, "to") == target }
	}
	number := func(f map[string]any) bool {
		switch str(f, "type") {
		case "int", "float", "currency", "percent":
			return true
		}
		return false
	}
	if d, ok := st["date"].(string); ok {
		fieldKind("stock/date", fields, d, "", func(f map[string]any) bool { t := str(f, "type"); return t == "date" || t == "datetime" }, "is not a date field")
	}
	var reasons []string
	hasReasons := false
	if sm, ok := visible["stock_move"]; ok {
		if rf, ok := fieldMap(sm.m)["reason"]; ok {
			hasReasons = true
			for _, o := range asList(rf["options"]) {
				if om := asMap(o); om != nil {
					reasons = append(reasons, pyStr(om["value"]))
				}
			}
		}
	}
	movesList, _ := st["moves"].([]any)
	hasOut := false
	for _, r := range movesList {
		if rm := asMap(r); rm != nil {
			if _, ok := rm["out"]; ok {
				hasOut = true
			}
		}
	}
	for i, rv := range movesList {
		r := asMap(rv)
		if r == nil {
			continue
		}
		w := fmt.Sprintf("stock/moves/%d", i)
		var cfields map[string]map[string]any
		of := ""
		if lines, ok := r["lines"].(string); ok {
			lf, found := fields[lines]
			switch {
			case !found:
				v.rep.add("error", p, w+"/lines", "unknown_field", fmt.Sprintf(`"%s" is not a field`, lines))
			case str(lf, "type") != "table":
				v.rep.add("error", p, w+"/lines", "not_table", fmt.Sprintf(`"%s" is not a table field`, lines))
			default:
				if x, ok := visible[str(lf, "of")]; ok {
					of = str(lf, "of")
					cfields = fieldMap(x.m)
				}
			}
		}
		_, hasIn := r["in"]
		_, isOut := r["out"]
		if hasIn == isOut {
			v.rep.add("error", p, w, "stock_direction", "a move needs exactly one of in or out")
		}
		for _, key := range []string{"in", "out"} {
			o, ok := r[key].(string)
			if !ok {
				continue
			}
			if n, line := strings.CutPrefix(o, "line."); line {
				if cfields != nil {
					fieldKind(w+"/"+key, cfields, n, " of "+of, linkTo("outlet"), "is not a link to outlet")
				}
			} else {
				fieldKind(w+"/"+key, fields, o, "", linkTo("outlet"), "is not a link to outlet")
			}
		}
		if cfields != nil {
			it, isStr := r["item"].(string)
			if _, present := r["item"]; !present {
				it, isStr = "item", true
			}
			if isStr {
				fieldKind(w+"/item", cfields, it, " of "+of, linkTo("item"), "is not a link to item")
			}
			if q, ok := r["qty"].(string); ok {
				fieldKind(w+"/qty", cfields, q, " of "+of, number, "is not a number field")
			}
			if c, ok := r["cogs"].(string); ok {
				fieldKind(w+"/cogs", cfields, c, " of "+of, readOnlyCurrency, "must be a read_only currency field")
			}
		}
		if hasIn {
			for _, k := range []string{"explode", "cogs"} {
				if _, ok := r[k]; ok {
					v.rep.add("error", p, w+"/"+k, "stock_direction", k+" is only for out moves")
				}
			}
		}
		if _, ok := r["cost"]; ok && isOut {
			v.rep.add("error", p, w+"/cost", "stock_direction", "cost is only for in moves")
		}
		if c := asMap(r["cost"]); c != nil {
			for _, k := range []string{"unit", "amount"} {
				if f, ok := c[k].(string); ok && cfields != nil {
					fieldKind(w+"/cost/"+k, cfields, f, " of "+of, number, "is not a number field")
				}
			}
			if s, ok := c["source"].(string); ok {
				fieldKind(w+"/cost/source", fields, s, "", func(f map[string]any) bool { return str(f, "type") == "link" }, "is not a link field")
			}
			if c["consumed"] == true {
				if !hasOut {
					v.rep.add("error", p, w+"/cost", "stock_consumed", "cost consumed needs an out move in this document")
				}
				if sp, ok := c["split"].(string); ok && sp != "qty" && cfields != nil {
					fieldKind(w+"/cost/split", cfields, sp, " of "+of, number, "is not a number field")
				}
			}
		}
		if rs, ok := r["reason"].(string); ok && hasReasons && !contains(reasons, rs) {
			v.rep.add("error", p, w+"/reason", "unknown_option", fmt.Sprintf(`"%s" is not an option of stock_move.reason`, rs))
		}
	}
	if ct, ok := st["cogs_total"].(string); ok {
		fieldKind("stock/cogs_total", fields, ct, "", readOnlyCurrency, "must be a read_only currency field")
	}
}
