package engine

import (
	"context"
	"errors"
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

// money reads a currency value as whole rupiah.
func money(v any) int64 { return moneyIn(v) }

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
	cases := []struct {
		in   float64
		want int64
	}{{2.5, 3}, {-2.5, -3}, {2.4999999999, 3}, {12999.17, 12999}, {0.1 * 13000 * 30, 39000}, {-0.5, -1}}
	for _, c := range cases {
		if got := roundHalfAway(c.in); got != c.want {
			t.Errorf("roundHalfAway(%v) = %d, want %d", c.in, got, c.want)
		}
	}
	if mulRound(0.1*300, 13000) != 390000 || divRound(1950000, 150) != 13000 || moneyIn(11100000.0) != 11100000 || moneyIn("1250000") != 1250000 {
		t.Error("money helpers")
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
		if err != nil || money(locked["outstanding"]) != 600000 {
			t.Errorf("lock after write: %v %v", locked["outstanding"], err)
		}
		return nil, nil
	}
	name := p.submit("sales_invoice", inv)
	got := p.get("sales_invoice", name)
	if money(got["paid_amount"]) != 400000 || money(got["outstanding"]) != 600000 || got["status"] != "partly" {
		t.Fatalf("invoice after SetSubmitted: paid %v outstanding %v status %v", got["paid_amount"], got["outstanding"], got["status"])
	}
	if c := p.get("customer", m.cust); money(c["outstanding"]) != 600000 {
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
	return money(p.get(model, name)["outstanding"])
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
		if money(r["outstanding"]) != 0 || r["status"] != "paid" {
			t.Fatalf("%s after payment: outstanding %v status %v", inv, r["outstanding"], r["status"])
		}
	}
	if got := p.outstanding("customer", m.cust); got != 0 {
		t.Fatalf("customer outstanding after payment = %d", got)
	}
	for _, row := range rows(p.get("payment", pay)["allocations"]) {
		if row["applied_idr"] != money(row["amount"]) {
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

// TestPilotForeignPayment: a USD payment applies round(amount × rate) rupiah and cancel reverses exactly that.
func TestPilotForeignPayment(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	inv := p.invoice(m, m.cust, 2000000, false, nil)
	pay := p.doc("payment", map[string]any{"customer": m.cust, "currency": "USD", "rate": 15500.55, "amount": 100, "allocations": []any{alloc(inv, 100)}})
	if got := p.outstanding("sales_invoice", inv); got != 2000000-1550055 {
		t.Fatalf("outstanding after USD payment = %d", got)
	}
	if r := rows(p.get("payment", pay)["allocations"])[0]; r["applied_idr"] != int64(1550055) {
		t.Fatalf("applied_idr = %v", r["applied_idr"])
	}
	if err := p.cancel("payment", pay); err != nil {
		t.Fatal(err)
	}
	if p.outstanding("sales_invoice", inv) != 2000000 || p.outstanding("customer", m.cust) != 2000000 {
		t.Fatal("USD payment cancel")
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

	_, err = p.e.RunAction(p.ctx, p.a, "quotation", q1, "make_order", map[string]any{"po_number": "PO-1"}, ActionOpts{})
	wantCode(t, err, 422, "po_required")
	args := map[string]any{"po_number": "PO/BP/0091", "po_date": "2026-10-29"}
	so := opened(p.action("quotation", q1, "make_order", args))
	o := p.get("customer_order", so)
	line := rows(o["items"])[0]
	if o["customer"] != m.cust || o["quotation"] != q1 || o["po_number"] != "PO/BP/0091" || o["po_date"] != "2026-10-29" ||
		line["qty"] != 2.0 || money(line["rate"]) != 900000 || money(o["subtotal"]) != 1800000 {
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
	if got := money(p.get("customer_order", so)["invoiced_amount"]); got != 1000000 {
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
	if got := money(p.get("customer_order", so)["invoiced_amount"]); got != 2000000 {
		t.Fatalf("invoiced_amount after cancel = %d", got)
	}
}

func TestPilotPurchaseRequestToPO(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	pack := p.create("uom_conversion", map[string]any{"item": m.beras, "uom": m.karung, "factor": 50})
	pr := p.doc("purchase_request", map[string]any{"outlet": m.outB, "needed_by": "2026-11-05",
		"items": []any{map[string]any{"item": m.beras, "qty": 2, "uom": m.karung, "packing": pack}}})
	po := opened(p.action("purchase_request", pr, "make_po", nil))
	d := p.get("purchase_order", po)
	line := rows(d["items"])[0]
	if d["vendor"] != "" || d["purchase_request"] != pr || d["outlet"] != m.outB || line["packing"] != pack || money(line["rate"]) != 11000 || line["stock_qty"] != 100.0 {
		t.Fatalf("draft PO = %v, line %v", d, line)
	}
	if st := p.get("purchase_request", pr)["state"]; st != "ordered" {
		t.Fatalf("request state = %v", st)
	}
	if again := opened(p.action("purchase_request", pr, "make_po", nil)); again != po {
		t.Fatal("draft PO not reused")
	}
	// the draft still needs a vendor before it is submitted
	if _, err := p.e.Submit(p.ctx, p.a, "purchase_order", po, true); err == nil {
		t.Fatal("PO without vendor submitted")
	}
	if _, err := p.e.Update(p.ctx, p.a, "purchase_order", po, map[string]any{"vendor": m.vend}); err != nil {
		t.Fatal(err)
	}
	po = p.submit("purchase_order", po)
	gr := opened(p.action("purchase_order", po, "receive", nil))
	g := p.get("goods_receipt", gr)
	if g["vendor"] != m.vend || g["outlet"] != m.outB || g["purchase_order"] != po || rows(g["items"])[0]["qty"] != 2.0 || rows(g["items"])[0]["stock_qty"] != 100.0 {
		t.Fatalf("draft receipt = %v", g)
	}
}
