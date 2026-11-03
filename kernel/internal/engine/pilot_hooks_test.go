package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// The pilot hooks run against a self-contained catalogue (testdata/pilot), trimmed from the rangka-apps S8
// Model reference to the fields the hooks read and write.
const (
	pilotApps = "testdata/pilot/apps"
	pilotSite = "testdata/pilot/site"
)

type pilot struct {
	t   *testing.T
	e   *Engine
	a   Actor
	ctx context.Context
}

func setupPilot(t *testing.T) *pilot {
	t.Helper()
	url := testdb.New(t)
	ctx := context.Background()
	reg, rep := meta.Validate(pilotApps, pilotSite, nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("pilot catalogue errors: %v", rep.All())
	}
	db, err := pg.NewPool(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := store.Migrate(ctx, db, reg, false, "test"); err != nil {
		t.Fatal(err)
	}
	e := New(db, reg)
	e.Now = FixedNow
	return &pilot{t: t, e: e, ctx: ctx, a: Actor{Org: "pilot", ID: "owner@pilot.test", UserName: "USR-001", Roles: []string{"owner"}, Human: true}}
}

// create makes a record and fails the test on error; it returns the record's name.
func (p *pilot) create(model string, in map[string]any) string {
	p.t.Helper()
	r, err := p.e.Create(p.ctx, p.a, model, in)
	if err != nil {
		p.t.Fatalf("create %s: %v", model, err)
	}
	return str(r.Record["name"])
}

// submit submits a draft (force) and fails the test on error; it returns the submitted name.
func (p *pilot) submit(model, name string) string {
	p.t.Helper()
	r, err := p.e.Submit(p.ctx, p.a, model, name, true)
	if err != nil {
		p.t.Fatalf("submit %s %s: %v", model, name, err)
	}
	return str(r.Record["name"])
}

// doc creates and submits a document.
func (p *pilot) doc(model string, in map[string]any) string {
	p.t.Helper()
	return p.submit(model, p.create(model, in))
}

func (p *pilot) get(model, name string) store.Record {
	p.t.Helper()
	r, err := p.e.Get(p.ctx, p.a, model, name)
	if err != nil {
		p.t.Fatalf("get %s %s: %v", model, name, err)
	}
	return r
}

func (p *pilot) cancel(model, name string) error {
	_, err := p.e.Cancel(p.ctx, p.a, model, name)
	return err
}

func (p *pilot) action(model, name, action string, args map[string]any) map[string]any {
	p.t.Helper()
	r, err := p.e.RunAction(p.ctx, p.a, model, name, action, args, ActionOpts{})
	if err != nil {
		p.t.Fatalf("action %s on %s %s: %v", action, model, name, err)
	}
	return r.Result
}

// opened is the record an action result opens.
func opened(res map[string]any) string { return str(meta.AsMap(res["open"])["name"]) }

// wantCode asserts a StatusError with a status and code.
func wantCode(t *testing.T, err error, status int, code string) {
	t.Helper()
	var se *StatusError
	if !errors.As(err, &se) || se.Status != status || se.Code != code {
		t.Fatalf("want %d %s, got %v", status, code, err)
	}
}

// idr reads a stored amount as whole rupiah.
func rp(v any) int64 { return moneyIn(v) }

// master creates the shared master data: uoms Kg and Karung, items, two outlets, a customer and a vendor.
type masters struct {
	kg, karung, porsi         string
	beras, nasi, jasa, minyak string
	outA, outB, outC          string
	cust, cust2, vend         string
}

func (p *pilot) masters() masters {
	var m masters
	m.kg = p.create("uom", map[string]any{"uom_name": "Kg"})
	m.karung = p.create("uom", map[string]any{"uom_name": "Karung"})
	m.porsi = p.create("uom", map[string]any{"uom_name": "Porsi"})
	m.beras = p.create("item", map[string]any{"item_name": "Beras", "kind": "material", "stock_uom": m.kg, "standard_cost": 11000})
	m.minyak = p.create("item", map[string]any{"item_name": "Minyak", "kind": "material", "stock_uom": m.kg})
	m.nasi = p.create("item", map[string]any{"item_name": "Nasi goreng", "kind": "product", "stock_uom": m.porsi, "rate": 25000})
	m.jasa = p.create("item", map[string]any{"item_name": "Inspeksi NDT", "kind": "service", "stock_uom": m.kg, "rate": 1000000})
	m.outA = p.create("outlet", map[string]any{"outlet_name": "Gudang pusat"})
	m.outB = p.create("outlet", map[string]any{"outlet_name": "Cabang Dago"})
	m.outC = p.create("outlet", map[string]any{"outlet_name": "Cabang Braga"})
	m.cust = p.create("customer", map[string]any{"customer_name": "PT Baja Prima", "credit_limit": 1000000000})
	m.cust2 = p.create("customer", map[string]any{"customer_name": "PT Lain", "credit_limit": 1000000000})
	m.vend = p.create("vendor", map[string]any{"vendor_name": "CV Tani Makmur"})
	return m
}

func TestMoneyRounding(t *testing.T) {
	if mulRound(0.1*300, 13000) != 390000 || mulRound(2.5, 1) != 3 || mulRound(-2.5, 1) != -3 || mulRound(0.5, 25) != 13 {
		t.Error("mulRound")
	}
	if divRound(1950000, 150) != 13000 || divRound(1421000, 117) != 12145 || divRound(5, 2) != 3 || divRound(-5, 2) != -3 {
		t.Error("divRound")
	}
	if percentOff(1000000, 10) != 900000 || percentOff(15, 50) != 8 {
		t.Error("percentOff")
	}
	if moneyIn(11100000.0) != 11100000 || moneyIn("1250000.5") != 1250001 || moneyIn(int64(7)) != 7 || moneyIn(nil) != 0 {
		t.Error("moneyIn")
	}
}

// TestPilotSetSubmitted: hooks write read-only fields of submitted documents, dependent computed fields
// follow, and editable fields are refused.
func TestPilotSetSubmitted(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	inv := p.create("sales_invoice", map[string]any{"customer": m.cust, "ppn_applies": false, "items": []any{map[string]any{"item": m.jasa, "qty": 1}}})
	// a stand-in hook (the real ones arrive in the next tasks) that pays 400.000 on submit
	p.e.Hooks["ledger.post_receivable"] = func(h *HookCtx) (map[string]any, error) {
		if _, err := h.SetSubmitted("sales_invoice", str(h.Record["name"]), map[string]any{"paid_amount": moneyOut(400000)}); err != nil {
			return nil, err
		}
		if _, err := h.SetSubmitted("sales_invoice", str(h.Record["name"]), map[string]any{"notes": "x"}); err == nil {
			t.Error("SetSubmitted accepted a field that does not exist")
		}
		if _, err := h.SetSubmitted("sales_invoice", str(h.Record["name"]), map[string]any{"po_number": "x"}); err == nil {
			t.Error("SetSubmitted accepted an editable field")
		}
		if _, err := h.SetSubmitted("customer", str(h.Record["customer"]), map[string]any{"outstanding": moneyOut(600000)}); err != nil {
			return nil, err
		}
		locked, err := h.Lock("customer", str(h.Record["customer"]))
		if err != nil || rp(locked["outstanding"]) != 600000 {
			t.Errorf("lock after write: %v %v", locked["outstanding"], err)
		}
		return nil, nil
	}
	name := p.submit("sales_invoice", inv)
	got := p.get("sales_invoice", name)
	if rp(got["paid_amount"]) != 400000 || rp(got["outstanding"]) != 600000 || got["status"] != "partly" {
		t.Fatalf("invoice after SetSubmitted: paid %v outstanding %v status %v", got["paid_amount"], got["outstanding"], got["status"])
	}
	if c := p.get("customer", m.cust); rp(c["outstanding"]) != 600000 {
		t.Fatalf("customer outstanding = %v", c["outstanding"])
	}
}

// ---------- Task 2: receivables, payables, payments

// invoice submits a sales invoice of one service line; ppn on gives 11/12 × 12% PPN.
func (p *pilot) invoice(m masters, cust string, rate int64, ppn bool, extra map[string]any) string {
	p.t.Helper()
	in := map[string]any{"customer": cust, "ppn_applies": ppn, "items": []any{map[string]any{"item": m.jasa, "qty": 1, "rate": rate}}}
	for k, v := range extra {
		in[k] = v
	}
	return p.doc("sales_invoice", in)
}

func (p *pilot) outstanding(model, name string) int64 {
	p.t.Helper()
	return rp(p.get(model, name)["outstanding"])
}

func alloc(inv string, amount int64) map[string]any {
	return map[string]any{"sales_invoice": inv, "amount": amount}
}

// TestPilotPaymentAllocation is the Review Focus: over-allocation against the stored outstanding, one payment
// across two invoices, and cancel as the exact inverse.
func TestPilotPaymentAllocation(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	inv1 := p.invoice(m, m.cust, 10000000, true, nil) // 10.000.000 + PPN 1.100.000
	inv2 := p.invoice(m, m.cust, 3900000, false, nil)
	if got := p.outstanding("sales_invoice", inv1); got != 11100000 {
		t.Fatalf("invoice 1 outstanding = %d", got)
	}
	if got := p.outstanding("customer", m.cust); got != 15000000 {
		t.Fatalf("customer outstanding after two invoices = %d", got)
	}

	over := p.create("payment", map[string]any{"customer": m.cust, "amount": 12000000, "allocations": []any{alloc(inv1, 12000000)}})
	_, err := p.e.Submit(p.ctx, p.a, "payment", over, true)
	wantCode(t, err, 409, "over_allocation")
	if !strings.Contains(err.Error(), "Alokasi melebihi sisa tagihan "+inv1) {
		t.Errorf("message = %q", err.Error())
	}
	if p.get("payment", over)["docstatus"] != int64(0) || p.outstanding("sales_invoice", inv1) != 11100000 {
		t.Fatal("refused submit was not rolled back")
	}

	short := p.create("payment", map[string]any{"customer": m.cust, "amount": 10000000, "allocations": []any{alloc(inv1, 11100000)}})
	_, err = p.e.Submit(p.ctx, p.a, "payment", short, true)
	wantCode(t, err, 409, "over_payment")

	pay := p.doc("payment", map[string]any{"customer": m.cust, "amount": 15000000, "allocations": []any{alloc(inv1, 11100000), alloc(inv2, 3900000)}})
	for _, inv := range []string{inv1, inv2} {
		r := p.get("sales_invoice", inv)
		if rp(r["outstanding"]) != 0 || r["status"] != "paid" {
			t.Fatalf("%s after payment: outstanding %v status %v", inv, r["outstanding"], r["status"])
		}
	}
	if got := p.outstanding("customer", m.cust); got != 0 {
		t.Fatalf("customer outstanding after payment = %d", got)
	}
	for _, row := range rows(p.get("payment", pay)["allocations"]) {
		if row["applied_idr"] != rp(row["amount"]) {
			t.Errorf("applied_idr = %v for amount %v", row["applied_idr"], row["amount"])
		}
	}

	wantCode(t, p.cancel("sales_invoice", inv1), 409, "has_payments")
	if err := p.cancel("payment", pay); err != nil {
		t.Fatal(err)
	}
	if p.outstanding("sales_invoice", inv1) != 11100000 || p.outstanding("sales_invoice", inv2) != 3900000 || p.outstanding("customer", m.cust) != 15000000 {
		t.Fatal("cancel did not restore the outstanding amounts")
	}
	if err := p.cancel("sales_invoice", inv2); err != nil {
		t.Fatal(err)
	}
	if got := p.outstanding("customer", m.cust); got != 11100000 {
		t.Fatalf("customer outstanding after cancelling invoice 2 = %d", got)
	}
}

func TestPilotPaymentRefusals(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	other := p.invoice(m, m.cust2, 1000000, false, nil)
	draft := p.create("sales_invoice", map[string]any{"customer": m.cust, "items": []any{map[string]any{"item": m.jasa}}})
	for _, c := range []struct{ inv, code string }{{other, "wrong_party"}, {draft, "not_submitted"}} {
		pay := p.create("payment", map[string]any{"customer": m.cust, "amount": 1000000, "allocations": []any{alloc(c.inv, 1000)}})
		_, err := p.e.Submit(p.ctx, p.a, "payment", pay, true)
		wantCode(t, err, 409, c.code)
	}
	// two rows on one invoice: the second sees the first
	inv := p.invoice(m, m.cust, 1000000, false, nil)
	pay := p.create("payment", map[string]any{"customer": m.cust, "amount": 1200000, "allocations": []any{alloc(inv, 600000), alloc(inv, 600000)}})
	_, err := p.e.Submit(p.ctx, p.a, "payment", pay, true)
	wantCode(t, err, 409, "over_allocation")
	// withheld PPh 23 settles the invoice with the cash
	p.doc("payment", map[string]any{"customer": m.cust, "amount": 980000, "allocations": []any{map[string]any{"sales_invoice": inv, "amount": 980000, "withheld": 20000}}})
	if p.outstanding("sales_invoice", inv) != 0 || p.outstanding("customer", m.cust) != 0 {
		t.Fatal("withheld not applied")
	}
}

// TestPilotAllocationRuling: rows are IDR as entered (applied_idr = amount), non-positive rows and PPh 23
// withheld twice are refused, and two rows on one invoice are checked cumulatively.
func TestPilotAllocationRuling(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	inv := p.invoice(m, m.cust, 2000000, false, nil)
	for _, row := range []map[string]any{alloc(inv, 0), alloc(inv, -5), {"sales_invoice": inv, "amount": 1000, "withheld": -1}} {
		pay := p.create("payment", map[string]any{"customer": m.cust, "amount": 1000000, "allocations": []any{row}})
		_, err := p.e.Submit(p.ctx, p.a, "payment", pay, true)
		wantCode(t, err, 409, "negative_allocation")
	}
	withPPh := p.invoice(m, m.cust, 1000000, false, map[string]any{"pph23_applies": true})
	pay := p.create("payment", map[string]any{"customer": m.cust, "amount": 980000, "allocations": []any{map[string]any{"sales_invoice": withPPh, "amount": 980000, "withheld": 20000}}})
	_, err := p.e.Submit(p.ctx, p.a, "payment", pay, true)
	wantCode(t, err, 409, "pph23_already_deducted")
	// two rows on the same invoice that fit together are applied in order
	two := p.doc("payment", map[string]any{"customer": m.cust, "amount": 2000000, "allocations": []any{alloc(inv, 1500000), alloc(inv, 500000)}})
	for _, r := range rows(p.get("payment", two)["allocations"]) {
		if r["applied_idr"] != rp(r["amount"]) {
			t.Fatalf("applied_idr %v for amount %v", r["applied_idr"], r["amount"])
		}
	}
	if p.outstanding("sales_invoice", inv) != 0 {
		t.Fatal("two rows on one invoice")
	}
}

func TestPilotDownPaymentAndRetention(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	dp := p.invoice(m, m.cust, 3000000, false, map[string]any{"kind": "down_payment"})
	a := p.invoice(m, m.cust, 5000000, false, map[string]any{"dp_invoice": dp, "dp_deduction": 2000000})
	if got := p.outstanding("sales_invoice", a); got != 3000000 {
		t.Fatalf("invoice after DP deduction = %d", got)
	}
	b := p.create("sales_invoice", map[string]any{"customer": m.cust, "ppn_applies": false, "dp_invoice": dp, "dp_deduction": 2000000,
		"items": []any{map[string]any{"item": m.jasa, "rate": 5000000}}})
	_, err := p.e.Submit(p.ctx, p.a, "sales_invoice", b, true)
	wantCode(t, err, 409, "dp_over_deducted")
	wantCode(t, p.cancel("sales_invoice", dp), 409, "has_dependents")

	ret := map[string]any{"kind": "retention", "retention_of": a}
	p.invoice(m, m.cust, 250000, false, ret)
	again := p.create("sales_invoice", map[string]any{"customer": m.cust, "kind": "retention", "retention_of": a, "ppn_applies": false,
		"items": []any{map[string]any{"item": m.jasa, "rate": 250000}}})
	_, err = p.e.Submit(p.ctx, p.a, "sales_invoice", again, true)
	wantCode(t, err, 409, "retention_billed")
	if got := p.outstanding("customer", m.cust); got != 3000000+3000000+250000 {
		t.Fatalf("customer outstanding = %d", got)
	}
}

func TestPilotPayables(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	bill := p.doc("vendor_bill", map[string]any{"vendor": m.vend, "bill_no": "TM/001", "items": []any{map[string]any{"item": m.beras, "qty": 100, "rate": 12000}}})
	if p.outstanding("vendor", m.vend) != 1200000 {
		t.Fatal("vendor outstanding after bill")
	}
	pay := p.doc("payment", map[string]any{"direction": "pay", "vendor": m.vend, "amount": 1200000, "allocations": []any{map[string]any{"vendor_bill": bill, "amount": 1200000}}})
	if p.outstanding("vendor_bill", bill) != 0 || p.outstanding("vendor", m.vend) != 0 {
		t.Fatal("vendor bill payment")
	}
	wantCode(t, p.cancel("vendor_bill", bill), 409, "has_payments")
	if err := p.cancel("payment", pay); err != nil {
		t.Fatal(err)
	}
	if err := p.cancel("vendor_bill", bill); err != nil {
		t.Fatal(err)
	}
	if p.outstanding("vendor", m.vend) != 0 {
		t.Fatal("vendor outstanding after cancel")
	}
}

// ---------- Task 3: conversions

func TestPilotQuotationRevisionOutcomeOrder(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	q := p.doc("quotation", map[string]any{"customer": m.cust, "subject": "Inspeksi tangki", "rfq_ref": "RFQ-77",
		"items": []any{map[string]any{"item": m.jasa, "qty": 2, "rate": 1000000, "discount": 10}}})
	if r := p.get("quotation", q); r["revision"] != int64(0) || q != "QUO-2026-00001" {
		t.Fatalf("quotation %s revision %v", q, r["revision"])
	}
	res := p.action("quotation", q, "new_revision", nil)
	draft := p.get("quotation", opened(res))
	if draft["revision"] != int64(1) || draft["outcome"] != "open" || draft["amended_from"] != q || draft["docstatus"] != int64(0) {
		t.Fatalf("revision draft = %v %v %v %v", draft["revision"], draft["outcome"], draft["amended_from"], draft["docstatus"])
	}
	if p.get("quotation", q)["docstatus"] != int64(2) {
		t.Fatal("old revision not cancelled")
	}
	q1 := p.submit("quotation", opened(res))
	if q1 != "QUO-2026-00001-1" {
		t.Fatalf("revision name = %s", q1)
	}

	_, err := p.e.RunAction(p.ctx, p.a, "quotation", q1, "mark_outcome", map[string]any{"outcome": "won"}, ActionOpts{})
	wantCode(t, err, 409, "bad_outcome")
	_, err = p.e.RunAction(p.ctx, p.a, "quotation", q1, "mark_outcome", map[string]any{"outcome": "lost"}, ActionOpts{})
	wantCode(t, err, 409, "lost_reason_required")
	_, err = p.e.RunAction(p.ctx, p.a, "quotation", q1, "make_order", map[string]any{"po_number": "PO-1"}, ActionOpts{})
	wantCode(t, err, 409, "action_not_allowed") // still open
	p.action("quotation", q1, "mark_outcome", map[string]any{"outcome": "accepted"})
	if r := p.get("quotation", q1); r["outcome"] != "accepted" || r["docstatus"] != int64(1) {
		t.Fatalf("outcome = %v", r["outcome"])
	}
	_, err = p.e.RunAction(p.ctx, p.a, "quotation", q1, "mark_outcome", map[string]any{"outcome": "lost", "lost_reason": "harga"}, ActionOpts{})
	wantCode(t, err, 409, "not_open")

	_, err = p.e.RunAction(p.ctx, p.a, "quotation", q1, "make_order", map[string]any{"po_number": "PO-1"}, ActionOpts{})
	wantCode(t, err, 422, "po_required")
	args := map[string]any{"po_number": "PO/BP/0091", "po_date": "2026-10-29"}
	so := opened(p.action("quotation", q1, "make_order", args))
	o := p.get("customer_order", so)
	line := rows(o["items"])[0]
	if o["customer"] != m.cust || o["quotation"] != q1 || o["po_number"] != "PO/BP/0091" || o["po_date"] != "2026-10-29" ||
		line["qty"] != 2.0 || rp(line["rate"]) != 900000 || rp(o["subtotal"]) != 1800000 {
		t.Fatalf("order = %v, line %v", o, line)
	}
	if again := opened(p.action("quotation", q1, "make_order", args)); again != so {
		t.Fatalf("second make_order made %s, want the draft %s", again, so)
	}
}

func TestPilotInvoiceFromOrder(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	so := p.create("customer_order", map[string]any{"customer": m.cust, "po_number": "PO-9", "po_date": "2026-10-01",
		"items": []any{map[string]any{"item": m.jasa, "qty": 3, "rate": 1000000}}})
	_, err := p.e.RunAction(p.ctx, p.a, "customer_order", so, "make_invoice", nil, ActionOpts{})
	wantCode(t, err, 409, "action_not_allowed")
	so = p.submit("customer_order", so)
	d1 := opened(p.action("customer_order", so, "make_invoice", nil))
	if again := opened(p.action("customer_order", so, "make_invoice", nil)); again != d1 {
		t.Fatal("draft not reused")
	}
	inv := p.get("sales_invoice", d1)
	if inv["customer_order"] != so || inv["po_number"] != "PO-9" || rows(inv["items"])[0]["qty"] != 3.0 {
		t.Fatalf("draft invoice = %v", inv)
	}
	if _, err := p.e.Update(p.ctx, p.a, "sales_invoice", d1, map[string]any{"ppn_applies": false, "items": []any{map[string]any{"item": m.jasa, "qty": 1, "rate": 1000000}}}); err != nil {
		t.Fatal(err)
	}
	i1 := p.submit("sales_invoice", d1)
	if got := rp(p.get("customer_order", so)["invoiced_amount"]); got != 1000000 {
		t.Fatalf("invoiced_amount = %d", got)
	}
	d2 := opened(p.action("customer_order", so, "make_invoice", nil))
	if q := rows(p.get("sales_invoice", d2)["items"])[0]["qty"]; q != 2.0 {
		t.Fatalf("remainder qty = %v", q)
	}
	p.submit("sales_invoice", d2)
	_, err = p.e.RunAction(p.ctx, p.a, "customer_order", so, "make_invoice", nil, ActionOpts{})
	wantCode(t, err, 409, "nothing_to_invoice")
	if err := p.cancel("sales_invoice", i1); err != nil {
		t.Fatal(err)
	}
	if got := rp(p.get("customer_order", so)["invoiced_amount"]); got != 2000000 {
		t.Fatalf("invoiced_amount after cancel = %d", got)
	}
}

func TestPilotPurchaseRequestToPO(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	pack := p.create("uom_conversion", map[string]any{"item": m.beras, "uom": m.karung, "factor": 50})
	pr := p.doc("purchase_request", map[string]any{"outlet": m.outB, "needed_by": "2026-11-05",
		"items": []any{map[string]any{"item": m.beras, "qty": 2, "uom": m.karung, "packing": pack}}})
	_, err := p.e.RunAction(p.ctx, p.a, "purchase_request", pr, "make_po", nil, ActionOpts{})
	wantCode(t, err, 422, "vendor_required")
	po := opened(p.action("purchase_request", pr, "make_po", map[string]any{"vendor": m.vend}))
	d := p.get("purchase_order", po)
	line := rows(d["items"])[0]
	if d["vendor"] != m.vend || d["purchase_request"] != pr || d["outlet"] != m.outB || line["packing"] != pack || rp(line["rate"]) != 11000 || line["stock_qty"] != 100.0 {
		t.Fatalf("draft PO = %v, line %v", d, line)
	}
	if st := p.get("purchase_request", pr)["state"]; st != "ordered" {
		t.Fatalf("request state = %v", st)
	}
	if again := opened(p.action("purchase_request", pr, "make_po", map[string]any{"vendor": m.vend})); again != po {
		t.Fatal("draft PO not reused")
	}
	po = p.submit("purchase_order", po)
	gr := opened(p.action("purchase_order", po, "receive", nil))
	g := p.get("goods_receipt", gr)
	if g["vendor"] != m.vend || g["outlet"] != m.outB || g["purchase_order"] != po || rows(g["items"])[0]["qty"] != 2.0 || rows(g["items"])[0]["stock_qty"] != 100.0 {
		t.Fatalf("draft receipt = %v", g)
	}
}

// ---------- Task 4: stock

func (p *pilot) balance(outlet, item string) store.Record {
	p.t.Helper()
	rows, _, err := p.e.List(p.ctx, p.a, "stock_balance", ListOpts{Filter: "outlet == '" + outlet + "' && item == '" + item + "'"})
	if err != nil || len(rows) != 1 {
		p.t.Fatalf("balance %s/%s: %d rows, %v", outlet, item, len(rows), err)
	}
	return rows[0]
}

func (p *pilot) wantBalance(outlet, item string, qty float64, value, avg int64) store.Record {
	p.t.Helper()
	b := p.balance(outlet, item)
	if qtyOf(b["qty"]) != qty || rp(b["value"]) != value || rp(b["avg_cost"]) != avg {
		p.t.Fatalf("balance %s/%s = qty %v value %v avg %v, want %v %d %d", outlet, item, b["qty"], b["value"], b["avg_cost"], qty, value, avg)
	}
	return b
}

func (p *pilot) moves(voucher string) []store.Record {
	p.t.Helper()
	rows, _, err := p.e.List(p.ctx, p.a, "stock_move", ListOpts{Filter: "voucher == '" + voucher + "'", Sort: "name"})
	if err != nil {
		p.t.Fatal(err)
	}
	return rows
}

func (p *pilot) receipt(m masters, outlet string, qty, rate int64) string {
	p.t.Helper()
	return p.doc("goods_receipt", map[string]any{"vendor": m.vend, "outlet": outlet, "items": []any{map[string]any{"item": m.beras, "qty": qty, "rate": rate}}})
}

// TestPilotStockAverageTransferSale: moving average, reversal back to the prior balance, transfers at the
// average, and a recipe sale that drives an outlet negative (the negative-stock exception).
func TestPilotStockAverageTransferSale(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	p.receipt(m, m.outA, 100, 12000)
	p.wantBalance(m.outA, m.beras, 100, 1200000, 12000)
	gr2 := p.receipt(m, m.outA, 50, 15000)
	p.wantBalance(m.outA, m.beras, 150, 1950000, 13000)

	if err := p.cancel("goods_receipt", gr2); err != nil {
		t.Fatal(err)
	}
	p.wantBalance(m.outA, m.beras, 100, 1200000, 12000)
	if mv := p.moves(gr2); len(mv) != 2 || mv[1]["reason"] != "reversal" || mv[1]["qty"] != -50.0 || rp(mv[1]["value"]) != -750000 {
		t.Fatalf("reversal moves = %v", mv)
	}
	am, err := p.e.Amend(p.ctx, p.a, "goods_receipt", gr2)
	if err != nil {
		t.Fatal(err)
	}
	p.submit("goods_receipt", str(am.Record["name"]))
	p.wantBalance(m.outA, m.beras, 150, 1950000, 13000)

	trf := p.doc("stock_transfer", map[string]any{"from_outlet": m.outA, "to_outlet": m.outB, "items": []any{map[string]any{"item": m.beras, "qty": 30}}})
	p.wantBalance(m.outA, m.beras, 120, 1560000, 13000)
	p.wantBalance(m.outB, m.beras, 30, 390000, 13000)
	if mv := p.moves(trf); len(mv) != 2 || mv[0]["reason"] != "transfer_out" || rp(mv[0]["value"]) != -390000 || mv[1]["reason"] != "transfer_in" || rp(mv[1]["unit_cost"]) != 13000 {
		t.Fatalf("transfer moves = %v", mv)
	}
	p.doc("stock_transfer", map[string]any{"from_outlet": m.outA, "to_outlet": m.outC, "items": []any{map[string]any{"item": m.beras, "qty": 20}}})
	p.wantBalance(m.outC, m.beras, 20, 260000, 13000)

	p.create("recipe", map[string]any{"product": m.nasi, "yield_qty": 1, "lines": []any{map[string]any{"material": m.beras, "qty": 0.1}}})
	sale := p.doc("outlet_sales", map[string]any{"outlet": m.outC, "date": "2026-10-30", "lines": []any{map[string]any{"item": m.nasi, "qty": 300}}})
	b := p.wantBalance(m.outC, m.beras, -10, -130000, 13000)
	if b["negative"] != true {
		t.Fatal("stock_balance.negative not set")
	}
	s := p.get("outlet_sales", sale)
	if line := rows(s["lines"])[0]; rp(line["cogs"]) != 390000 || rp(s["cogs_total"]) != 390000 || rp(s["margin"]) != 7500000-390000 {
		t.Fatalf("sale cogs = %v total %v margin %v", line["cogs"], s["cogs_total"], s["margin"])
	}
	mv := p.moves(sale)
	if len(mv) != 1 || mv[0]["negative"] != true || mv[0]["reason"] != "sale" || mv[0]["qty"] != -30.0 || mv[0]["balance_qty"] != -10.0 {
		t.Fatalf("sale move = %v", mv)
	}
	// the module's flow, run with the move record as its event, notifies head office
	fr, err := p.e.RunFlow(p.ctx, p.a, "stock_negative_alert", map[string]any(mv[0]))
	if err != nil || fr.Skipped || len(fr.Notifications) != 1 || fr.Notifications[0].Text != "Stok "+m.beras+" di "+m.outC+" minus (-10)" {
		t.Fatalf("negative alert flow = %+v, %v", fr, err)
	}
	if fr, _ := p.e.RunFlow(p.ctx, p.a, "stock_negative_alert", map[string]any(p.moves(trf)[1])); !fr.Skipped {
		t.Fatal("flow ran for a move that is not negative")
	}

	dup := p.create("outlet_sales", map[string]any{"outlet": m.outC, "date": "2026-10-30", "lines": []any{map[string]any{"item": m.nasi, "qty": 1}}})
	_, err = p.e.Submit(p.ctx, p.a, "outlet_sales", dup, true)
	wantCode(t, err, 409, "duplicate_sales")
	if err := p.cancel("outlet_sales", sale); err != nil {
		t.Fatal(err)
	}
	p.wantBalance(m.outC, m.beras, 20, 260000, 13000)
}

// TestPilotStockCountWastePacking: a count posts the difference and records system_qty; waste goes out at
// the average; a PO in Karung (factor 50) is received as kilograms and its received_qty follows.
func TestPilotStockCountWastePacking(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	p.receipt(m, m.outB, 20, 13000)
	opn := p.doc("stock_count", map[string]any{"outlet": m.outB, "items": []any{map[string]any{"item": m.beras, "counted_qty": 18}}})
	if l := rows(p.get("stock_count", opn)["items"])[0]; l["system_qty"] != 20.0 || l["difference"] != -2.0 {
		t.Fatalf("count line = %v", l)
	}
	if mv := p.moves(opn); len(mv) != 1 || mv[0]["qty"] != -2.0 || rp(mv[0]["value"]) != -26000 || mv[0]["reason"] != "count" {
		t.Fatalf("count move = %v", mv)
	}
	p.wantBalance(m.outB, m.beras, 18, 234000, 13000)
	adj := p.doc("stock_adjustment", map[string]any{"outlet": m.outB, "reason": "waste", "items": []any{map[string]any{"item": m.beras, "qty": 1}}})
	if mv := p.moves(adj); len(mv) != 1 || mv[0]["reason"] != "waste" || rp(mv[0]["value"]) != -13000 {
		t.Fatalf("waste move = %v", mv)
	}
	p.wantBalance(m.outB, m.beras, 17, 221000, 13000)
	// a count that finds more posts it in at the average; a material with no average yet uses standard_cost
	p.doc("stock_count", map[string]any{"outlet": m.outA, "items": []any{map[string]any{"item": m.beras, "counted_qty": 5}}})
	p.wantBalance(m.outA, m.beras, 5, 55000, 11000)

	pack := p.create("uom_conversion", map[string]any{"item": m.beras, "uom": m.karung, "factor": 50})
	po := p.doc("purchase_order", map[string]any{"vendor": m.vend, "outlet": m.outB, "items": []any{map[string]any{"item": m.beras, "qty": 2, "uom": m.karung, "packing": pack, "rate": 600000}}})
	gr := p.submit("goods_receipt", opened(p.action("purchase_order", po, "receive", nil)))
	if mv := p.moves(gr); len(mv) != 1 || mv[0]["qty"] != 100.0 || rp(mv[0]["unit_cost"]) != 12000 {
		t.Fatalf("packed receipt move = %v", mv)
	}
	b := p.balance(m.outB, m.beras)
	if b["qty"] != 117.0 || rp(b["avg_cost"]) != 12145 || rp(b["value"]) != 1421000 {
		t.Fatalf("balance after packed receipt = %v %v %v", b["qty"], b["avg_cost"], b["value"])
	}
	o := p.get("purchase_order", po)
	if rows(o["items"])[0]["received_qty"] != 2.0 || o["received_pct"] != 100.0 {
		t.Fatalf("PO received = %v %v", rows(o["items"])[0]["received_qty"], o["received_pct"])
	}
	_, err := p.e.RunAction(p.ctx, p.a, "purchase_order", po, "receive", nil, ActionOpts{})
	wantCode(t, err, 409, "nothing_to_receive")
	if err := p.cancel("goods_receipt", gr); err != nil {
		t.Fatal(err)
	}
	p.wantBalance(m.outB, m.beras, 17, 221000, 13000) // back to the prior balance exactly
	if o := p.get("purchase_order", po); rows(o["items"])[0]["received_qty"] != 0.0 || o["received_pct"] != 0.0 {
		t.Fatal("PO received_qty not reverted")
	}
}

// TestPilotConcurrentAllocation: two payments submitted at once for the whole of one invoice; the row lock
// makes the second see the first, so exactly one succeeds.
func TestPilotConcurrentAllocation(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	inv := p.invoice(m, m.cust, 5000000, false, nil)
	a := p.create("payment", map[string]any{"customer": m.cust, "amount": 5000000, "allocations": []any{alloc(inv, 5000000)}})
	b := p.create("payment", map[string]any{"customer": m.cust, "amount": 5000000, "allocations": []any{alloc(inv, 5000000)}})
	errs := make(chan error, 2)
	for _, n := range []string{a, b} {
		go func(n string) { _, err := p.e.Submit(p.ctx, p.a, "payment", n, true); errs <- err }(n)
	}
	e1, e2 := <-errs, <-errs
	if (e1 == nil) == (e2 == nil) {
		t.Fatalf("want one success and one refusal, got %v / %v", e1, e2)
	}
	if e1 == nil {
		e1 = e2
	}
	wantCode(t, e1, 409, "over_allocation")
	if p.outstanding("sales_invoice", inv) != 0 || p.outstanding("customer", m.cust) != 0 {
		t.Fatal("outstanding after concurrent payments")
	}
}

// TestPilotHooksUnderRLS runs allocation, a conversion and stock hooks as a plain (non-superuser) role, so the
// row-level security policy (contract 0010 §4) applies to every locked find and SetSubmitted write.
func TestPilotHooksUnderRLS(t *testing.T) {
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
	defer admin.Close()
	if _, err := store.Migrate(ctx, admin, reg, false, "test"); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 4)
	rand.Read(b)
	role, pass := "rk_pilot_"+hex.EncodeToString(b), "pw"+hex.EncodeToString(b)
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
	p := &pilot{t: t, e: e, ctx: pg.WithOrg(ctx, "pilot"), a: Actor{Org: "pilot", ID: "owner@pilot.test", UserName: "USR-001", Roles: []string{"owner"}, Human: true}}
	m := p.masters()
	inv := p.invoice(m, m.cust, 1000000, false, nil)
	pay := p.doc("payment", map[string]any{"customer": m.cust, "amount": 1000000, "allocations": []any{alloc(inv, 1000000)}})
	if p.outstanding("sales_invoice", inv) != 0 || p.outstanding("customer", m.cust) != 0 {
		t.Fatal("allocation under RLS")
	}
	if err := p.cancel("payment", pay); err != nil || p.outstanding("customer", m.cust) != 1000000 {
		t.Fatalf("unallocate under RLS: %v", err)
	}
	p.receipt(m, m.outA, 10, 12000)
	p.doc("stock_adjustment", map[string]any{"outlet": m.outA, "reason": "waste", "items": []any{map[string]any{"item": m.beras, "qty": 12}}})
	if b := p.wantBalance(m.outA, m.beras, -2, -24000, 12000); b["negative"] != true {
		t.Fatal("negative under RLS")
	}
	// another org sees none of it
	other := Actor{Org: "other", ID: "x@other.test", Roles: []string{"owner"}}
	if rows, _, err := e.List(pg.WithOrg(ctx, "other"), other, "stock_move", ListOpts{}); err != nil || len(rows) != 0 {
		t.Fatalf("other org sees %d moves, %v", len(rows), err)
	}
}

// TestPilotReceiptAtLineAmount: a receipt is valued at its line amount, not at round(stock qty × rounded
// unit cost): 1 Karung of 3 kg at 100.000 → +3 kg worth 100.000 (unit cost 33.333 is informative).
func TestPilotReceiptAtLineAmount(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	pack := p.create("uom_conversion", map[string]any{"item": m.minyak, "uom": m.karung, "factor": 3})
	gr := p.doc("goods_receipt", map[string]any{"vendor": m.vend, "outlet": m.outA, "items": []any{map[string]any{"item": m.minyak, "qty": 1, "uom": m.karung, "packing": pack, "rate": 100000}}})
	mv := p.moves(gr)
	if len(mv) != 1 || mv[0]["qty"] != 3.0 || rp(mv[0]["value"]) != 100000 || rp(mv[0]["unit_cost"]) != 33333 {
		t.Fatalf("receipt move = %v", mv)
	}
	p.wantBalance(m.outA, m.minyak, 3, 100000, 33333)
	if err := p.cancel("goods_receipt", gr); err != nil {
		t.Fatal(err)
	}
	if b := p.balance(m.outA, m.minyak); b["qty"] != 0.0 || rp(b["value"]) != 0 {
		t.Fatalf("after reversal: %v %v", b["qty"], b["value"])
	}
}

// TestPilotCancelRacesAllocation: cancelling an invoice while a payment allocates to it. The row lock makes
// one wait for the other: either the cancel is refused (has_payments) or the allocation is (not_submitted),
// never both committed.
func TestPilotCancelRacesAllocation(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	for i := 0; i < 6; i++ {
		inv := p.invoice(m, m.cust, 1000000, false, nil)
		pay := p.create("payment", map[string]any{"customer": m.cust, "amount": 1000000, "allocations": []any{alloc(inv, 1000000)}})
		cancelErr, payErr := make(chan error, 1), make(chan error, 1)
		go func() { cancelErr <- p.cancel("sales_invoice", inv) }()
		go func() { _, err := p.e.Submit(p.ctx, p.a, "payment", pay, true); payErr <- err }()
		ce, pe := <-cancelErr, <-payErr
		switch {
		case ce == nil && pe != nil:
			wantCode(t, pe, 409, "not_submitted")
			if r := p.get("sales_invoice", inv); r["docstatus"] != int64(2) || rp(r["paid_amount"]) != 0 {
				t.Fatalf("cancel won but invoice = %v %v", r["docstatus"], r["paid_amount"])
			}
		case pe == nil && ce != nil:
			wantCode(t, ce, 409, "has_payments")
			if r := p.get("sales_invoice", inv); r["docstatus"] != int64(1) || rp(r["paid_amount"]) != 1000000 {
				t.Fatalf("payment won but invoice = %v %v", r["docstatus"], r["paid_amount"])
			}
			if err := p.cancel("payment", p.mustSubmittedName("payment", pay)); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("round %d: cancel %v, payment %v", i, ce, pe)
		}
		if got := p.outstanding("customer", m.cust); got != 0 && got != 1000000 {
			t.Fatalf("customer outstanding = %d", got)
		}
	}
}

// mustSubmittedName finds the submitted record that was the draft name (Submit renames DRAFT-…).
func (p *pilot) mustSubmittedName(model, draft string) string {
	p.t.Helper()
	rows, _, err := p.e.List(p.ctx, p.a, model, ListOpts{Filter: "docstatus == 1", Sort: "-name", Limit: 1})
	if err != nil || len(rows) == 0 {
		p.t.Fatalf("no submitted %s for %s: %v", model, draft, err)
	}
	return str(rows[0]["name"])
}

// TestPilotCrossDocumentStockLocks: a goods receipt with lines [minyak, beras] and a stock adjustment with
// lines [beras, minyak] at the same outlet, submitted at once (different number series, so nothing else
// serialises them). Without sorted balance locks they deadlock; with lockAll both post.
func TestPilotCrossDocumentStockLocks(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	p.receipt(m, m.outA, 1000, 10000)
	p.doc("goods_receipt", map[string]any{"vendor": m.vend, "outlet": m.outA, "items": []any{map[string]any{"item": m.minyak, "qty": 1000, "rate": 20000}}})
	for i := 0; i < 8; i++ {
		gr := p.create("goods_receipt", map[string]any{"vendor": m.vend, "outlet": m.outA, "items": []any{
			map[string]any{"item": m.minyak, "qty": 1, "rate": 20000}, map[string]any{"item": m.beras, "qty": 1, "rate": 10000}}})
		adj := p.create("stock_adjustment", map[string]any{"outlet": m.outA, "reason": "damage", "items": []any{
			map[string]any{"item": m.beras, "qty": 1}, map[string]any{"item": m.minyak, "qty": 1}}})
		errs := make(chan error, 2)
		go func() { _, err := p.e.Submit(p.ctx, p.a, "goods_receipt", gr, true); errs <- err }()
		go func() { _, err := p.e.Submit(p.ctx, p.a, "stock_adjustment", adj, true); errs <- err }()
		if e1, e2 := <-errs, <-errs; e1 != nil || e2 != nil {
			t.Fatalf("round %d: %v / %v", i, e1, e2)
		}
	}
	p.wantBalance(m.outA, m.beras, 1000, 10000000, 10000)
	p.wantBalance(m.outA, m.minyak, 1000, 20000000, 20000)
}

// TestPilotHeldResubmitRacesApprove: a held vendor bill submitted again while its approval is approved. The
// record's advisory lock is always taken before its row, so the two queue instead of deadlocking, and the
// bill posts its payable once.
func TestPilotHeldResubmitRacesApprove(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	approver := Actor{Org: "pilot", ID: "boss@pilot.test", UserName: "USR-002", Roles: []string{"owner"}, Human: true}
	clerk := Actor{Org: "pilot", ID: "clerk@pilot.test", UserName: "USR-003", Roles: []string{"clerk"}, Human: true} // an owner's request approves itself
	for i := 0; i < 3; i++ {
		bill := p.create("vendor_bill", map[string]any{"vendor": m.vend, "bill_no": "BIG/" + formatInt(int64(i)), "items": []any{map[string]any{"item": m.beras, "qty": 1, "rate": 200000000}}})
		res, err := p.e.Submit(p.ctx, clerk, "vendor_bill", bill, true)
		if err != nil || !res.Held || len(res.Approvals) == 0 {
			t.Fatalf("first submit not held: %v %v", res, err)
		}
		id := res.Approvals[0].ID
		errs := make(chan error, 2)
		go func() { _, err := p.e.Submit(p.ctx, clerk, "vendor_bill", bill, true); errs <- err }()
		go func() { _, err := p.e.Approve(p.ctx, approver, id); errs <- err }()
		for _, err := range []error{<-errs, <-errs} {
			if err != nil && (strings.Contains(err.Error(), "deadlock") || !(IsNotFound(err) || isConflict(err))) {
				t.Fatalf("round %d: %v", i, err)
			}
		}
		if got := p.outstanding("vendor", m.vend); got != int64(i+1)*200000000 {
			t.Fatalf("round %d: vendor outstanding = %d", i, got)
		}
	}
}

func isConflict(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Status == 409
}

// TestPilotReviseHeldCancel: when cancelling the quotation needs approval, revise keeps the request (held)
// and amends nothing; after approval, revise on the cancelled quotation makes the revision once.
func TestPilotReviseHeldCancel(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	q := p.doc("quotation", map[string]any{"customer": m.cust, "subject": "Perlu persetujuan", "items": []any{map[string]any{"item": m.jasa}}})
	clerk := Actor{Org: "pilot", ID: "clerk@pilot.test", UserName: "USR-003", Roles: []string{"clerk"}, Human: true}
	out, err := p.e.RunAction(p.ctx, clerk, "quotation", q, "new_revision", nil, ActionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	res := out.Result
	if res["held"] != true || p.get("quotation", q)["docstatus"] != int64(1) {
		t.Fatalf("revise with a held cancel = %v", res)
	}
	aps, _, err := p.e.ListApprovals(p.ctx, p.a, store.ApprovalFilter{Org: "pilot", Model: "quotation", Name: q})
	if err != nil || len(aps) != 1 || aps[0].State != "pending" {
		t.Fatalf("approval request not kept: %v %v", aps, err)
	}
	approver := Actor{Org: "pilot", ID: "boss@pilot.test", UserName: "USR-002", Roles: []string{"owner"}, Human: true}
	if _, err := p.e.Approve(p.ctx, approver, aps[0].ID); err != nil {
		t.Fatal(err)
	}
	if p.get("quotation", q)["docstatus"] != int64(2) {
		t.Fatal("approved cancel did not run")
	}
	// the module's new_revision if: must allow a cancelled quotation for this; the hook is called directly
	var draft string
	for i := 0; i < 2; i++ {
		if err := p.e.run(p.ctx, p.a, func(o *op) error {
			mq := p.e.Reg().Model("quotation")
			rec, err := o.lockLoad(mq, q)
			if err != nil {
				return err
			}
			out, err := callHook(reviseQuotation, &HookCtx{op: o, Model: mq, Record: rec})
			if err == nil {
				if draft == "" {
					draft = opened(out)
				} else if opened(out) != draft {
					t.Errorf("second revise made %s, want %s", opened(out), draft)
				}
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if d := p.get("quotation", draft); d["revision"] != int64(1) || d["amended_from"] != q {
		t.Fatalf("revision draft = %v %v", d["revision"], d["amended_from"])
	}
}

// runHook calls a hook on a record directly, inside one transaction (for refusals an action's if: hides).
func (p *pilot) runHook(fn Hook, model, name string, args map[string]any) error {
	return p.e.run(p.ctx, p.a, func(o *op) error {
		m := p.e.Reg().Model(model)
		rec, err := o.lockLoad(m, name)
		if err != nil {
			return err
		}
		_, err = callHook(fn, &HookCtx{op: o, Model: m, Record: rec, Args: args})
		return err
	})
}

func TestPilotReviewMinors(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	q := p.doc("quotation", map[string]any{"customer": m.cust, "subject": "x", "items": []any{map[string]any{"item": m.jasa}}})
	wantCode(t, p.runHook(orderFromQuotation, "quotation", q, map[string]any{"po_number": "PO-1", "po_date": "2026-10-30"}), 409, "not_accepted")

	po := p.doc("purchase_order", map[string]any{"vendor": m.vend, "items": []any{map[string]any{"item": m.beras, "qty": 1, "rate": 1000}}})
	_, err := p.e.RunAction(p.ctx, p.a, "purchase_order", po, "receive", nil, ActionOpts{})
	wantCode(t, err, 422, "outlet_required")

	// an unreadable amount fails the hook instead of counting as 0
	bad := func(h *HookCtx) (map[string]any, error) { return map[string]any{"n": moneyIn("12abc")}, nil }
	if err := p.runHook(bad, "quotation", q, nil); err == nil || !strings.Contains(err.Error(), "not a number") {
		t.Fatalf("bad amount: %v", err)
	}
	huge := func(h *HookCtx) (map[string]any, error) { return map[string]any{"n": mulRound(1e30, 1e9)}, nil }
	if err := p.runHook(huge, "quotation", q, nil); err == nil {
		t.Fatal("overflow accepted")
	}
	wrong := func(h *HookCtx) (map[string]any, error) {
		_, err := h.SetSubmitted("quotation", q, map[string]any{"items": map[string]any{"x": 1}})
		return nil, err
	}
	if err := p.runHook(wrong, "quotation", q, nil); err == nil {
		t.Fatal("SetSubmitted accepted rows of the wrong type")
	}
}
