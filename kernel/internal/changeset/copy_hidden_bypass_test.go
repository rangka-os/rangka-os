package changeset

import (
	"strings"
	"testing"
)

// Audit copy-hidden G1-G4: the equivalent spellings of a copy (event.*, an index by a literal or a computed key,
// spaces around the dot, raw strings, find_all lists, child tables, app models) are all high; reads of a same-named
// field of another model, of a string literal or of a macro variable stay medium.
func TestAssessCopiesHiddenBypasses(t *testing.T) {
	customer := map[string]any{"name": "customer", "fields": []any{
		map[string]any{"name": "customer_name", "type": "data"},
		map[string]any{"name": "city", "type": "data"},
		map[string]any{"name": "npwp", "type": "data", "hidden_for": []any{"outlet_staff"}},
		map[string]any{"name": "ktp", "type": "data", "hidden_for": []any{"outlet_staff", "cashier"}},
		map[string]any{"name": "contact_persons", "type": "table", "of": "contact_person"},
	}}
	cp := map[string]any{"name": "contact_person", "fields": []any{
		map[string]any{"name": "person", "type": "data"},
		map[string]any{"name": "nik", "type": "data", "hidden_for": []any{"outlet_staff"}},
	}}
	invoice := map[string]any{"name": "sales_invoice", "fields": []any{
		map[string]any{"name": "customer", "type": "link", "to": "customer"},
		map[string]any{"name": "notes", "type": "text"},
		map[string]any{"name": "npwp", "type": "data"},
		map[string]any{"name": "secret_note", "type": "text", "hidden_for": []any{"outlet_staff"}},
		map[string]any{"name": "secret2", "type": "text", "hidden_for": []any{"outlet_staff", "cashier", "x"}},
	}}
	model := func(n string) map[string]any {
		return map[string]map[string]any{"customer": customer, "sales_invoice": invoice, "contact_person": cp}[n]
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
	findC := map[string]any{"find": "customer", "where": "name == event.name", "as": "c"}
	cflow := func(expr string) map[string]any {
		return map[string]any{"name": "x_f", "on": "customer.updated", "steps": []any{findC,
			map[string]any{"update": "c", "set": map[string]any{"city": expr}}}}
	}
	ov := func(m string, f map[string]any) map[string]any { return map[string]any{"model": m, "fields": []any{f}} }
	cases := []struct {
		id, path string
		doc      map[string]any
		want     bool
	}{
		{"E1 event.npwp", "flows/x_f.rk", cflow("event.npwp"), true},
		{"E2 event.record.npwp", "flows/x_f.rk", cflow("event.record.npwp"), true},
		{"E3 event.before.npwp", "flows/x_f.rk", cflow("event.before.npwp"), true},
		{"F1 string(c.npwp)", "flows/x_f.rk", cflow("string(c.npwp)"), true},
		{"F2 c.npwp.size()", "flows/x_f.rk", cflow("c.npwp.size() > 0 ? c.npwp : ''"), true},
		{"F3 c['npwp']", "flows/x_f.rk", cflow("c['npwp']"), true},
		{"F3b c[('np'+'wp')]", "flows/x_f.rk", cflow("c[('np' + 'wp')]"), true},
		{"F4 c . npwp", "flows/x_f.rk", cflow("c . npwp"), true},
		{"F5 raw string", "flows/x_f.rk", cflow(`r'\' + c.npwp + r'\'`), true},
		{"F6 triple", "flows/x_f.rk", cflow(`'''x'y''' + c.npwp`), true},
		{"F7 find_all cs[0].npwp", "flows/x_f.rk", map[string]any{"name": "x_f", "on": "customer.updated", "steps": []any{findC,
			map[string]any{"find_all": "customer", "as": "cs"},
			map[string]any{"update": "c", "set": map[string]any{"city": "cs[0].npwp"}}}}, true},
		{"F8 find_all map", "flows/x_f.rk", map[string]any{"name": "x_f", "on": "customer.updated", "steps": []any{findC,
			map[string]any{"find_all": "customer", "as": "cs"},
			map[string]any{"update": "c", "set": map[string]any{"city": "cs.map(x, x.npwp)[0]"}}}}, true},
		{"F9 for_each child rows", "flows/x_f.rk", map[string]any{"name": "x_f", "on": "customer.updated", "steps": []any{findC,
			map[string]any{"for_each": "c.contact_persons", "as": "row", "do": []any{
				map[string]any{"update": "c", "set": map[string]any{"city": "row.nik"}}}}}}, true},
		{"F10 child index", "flows/x_f.rk", cflow("c.contact_persons[0].nik"), true},
		{"F11 for_each find_all", "flows/x_f.rk", map[string]any{"name": "x_f", "on": "customer.updated", "steps": []any{findC,
			map[string]any{"find_all": "customer", "as": "cs"},
			map[string]any{"for_each": "cs", "as": "x", "do": []any{
				map[string]any{"update": "c", "set": map[string]any{"city": "x.npwp"}}}}}}, true},
		{"F12 create invoice notes", "flows/x_f.rk", map[string]any{"name": "x_f", "on": "customer.updated", "steps": []any{findC,
			map[string]any{"create": "sales_invoice", "set": map[string]any{"customer": "c.name", "notes": "c.npwp"}}}}, true},
		{"F13 multirole partial cover", "flows/x_f.rk", map[string]any{"name": "x_f", "on": "customer.updated", "steps": []any{findC,
			map[string]any{"create": "sales_invoice", "set": map[string]any{"secret_note": "c.ktp"}}}}, true},
		{"F14 app flow", "apps/x_a/flows/x_f.rk", cflow("c.npwp"), true},
		{"K1 compute child map", "custom/customer.rk", ov("customer", map[string]any{"name": "x_n", "type": "data", "compute": "contact_persons.map(r, r.nik)[0]"}), true},
		{"K2 compute link index", "custom/sales_invoice.rk", ov("sales_invoice", map[string]any{"name": "x_n", "type": "data", "compute": "customer['npwp']"}), true},
		{"K3 default whitespace", "custom/sales_invoice.rk", ov("sales_invoice", map[string]any{"name": "x_n", "type": "data", "default": "customer . npwp"}), true},
		{"K4 compute link", "custom/sales_invoice.rk", ov("sales_invoice", map[string]any{"name": "x_n", "type": "data", "compute": "customer.npwp"}), true},
		{"K5 new app model default", "apps/x_a/models/x_salin.rk", map[string]any{"name": "x_salin", "permissions": []any{}, "fields": []any{
			map[string]any{"name": "customer", "type": "link", "to": "customer"},
			map[string]any{"name": "x_tax", "type": "data", "default": "customer.npwp"}}}, true},
		// false positives
		{"P1 other model same name", "flows/x_f.rk", map[string]any{"name": "x_f", "on": "customer.updated", "steps": []any{
			map[string]any{"find": "sales_invoice", "as": "s"},
			map[string]any{"update": "s", "set": map[string]any{"notes": "s.npwp"}}}}, false},
		{"P2 compute self same name", "custom/sales_invoice.rk", ov("sales_invoice", map[string]any{"name": "x_n", "type": "data", "compute": "npwp"}), false},
		{"P3 string literal", "custom/sales_invoice.rk", ov("sales_invoice", map[string]any{"name": "x_n", "type": "data", "compute": "'customer.npwp'"}), false},
		{"P4 macro var named npwp", "custom/customer.rk", ov("customer", map[string]any{"name": "x_n", "type": "data", "compute": "contact_persons.map(npwp, npwp.person)[0]"}), false},
		{"P5 superset cover", "flows/x_f.rk", map[string]any{"name": "x_f", "on": "customer.updated", "steps": []any{findC,
			map[string]any{"create": "sales_invoice", "set": map[string]any{"secret2": "c.ktp"}}}}, false},
		{"P6 event.name", "flows/x_f.rk", cflow("event.name + c.customer_name"), false},
		// audit copy-hidden H1: a link field's value is the linked record's name; copying, comparing or counting
		// copies nothing hidden, following it still reads the linked record
		{"Q1 link name copy", "flows/x_f.rk", map[string]any{"name": "x_f", "on": "sales_invoice.created", "steps": []any{
			map[string]any{"create": "sales_invoice", "set": map[string]any{"customer": "event.customer"}}}}, false},
		{"Q2 link compare", "custom/sales_invoice.rk", ov("sales_invoice", map[string]any{"name": "x_n", "type": "data", "compute": "customer != '' ? customer.customer_name : ''"}), false},
		{"Q3 size find_all", "flows/x_f.rk", map[string]any{"name": "x_f", "on": "customer.updated", "steps": []any{findC,
			map[string]any{"find_all": "customer", "as": "cs"},
			map[string]any{"update": "c", "set": map[string]any{"city": "string(size(cs)) + string(cs.size())"}}}}, false},
		{"Q4 row link in map", "flows/x_f.rk", map[string]any{"name": "x_f", "on": "sales_invoice.created", "steps": []any{
			map[string]any{"find": "sales_invoice", "as": "s"},
			map[string]any{"update": "s", "set": map[string]any{"notes": "[s.customer].map(x, x)[0]"}}}}, false},
		{"Q5 link in map literal", "flows/x_f.rk", map[string]any{"name": "x_f", "on": "sales_invoice.created", "steps": []any{
			map[string]any{"update": "event", "set": map[string]any{"notes": "string({'a': event.customer})"}}}}, false},
		{"Q6 record compare", "flows/x_f.rk", cflow("c == c ? 'a' : 'b'"), false},
		{"R1 link list followed", "flows/x_f.rk", map[string]any{"name": "x_f", "on": "sales_invoice.created", "steps": []any{
			map[string]any{"update": "event", "set": map[string]any{"notes": "[event.customer][0].npwp"}}}}, true},
		{"R2 link map followed", "flows/x_f.rk", map[string]any{"name": "x_f", "on": "sales_invoice.created", "steps": []any{
			map[string]any{"update": "event", "set": map[string]any{"notes": "{'a': event.customer}.a + {'b': event.customer}['b'].npwp"}}}}, true},
		{"R3 link followed", "flows/x_f.rk", map[string]any{"name": "x_f", "on": "sales_invoice.created", "steps": []any{
			map[string]any{"update": "event", "set": map[string]any{"notes": "event.customer.npwp"}}}}, true},
		{"R4 link list map followed", "flows/x_f.rk", map[string]any{"name": "x_f", "on": "sales_invoice.created", "steps": []any{
			map[string]any{"update": "event", "set": map[string]any{"notes": "[event.customer].map(x, x.npwp)[0]"}}}}, true},
		{"R5 string(record)", "flows/x_f.rk", cflow("string(c)"), true},
		{"R6 child rows whole", "flows/x_f.rk", cflow("string(c.contact_persons)"), true},
		{"R7 cond record whole", "flows/x_f.rk", cflow("string(true ? c : event.customer)"), true},
		// audit copy-hidden H2: a link carried through a function, list concatenation, a list or map literal is still followed
		{"S1 max list", "custom/sales_invoice.rk", ov("sales_invoice", map[string]any{"name": "x_n", "type": "data", "compute": "max([customer]).npwp"}), true},
		{"S1b min index", "custom/sales_invoice.rk", ov("sales_invoice", map[string]any{"name": "x_n", "type": "data", "compute": "min([customer])['npwp']"}), true},
		{"S2 list plus", "custom/sales_invoice.rk", ov("sales_invoice", map[string]any{"name": "x_n", "type": "data", "compute": "([customer] + [])[0].npwp"}), true},
		{"S2b list plus map", "custom/sales_invoice.rk", ov("sales_invoice", map[string]any{"name": "x_n", "type": "data", "compute": "([customer] + []).map(x, x.npwp)[0]"}), true},
		{"S3 list of maps", "custom/sales_invoice.rk", ov("sales_invoice", map[string]any{"name": "x_n", "type": "data", "compute": "[{'a': customer}][0].a.npwp"}), true},
		{"S3b nested map list", "custom/sales_invoice.rk", ov("sales_invoice", map[string]any{"name": "x_n", "type": "data", "compute": "{'a': [{'b': customer}]}.a[0].b.npwp"}), true},
		{"S4 cond map or link", "custom/sales_invoice.rk", ov("sales_invoice", map[string]any{"name": "x_n", "type": "data", "compute": "(true ? {'a': customer} : customer).a.npwp"}), true},
		{"S5 map to maps", "custom/sales_invoice.rk", ov("sales_invoice", map[string]any{"name": "x_n", "type": "data", "compute": "[customer].map(x, {'a': x})[0].a.npwp"}), true},
		{"S6 field-named key", "custom/sales_invoice.rk", ov("sales_invoice", map[string]any{"name": "x_n", "type": "data", "compute": "{'customer_name': customer}.customer_name.npwp"}), true},
		{"S7 default max", "custom/sales_invoice.rk", ov("sales_invoice", map[string]any{"name": "x_n", "type": "data", "default": "max([customer]).npwp"}), true},
		{"S8 string of link", "custom/sales_invoice.rk", ov("sales_invoice", map[string]any{"name": "x_n", "type": "data", "compute": "string(max([customer]))"}), false},
		{"R8 link computed key", "flows/x_f.rk", map[string]any{"name": "x_f", "on": "sales_invoice.created", "steps": []any{
			map[string]any{"update": "event", "set": map[string]any{"notes": "event.customer[('np' + 'wp')]"}}}}, true},
	}
	for _, c := range cases {
		a := AssessWith([]Op{{Op: "create", Path: c.path}}, none, with(c.path, c.doc), Env{Model: model})
		copied := strings.Contains(strings.Join(a.Reasons, "; "), "hidden from some roles")
		got := a.Risk == "high" && copied
		mark := "ok"
		if got != c.want {
			mark = "MISS"
			if !c.want {
				mark = "FALSE-POS"
			}
		}
		if mark != "ok" {
			t.Errorf("%-9s %-28s risk=%s copy=%v %v", mark, c.id, a.Risk, copied, a.Reasons)
		}
	}
}
