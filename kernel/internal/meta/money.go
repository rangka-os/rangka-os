package meta

import (
	"fmt"
	"sort"
)

// Money fields (contract 0013): a `money` field holds an amount in the currency named by its currency_field;
// the kernel adds a read-only `currency` twin <field>_idr holding the amount in whole rupiah.

// TwinName is the name of the IDR twin of money field field.
func TwinName(field string) string { return field + "_idr" }

// WithTwins returns a new field list: fields plus, at the end, the IDR twin of every money field whose twin
// name is not already declared. fields is not changed. rangka_validate.py with_twins does the same.
func WithTwins(fields []any) []any {
	declared := map[string]bool{}
	for _, fv := range fields {
		if fm := asMap(fv); fm != nil {
			if n, ok := fm["name"].(string); ok {
				declared[n] = true
			}
		}
	}
	out := append(make([]any, 0, len(fields)), fields...)
	for _, fv := range fields {
		fm := asMap(fv)
		n, _ := fm["name"].(string)
		if fm == nil || n == "" || fm["type"] != "money" || declared[TwinName(n)] {
			continue
		}
		declared[TwinName(n)] = true
		tw := map[string]any{"name": TwinName(n), "type": "currency", "read_only": true, "money_of": n}
		if lm := asMap(fm["label"]); lm != nil {
			l := map[string]any{}
			for k, x := range lm {
				l[k] = pyStr(x) + " (IDR)"
			}
			tw["label"] = l
		}
		out = append(out, tw)
	}
	return out
}

// withTwinsRaw returns a shallow copy of a model's raw data whose fields include the IDR twins. raw itself is
// never changed, so the schema pass keeps validating the file as written.
func withTwinsRaw(raw map[string]any) map[string]any {
	if raw == nil {
		return nil
	}
	out := make(map[string]any, len(raw))
	for k, x := range raw {
		out[k] = x
	}
	if l, ok := raw["fields"].([]any); ok {
		out["fields"] = WithTwins(l)
	}
	return out
}

// CurrencyField is the field holding a money field's ISO 4217 code.
func (f *Field) CurrencyField() string { return str(f.Raw, "currency_field") }

// RateField is the field holding a money field's IDR rate (default "rate").
func (f *Field) RateField() string {
	if r := str(f.Raw, "rate_field"); r != "" {
		return r
	}
	return "rate"
}

// MoneyOf names the money field an IDR twin belongs to ("" for other fields).
func (f *Field) MoneyOf() string { return str(f.Raw, "money_of") }

var currencyFieldTypes = []string{"select", "data", "link"}
var rateFieldTypes = []string{"float", "currency", "int"}

type docField struct {
	model string
	f     map[string]any
}

// resolveDoc finds field name for model m (own: m's fields). A model that is not a child resolves it on itself.
// A child resolves it on every visible model with a table of it (sorted by name), after first trying itself
// when childFirst is set (currency_field; rate_field is never read from the child). missing names the first
// model that lacks it.
func resolveDoc(m *Model, own map[string]map[string]any, name string, visible map[string]vis, childFirst bool) (found []docField, missing string) {
	child := boolean(m.Raw, "is_child")
	if f := own[name]; f != nil && (childFirst || !child) {
		return []docField{{m.Name, f}}, ""
	}
	if !child {
		return nil, m.Name
	}
	var parents []string
	for pn, x := range visible {
		for _, f := range fieldMap(x.m) {
			if str(f, "type") == "table" && str(f, "of") == m.Name {
				parents = append(parents, pn)
				break
			}
		}
	}
	if len(parents) == 0 {
		return nil, m.Name
	}
	sort.Strings(parents)
	for _, pn := range parents {
		f := fieldMap(visible[pn].m)[name]
		if f == nil {
			return nil, pn
		}
		found = append(found, docField{pn, f})
	}
	return found, ""
}

// money checks field i (f) of model m: currency_field, rate_field and twin name clashes (contract 0013 §6).
func (v *validator) money(p string, m *Model, i int, f map[string]any, fields map[string]map[string]any, visible map[string]vis) {
	n := pyStr(f["name"])
	if str(f, "type") == "money" {
		if cf, ok := f["currency_field"].(string); ok {
			found, missing := resolveDoc(m, fields, cf, visible, true)
			if missing != "" {
				v.rep.add("error", p, fmt.Sprintf("fields/%d/currency_field", i), "bad_currency_field", fmt.Sprintf(`"%s" is not a field of %s`, cf, missing))
			} else {
				for _, c := range found {
					if !contains(currencyFieldTypes, str(c.f, "type")) {
						v.rep.add("error", p, fmt.Sprintf("fields/%d/currency_field", i), "bad_currency_field",
							fmt.Sprintf(`currency field "%s" must be select, data or link; it is %s`, cf, pyStr(c.f["type"])))
						break
					}
				}
			}
		}
		rate := "rate"
		if r, ok := f["rate_field"].(string); ok && r != "" {
			rate = r
		}
		found, missing := resolveDoc(m, fields, rate, visible, false)
		for _, c := range found {
			if missing == "" && !contains(rateFieldTypes, str(c.f, "type")) {
				missing = c.model
			}
		}
		if missing != "" {
			v.rep.add("error", p, fmt.Sprintf("fields/%d", i), "missing_rate_field",
				fmt.Sprintf(`money fields need a rate field "%s" (float, currency or int) on %s`, rate, missing))
		}
	}
	if _, isTwin := f["money_of"]; !isTwin && len(n) > 4 && n[len(n)-4:] == "_idr" {
		if mf := fields[n[:len(n)-4]]; mf != nil && str(mf, "type") == "money" {
			v.rep.add("error", p, fmt.Sprintf("fields/%d", i), "twin_clash", fmt.Sprintf(`"%s" is generated for money field "%s"; rename it`, n, n[:len(n)-4]))
		}
	}
}
