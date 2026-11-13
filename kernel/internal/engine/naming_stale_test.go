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
