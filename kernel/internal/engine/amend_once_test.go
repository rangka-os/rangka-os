package engine

import (
	"sync"
	"testing"
)

// TestAmendOnce: a cancelled document has at most one live amendment (draft or submitted). Amending it a second
// time would let two copies be submitted, so a cancelled invoice or payment would count twice in outstanding.
// Once the copy is cancelled too, the chain continues from the copy (and the original may be amended again).
func TestAmendOnce(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()

	inv := p.invoice(m, m.cust, 1000000, false, nil)
	if err := p.cancel("sales_invoice", inv); err != nil {
		t.Fatal(err)
	}
	first, err := p.e.Amend(p.ctx, p.a, "sales_invoice", inv)
	if err != nil {
		t.Fatalf("first amend: %v", err)
	}
	// a second amend while the first copy is a draft
	_, err = p.e.Amend(p.ctx, p.a, "sales_invoice", inv)
	wantCode(t, err, 409, "already_amended")
	copyName := p.submit("sales_invoice", str(first.Record["name"]))
	// and while it is submitted
	_, err = p.e.Amend(p.ctx, p.a, "sales_invoice", inv)
	wantCode(t, err, 409, "already_amended")
	if got := p.outstanding("customer", m.cust); got != 1000000 {
		t.Fatalf("customer outstanding = %d, want 1000000 (one live copy)", got)
	}
	// the copy cancelled: the chain goes on from the copy, never again from the original (audit F1: amending
	// the original again would branch the chain and both branches could be submitted)
	if err := p.cancel("sales_invoice", copyName); err != nil {
		t.Fatal(err)
	}
	_, err = p.e.Amend(p.ctx, p.a, "sales_invoice", inv)
	wantCode(t, err, 409, "already_amended")
	second, err := p.e.Amend(p.ctx, p.a, "sales_invoice", copyName)
	if err != nil {
		t.Fatalf("amend the cancelled copy: %v", err)
	}
	tip := p.submit("sales_invoice", str(second.Record["name"]))
	_, err = p.e.Amend(p.ctx, p.a, "sales_invoice", inv)
	wantCode(t, err, 409, "already_amended")
	_, err = p.e.Amend(p.ctx, p.a, "sales_invoice", copyName)
	wantCode(t, err, 409, "already_amended")
	if got := p.outstanding("customer", m.cust); got != 1000000 {
		t.Fatalf("customer outstanding with the chain %s -> %s -> %s = %d, want 1000000", inv, copyName, tip, got)
	}

	// payment: the same rule
	inv2 := p.invoice(m, m.cust2, 500000, false, nil)
	pay := p.doc("payment", map[string]any{"customer": m.cust2, "amount": 500000, "allocations": []any{alloc(inv2, 500000)}})
	if err := p.cancel("payment", pay); err != nil {
		t.Fatal(err)
	}
	pa, err := p.e.Amend(p.ctx, p.a, "payment", pay)
	if err != nil {
		t.Fatalf("amend payment: %v", err)
	}
	_, err = p.e.Amend(p.ctx, p.a, "payment", pay)
	wantCode(t, err, 409, "already_amended")
	p.submit("payment", str(pa.Record["name"]))
	_, err = p.e.Amend(p.ctx, p.a, "payment", pay)
	wantCode(t, err, 409, "already_amended")
	if got := p.outstanding("sales_invoice", inv2); got != 0 {
		t.Fatalf("invoice outstanding after one amended payment = %d", got)
	}
}

// TestAmendOnceSubmitGuard: two draft copies of one cancelled document (made before this rule, or by a
// create with amended_from) cannot both be submitted.
func TestAmendOnceSubmitGuard(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	inv := p.invoice(m, m.cust, 1000000, false, nil)
	if err := p.cancel("sales_invoice", inv); err != nil {
		t.Fatal(err)
	}
	a, err := p.e.Amend(p.ctx, p.a, "sales_invoice", inv)
	if err != nil {
		t.Fatal(err)
	}
	// a second draft that names the same original, as an older kernel would have allowed
	in := map[string]any{"customer": m.cust, "amended_from": inv, "items": []any{map[string]any{"item": m.jasa, "qty": 1, "rate": 1000000}}}
	var b *Result
	err = p.e.run(p.ctx, p.a, func(o *op) error {
		in["name"] = "DRAFT-OLD01"
		var err error
		b, err = o.create("sales_invoice", in, CreateOpts{KeepName: true})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if str(b.Record["amended_from"]) != inv {
		t.Fatalf("second draft amended_from = %v", b.Record["amended_from"])
	}
	p.submit("sales_invoice", str(a.Record["name"]))
	_, err = p.e.Submit(p.ctx, p.a, "sales_invoice", str(b.Record["name"]), true)
	wantCode(t, err, 409, "already_amended")
	// a copy kept under a name that is not DRAFT-… (imports) is guarded too (audit F2)
	var c *Result
	err = p.e.run(p.ctx, p.a, func(o *op) error {
		in["name"] = "INV-IMPORT-1"
		var err error
		c, err = o.create("sales_invoice", in, CreateOpts{KeepName: true})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.e.Submit(p.ctx, p.a, "sales_invoice", str(c.Record["name"]), true)
	wantCode(t, err, 409, "already_amended")
	if got := p.outstanding("customer", m.cust); got != 1000000 {
		t.Fatalf("customer outstanding = %d", got)
	}
}

// TestAmendOnceRace: two amends of the same cancelled document at the same time give one copy.
func TestAmendOnceRace(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	inv := p.invoice(m, m.cust, 1000000, false, nil)
	if err := p.cancel("sales_invoice", inv); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = p.e.Amend(p.ctx, p.a, "sales_invoice", inv)
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			wantCode(t, err, 409, "already_amended")
		}
	}
	if ok != 1 {
		t.Fatalf("%d amends succeeded, want 1: %v", ok, errs)
	}
}
