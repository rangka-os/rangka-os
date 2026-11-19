package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// setupLots is the pilot catalogue with the contract 0070 registers (kernel/testdata/stock-apps/apps/stock:
// stock_lot, stock_lot_balance, serial_no), item.tracking, stock_move.lot/serial_nos, the conventional lot and
// serial line fields on the Go-rule documents, and the declared lot documents of kernel/testdata/stock-apps/apps/lots.
func setupLots(t *testing.T, edits ...func(apps string)) *pilot {
	t.Helper()
	dir := t.TempDir()
	apps, site := filepath.Join(dir, "apps"), filepath.Join(dir, "site")
	for _, c := range [][2]string{{pilotApps, apps}, {pilotSite, site}, {"../../testdata/stock-apps/apps/lots", filepath.Join(apps, "lots")}} {
		if err := exec.Command("cp", "-r", c[0], c[1]).Run(); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"stock_lot.rk", "stock_lot_balance.rk", "serial_no.rk"} {
		if err := exec.Command("cp", filepath.Join("../../testdata/stock-apps/apps/stock/models", f), filepath.Join(apps, "stock", "models", f)).Run(); err != nil {
			t.Fatal(err)
		}
	}
	add := func(file string, lines ...string) {
		p := filepath.Join(apps, file)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		s := strings.TrimRight(string(b), "\n") + "\n  - " + strings.Join(lines, "\n  - ") + "\n"
		os.WriteFile(p, []byte(s), 0o644)
	}
	edit := func(file, old, new string) {
		p := filepath.Join(apps, file)
		b, err := os.ReadFile(p)
		if err != nil || !strings.Contains(string(b), old) {
			t.Fatalf("%s: %v (no %q)", file, err, old)
		}
		os.WriteFile(p, []byte(strings.Replace(string(b), old, new, 1)), 0o644)
	}
	add("catalogue/models/item.rk", "{name: tracking, type: select, options: [{value: none, label: {id: none}}, {value: lot, label: {id: lot}}, {value: lot_expiry, label: {id: lot_expiry}}, {value: serial, label: {id: serial}}]}")
	add("stock/models/stock_move.rk", "{name: lot, type: link, to: stock_lot, read_only: true}", "{name: serial_nos, type: text, read_only: true}")
	edit("stock/models/stock_move.rk", "{value: reversal, label: {id: reversal}}]", "{value: reversal, label: {id: reversal}}, {value: delivery, label: {id: delivery}}, {value: return_in, label: {id: return_in}}]")
	add("procurement/models/goods_receipt_item.rk", "{name: lot_no, type: data}", "{name: expiry_date, type: date}", "{name: serial_nos, type: text}")
	add("stock/models/stock_transfer_item.rk", "{name: lot, type: link, to: stock_lot}", "{name: serial_nos, type: text}")
	add("stock/models/stock_transfer_receipt_item.rk", "{name: serial_nos, type: text}")
	add("stock/models/stock_count_item.rk", "{name: lot_no, type: data}", "{name: expiry_date, type: date}")
	add("stock/models/stock_adjustment_item.rk", "{name: lot, type: link, to: stock_lot}", "{name: serial_nos, type: text}")
	edit(filepath.Join("..", "site", "config.rk"), "outlet_sales]", "outlet_sales, lots]")
	for _, e := range edits {
		e(apps)
	}
	return setupPilotAppsSite(t, apps, site)
}

// lotRow is one stock_lot_balance row as "lot_no=qty" (lot_no "-" for the no-lot bucket).
func (p *pilot) lotRows(outlet, item string) string {
	p.t.Helper()
	res, err := p.e.DB.Query(p.ctx, `select coalesce(l.lot_no, '-'), b.qty from r_stock_lot_balance b left join r_stock_lot l
		on l.org_id = b.org_id and l.name = b.lot where b.org_id = $1 and b.outlet = $2 and b.item = $3 order by 1`, p.a.Org, outlet, item)
	if err != nil {
		p.t.Fatal(err)
	}
	var out []string
	for _, r := range res.Rows {
		out = append(out, fmt.Sprintf("%v=%v", r[0], fmtQty(qtyOf(r[1]))))
	}
	return strings.Join(out, " ")
}

func (p *pilot) wantLots(outlet, item, want string) {
	p.t.Helper()
	if got := p.lotRows(outlet, item); got != want {
		p.t.Fatalf("lots %s/%s = %q, want %q", outlet, item, got, want)
	}
}

// moveLots are a voucher's moves as "lot_no:qty:value" in posting order.
func (p *pilot) moveLots(voucher string) string {
	p.t.Helper()
	res, err := p.e.DB.Query(p.ctx, `select coalesce(l.lot_no, '-'), m.qty, m.value from r_stock_move m left join r_stock_lot l
		on l.org_id = m.org_id and l.name = m.lot where m.org_id = $1 and m.voucher = $2 order by m.name`, p.a.Org, voucher)
	if err != nil {
		p.t.Fatal(err)
	}
	var out []string
	for _, r := range res.Rows {
		out = append(out, fmt.Sprintf("%v:%v:%d", r[0], fmtQty(qtyOf(r[1])), rp(r[2])))
	}
	return strings.Join(out, " ")
}

func (p *pilot) wantMoveLots(voucher, want string) {
	p.t.Helper()
	if got := p.moveLots(voucher); got != want {
		p.t.Fatalf("moves of %s = %q, want %q", voucher, got, want)
	}
}

// serials are an item's serial_no rows as "serial:state@outlet", sorted.
func (p *pilot) serials(item string) string {
	p.t.Helper()
	got, err := store.Find(p.ctx, p.e.DB, p.e.Reg(), p.a.Org, p.e.Reg().Model("serial_no"), map[string]any{"item": item}, store.FindOpts{})
	if err != nil {
		p.t.Fatal(err)
	}
	var out []string
	for _, r := range got {
		out = append(out, fmt.Sprintf("%s:%s@%s", r["serial"], r["state"], r["outlet"]))
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func (p *pilot) wantSerials(item, want string) {
	p.t.Helper()
	if got := p.serials(item); got != want {
		p.t.Fatalf("serials of %s = %q, want %q", item, got, want)
	}
}

func (p *pilot) docErr(model string, in map[string]any) error {
	p.t.Helper()
	return p.submitErr(model, p.create(model, in))
}

// submitNamed submits a draft and returns its submitted name and the error.
func (p *pilot) submitNamed(model, name string) (string, error) {
	r, err := p.e.Submit(p.ctx, p.a, model, name, true)
	if err != nil {
		return name, err
	}
	return str(r.Record["name"]), nil
}

func (p *pilot) lotName(item, no string) string {
	p.t.Helper()
	got, err := store.Find(p.ctx, p.e.DB, p.e.Reg(), p.a.Org, p.e.Reg().Model("stock_lot"), map[string]any{"item": item, "lot_no": no}, store.FindOpts{})
	if err != nil || len(got) != 1 {
		p.t.Fatalf("lot %s/%s: %v %v", item, no, got, err)
	}
	return str(got[0]["name"])
}

type lotMasters struct {
	masters
	susu, twin, gula, hp string
}

func (p *pilot) lotMasters() lotMasters {
	m := lotMasters{masters: p.masters()}
	m.susu = p.create("item", map[string]any{"item_name": "Susu UHT", "kind": "good", "stock_uom": m.kg, "tracking": "lot_expiry"})
	m.twin = p.create("item", map[string]any{"item_name": "Susu UHT (tanpa lot)", "kind": "good", "stock_uom": m.kg})
	m.gula = p.create("item", map[string]any{"item_name": "Gula", "kind": "good", "stock_uom": m.kg, "tracking": "lot", "standard_cost": 9000})
	m.hp = p.create("item", map[string]any{"item_name": "Ponsel", "kind": "good", "stock_uom": m.porsi, "tracking": "serial"})
	return m
}

func lotLine(item string, qty float64, rate int64, lot, expiry string) map[string]any {
	l := map[string]any{"item": item, "qty": qty, "rate": rate, "lot_no": lot}
	if expiry != "" {
		l["expiry_date"] = expiry
	}
	return l
}

// TestLotsFEFOValueSplit (scenario 1): a delivery with no lot takes the earliest-expiring lot first; the parts are
// valued at the outlet average split by qty, adding up to the rupiah to what an untracked twin item posts with the
// same receipts; the balances match too.
func TestLotsFEFOValueSplit(t *testing.T) {
	p := setupLots(t)
	m := p.lotMasters()
	p.doc("lot_receipt", map[string]any{"outlet": m.outA, "received_on": "2026-11-01", "items": []any{
		lotLine(m.susu, 100, 10000, "L1", "2027-01-31"), lotLine(m.susu, 100, 10333, "L2", "2026-12-31"),
		lotLine(m.twin, 100, 10000, "", ""), lotLine(m.twin, 100, 10333, "", "")}})
	p.wantLots(m.outA, m.susu, "L1=100 L2=100")
	p.wantLots(m.outA, m.twin, "") // untracked: no lot rows
	d := p.doc("lot_delivery", map[string]any{"outlet": m.outA, "delivery_date": "2026-11-20", "items": []any{
		map[string]any{"item": m.susu, "qty": 150}, map[string]any{"item": m.twin, "qty": 150}}})
	// avg = round(2033300 / 200) = 10167; 150 × 10167 = 1525050 split 100 (L2) + 50 (L1)
	p.wantMoveLots(d, "L2:-100:-1016700 L1:-50:-508350 -:-150:-1525050")
	a, b := p.balance(m.outA, m.susu), p.balance(m.outA, m.twin)
	if qtyOf(a["qty"]) != qtyOf(b["qty"]) || rp(a["value"]) != rp(b["value"]) || rp(a["avg_cost"]) != rp(b["avg_cost"]) {
		t.Fatalf("lot balance %v/%v/%v differs from untracked %v/%v/%v", a["qty"], a["value"], a["avg_cost"], b["qty"], b["value"], b["avg_cost"])
	}
	r := p.get("lot_delivery", d)
	for _, l := range rows(r["items"]) {
		if rp(l["cogs"]) != 1525050 {
			t.Fatalf("cogs %v", l["cogs"])
		}
	}
	p.wantLots(m.outA, m.susu, "L1=50 L2=0")
}

// TestLotsExpiredModes (scenario 2 and the edge cases): skip leaves expired lots, first takes them first, refuse
// answers 409 when the line needs one, allow keeps FEFO order, an explicit pick wins over skip; lot_required,
// expiry_required and lot_expiry_mismatch on receipts.
func TestLotsExpiredModes(t *testing.T) {
	p := setupLots(t)
	m := p.lotMasters()
	p.doc("lot_receipt", map[string]any{"outlet": m.outA, "received_on": "2026-11-01", "items": []any{
		lotLine(m.susu, 100, 10000, "L1", "2027-01-31"), lotLine(m.susu, 20, 10000, "L2", "2026-12-31")}})
	deliver := func(mode string, qty float64, lot string) (string, error) {
		line := map[string]any{"item": m.susu, "qty": qty}
		if lot != "" {
			line["lot"] = lot
		}
		return p.submitNamed("lot_delivery", p.create("lot_delivery", map[string]any{"outlet": m.outA, "delivery_date": "2027-01-01", "mode": mode, "items": []any{line}}))
	}
	d, err := deliver("skip", 10, "")
	if err != nil {
		t.Fatal(err)
	}
	p.wantMoveLots(d, "L1:-10:-100000")
	d, _ = deliver("first", 5, "")
	p.wantMoveLots(d, "L2:-5:-50000")
	d, _ = deliver("allow", 20, "")
	p.wantMoveLots(d, "L2:-15:-150000 L1:-5:-50000")
	// an explicit pick of an expired lot: skip does not stop it; L2 is empty, so the unit comes out of the bucket
	d, _ = deliver("skip", 1, p.lotName(m.susu, "L2"))
	p.wantMoveLots(d, "-:-1:-10000")
	p.wantLots(m.outA, m.susu, "-=-1 L1=85 L2=0")
	p.doc("lot_receipt", map[string]any{"outlet": m.outA, "received_on": "2026-11-01", "items": []any{lotLine(m.susu, 10, 10000, "L2", "")}})
	_, err = deliver("refuse", 90, "")
	wantCode(t, err, 409, "lot_expired")
	_, err = deliver("refuse", 1, p.lotName(m.susu, "L2"))
	wantCode(t, err, 409, "lot_expired")
	if _, err = deliver("refuse", 85, ""); err != nil { // the fresh lot covers it
		t.Fatal(err)
	}
	p.wantLots(m.outA, m.susu, "-=-1 L1=0 L2=10")

	rec := func(l map[string]any) error {
		return p.docErr("lot_receipt", map[string]any{"outlet": m.outA, "received_on": "2026-11-02", "items": []any{l}})
	}
	wantCode(t, rec(lotLine(m.susu, 1, 1, "", "2027-05-01")), 409, "lot_required")
	wantCode(t, rec(lotLine(m.susu, 1, 1, "L9", "")), 409, "expiry_required")
	wantCode(t, rec(lotLine(m.susu, 1, 1, "L1", "2027-02-01")), 409, "lot_expiry_mismatch")
	if err := rec(lotLine(m.gula, 5, 9000, "", "")); err != nil { // tracking lot: no number goes to the bucket
		t.Fatal(err)
	}
	p.wantLots(m.outA, m.gula, "-=5")
}

// TestLotsNoLotBucket (scenario 3 and tracking switched on later): what the lots do not cover comes out of the
// no-lot bucket, which goes negative with the item, flagged, never refused; stock from before tracking sits in the
// bucket and FEFO uses the real lots first.
func TestLotsNoLotBucket(t *testing.T) {
	p := setupLots(t)
	m := p.lotMasters()
	p.doc("lot_receipt", map[string]any{"outlet": m.outA, "received_on": "2026-11-01", "items": []any{
		lotLine(m.susu, 100, 10000, "L1", "2027-01-31"), lotLine(m.susu, 100, 10000, "L2", "2026-12-31")}})
	d := p.doc("lot_delivery", map[string]any{"outlet": m.outA, "delivery_date": "2026-11-20", "items": []any{map[string]any{"item": m.susu, "qty": 300}}})
	p.wantMoveLots(d, "L2:-100:-1000000 L1:-100:-1000000 -:-100:-1000000")
	mv := p.moves(d)
	if mv[2]["negative"] != true || mv[1]["negative"] == true {
		t.Fatalf("negative flags %v %v", mv[1]["negative"], mv[2]["negative"])
	}
	p.wantLots(m.outA, m.susu, "-=-100 L1=0 L2=0")
	p.wantBalance(m.outA, m.susu, -100, -1000000, 10000)

	// an item with stock before tracking: switching it on puts that stock in the bucket on the first lot move
	p.doc("goods_receipt", map[string]any{"vendor": m.vend, "outlet": m.outB, "items": []any{map[string]any{"item": m.twin, "qty": 30, "rate": 5000}}})
	if _, err := p.e.Update(p.ctx, p.a, "item", m.twin, map[string]any{"tracking": "lot"}); err != nil {
		t.Fatal(err)
	}
	p.doc("lot_receipt", map[string]any{"outlet": m.outB, "received_on": "2026-11-02", "items": []any{lotLine(m.twin, 10, 5000, "T1", "")}})
	p.wantLots(m.outB, m.twin, "-=30 T1=10")
	d = p.doc("lot_delivery", map[string]any{"outlet": m.outB, "delivery_date": "2026-11-20", "items": []any{map[string]any{"item": m.twin, "qty": 15}}})
	p.wantMoveLots(d, "T1:-10:-50000 -:-5:-25000")
	p.wantLots(m.outB, m.twin, "-=25 T1=0")
}

// TestLotsTransferFollowsLots (scenario 4): a two-step transfer takes lots by FEFO; a partial receipt brings in
// the earliest-expiring sent lot first, the last receipt exactly the rest; cancelling a receipt puts its lots back
// on the way. A one-step transfer brings in the lots that left.
func TestLotsTransferFollowsLots(t *testing.T) {
	p := setupLots(t)
	m := p.lotMasters()
	p.doc("lot_receipt", map[string]any{"outlet": m.outA, "received_on": "2026-11-01", "items": []any{
		lotLine(m.susu, 100, 10000, "L1", "2027-01-31"), lotLine(m.susu, 50, 12000, "L2", "2026-12-31")}})
	trf := p.doc("stock_transfer", map[string]any{"from_outlet": m.outA, "to_outlet": m.outB, "transfer_date": "2026-11-05",
		"items": []any{map[string]any{"item": m.susu, "qty": 60}}})
	// avg = round(1600000 / 150) = 10667
	p.wantMoveLots(trf, "L2:-50:-533350 L1:-10:-106670")
	r1 := p.submit("stock_transfer_receipt", p.trReceipt(trf, m.outB, m.susu, 40))
	p.wantMoveLots(r1, "L2:40:426680")
	p.wantLots(m.outB, m.susu, "L2=40")
	r2 := p.submit("stock_transfer_receipt", p.trReceipt(trf, m.outB, m.susu, 20))
	p.wantMoveLots(r2, "L2:10:106670 L1:10:106670")
	p.wantLots(m.outB, m.susu, "L1=10 L2=50")
	p.wantLots(m.outA, m.susu, "L1=90 L2=0")
	p.wantBalance(m.outB, m.susu, 60, 640020, 10667)
	if err := p.cancel("stock_transfer_receipt", r1); err != nil {
		t.Fatal(err)
	}
	p.wantLots(m.outB, m.susu, "L1=10 L2=10")
	r3 := p.submit("stock_transfer_receipt", p.trReceipt(trf, m.outB, m.susu, 40))
	p.wantMoveLots(r3, "L2:40:426680")
	p.wantLots(m.outB, m.susu, "L1=10 L2=50")

	// one step: no receipt model
	delete(p.e.Reg().Models, "stock_transfer_receipt")
	one := p.doc("stock_transfer", map[string]any{"from_outlet": m.outA, "to_outlet": m.outC, "transfer_date": "2026-11-06",
		"items": []any{map[string]any{"item": m.susu, "qty": 30, "lot": p.lotName(m.susu, "L1")}}})
	p.wantMoveLots(one, "L1:-30:-319980 L1:30:319980")
	p.wantLots(m.outC, m.susu, "L1=30")
	if err := p.cancel("stock_transfer", one); err != nil {
		t.Fatal(err)
	}
	p.wantLots(m.outC, m.susu, "L1=0")
	p.wantLots(m.outA, m.susu, "L1=90 L2=0")
}

// TestLotsCountPerLot (scenario 5 and the upgrade path): a count line names a lot; system_qty is that lot and the
// difference posts into it only; a lot not seen before is created; a line with no lot number counts the bucket.
func TestLotsCountPerLot(t *testing.T) {
	p := setupLots(t)
	m := p.lotMasters()
	p.doc("lot_receipt", map[string]any{"outlet": m.outA, "received_on": "2026-11-01", "items": []any{
		lotLine(m.susu, 50, 10000, "L1", "2027-01-31"), lotLine(m.susu, 30, 10000, "L2", "2026-12-31")}})
	c := p.doc("stock_count", map[string]any{"outlet": m.outA, "count_date": "2026-11-10", "items": []any{
		map[string]any{"item": m.susu, "counted_qty": 45, "lot_no": "L1"}}})
	p.wantMoveLots(c, "L1:-5:-50000")
	if l := rows(p.get("stock_count", c)["items"])[0]; qtyOf(l["system_qty"]) != 50 || qtyOf(l["difference"]) != -5 {
		t.Fatalf("count line %v", l)
	}
	p.wantLots(m.outA, m.susu, "L1=45 L2=30")

	// upgrade: 40 kg of gula from before tracking sits in the bucket; a full lot count moves it into lots
	p.doc("goods_receipt", map[string]any{"vendor": m.vend, "outlet": m.outB, "items": []any{map[string]any{"item": m.gula, "qty": 40, "rate": 9000}}})
	p.wantLots(m.outB, m.gula, "-=40")
	c = p.doc("stock_count", map[string]any{"outlet": m.outB, "count_date": "2026-11-10", "items": []any{
		map[string]any{"item": m.gula, "counted_qty": 25, "lot_no": "G1"}, map[string]any{"item": m.gula, "counted_qty": 15, "lot_no": "G2"},
		map[string]any{"item": m.gula, "counted_qty": 0, "lot_no": ""}}})
	p.wantMoveLots(c, "G1:25:225000 G2:15:135000 -:-40:-360000")
	p.wantLots(m.outB, m.gula, "-=0 G1=25 G2=15")
	p.wantBalance(m.outB, m.gula, 40, 360000, 9000)
	if err := p.cancel("stock_count", c); err != nil {
		t.Fatal(err)
	}
	p.wantLots(m.outB, m.gula, "-=40 G1=0 G2=0")
}

// TestSerials (scenario 7): receipts create serials in stock; a delivery names them and sells them; a serial not
// here, one already in stock, a count that does not match and a duplicate are refused; cancelling the delivery puts
// them back; a receipt whose serial has moved on cannot be cancelled; a two-step transfer carries serials.
func TestSerials(t *testing.T) {
	p := setupLots(t)
	m := p.lotMasters()
	sl := func(qty float64, serials string) map[string]any {
		return map[string]any{"item": m.hp, "qty": qty, "rate": 2000000, "serial_nos": serials}
	}
	r := p.doc("lot_receipt", map[string]any{"outlet": m.outA, "received_on": "2026-11-01", "items": []any{sl(3, "S1\nS2\nS3")}})
	p.wantSerials(m.hp, "S1:in_stock@"+m.outA+" S2:in_stock@"+m.outA+" S3:in_stock@"+m.outA)
	out := func(qty float64, serials string) (string, error) {
		return p.submitNamed("lot_delivery", p.create("lot_delivery", map[string]any{"outlet": m.outA, "delivery_date": "2026-11-05", "items": []any{map[string]any{"item": m.hp, "qty": qty, "serial_nos": serials}}}))
	}
	d, err := out(2, "S1, S2")
	if err != nil {
		t.Fatal(err)
	}
	p.wantSerials(m.hp, "S1:sold@"+m.outA+" S2:sold@"+m.outA+" S3:in_stock@"+m.outA)
	if sn := p.moves(d)[0]["serial_nos"]; sn != "S1\nS2" {
		t.Fatalf("move serial_nos %q", sn)
	}
	_, err = out(1, "S1")
	wantCode(t, err, 409, "serial_not_here")
	wantCode(t, p.docErr("lot_receipt", map[string]any{"outlet": m.outB, "received_on": "2026-11-06", "items": []any{sl(1, "S3")}}), 409, "serial_in_stock")
	_, err = out(2, "S3")
	wantCode(t, err, 409, "serial_count_mismatch")
	_, err = out(1.5, "S3\nS3")
	wantCode(t, err, 409, "serial_duplicate")
	wantCode(t, p.cancel("lot_receipt", r), 409, "serial_moved")
	if err := p.cancel("lot_delivery", d); err != nil {
		t.Fatal(err)
	}
	p.wantSerials(m.hp, "S1:in_stock@"+m.outA+" S2:in_stock@"+m.outA+" S3:in_stock@"+m.outA)
	p.wantBalance(m.outA, m.hp, 3, 6000000, 2000000)

	// two-step transfer: S1 and S2 go out in transit; a partial receipt names S2, the last takes the rest
	trf := p.doc("stock_transfer", map[string]any{"from_outlet": m.outA, "to_outlet": m.outB, "transfer_date": "2026-11-07",
		"items": []any{map[string]any{"item": m.hp, "qty": 2, "serial_nos": "S1\nS2"}}})
	p.wantSerials(m.hp, "S1:in_transit@"+m.outA+" S2:in_transit@"+m.outA+" S3:in_stock@"+m.outA)
	wantCode(t, p.submitErr("stock_transfer_receipt", p.trReceipt(trf, m.outB, m.hp, 1)), 409, "serial_count_mismatch")
	rc := p.create("stock_transfer_receipt", map[string]any{"stock_transfer": trf, "outlet": m.outB, "items": []any{map[string]any{"item": m.hp, "qty": 1, "serial_nos": "S2"}}})
	p.submit("stock_transfer_receipt", rc)
	p.wantSerials(m.hp, "S1:in_transit@"+m.outA+" S2:in_stock@"+m.outB+" S3:in_stock@"+m.outA)
	r2 := p.submit("stock_transfer_receipt", p.trReceipt(trf, m.outB, m.hp, 1))
	p.wantSerials(m.hp, "S1:in_stock@"+m.outB+" S2:in_stock@"+m.outB+" S3:in_stock@"+m.outA)
	if err := p.cancel("stock_transfer_receipt", r2); err != nil {
		t.Fatal(err)
	}
	p.wantSerials(m.hp, "S1:in_transit@"+m.outB+" S2:in_stock@"+m.outB+" S3:in_stock@"+m.outA)

	// a sold serial comes back on a return; cancelling the return makes it sold again
	d3, err := out(1, "S3")
	if err != nil {
		t.Fatal(err)
	}
	rt := p.doc("lot_return", map[string]any{"lot_delivery": d3, "outlet": m.outA, "return_date": "2026-11-08", "items": []any{map[string]any{"item": m.hp, "qty": 1, "serial_nos": "S3"}}})
	p.wantSerials(m.hp, "S1:in_transit@"+m.outB+" S2:in_stock@"+m.outB+" S3:in_stock@"+m.outA)
	if err := p.cancel("lot_return", rt); err != nil {
		t.Fatal(err)
	}
	p.wantSerials(m.hp, "S1:in_transit@"+m.outB+" S2:in_stock@"+m.outB+" S3:sold@"+m.outA)
}

// TestLotsReturnIntoLot (scenario 8): a return with no lot number goes back into the lot its delivery took when it
// took exactly one, else into the bucket; a named lot wins.
func TestLotsReturnIntoLot(t *testing.T) {
	p := setupLots(t)
	m := p.lotMasters()
	p.doc("lot_receipt", map[string]any{"outlet": m.outA, "received_on": "2026-11-01", "items": []any{
		lotLine(m.susu, 100, 10000, "L1", "2027-01-31"), lotLine(m.susu, 100, 10000, "L2", "2026-12-31")}})
	two := p.doc("lot_delivery", map[string]any{"outlet": m.outA, "delivery_date": "2026-11-20", "items": []any{map[string]any{"item": m.susu, "qty": 150}}})
	one := p.doc("lot_delivery", map[string]any{"outlet": m.outA, "delivery_date": "2026-11-21", "items": []any{map[string]any{"item": m.susu, "qty": 20}}})
	ret := func(dn, lot string) string {
		return p.doc("lot_return", map[string]any{"lot_delivery": dn, "outlet": m.outA, "return_date": "2026-11-22", "items": []any{map[string]any{"item": m.susu, "qty": 10, "lot_no": lot}}})
	}
	p.wantMoveLots(ret(two, ""), "-:10:100000")
	p.wantMoveLots(ret(two, "L1"), "L1:10:100000")
	p.wantMoveLots(ret(one, ""), "L1:10:100000")
	p.wantLots(m.outA, m.susu, "-=10 L1=50 L2=0")
}

// TestLotsReverseEverything (scenario 9): cancelling every document in reverse order restores stock_balance, the
// lot balances and the serial states exactly; the Go goods_receipt and stock_adjustment read the conventional line
// fields (an expired adjustment takes expired lots first); outlet_sales takes expired lots by FEFO.
func TestLotsReverseEverything(t *testing.T) {
	p := setupLots(t)
	m := p.lotMasters()
	gr := p.doc("goods_receipt", map[string]any{"vendor": m.vend, "outlet": m.outA, "received_on": "2026-11-01", "items": []any{
		map[string]any{"item": m.susu, "qty": 40, "rate": 10000, "lot_no": "L1", "expiry_date": "2027-03-31"},
		map[string]any{"item": m.susu, "qty": 10, "rate": 11000, "lot_no": "L2", "expiry_date": "2026-11-15"},
		map[string]any{"item": m.hp, "qty": 2, "rate": 2000000, "serial_nos": "S1\nS2"}}})
	p.wantMoveLots(gr, "L1:40:400000 L2:10:110000 -:2:4000000")
	adj := p.doc("stock_adjustment", map[string]any{"outlet": m.outA, "adjustment_date": "2026-11-20", "reason": "expired", "items": []any{
		map[string]any{"item": m.susu, "qty": 4}, map[string]any{"item": m.hp, "qty": 1, "serial_nos": "S2"}}})
	p.wantMoveLots(adj, "L2:-4:-40800 -:-1:-2000000")
	p.create("recipe", map[string]any{"product": m.nasi, "lines": []any{map[string]any{"material": m.susu, "qty": 1}}})
	sales := p.doc("outlet_sales", map[string]any{"outlet": m.outA, "date": "2026-11-21", "lines": []any{map[string]any{"item": m.nasi, "qty": 8}}})
	p.wantMoveLots(sales, "L2:-6:-61200 L1:-2:-20400")
	p.wantLots(m.outA, m.susu, "L1=38 L2=0")
	p.wantSerials(m.hp, "S1:in_stock@"+m.outA+" S2:scrapped@"+m.outA)
	for _, c := range [][2]string{{"outlet_sales", sales}, {"stock_adjustment", adj}, {"goods_receipt", gr}} {
		if err := p.cancel(c[0], c[1]); err != nil {
			t.Fatalf("cancel %s: %v", c[1], err)
		}
	}
	p.wantLots(m.outA, m.susu, "L1=0 L2=0")
	p.wantBalance(m.outA, m.susu, 0, 0, 11000)
	p.wantBalance(m.outA, m.hp, 0, 0, 2000000)
	p.wantSerials(m.hp, "S1:cancelled@"+m.outA+" S2:cancelled@"+m.outA)
}

// TestLotsDeclaredGoodsReceipt: goods_receipt may declare stock: (contract 0070 §7); the declaration posts with
// lots and the purchase order still gets its received quantities.
func TestLotsDeclaredGoodsReceipt(t *testing.T) {
	p := setupLots(t, func(apps string) {
		f := filepath.Join(apps, "procurement", "models", "goods_receipt.rk")
		b, _ := os.ReadFile(f)
		s := strings.Replace(string(b), "on_cancel: [stock.reverse_moves]\n", "on_cancel: [stock.reverse_moves]\nstock:\n  date: received_on\n  moves:\n    - {lines: items, in: outlet, reason: receipt, cost: {amount: amount}, lot: {no: lot_no, expiry: expiry_date}, serials: serial_nos}\n", 1)
		s = strings.Replace(s, "schema/0.4/model.json", "schema/0.5/model.json", 1)
		os.WriteFile(f, []byte(s), 0o644)
	})
	m := p.lotMasters()
	po := p.doc("purchase_order", map[string]any{"vendor": m.vend, "outlet": m.outA, "items": []any{map[string]any{"item": m.susu, "qty": 10, "rate": 10000}}})
	gr := p.doc("goods_receipt", map[string]any{"vendor": m.vend, "outlet": m.outA, "purchase_order": po, "received_on": "2026-11-01", "items": []any{
		map[string]any{"item": m.susu, "qty": 6, "rate": 10000, "lot_no": "P1", "expiry_date": "2027-01-01"}}})
	p.wantMoveLots(gr, "P1:6:60000")
	if l := rows(p.get("purchase_order", po)["items"])[0]; qtyOf(l["received_qty"]) != 6 {
		t.Fatalf("received_qty %v", l["received_qty"])
	}
	wantCode(t, p.docErr("goods_receipt", map[string]any{"vendor": m.vend, "outlet": m.outA, "items": []any{
		map[string]any{"item": m.susu, "qty": 1, "rate": 1}}}), 409, "lot_required")
}

// TestLotsDeviceWriteNeverRefused: a device write (contract 0058) is never refused for stock: a serial not here
// and an expired-only lot become exceptions, the sale posts by quantity, and the same body over REST is refused.
func TestLotsDeviceWriteNeverRefused(t *testing.T) {
	p := setupLots(t)
	m := p.lotMasters()
	p.doc("lot_receipt", map[string]any{"outlet": m.outA, "received_on": "2026-11-01", "items": []any{
		lotLine(m.susu, 5, 10000, "L2", "2026-12-31"),
		map[string]any{"item": m.hp, "qty": 1, "rate": 2000000, "serial_nos": "S1"}}})
	body := func() map[string]any {
		return map[string]any{"outlet": m.outA, "delivery_date": "2027-01-01", "mode": "refuse", "items": []any{
			map[string]any{"item": m.susu, "qty": 2}, map[string]any{"item": m.hp, "qty": 1, "serial_nos": "S9"}}}
	}
	a := p.a
	a.Device = &DeviceRef{ID: "dev_00000000000000000009", Code: "K1", Outlet: m.outA}
	ctx := WithDeviceWrite(p.ctx, DeviceWrite{Model: "lot_delivery", OccurredAt: FixedNow()})
	res, err := p.e.CreateAndSubmit(ctx, a, "lot_delivery", body(), true)
	if err != nil {
		t.Fatalf("device delivery: %v", err)
	}
	p.wantMoveLots(str(res.Record["name"]), "-:-2:-20000 -:-1:-2000000")
	p.wantSerials(m.hp, "S1:in_stock@"+m.outA)
	for _, kind := range []string{"stock_lot_expired", "stock_serial_not_here"} {
		xs, err := store.ListExceptions(p.ctx, p.e.DB, p.a.Org, "", kind, false)
		if err != nil || len(xs) != 1 {
			t.Fatalf("%s exceptions = %v %v", kind, xs, err)
		}
	}
	_, err = p.e.CreateAndSubmit(p.ctx, p.a, "lot_delivery", body(), true)
	wantCode(t, err, 409, "lot_expired")
}

// sameAsTwin: the lot item's balance at outlet equals the untracked twin's (qty, value, average).
func (p *pilot) sameAsTwin(m lotMasters, outlet, where string) {
	p.t.Helper()
	x, y := p.balance(outlet, m.susu), p.balance(outlet, m.twin)
	if qtyOf(x["qty"]) != qtyOf(y["qty"]) || rp(x["value"]) != rp(y["value"]) || rp(x["avg_cost"]) != rp(y["avg_cost"]) {
		p.t.Fatalf("%s: lot item %v/%v/%v, untracked twin %v/%v/%v", where, x["qty"], x["value"], x["avg_cost"], y["qty"], y["value"], y["avg_cost"])
	}
}

// TestLotsReversalKeepsAverage (audit F1): cancelling a transfer receipt split over lots, with the destination
// ending below zero, keeps the average one reversal of the line keeps, like the untracked twin; so does cancelling
// a one-step transfer.
func TestLotsReversalKeepsAverage(t *testing.T) {
	p := setupLots(t)
	m := p.lotMasters()
	p.doc("lot_receipt", map[string]any{"outlet": m.outB, "received_on": "2026-11-01", "items": []any{
		lotLine(m.susu, 10, 10000, "X", "2027-06-30"), lotLine(m.twin, 10, 10000, "", "")}})
	p.doc("lot_receipt", map[string]any{"outlet": m.outA, "received_on": "2026-11-01", "items": []any{
		lotLine(m.susu, 4, 20000, "L1", "2027-01-31"), lotLine(m.susu, 3, 20000, "L2", "2026-12-31"),
		lotLine(m.twin, 4, 20000, "", ""), lotLine(m.twin, 3, 20000, "", "")}})
	trf := p.doc("stock_transfer", map[string]any{"from_outlet": m.outA, "to_outlet": m.outB, "transfer_date": "2026-11-05",
		"items": []any{map[string]any{"item": m.susu, "qty": 7}, map[string]any{"item": m.twin, "qty": 7}}})
	r := p.submit("stock_transfer_receipt", p.create("stock_transfer_receipt", map[string]any{"stock_transfer": trf, "outlet": m.outB, "items": []any{
		map[string]any{"item": m.susu, "qty": 7}, map[string]any{"item": m.twin, "qty": 7}}}))
	p.wantMoveLots(r, "L2:3:60000 L1:4:80000 -:7:140000")
	p.doc("lot_delivery", map[string]any{"outlet": m.outB, "delivery_date": "2026-11-20", "mode": "allow", "items": []any{
		map[string]any{"item": m.susu, "qty": 12}, map[string]any{"item": m.twin, "qty": 12}}})
	p.sameAsTwin(m, m.outB, "after sales at B")
	if err := p.cancel("stock_transfer_receipt", r); err != nil {
		t.Fatal(err)
	}
	p.sameAsTwin(m, m.outB, "after receipt cancel")

	delete(p.e.Reg().Models, "stock_transfer_receipt")
	one := p.doc("stock_transfer", map[string]any{"from_outlet": m.outA, "to_outlet": m.outC, "transfer_date": "2026-11-06",
		"items": []any{map[string]any{"item": m.susu, "qty": 7}, map[string]any{"item": m.twin, "qty": 7}}})
	p.doc("lot_delivery", map[string]any{"outlet": m.outC, "delivery_date": "2026-11-20", "items": []any{
		map[string]any{"item": m.susu, "qty": 9}, map[string]any{"item": m.twin, "qty": 9}}})
	if err := p.cancel("stock_transfer", one); err != nil {
		t.Fatal(err)
	}
	p.sameAsTwin(m, m.outC, "after one-step cancel")
	p.sameAsTwin(m, m.outA, "origin after one-step cancel")
}

// TestSerialHistory (audit F2, F6): the document that moved a serial last is the only one that can be cancelled for
// it. Sold, returned and sold again: cancelling the first delivery is refused; cancelling in reverse order walks the
// serial back to the receipt, which then cancels it. A serial one transfer sent and a later transfer carries now is
// not the earlier transfer's to receive.
func TestSerialHistory(t *testing.T) {
	p := setupLots(t)
	m := p.lotMasters()
	gr := p.doc("lot_receipt", map[string]any{"outlet": m.outA, "received_on": "2026-11-01", "items": []any{
		map[string]any{"item": m.hp, "qty": 1, "rate": 2000000, "serial_nos": "S1"}}})
	sell := func() string {
		return p.doc("lot_delivery", map[string]any{"outlet": m.outA, "delivery_date": "2026-11-05", "items": []any{map[string]any{"item": m.hp, "qty": 1, "serial_nos": "S1"}}})
	}
	d1 := sell()
	ret := p.doc("lot_return", map[string]any{"lot_delivery": d1, "outlet": m.outA, "return_date": "2026-11-06", "items": []any{map[string]any{"item": m.hp, "qty": 1, "serial_nos": "S1"}}})
	d2 := sell()
	wantCode(t, p.cancel("lot_delivery", d1), 409, "serial_moved")
	wantCode(t, p.cancel("lot_return", ret), 409, "serial_moved")
	wantCode(t, p.cancel("lot_receipt", gr), 409, "serial_moved")
	for _, c := range [][2]string{{"lot_delivery", d2}, {"lot_return", ret}} {
		if err := p.cancel(c[0], c[1]); err != nil {
			t.Fatalf("cancel %s: %v", c[1], err)
		}
	}
	p.wantSerials(m.hp, "S1:sold@"+m.outA)
	if err := p.cancel("lot_delivery", d1); err != nil {
		t.Fatal(err)
	}
	p.wantSerials(m.hp, "S1:in_stock@"+m.outA)
	if err := p.cancel("lot_receipt", gr); err != nil {
		t.Fatal(err)
	}
	p.wantSerials(m.hp, "S1:cancelled@"+m.outA)
	p.wantBalance(m.outA, m.hp, 0, 0, 2000000)

	// transfers: T1 sends S2, a receipt brings it in, T2 sends it on; cancelling T1's receipt is refused, and a new
	// receipt of T1 cannot take S2 out of T2's transit
	p.doc("lot_receipt", map[string]any{"outlet": m.outA, "received_on": "2026-11-01", "items": []any{
		map[string]any{"item": m.hp, "qty": 1, "rate": 2000000, "serial_nos": "S2"}}})
	t1 := p.doc("stock_transfer", map[string]any{"from_outlet": m.outA, "to_outlet": m.outB, "transfer_date": "2026-11-07",
		"items": []any{map[string]any{"item": m.hp, "qty": 1, "serial_nos": "S2"}}})
	r1 := p.submit("stock_transfer_receipt", p.trReceipt(t1, m.outB, m.hp, 1))
	p.doc("stock_transfer", map[string]any{"from_outlet": m.outB, "to_outlet": m.outC, "transfer_date": "2026-11-08",
		"items": []any{map[string]any{"item": m.hp, "qty": 1, "serial_nos": "S2"}}})
	wantCode(t, p.cancel("stock_transfer_receipt", r1), 409, "serial_moved")
	p.wantSerials(m.hp, "S1:cancelled@"+m.outA+" S2:in_transit@"+m.outB)
}

// TestLotsPartRounding (audit F3): parts of a FEFO split never flip sign when the average is below a rupiah per
// part; the parts still add up to the line's value.
func TestLotsPartRounding(t *testing.T) {
	p := setupLots(t)
	m := p.lotMasters()
	var lines []any
	for i, no := range []string{"A", "B", "C", "D"} {
		rate := int64(4)
		if i >= 2 {
			rate = 0
		}
		lines = append(lines, lotLine(m.susu, 0.25, rate, no, fmt.Sprintf("2027-0%d-01", i+1)))
	}
	p.doc("lot_receipt", map[string]any{"outlet": m.outA, "received_on": "2026-11-01", "items": lines})
	d := p.doc("lot_delivery", map[string]any{"outlet": m.outA, "delivery_date": "2026-11-20", "items": []any{map[string]any{"item": m.susu, "qty": 1}}})
	var sum int64
	for _, mv := range p.moves(d) {
		if rp(mv["value"]) > 0 {
			t.Fatalf("out part with a positive value: %s", p.moveLots(d))
		}
		sum += rp(mv["value"])
	}
	if sum != -2 {
		t.Fatalf("parts add up to %d, want -2: %s", sum, p.moveLots(d))
	}
}
