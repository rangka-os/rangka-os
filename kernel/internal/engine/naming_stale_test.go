package engine

import (
	"fmt"
	"regexp"
	"strconv"
	"testing"
)

// TestStaleCounterSkipsTakenNames: names written outside the v0 counter after it started (here kept by a create,
// as rows loaded by SQL or a partial restore would be) are not seen by the counter; when it gives one of them,
// the write is not refused: the counter moves past the existing names and the document gets the next free number
// (card t_32af87a2, contract 0009 §4).
func TestStaleCounterSkipsTakenNames(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	if s := p.e.scheme(p.e.Reg().Model("sales_invoice"), p.a.Org); s.Series != "" {
		t.Skipf("sales_invoice is numbered by series %s here; this test is about the v0 counter", s.Series)
	}
	first := p.invoice(m, m.cust, 1000, false, nil)
	sm := regexp.MustCompile(`^(.*?)([0-9]+)$`).FindStringSubmatch(first)
	if sm == nil {
		t.Fatalf("invoice name %s has no counter", first)
	}
	n, _ := strconv.Atoi(sm[2])
	name := func(k int) string { return fmt.Sprintf("%s%0*d", sm[1], len(sm[2]), k) }
	for _, k := range []int{n + 1, n + 500} {
		in := map[string]any{"name": name(k), "customer": m.cust, "items": []any{map[string]any{"item": m.jasa, "qty": 1, "rate": 1000}}}
		if err := p.e.run(p.ctx, p.a, func(o *op) error {
			_, err := o.create("sales_invoice", in, CreateOpts{KeepName: true})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := p.invoice(m, m.cust, 1000, false, nil), name(n+501); got != want {
		t.Fatalf("the counter's next name %s was taken: got %s, want %s", name(n+1), got, want)
	}
	if got, want := p.invoice(m, m.cust, 1000, false, nil), name(n+502); got != want {
		t.Fatalf("then %s, want %s", got, want)
	}
}

// keep writes a record of model under name without its counter (as rows loaded by SQL would be).
func keep(t *testing.T, p *pilot, model, name string, in map[string]any) {
	t.Helper()
	in["name"] = name
	if err := p.e.run(p.ctx, p.a, func(o *op) error {
		_, err := o.create(model, in, CreateOpts{KeepName: true})
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func counterOf(t *testing.T, name string) (prefix string, n, width int) {
	t.Helper()
	sm := regexp.MustCompile(`^(.*?)([0-9]+)$`).FindStringSubmatch(name)
	if sm == nil {
		t.Fatalf("name %s has no counter", name)
	}
	n, _ = strconv.Atoi(sm[2])
	return sm[1], n, len(sm[2])
}

// An import (CreateMany, the pilots' path) whose counter gives a taken name catches up and imports again, instead
// of failing the row "Nama ini sudah dipakai" on every try (audit naming-skip-imported, round 2 finding 1).
func TestStaleCounterImport(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	if s := p.e.scheme(p.e.Reg().Model("customer"), p.a.Org); s.Series != "" {
		t.Skipf("customer is numbered by series %s here", s.Series)
	}
	pre, n, w := counterOf(t, m.cust2)
	keep(t, p, "customer", fmt.Sprintf("%s%0*d", pre, w, n+1), map[string]any{"customer_name": "Dimuat lewat SQL"})
	res, err := p.e.CreateMany(p.ctx, p.a, "customer", []map[string]any{{"customer_name": "Impor 1"}, {"customer_name": "Impor 2"}}, ManyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range res {
		if r.Err != nil {
			t.Fatalf("row %d: %v", i, r.Err)
		}
	}
	if got, want := res[0].Name, fmt.Sprintf("%s%0*d", pre, w, n+2); got != want {
		t.Fatalf("first imported = %s, want %s", got, want)
	}
}

// A submit that runs when its approval is approved (runApproved) catches up the same way (round 2 finding 2).
func TestStaleCounterApprovedSubmit(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	if s := p.e.scheme(p.e.Reg().Model("vendor_bill"), p.a.Org); s.Series != "" {
		t.Skipf("vendor_bill is numbered by series %s here", s.Series)
	}
	first := p.doc("vendor_bill", map[string]any{"vendor": m.vend, "bill_no": "B-1", "items": []any{map[string]any{"item": m.beras, "qty": 1, "rate": 1000}}})
	pre, n, w := counterOf(t, first)
	keep(t, p, "vendor_bill", fmt.Sprintf("%s%0*d", pre, w, n+1), map[string]any{"vendor": m.vend, "bill_no": "B-SQL", "items": []any{map[string]any{"item": m.beras, "qty": 1, "rate": 1000}}})
	clerk := Actor{Org: "pilot", ID: "clerk@pilot.test", UserName: "USR-003", Roles: []string{"clerk"}, Human: true}
	approver := Actor{Org: "pilot", ID: "boss@pilot.test", UserName: "USR-002", Roles: []string{"owner"}, Human: true}
	bill := p.create("vendor_bill", map[string]any{"vendor": m.vend, "bill_no": "BIG/1", "items": []any{map[string]any{"item": m.beras, "qty": 1, "rate": 200000000}}})
	res, err := p.e.Submit(p.ctx, clerk, "vendor_bill", bill, true)
	if err != nil || !res.Held || len(res.Approvals) == 0 {
		t.Fatalf("submit not held: %v %v", res, err)
	}
	d, err := p.e.Approve(p.ctx, approver, res.Approvals[0].ID)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got, want := str(d.Record["name"]), fmt.Sprintf("%s%0*d", pre, w, n+2); got != want {
		t.Fatalf("approved bill = %s, want %s", got, want)
	}
}
