package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// setupPilotPlain is setupPilot on a plain (non-superuser) login role, so row-level security (contract 0010
// §4) applies to every query the engine and its hooks make.
func setupPilotPlain(t *testing.T) *pilot {
	t.Helper()
	dbURL := testdb.New(t)
	ctx := context.Background()
	reg, rep := meta.Validate(pilotApps, pilotSite, nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("pilot catalogue errors: %v", rep.All())
	}
	admin, err := pg.NewPool(ctx, dbURL, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	if _, err := store.Migrate(ctx, admin, reg, false, "test"); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 4)
	rand.Read(b)
	role, pass := "rk_trf_"+hex.EncodeToString(b), "pw"+hex.EncodeToString(b)
	for _, s := range []string{"create role " + role + " login password '" + pass + "'", "grant usage on schema public to " + role,
		"grant all on all tables in schema public to " + role, "grant all on all sequences in schema public to " + role} {
		if _, err := admin.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	u, _ := url.Parse(dbURL)
	u.User = url.UserPassword(role, pass)
	plain, err := pg.NewPool(ctx, u.String(), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		plain.Close()
		admin.Exec(context.Background(), "drop owned by "+role)
		admin.Exec(context.Background(), "drop role if exists "+role)
	})
	e := New(plain, reg)
	e.Now = FixedNow
	return &pilot{t: t, e: e, ctx: pg.WithOrg(ctx, "pilot"), a: Actor{Org: "pilot", ID: "owner@pilot.test", UserName: "USR-001", Roles: []string{"owner"}, Human: true}}
}

func (p *pilot) as(a Actor) *pilot { q := *p; q.a = a; return &q }

func (p *pilot) noBalance(outlet, item string) {
	p.t.Helper()
	rows, _, err := p.e.List(p.ctx, p.a, "stock_balance", ListOpts{Filter: "outlet == '" + outlet + "' && item == '" + item + "'"})
	if err != nil || len(rows) != 0 {
		p.t.Fatalf("balance %s/%s exists: %v %v", outlet, item, rows, err)
	}
}

func (p *pilot) wantTransfer(name, state string, received float64, unit int64) {
	p.t.Helper()
	tr := p.get("stock_transfer", name)
	l := rows(tr["items"])[0]
	if tr["state"] != state || qtyOf(l["received_qty"]) != received || rp(l["unit_cost"]) != unit {
		p.t.Fatalf("transfer %s: state %v received %v unit %v, want %s %v %d", name, tr["state"], l["received_qty"], l["unit_cost"], state, received, unit)
	}
}

func (p *pilot) trReceipt(trf, outlet, item string, qty float64) string {
	p.t.Helper()
	return p.create("stock_transfer_receipt", map[string]any{"stock_transfer": trf, "outlet": outlet, "items": []any{map[string]any{"item": item, "qty": qty}}})
}

func (p *pilot) submitErr(model, name string) error {
	_, err := p.e.Submit(p.ctx, p.a, model, name, true)
	return err
}

// sumIn is the total value the receipts of a transfer brought in (transfer_in and reversal moves).
func (p *pilot) sumIn(receipts ...string) int64 {
	var v int64
	for _, r := range receipts {
		for _, mv := range p.moves(r) {
			v += rp(mv["value"])
		}
	}
	return v
}

// TestPilotTwoStepTransfer (S8e), as a plain role under RLS: Braga sends, Dago's outlet staff receive in two
// parts at the transfer's unit cost; refusals; exact reversal; a fractional split whose in-values still add
// up to the out-value; and the row scope that lets Dago staff receive what they may not send.
func TestPilotTwoStepTransfer(t *testing.T) {
	p := setupPilotPlain(t)
	m := p.masters()
	braga, dago := m.outC, m.outB
	dagoStaff := p.as(Actor{Org: "pilot", ID: "staff@dago.test", UserName: "USR-009", Roles: []string{"outlet_staff"}, Outlets: []string{dago}, Human: true})

	// Braga holds 70 kg worth 1.000.000 (one 70 kg sack): avg 14.286
	pack := p.create("uom_conversion", map[string]any{"item": m.beras, "uom": m.karung, "factor": 70})
	p.doc("goods_receipt", map[string]any{"vendor": m.vend, "outlet": braga, "items": []any{map[string]any{"item": m.beras, "qty": 1, "uom": m.karung, "packing": pack, "rate": 1000000}}})
	p.wantBalance(braga, m.beras, 70, 1000000, 14286)

	// send: only the outgoing side
	trf := p.doc("stock_transfer", map[string]any{"from_outlet": braga, "to_outlet": dago, "items": []any{map[string]any{"item": m.beras, "qty": 30}}})
	p.wantBalance(braga, m.beras, 40, 1000000-428580, 14286)
	p.noBalance(dago, m.beras)
	p.wantTransfer(trf, "in_transit", 0, 14286)
	if mv := p.moves(trf); len(mv) != 1 || mv[0]["reason"] != "transfer_out" || rp(mv[0]["value"]) != -428580 {
		t.Fatalf("transfer moves = %v", mv)
	}

	// refusals
	wantCode(t, p.submitErr("stock_transfer_receipt", p.trReceipt(trf, dago, m.beras, 31)), 409, "over_receipt")
	wantCode(t, p.submitErr("stock_transfer_receipt", p.trReceipt(trf, dago, m.minyak, 1)), 409, "over_receipt")
	wantCode(t, p.submitErr("stock_transfer_receipt", p.trReceipt(trf, m.outA, m.beras, 10)), 409, "wrong_outlet")
	p.noBalance(dago, m.beras)

	// Dago's outlet staff receive 20, then 10, at the same unit cost
	r1 := dagoStaff.submit("stock_transfer_receipt", dagoStaff.trReceipt(trf, dago, m.beras, 20))
	p.wantBalance(dago, m.beras, 20, 285720, 14286)
	p.wantTransfer(trf, "partly_received", 20, 14286)
	r2 := dagoStaff.submit("stock_transfer_receipt", dagoStaff.trReceipt(trf, dago, m.beras, 10))
	p.wantBalance(dago, m.beras, 30, 428580, 14286)
	p.wantTransfer(trf, "received", 30, 14286)
	if mv := p.moves(r2); len(mv) != 1 || mv[0]["reason"] != "transfer_in" || rp(mv[0]["unit_cost"]) != 14286 {
		t.Fatalf("receipt moves = %v", mv)
	}
	if got := p.sumIn(r1, r2); got != 428580 {
		t.Fatalf("in-values %d, out-value 428580", got)
	}
	wantCode(t, dagoStaff.submitErr("stock_transfer_receipt", dagoStaff.trReceipt(trf, dago, m.beras, 1)), 409, "not_in_transit")

	// cancelling: the transfer waits for its receipts; each receipt reverses exactly
	wantCode(t, p.cancel("stock_transfer", trf), 409, "has_receipts")
	if err := dagoStaff.cancel("stock_transfer_receipt", r2); err != nil {
		t.Fatal(err)
	}
	p.wantBalance(dago, m.beras, 20, 285720, 14286)
	p.wantTransfer(trf, "partly_received", 20, 14286)
	if mv := p.moves(r2); len(mv) != 2 || mv[1]["reason"] != "reversal" || mv[1]["qty"] != -10.0 || rp(mv[1]["value"]) != -142860 {
		t.Fatalf("receipt reversal = %v", mv)
	}
	if err := p.cancel("stock_transfer_receipt", r1); err != nil {
		t.Fatal(err)
	}
	p.wantBalance(dago, m.beras, 0, 0, 14286)
	p.wantTransfer(trf, "in_transit", 0, 14286)
	if err := p.cancel("stock_transfer", trf); err != nil {
		t.Fatal(err)
	}
	p.wantBalance(braga, m.beras, 70, 1000000, 14286)

	// 20,25 + 9,75 kg: round(20,25 × 14.286) + round(9,75 × 14.286) = 289.292 + 139.289 is one rupiah over;
	// the last receipt takes the exact remainder (139.288)
	trf2 := p.doc("stock_transfer", map[string]any{"from_outlet": braga, "to_outlet": dago, "items": []any{map[string]any{"item": m.beras, "qty": 30}}})
	a := dagoStaff.submit("stock_transfer_receipt", dagoStaff.trReceipt(trf2, dago, m.beras, 20.25))
	if v := p.sumIn(a); v != 289292 {
		t.Fatalf("first part = %d", v)
	}
	b := dagoStaff.submit("stock_transfer_receipt", dagoStaff.trReceipt(trf2, dago, m.beras, 9.75))
	if v := p.sumIn(b); v != 139288 {
		t.Fatalf("remainder = %d", v)
	}
	if out := -p.sumIn(trf2); out != 428580 || p.sumIn(a, b) != out {
		t.Fatalf("in %d, out %d", p.sumIn(a, b), out)
	}
	p.wantBalance(dago, m.beras, 30, 428580, 14286)
	p.wantTransfer(trf2, "received", 30, 14286)

	// row scope: Dago staff receive a Braga→Dago transfer they may neither create nor read
	_, err := dagoStaff.e.Create(dagoStaff.ctx, dagoStaff.a, "stock_transfer", map[string]any{"from_outlet": braga, "to_outlet": dago, "items": []any{map[string]any{"item": m.beras, "qty": 1}}})
	var pe *PermissionError
	if !errors.As(err, &pe) || pe.Model != "stock_transfer" || pe.Action != "create" {
		t.Fatalf("Dago staff create Braga→Dago = %v, want 403", err)
	}
	if _, err := dagoStaff.e.Get(dagoStaff.ctx, dagoStaff.a, "stock_transfer", trf2); !errors.As(err, &pe) {
		t.Fatalf("Dago staff read Braga→Dago = %v, want 403", err)
	}
	if _, err := dagoStaff.e.Get(dagoStaff.ctx, dagoStaff.a, "stock_transfer_receipt", b); err != nil {
		t.Fatalf("Dago staff read their receipt: %v", err)
	}
	// a receipt for another outlet is outside their scope before any hook runs
	_, err = dagoStaff.e.Create(dagoStaff.ctx, dagoStaff.a, "stock_transfer_receipt", map[string]any{"stock_transfer": trf2, "outlet": braga, "items": []any{map[string]any{"item": m.beras, "qty": 1}}})
	if !errors.As(err, &pe) {
		t.Fatalf("Dago staff receipt for Braga = %v, want 403", err)
	}
}

// TestPilotTwoStepTransferDuplicateLines: two lines of one item are received as one quantity; received_qty
// fills them in order and empties them from the last.
func TestPilotTwoStepTransferDuplicateLines(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	p.receipt(m, m.outA, 100, 12000)
	trf := p.doc("stock_transfer", map[string]any{"from_outlet": m.outA, "to_outlet": m.outB, "items": []any{
		map[string]any{"item": m.beras, "qty": 10}, map[string]any{"item": m.beras, "qty": 5}}})
	r := p.submit("stock_transfer_receipt", p.create("stock_transfer_receipt", map[string]any{"stock_transfer": trf, "outlet": m.outB, "items": []any{
		map[string]any{"item": m.beras, "qty": 7}, map[string]any{"item": m.beras, "qty": 5}}}))
	tr := p.get("stock_transfer", trf)
	if l := rows(tr["items"]); l[0]["received_qty"] != 10.0 || l[1]["received_qty"] != 2.0 || tr["state"] != "partly_received" {
		t.Fatalf("received = %v %v %v", l[0]["received_qty"], l[1]["received_qty"], tr["state"])
	}
	p.wantBalance(m.outB, m.beras, 12, 144000, 12000)
	r2 := p.doc("stock_transfer_receipt", map[string]any{"stock_transfer": trf, "outlet": m.outB, "items": []any{map[string]any{"item": m.beras, "qty": 3}}})
	if p.sumIn(r, r2) != 180000 || p.get("stock_transfer", trf)["state"] != "received" {
		t.Fatal("full receipt")
	}
	if err := p.cancel("stock_transfer_receipt", r2); err != nil {
		t.Fatal(err)
	}
	tr = p.get("stock_transfer", trf)
	if l := rows(tr["items"]); l[0]["received_qty"] != 10.0 || l[1]["received_qty"] != 2.0 {
		t.Fatalf("after cancel = %v %v", l[0]["received_qty"], l[1]["received_qty"])
	}
}

// TestPilotTransferReceiptRaces: two receipts of the whole quantity at once, and a receipt racing the
// transfer's cancel. The transfer row lock serialises them: one receipt wins (the other finds the transfer
// received: not_in_transit), and
// either the cancel (has_receipts) or the receipt (not_in_transit) is refused.
func TestPilotTransferReceiptRaces(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	p.receipt(m, m.outA, 1000, 12000)
	for i := 0; i < 4; i++ {
		trf := p.doc("stock_transfer", map[string]any{"from_outlet": m.outA, "to_outlet": m.outB, "items": []any{map[string]any{"item": m.beras, "qty": 10}}})
		a, b := p.trReceipt(trf, m.outB, m.beras, 10), p.trReceipt(trf, m.outB, m.beras, 10)
		errs := make(chan error, 2)
		for _, n := range []string{a, b} {
			go func(n string) { errs <- p.submitErr("stock_transfer_receipt", n) }(n)
		}
		e1, e2 := <-errs, <-errs
		if (e1 == nil) == (e2 == nil) {
			t.Fatalf("round %d: two receipts = %v / %v", i, e1, e2)
		}
		if e1 == nil {
			e1 = e2
		}
		wantCode(t, e1, 409, "not_in_transit") // the winner received it all
		p.wantTransfer(trf, "received", 10, 12000)

		trf2 := p.doc("stock_transfer", map[string]any{"from_outlet": m.outA, "to_outlet": m.outB, "items": []any{map[string]any{"item": m.beras, "qty": 10}}})
		r := p.trReceipt(trf2, m.outB, m.beras, 10)
		cancelErr, recErr := make(chan error, 1), make(chan error, 1)
		go func() { cancelErr <- p.cancel("stock_transfer", trf2) }()
		go func() { recErr <- p.submitErr("stock_transfer_receipt", r) }()
		ce, re := <-cancelErr, <-recErr
		switch {
		case ce == nil && re != nil:
			wantCode(t, re, 409, "not_in_transit")
		case re == nil && ce != nil:
			wantCode(t, ce, 409, "has_receipts")
		default:
			t.Fatalf("round %d: cancel %v, receipt %v", i, ce, re)
		}
	}
	// every kilogram that left the warehouse is either at Dago or back: 1000 = A + B
	a, b := p.balance(m.outA, m.beras), p.balance(m.outB, m.beras)
	if qtyOf(a["qty"])+qtyOf(b["qty"]) != 1000 || rp(a["value"])+rp(b["value"]) != 12000000 {
		t.Fatalf("stock not conserved: %v + %v", a, b)
	}
}
