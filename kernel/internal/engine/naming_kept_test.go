package engine

import (
	"fmt"
	"regexp"
	"strconv"
	"testing"
)

// TestKeptNameMovesCounter: a record created with its own number (imports, seeding: CreateOpts.KeepName) moves the
// v0 counter past it, so the next submit never reissues it (card t_32af87a2, contract 0009 §4).
func TestKeptNameMovesCounter(t *testing.T) {
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
	imported := fmt.Sprintf("%s%0*d", sm[1], len(sm[2]), n+500)
	in := map[string]any{"name": imported, "customer": m.cust, "items": []any{map[string]any{"item": m.jasa, "qty": 1, "rate": 1000}}}
	if err := p.e.run(p.ctx, p.a, func(o *op) error {
		_, err := o.create("sales_invoice", in, CreateOpts{KeepName: true})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	next := p.invoice(m, m.cust, 1000, false, nil)
	want := fmt.Sprintf("%s%0*d", sm[1], len(sm[2]), n+501)
	if next != want {
		t.Fatalf("after an imported %s the next invoice is %s, want %s", imported, next, want)
	}
}
