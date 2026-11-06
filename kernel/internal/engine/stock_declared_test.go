package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupDeclared is the pilot catalogue plus kernel/testdata/stock-apps/apps/declared (contract 0021 documents), with the stock_move
// reasons those documents use.
func setupDeclared(t *testing.T) *pilot {
	t.Helper()
	dir := t.TempDir()
	apps, site := filepath.Join(dir, "apps"), filepath.Join(dir, "site")
	for _, c := range [][2]string{{pilotApps, apps}, {pilotSite, site}, {"../../testdata/stock-apps/apps/declared", filepath.Join(apps, "declared")}} {
		if err := exec.Command("cp", "-r", c[0], c[1]).Run(); err != nil {
			t.Fatal(err)
		}
	}
	edit := func(file, old, new string) {
		b, err := os.ReadFile(file)
		if err != nil || !strings.Contains(string(b), old) {
			t.Fatalf("%s: %v (no %q)", file, err, old)
		}
		os.WriteFile(file, []byte(strings.Replace(string(b), old, new, 1)), 0o644)
	}
	var extra []string
	for _, r := range []string{"delivery", "return_in", "production_in", "production_out"} {
		extra = append(extra, fmt.Sprintf("{value: %s, label: {id: %s}}", r, r))
	}
	edit(filepath.Join(apps, "stock", "models", "stock_move.rk"), "{value: reversal, label: {id: reversal}}]",
		"{value: reversal, label: {id: reversal}}, "+strings.Join(extra, ", ")+"]")
	edit(filepath.Join(site, "config.rk"), "outlet_sales]", "outlet_sales, declared]")
	return setupPilotAppsSite(t, apps, site)
}

// moveRows are the stock moves of the org in posting order, without the document names, for comparison.
func (p *pilot) moveRows() []string {
	p.t.Helper()
	res, err := p.e.DB.Query(p.ctx, `select outlet, item, qty, unit_cost, value, balance_qty, balance_value, avg_cost, negative, reason, posting_date
		from r_stock_move where org_id = $1 order by created_at, name`, p.a.Org)
	if err != nil {
		p.t.Fatal(err)
	}
	var out []string
	for _, r := range res.Rows {
		out = append(out, fmt.Sprint(r))
	}
	return out
}

func (p *pilot) balanceRows() []string {
	p.t.Helper()
	res, err := p.e.DB.Query(p.ctx, `select outlet, item, qty, value, avg_cost, last_sale_date from r_stock_balance where org_id = $1 order by outlet, item`, p.a.Org)
	if err != nil {
		p.t.Fatal(err)
	}
	var out []string
	for _, r := range res.Rows {
		out = append(out, fmt.Sprint(r))
	}
	return out
}

// TestDeclaredMovesEquivalent: goods_receipt, stock_adjustment (both reasons) and outlet_sales (recipe, cogs)
// written as stock: declarations post the same moves, balances, cogs and reversals as their Go rules, on the
// same script in two orgs (negative stock, standard cost fallback, a merged recipe material and odd rounding
// included).
func TestDeclaredMovesEquivalent(t *testing.T) {
	p := setupDeclared(t)
	q := p.as(Actor{Org: "twin", ID: "owner@twin.test", UserName: "USR-001", Roles: []string{"owner"}, Human: true})
	type docs struct{ gr, adj, sales string }
	run := func(p *pilot, d docs) (cogs []string) {
		m := p.masters()
		line := func(item string, qty float64, rate int64) map[string]any {
			return map[string]any{"item": item, "qty": qty, "rate": rate}
		}
		gr := map[string]any{"outlet": m.outA, "received_on": "2026-10-01", "items": []any{line(m.beras, 100, 12000), line(m.beras, 3, 12345), line(m.minyak, 10, 20000)}}
		if d.gr == "goods_receipt" {
			gr["vendor"] = m.vend
		}
		p.doc(d.gr, gr)
		p.doc(d.gr, map[string]any{"outlet": m.outA, "received_on": "2026-10-02", "vendor": m.vend, "items": []any{line(m.beras, 7, 13001)}})
		p.doc(d.adj, map[string]any{"outlet": m.outA, "adjustment_date": "2026-10-03", "reason": "waste", "items": []any{map[string]any{"item": m.beras, "qty": 2.5}}})
		adj := p.doc(d.adj, map[string]any{"outlet": m.outA, "adjustment_date": "2026-10-04", "reason": "other", "items": []any{map[string]any{"item": m.minyak, "qty": 1}, map[string]any{"item": m.beras, "qty": 0}}})
		p.create("recipe", map[string]any{"product": m.nasi, "lines": []any{map[string]any{"material": m.beras, "qty": 0.1}, map[string]any{"material": m.minyak, "qty": 0.05}, map[string]any{"material": m.beras, "qty": 0.02}}})
		sale := func(outlet, date string, qtys ...float64) string {
			var ls []any
			for _, x := range qtys {
				ls = append(ls, map[string]any{"item": m.nasi, "qty": x})
			}
			ls = append(ls, map[string]any{"item": m.beras, "qty": 1})
			return p.doc(d.sales, map[string]any{"outlet": outlet, "date": date, "lines": ls})
		}
		s1 := sale(m.outA, "2026-10-05", 10, 3)
		sale(m.outB, "2026-10-06", 4) // no stock at B: negative, valued at standard_cost (beras) or 0 (minyak)
		s3 := sale(m.outA, "2026-10-07", 7)
		if err := p.cancel(d.adj, adj); err != nil {
			t.Fatal(err)
		}
		if err := p.cancel(d.sales, s1); err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{s1, s3} {
			r := p.get(d.sales, s)
			c := fmt.Sprint(rp(r["cogs_total"]))
			for _, l := range rows(r["lines"]) {
				c += " " + fmt.Sprint(rp(l["cogs"]))
			}
			cogs = append(cogs, c)
		}
		return cogs
	}
	cogsGo := run(p, docs{"goods_receipt", "stock_adjustment", "outlet_sales"})
	cogsDecl := run(q, docs{"gr_declared", "adj_declared", "sales_declared"})
	if a, b := strings.Join(cogsGo, "|"), strings.Join(cogsDecl, "|"); a != b {
		t.Fatalf("cogs differ:\n go       %s\n declared %s", a, b)
	}
	a, b := p.moveRows(), q.moveRows()
	if len(a) < 20 || len(a) != len(b) {
		t.Fatalf("%d go moves, %d declared moves", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("move %d differs:\n go       %s\n declared %s", i, a[i], b[i])
		}
	}
	if a, b := strings.Join(p.balanceRows(), "\n"), strings.Join(q.balanceRows(), "\n"); a != b {
		t.Fatalf("balances differ:\n go:\n%s\n declared:\n%s", a, b)
	}
}

// TestDeclaredDeliveryReturnProduction: a delivery note takes stock out at the average with line cogs; a sales
// return brings good items back at the cost they left at and damaged ones into quarantine; a production result
// puts outputs in at the value of the materials (contract 0021 example A.4); cancel reverses every move.
func TestDeclaredDeliveryReturnProduction(t *testing.T) {
	p := setupDeclared(t)
	m := p.masters()
	p.receipt(m, m.outA, 100, 12000)
	p.receipt(m, m.outA, 100, 14000) // average 13.000
	dn := p.doc("delivery_note", map[string]any{"outlet": m.outA, "delivery_date": "2026-10-10", "items": []any{map[string]any{"item": m.beras, "qty": 10}}})
	d := p.get("delivery_note", dn)
	if rp(d["cogs_total"]) != 130000 || rp(rows(d["items"])[0]["cogs"]) != 130000 {
		t.Fatalf("delivery cogs = %v / %v, want 130000", d["cogs_total"], rows(d["items"])[0]["cogs"])
	}
	// the average moves after the delivery; the return still comes back at 13.000
	p.receipt(m, m.outA, 10, 30000)
	rj := p.doc("sales_return", map[string]any{"delivery_note": dn, "outlet": m.outA, "quarantine_outlet": m.outC, "return_date": "2026-10-11",
		"items": []any{map[string]any{"item": m.beras, "qty": 3, "condition": "good"}, map[string]any{"item": m.beras, "qty": 2, "condition": "damaged"}}})
	mv := p.moves(rj)
	if len(mv) != 2 {
		t.Fatalf("return moves = %d", len(mv))
	}
	for _, x := range mv {
		if rp(x["unit_cost"]) != 13000 || x["reason"] != "return_in" {
			t.Fatalf("return move %v at %v (%v), want 13000", x["outlet"], x["unit_cost"], x["reason"])
		}
	}
	if b := p.balance(m.outC, m.beras); qtyOf(b["qty"]) != 2 || rp(b["value"]) != 26000 {
		t.Fatalf("quarantine balance = %v %v", b["qty"], b["value"])
	}
	// a return without a delivery note comes back at the outlet's average
	rj2 := p.doc("sales_return", map[string]any{"outlet": m.outB, "quarantine_outlet": m.outC, "return_date": "2026-10-11",
		"items": []any{map[string]any{"item": m.beras, "qty": 1}}})
	if x := p.moves(rj2)[0]; rp(x["unit_cost"]) != 11000 {
		t.Fatalf("return without source at %v, want standard cost 11000", x["unit_cost"])
	}

	// production: 10 kg beras (avg A) and 2 kg minyak (avg 90.000) out, 100 + 0 outputs at the materials' value
	p.receipt(m, m.outB, 20, 12000)
	p.doc("goods_receipt", map[string]any{"vendor": m.vend, "outlet": m.outB, "received_on": "2026-10-01", "items": []any{map[string]any{"item": m.minyak, "qty": 2, "rate": 90000}}})
	avgB := rp(p.balance(m.outB, m.beras)["avg_cost"]) // 1 returned at 11.000 + 20 at 12.000
	if avgB != 11952 {
		t.Fatalf("avg beras B = %d", avgB)
	}
	prd := p.doc("production_result", map[string]any{"outlet": m.outB, "production_date": "2026-10-12",
		"materials": []any{map[string]any{"item": m.beras, "qty": 10}, map[string]any{"item": m.minyak, "qty": 2}},
		"outputs":   []any{map[string]any{"item": m.nasi, "qty": 100, "outlet": m.outB, "cost_share": 70}, map[string]any{"item": m.nasi, "qty": 30, "outlet": m.outC, "cost_share": 30}}})
	used := int64(0)
	var in []int64
	for _, x := range p.moves(prd) {
		switch x["reason"] {
		case "production_out":
			used += -rp(x["value"])
		case "production_in":
			in = append(in, rp(x["value"]))
		}
	}
	want := 10*avgB + 2*90000 // 299.520
	if used != want || len(in) != 2 || in[0] != mulRound(0.7, want) || in[0]+in[1] != used {
		t.Fatalf("production used %d, in %v; want %d split 70/30", used, in, want)
	}
	if b := p.balance(m.outB, m.nasi); rp(b["avg_cost"]) != divRound(mulRound(0.7, want), 100) {
		t.Fatalf("finished goods avg = %v", b["avg_cost"])
	}

	// cancel reverses every move of the declared documents
	for _, c := range [][2]string{{"production_result", prd}, {"sales_return", rj}, {"delivery_note", dn}} {
		if err := p.cancel(c[0], c[1]); err != nil {
			t.Fatalf("cancel %s: %v", c[0], err)
		}
	}
	if b := p.balance(m.outC, m.beras); qtyOf(b["qty"]) != 0 || rp(b["value"]) != 0 {
		t.Fatalf("quarantine after cancel = %v %v", b["qty"], b["value"])
	}
	if b := p.balance(m.outB, m.nasi); qtyOf(b["qty"]) != 0 || rp(b["value"]) != 0 {
		t.Fatalf("finished goods after cancel = %v %v", b["qty"], b["value"])
	}
	if b := p.balance(m.outA, m.beras); qtyOf(b["qty"]) != 210 {
		t.Fatalf("warehouse after cancel = %v, want 210", b["qty"])
	}
}

// TestDeclaredNegativeRefuse: negative: refuse fails the submit (nothing moves) when an out move would leave the
// outlet below zero; the default allows it.
func TestDeclaredNegativeRefuse(t *testing.T) {
	p := setupDeclared(t)
	m := p.masters()
	p.receipt(m, m.outA, 5, 12000)
	si := p.create("strict_issue", map[string]any{"outlet": m.outA, "issue_date": "2026-10-10", "items": []any{map[string]any{"item": m.beras, "qty": 6}}})
	_, err := p.e.Submit(p.ctx, p.a, "strict_issue", si, true)
	wantCode(t, err, 409, "negative_stock")
	if b := p.balance(m.outA, m.beras); qtyOf(b["qty"]) != 5 {
		t.Fatalf("refused issue moved stock: %v", b["qty"])
	}
	ok := p.doc("strict_issue", map[string]any{"outlet": m.outA, "issue_date": "2026-10-10", "items": []any{map[string]any{"item": m.beras, "qty": 5}}})
	if b := p.balance(m.outA, m.beras); qtyOf(b["qty"]) != 0 || ok == "" {
		t.Fatalf("issue of the whole stock: %v", b["qty"])
	}
	p.doc("delivery_note", map[string]any{"outlet": m.outA, "delivery_date": "2026-10-10", "items": []any{map[string]any{"item": m.beras, "qty": 1}}})
	if b := p.balance(m.outA, m.beras); qtyOf(b["qty"]) != -1 {
		t.Fatalf("delivery without refuse left %v, want -1", b["qty"])
	}
}

// TestDeclaredAuditFixes covers the K-o audit (reports/audit/2026-11-06-kernel-k-o.md):
// H1 two consumed rules share one pool; H2 a return from a sale exploded through a recipe comes back at the
// sale's cogs per unit; M3 a line without outlet is refused; M4 a return from a cancelled source is refused;
// M5 a datetime posts on the local date; M6 a non-bool if is an error; L9 two cogs fields on one table.
func TestDeclaredAuditFixes(t *testing.T) {
	p := setupDeclared(t)
	m := p.masters()
	p.receipt(m, m.outA, 10, 10000) // 100.000

	// H1: outputs and by-products split the materials' value once
	pr := p.doc("prod_two", map[string]any{"outlet": m.outA, "production_date": "2026-10-10",
		"materials":  []any{map[string]any{"item": m.beras, "qty": 10}},
		"outputs":    []any{map[string]any{"item": m.nasi, "qty": 30, "outlet": m.outA}},
		"byproducts": []any{map[string]any{"item": m.minyak, "qty": 10, "outlet": m.outA}}})
	var in, out int64
	for _, x := range p.moves(pr) {
		if v := rp(x["value"]); v > 0 {
			in += v
		} else {
			out -= v
		}
	}
	if out != 100000 || in != 100000 {
		t.Fatalf("two consumed rules: out %d, in %d; want 100000 both", out, in)
	}
	if b := p.balance(m.outA, m.nasi); rp(b["value"]) != 75000 {
		t.Fatalf("output share = %v, want 75000 (30 of 40 by qty)", b["value"])
	}

	// H2: a return of 2 portions from a recipe sale comes back at the sale's cogs per portion
	p.receipt(m, m.outB, 10, 12000)
	p.create("recipe", map[string]any{"product": m.jasa, "lines": []any{map[string]any{"material": m.beras, "qty": 0.2}}})
	sale := p.doc("sales_declared", map[string]any{"outlet": m.outB, "date": "2026-10-11", "lines": []any{map[string]any{"item": m.jasa, "qty": 10}}})
	if c := rp(p.get("sales_declared", sale)["cogs_total"]); c != 24000 {
		t.Fatalf("sale cogs = %d", c)
	}
	ret := p.doc("pos_return", map[string]any{"sale": sale, "outlet": m.outB, "return_date": "2026-10-12", "items": []any{map[string]any{"item": m.jasa, "qty": 2}}})
	if x := p.moves(ret)[0]; rp(x["unit_cost"]) != 2400 || rp(x["value"]) != 4800 {
		t.Fatalf("return from an exploded sale at %v / %v, want 2400 / 4800", x["unit_cost"], x["value"])
	}

	// M4: a return from a cancelled delivery note is refused (its stock went back already)
	dn := p.doc("delivery_note", map[string]any{"outlet": m.outB, "delivery_date": "2026-10-12", "items": []any{map[string]any{"item": m.beras, "qty": 1}}})
	if err := p.cancel("delivery_note", dn); err != nil {
		t.Fatal(err)
	}
	rj := p.create("sales_return", map[string]any{"delivery_note": dn, "outlet": m.outB, "quarantine_outlet": m.outC, "return_date": "2026-10-13",
		"items": []any{map[string]any{"item": m.beras, "qty": 1}}})
	_, err := p.e.Submit(p.ctx, p.a, "sales_return", rj, true)
	wantCode(t, err, 409, "source_not_submitted")

	// M3: a line without its outlet is refused, nothing moves
	il := p.create("issue_line", map[string]any{"issue_date": "2026-10-13", "items": []any{
		map[string]any{"item": m.beras, "qty": 3, "outlet": m.outB}, map[string]any{"item": m.beras, "qty": 4}}})
	before := qtyOf(p.balance(m.outB, m.beras)["qty"])
	_, err = p.e.Submit(p.ctx, p.a, "issue_line", il, true)
	wantCode(t, err, 409, "stock_outlet_missing")
	if after := qtyOf(p.balance(m.outB, m.beras)["qty"]); after != before {
		t.Fatalf("refused issue moved stock: %v -> %v", before, after)
	}

	// M5: 12 Oct 06:30 WIB posts on 12 Oct, not on the UTC date
	dt := p.doc("dt_issue", map[string]any{"outlet": m.outB, "issued_at": "2026-10-12T06:30:00+07:00", "items": []any{map[string]any{"item": m.beras, "qty": 1}}})
	if d := dateOnly(p.moves(dt)[0]["posting_date"]); d != "2026-10-12" {
		t.Fatalf("datetime posting_date = %s, want 2026-10-12", d)
	}

	// M6: an if that is not true or false is an error, not a skip
	nb := p.create("nonbool_issue", map[string]any{"outlet": m.outB, "issue_date": "2026-10-13", "items": []any{map[string]any{"item": m.beras, "qty": 1}}})
	_, err = p.e.Submit(p.ctx, p.a, "nonbool_issue", nb, true)
	wantCode(t, err, 422, "stock_if")
}

// TestDeclaredReturnFromInvoiceViaDeliveryNote: an invoice made from a delivery note moves no stock (its if guards
// the chain); a return against it comes back at the outlet average, not at the invoice's empty cogs (audit R1).
func TestDeclaredReturnFromInvoiceViaDeliveryNote(t *testing.T) {
	p := setupDeclared(t)
	m := p.masters()
	p.receipt(m, m.outA, 100, 12000)
	dn := p.doc("delivery_note", map[string]any{"outlet": m.outA, "delivery_date": "2026-10-10", "items": []any{map[string]any{"item": m.beras, "qty": 10}}})
	inv := p.doc("invoice_decl", map[string]any{"delivery_note": dn, "outlet": m.outA, "invoice_date": "2026-10-10", "items": []any{map[string]any{"item": m.beras, "qty": 10}}})
	if n := len(p.moves(inv)); n != 0 {
		t.Fatalf("invoice via delivery note moved stock: %d moves", n)
	}
	rt := p.doc("inv_return", map[string]any{"invoice": inv, "outlet": m.outA, "return_date": "2026-10-11", "items": []any{map[string]any{"item": m.beras, "qty": 2}}})
	if x := p.moves(rt)[0]; rp(x["unit_cost"]) != 12000 {
		t.Fatalf("return at %v, want the average 12000", x["unit_cost"])
	}
	if b := p.balance(m.outA, m.beras); rp(b["avg_cost"]) != 12000 {
		t.Fatalf("avg after return = %v", b["avg_cost"])
	}
}

// TestDeclaredReturnExplode (contract 0022): an in move with explode: recipe brings back the materials of each
// returned item, each at the cost the source sale took that material out (cost source) or at the outlet average;
// an item without a recipe comes back as itself, a service moves nothing; cancel reverses it.
func TestDeclaredReturnExplode(t *testing.T) {
	p := setupDeclared(t)
	m := p.masters()
	p.receipt(m, m.outB, 10, 12000) // beras 10 kg at 12.000
	p.create("recipe", map[string]any{"product": m.nasi, "lines": []any{map[string]any{"material": m.beras, "qty": 0.2}}})
	sale := p.doc("sales_declared", map[string]any{"outlet": m.outB, "date": "2026-10-11", "lines": []any{
		map[string]any{"item": m.nasi, "qty": 10}, map[string]any{"item": m.beras, "qty": 1}}})
	if c := rp(p.get("sales_declared", sale)["cogs_total"]); c != 36000 { // 2 kg in the recipe + 1 kg sold as is
		t.Fatalf("sale cogs = %d, want 36000", c)
	}
	p.receipt(m, m.outB, 7, 20000) // the average moves away from the sale's cost: 7 kg at 12.000 + 7 kg at 20.000
	ret := p.doc("recipe_return", map[string]any{"sale": sale, "outlet": m.outB, "return_date": "2026-10-12", "items": []any{
		map[string]any{"item": m.nasi, "qty": 3}, map[string]any{"item": m.beras, "qty": 1},
		map[string]any{"item": m.jasa, "qty": 1}, map[string]any{"item": m.nasi, "qty": 1, "condition": "damaged"}}})
	mv := p.moves(ret)
	if len(mv) != 3 {
		t.Fatalf("return moves = %d, want 3 (0,6 kg and 1 kg at the sale cost, 0,2 kg at the average after those two: 243.200 / 15,6 kg; the service none): %v", len(mv), mv)
	}
	want := [][3]any{{0.6, int64(12000), int64(7200)}, {1.0, int64(12000), int64(12000)}, {0.2, int64(15590), int64(3118)}}
	for i, x := range mv {
		if str(x["item"]) != m.beras || str(x["reason"]) != "return_in" || qtyOf(x["qty"]) != want[i][0] || rp(x["unit_cost"]) != want[i][1] || rp(x["value"]) != want[i][2] {
			t.Fatalf("move %d = %v %v %v %v %v, want beras %v", i, x["item"], x["qty"], x["unit_cost"], x["value"], x["reason"], want[i])
		}
	}
	if b := p.balance(m.outB, m.beras); qtyOf(b["qty"]) != 15.8 || rp(b["value"]) != 7*12000+7*20000+7200+12000+3118 {
		t.Fatalf("beras after the return = %v / %v", b["qty"], b["value"])
	}
	if err := p.cancel("recipe_return", ret); err != nil {
		t.Fatal(err)
	}
	if b := p.balance(m.outB, m.beras); qtyOf(b["qty"]) != 14 {
		t.Fatalf("beras after cancelling the return = %v, want 14", b["qty"])
	}
}

// TestDeclaredReturnRefundLines (audit K-p L1): the cogs-per-unit fallback of a cost source skips the source's refund
// lines (qty < 0), which took nothing out: 24.000 over the 10 sold, not over 10 - 4.
func TestDeclaredReturnRefundLines(t *testing.T) {
	p := setupDeclared(t)
	m := p.masters()
	p.receipt(m, m.outB, 10, 12000)
	rc := p.create("recipe", map[string]any{"product": m.nasi, "lines": []any{map[string]any{"material": m.beras, "qty": 0.2}}})
	sale := p.doc("sales_declared", map[string]any{"outlet": m.outB, "date": "2026-10-11", "lines": []any{
		map[string]any{"item": m.nasi, "qty": 10}, map[string]any{"item": m.nasi, "qty": -4}}})
	if c := rp(p.get("sales_declared", sale)["cogs_total"]); c != 24000 {
		t.Fatalf("sale cogs = %d, want 24000", c)
	}
	if _, err := p.e.Update(p.ctx, p.a, "recipe", rc, map[string]any{"active": false}); err != nil {
		t.Fatal(err)
	}
	ret := p.doc("recipe_return", map[string]any{"sale": sale, "outlet": m.outB, "return_date": "2026-10-12", "items": []any{
		map[string]any{"item": m.nasi, "qty": 1}}})
	mv := p.moves(ret)
	if len(mv) != 1 || str(mv[0]["item"]) != m.nasi || rp(mv[0]["unit_cost"]) != 2400 {
		t.Fatalf("return without a recipe = %v, want 1 nasi at 2400 (24.000 / 10)", mv)
	}
}
