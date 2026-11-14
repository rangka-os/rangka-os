package changeset

import (
	"strings"
	"testing"
)

// Audit flow-locked F2 (card t_d41100b0): a value hidden from some roles (a customer's NPWP, hidden_for outlet_staff)
// copied into a field those roles see, by a company flow's set or an overlay's or company model's compute/default, is
// high; a copy into a field hidden from the same roles, or an expression that only names it in a string, is not.
func TestAssessCopiesHiddenFields(t *testing.T) {
	customer := map[string]any{"name": "customer", "fields": []any{
		map[string]any{"name": "customer_name", "type": "data"},
		map[string]any{"name": "city", "type": "data"},
		map[string]any{"name": "npwp", "type": "data", "hidden_for": []any{"outlet_staff"}},
	}}
	invoice := map[string]any{"name": "sales_invoice", "fields": []any{
		map[string]any{"name": "customer", "type": "link", "to": "customer"},
		map[string]any{"name": "notes", "type": "text"},
	}}
	model := func(n string) map[string]any {
		return map[string]map[string]any{"customer": customer, "sales_invoice": invoice}[n]
	}
	none := func(string) (map[string]any, bool) { return nil, false }
	with := func(p string, m map[string]any) func(string) (map[string]any, bool) {
		return func(q string) (map[string]any, bool) {
			if q == p {
				return m, true
			}
			return nil, false
		}
	}
	flow := func(on string, steps ...any) map[string]any { return map[string]any{"name": "x_f", "on": on, "steps": steps} }
	field := func(name string, extra map[string]any) map[string]any {
		f := map[string]any{"name": name, "type": "data"}
		for k, v := range extra {
			f[k] = v
		}
		return f
	}
	cases := []struct {
		name string
		path string
		doc  map[string]any
		high bool
	}{
		{"flow copies c.npwp into city", "flows/x_f.rk", flow("customer.updated",
			map[string]any{"find": "customer", "where": "name == event.name", "as": "c"},
			map[string]any{"update": "doc", "set": map[string]any{"city": "c.npwp"}}), true},
		{"flow copies through a link (doc.customer.npwp)", "flows/x_f.rk", flow("sales_invoice.updated",
			map[string]any{"update": "doc", "set": map[string]any{"notes": "'NPWP ' + doc.customer.npwp"}}), true},
		{"flow sets a visible field from visible fields", "flows/x_f.rk", flow("customer.updated",
			map[string]any{"find": "customer", "where": "name == event.name", "as": "c"},
			map[string]any{"update": "doc", "set": map[string]any{"city": "c.customer_name + ' npwp'"}}), false},
		{"overlay adds x_npwp computed from npwp", "custom/customer.rk",
			map[string]any{"model": "customer", "fields": []any{field("x_npwp", map[string]any{"compute": "npwp"})}}, true},
		{"overlay adds x_npwp hidden from the same roles", "custom/customer.rk",
			map[string]any{"model": "customer", "fields": []any{field("x_npwp", map[string]any{"compute": "npwp", "hidden_for": []any{"outlet_staff"}})}}, false},
		{"new company model defaults a field from a link's hidden field", "models/x_salin.rk",
			map[string]any{"name": "x_salin", "permissions": []any{}, "fields": []any{
				map[string]any{"name": "customer", "type": "link", "to": "customer"},
				field("x_tax", map[string]any{"default": "customer.npwp"})}}, true},
	}
	for _, c := range cases {
		a := AssessWith([]Op{{Op: "create", Path: c.path}}, none, with(c.path, c.doc), Env{Model: model})
		copied := strings.Contains(strings.Join(a.Reasons, "; "), "hidden from some roles")
		if c.high != (a.Risk == "high" && copied) {
			t.Errorf("%s: risk %s %v, want high copy %v", c.name, a.Risk, a.Reasons, c.high)
		}
	}
}
