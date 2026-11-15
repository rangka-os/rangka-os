package engine

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// Replay rules of device writes (contract 0058 §10 and its conflict rules) over testdata/device-apps: penjualan has
// the rules shift_open (save) and sale_today (submit, date == today()) and the approval big_sale (grand_total above
// 1.000.000); tutup_sesi (not naming_on: device) has counted_not_negative.

// paid creates and submits a sale of qty × menu in one device write by a at at; body adds to the sale's fields.
func (d *deviceEnv) paid(a Actor, shift, name string, at time.Time, menu string, qty int, body map[string]any) (*Result, error) {
	d.t.Helper()
	in := map[string]any{"shift": shift, "outlet": a.Device.Outlet, "items": []any{map[string]any{"menu": menu, "qty": qty}}}
	for k, v := range body {
		in[k] = v
	}
	ctx := WithDeviceWrite(d.ctx, DeviceWrite{Model: "penjualan", Name: name, OccurredAt: at, Money: d.e.DeviceMoney("penjualan", in)})
	return d.e.CreateAndSubmit(ctx, a, "penjualan", in, true)
}

// exceptions are the org's exceptions of kind.
func (d *deviceEnv) exceptions(kind string) []store.ExceptionRow {
	d.t.Helper()
	xs, err := store.ListExceptions(d.ctx, d.e.DB, "toko", "", kind, false)
	if err != nil {
		d.t.Fatal(err)
	}
	return xs
}

// closeShift closes shift from head office (no device): tutup_sesi's flow sets the shift's state to closed.
func (d *deviceEnv) closeShift(shift string) {
	d.t.Helper()
	if _, err := d.e.CreateAndSubmit(d.ctx, d.owner, "tutup_sesi", map[string]any{"shift": shift, "counted_cash": 100000}, true); err != nil {
		d.t.Fatalf("close %s: %v", shift, err)
	}
	r, err := d.e.Get(d.ctx, d.owner, "sesi_kasir", shift)
	if err != nil || str(r["state"]) != "closed" {
		d.t.Fatalf("shift %s after close: %v %v", shift, r["state"], err)
	}
}

func (d *deviceEnv) menuAt(price int) string {
	d.t.Helper()
	r, err := d.e.Create(d.ctx, d.owner, "menu", map[string]any{"menu_name": fmt.Sprintf("Menu %d", price), "price": price})
	if err != nil {
		d.t.Fatal(err)
	}
	return str(r.Record["name"])
}

func wantMoney(t *testing.T, v any, want int64) {
	t.Helper()
	if got, ok := exact(v); !ok || got.Cmp(big.NewRat(want, 1)) != 0 {
		t.Fatalf("money = %v (%T), want %d", v, v, want)
	}
}

// today() and now() of a device write are its occurred_at: a sale paid at 23:50 the day before is that day's sale.
func TestDeviceWriteClockAtOccurredAt(t *testing.T) {
	d := setupDeviceApps(t)
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	sh := d.shift(k1, "SESI-OUT01-K1-2610-001")
	loc, _ := time.LoadLocation("Asia/Jakarta")
	late := time.Date(2026, 10, 29, 23, 50, 0, 0, loc) // the kernel's clock is 30 Oct 10.00
	res, err := d.paid(k1, sh, "STRK-OUT01-K1-2610-00001", late, d.menu, 1, map[string]any{"date": "2026-10-29"})
	if err != nil {
		t.Fatal(err)
	}
	if got := str(res.Record["date"]); got != "2026-10-29" {
		t.Fatalf("date = %q", got)
	}
	// the default date() is the occurred_at day too
	res, err = d.paid(k1, sh, "STRK-OUT01-K1-2610-00002", late, d.menu, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := str(res.Record["date"]); got != "2026-10-29" {
		t.Fatalf("default date = %q", got)
	}
	if xs := d.exceptions("rule_overridden"); len(xs) != 0 {
		t.Fatalf("rule overridden: %v", xs)
	}
}

// The device's computed money is stored; the kernel's own value goes into an edge_price_drift exception.
func TestDeviceWriteMoneyKept(t *testing.T) {
	d := setupDeviceApps(t)
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	sh := d.shift(k1, "SESI-OUT01-K1-2610-001")
	menu := d.menuAt(23000) // 2 × 23.000 = 46.000 at the kernel
	res, err := d.paid(k1, sh, "STRK-OUT01-K1-2610-00001", d.octNow, menu, 2, map[string]any{"grand_total": 45000})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := d.e.Get(d.ctx, d.owner, "penjualan", str(res.Record["name"]))
	if err != nil {
		t.Fatal(err)
	}
	wantMoney(t, stored["grand_total"], 45000)
	wantMoney(t, stored["cash_received"], 45000) // computes that read a device value see it
	xs := d.exceptions("edge_price_drift")
	if len(xs) != 1 {
		t.Fatalf("drift exceptions = %v", xs)
	}
	x := xs[0]
	if x.Ref != "penjualan/STRK-OUT01-K1-2610-00001" || x.Device != k1.Device.ID || x.Outlet != "OUT-01" ||
		fmt.Sprint(x.Detail["field"]) != "grand_total" || fmt.Sprint(x.Detail["device"]) != "45000" || fmt.Sprint(x.Detail["kernel"]) != "46000" {
		t.Fatalf("drift = %+v", x)
	}
}

func TestDeviceWriteMoneySame(t *testing.T) {
	d := setupDeviceApps(t)
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	sh := d.shift(k1, "SESI-OUT01-K1-2610-001")
	menu := d.menuAt(23000)
	body := map[string]any{"grand_total": 46000, "items": []any{map[string]any{"menu": menu, "qty": 2, "rate": 23000, "amount": 46000}}}
	if _, err := d.paid(k1, sh, "STRK-OUT01-K1-2610-00001", d.octNow, menu, 2, body); err != nil {
		t.Fatal(err)
	}
	if xs := d.exceptions("edge_price_drift"); len(xs) != 0 {
		t.Fatalf("drift = %v", xs)
	}
	// a row's computed money is the device's too
	body = map[string]any{"items": []any{map[string]any{"menu": menu, "qty": 2, "amount": 40000}}}
	res, err := d.paid(k1, sh, "STRK-OUT01-K1-2610-00002", d.octNow, menu, 2, body)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := d.e.Get(d.ctx, d.owner, "penjualan", str(res.Record["name"]))
	wantMoney(t, rows(stored["items"])[0]["amount"], 40000)
	wantMoney(t, stored["grand_total"], 40000)
	xs := d.exceptions("edge_price_drift")
	if len(xs) != 1 || fmt.Sprint(xs[0].Detail["field"]) != "items.0.amount" {
		t.Fatalf("row drift = %v", xs)
	}
}

// A sale on a shift head office closed meanwhile is accepted (it was paid at the outlet); the rule is an exception.
func TestDeviceWriteRuleOverridden(t *testing.T) {
	d := setupDeviceApps(t)
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	sh := d.shift(k1, "SESI-OUT01-K1-2610-001")
	d.closeShift(sh)
	res, err := d.paid(k1, sh, "STRK-OUT01-K1-2610-00001", d.octNow, d.menu, 1, nil)
	if err != nil {
		t.Fatalf("sale on a closed shift: %v", err)
	}
	if toInt(res.Record["docstatus"]) != 1 {
		t.Fatalf("docstatus = %v", res.Record["docstatus"])
	}
	xs := d.exceptions("rule_overridden")
	if len(xs) != 1 { // save and submit both check shift_open: one exception
		t.Fatalf("exceptions = %v", xs)
	}
	if xs[0].Ref != "penjualan/STRK-OUT01-K1-2610-00001" || xs[0].Detail["rule"] != "shift_open" || xs[0].Detail["model"] != "penjualan" ||
		xs[0].Detail["message"] != "Sesi kasir sudah ditutup" {
		t.Fatalf("exception = %+v", xs[0])
	}
}

// Permissions still refuse a device write, and so does a rule of a model that is not named on the device.
func TestDeviceWritePermissionStillRejects(t *testing.T) {
	d := setupDeviceApps(t)
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	sh := d.shift(k1, "SESI-OUT01-K1-2610-001")
	d.closeShift(sh)
	cook := Actor{Org: "toko", ID: "dapur@toko.test", Roles: []string{"dapur"}, Human: true, Outlets: []string{"OUT-01"},
		Device: k1.Device}
	_, err := d.paid(cook, sh, "STRK-OUT01-K1-2610-00001", d.octNow, d.menu, 1, nil)
	wantPermission(t, err, "penjualan", "create")
	sh2 := d.shift(k1, "SESI-OUT01-K1-2610-002")
	ctx := WithDeviceWrite(d.ctx, DeviceWrite{Model: "tutup_sesi", OccurredAt: d.octNow})
	_, err = d.e.CreateAndSubmit(ctx, k1, "tutup_sesi", map[string]any{"shift": sh2, "counted_cash": -1}, true)
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Errors) == 0 || ve.Errors[0].Rule != "counted_not_negative" {
		t.Fatalf("rule of tutup_sesi: %v", err)
	}
	if xs := d.exceptions(""); len(xs) != 0 {
		t.Fatalf("exceptions = %v", xs)
	}
}

// A device write that would wait for an approval is refused before anything is saved; REST still holds it.
func TestDeviceWriteApprovalRejected(t *testing.T) {
	d := setupDeviceApps(t)
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	sh := d.shift(k1, "SESI-OUT01-K1-2610-001")
	_, err := d.paid(k1, sh, "STRK-OUT01-K1-2610-00001", d.octNow, d.menu, 60, nil) // 1.080.000
	wantStatus(t, err, 422, "approval_needed_offline")
	n, err := d.e.DB.Query(d.ctx, `select (select count(*) from r_penjualan), (select count(*) from rk_approvals)`)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(n.Rows[0]) != "[0 0]" {
		t.Fatalf("saved after the refusal: %v", n.Rows[0])
	}
	// REST (no device write) holds it as before
	kasir := Actor{Org: "toko", ID: "kasir@toko.test", Roles: []string{"kasir"}, Human: true, Outlets: []string{"OUT-01"}}
	res, err := d.e.CreateAndSubmit(d.ctx, kasir, "penjualan", map[string]any{"shift": sh, "outlet": "OUT-01",
		"items": []any{map[string]any{"menu": d.menu, "qty": 60}}}, true)
	if err != nil || !res.Held {
		t.Fatalf("REST: held = %v, %v", res != nil && res.Held, err)
	}
}

// A sale cannot link a shift another device opened (contract 0058 §6).
func TestDeviceWriteShiftOtherDevice(t *testing.T) {
	d := setupDeviceApps(t)
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	k2 := d.cashier("OUT-01", "K2", "dev_00000000000000000002")
	sh := d.shift(k1, "SESI-OUT01-K1-2610-001")
	_, err := d.paid(k2, sh, "STRK-OUT01-K2-2610-00001", d.octNow, d.menu, 1, nil)
	wantStatus(t, err, 422, "shift_other_device")
	if _, err := d.paid(k1, sh, "STRK-OUT01-K1-2610-00001", d.octNow, d.menu, 1, nil); err != nil {
		t.Fatalf("own shift: %v", err)
	}
	// a shift head office opened has no device: any device of the outlet may use it
	ho, err := d.e.Create(d.ctx, d.owner, "sesi_kasir", map[string]any{"outlet": "OUT-01", "opening_cash": 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.paid(k2, str(ho.Record["name"]), "STRK-OUT01-K2-2610-00001", d.octNow, d.menu, 1, nil); err != nil {
		t.Fatalf("head-office shift: %v", err)
	}
}

// A stock move of a device write that leaves the balance below zero is accepted with a negative_stock exception,
// even for a document whose stock: says negative: refuse.
func TestDeviceWriteNegativeStock(t *testing.T) {
	p := setupDeclared(t)
	m := p.masters()
	p.receipt(m, m.outA, 5, 12000)
	a := p.a
	a.Device = &DeviceRef{ID: "dev_00000000000000000009", Code: "K1", Outlet: m.outA}
	ctx := WithDeviceWrite(p.ctx, DeviceWrite{Model: "strict_issue", OccurredAt: FixedNow()})
	res, err := p.e.CreateAndSubmit(ctx, a, "strict_issue", map[string]any{"outlet": m.outA, "issue_date": "2026-10-10",
		"items": []any{map[string]any{"item": m.beras, "qty": 6}}}, true)
	if err != nil {
		t.Fatalf("device issue below zero: %v", err)
	}
	if b := p.balance(m.outA, m.beras); qtyOf(b["qty"]) != -1 {
		t.Fatalf("balance = %v", b["qty"])
	}
	xs, err := store.ListExceptions(p.ctx, p.e.DB, p.a.Org, "", "negative_stock", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(xs) != 1 {
		t.Fatalf("exceptions = %v", xs)
	}
	x := xs[0]
	if x.Ref != "strict_issue/"+str(res.Record["name"]) || x.Device != a.Device.ID || x.Outlet != m.outA ||
		x.Detail["item"] != m.beras || x.Detail["outlet"] != m.outA || fmt.Sprint(x.Detail["balance"]) != "-1" {
		t.Fatalf("exception = %+v", x)
	}
	// the same document over REST is still refused
	si := p.create("strict_issue", map[string]any{"outlet": m.outA, "issue_date": "2026-10-10", "items": []any{map[string]any{"item": m.beras, "qty": 1}}})
	_, err = p.e.Submit(p.ctx, p.a, "strict_issue", si, true)
	wantCode(t, err, 409, "negative_stock")
}

// The same body over REST is recomputed and refused by the rule, with no exception.
func TestRestWriteUnchanged(t *testing.T) {
	d := setupDeviceApps(t)
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	sh := d.shift(k1, "SESI-OUT01-K1-2610-001")
	menu := d.menuAt(23000)
	kasir := Actor{Org: "toko", ID: "kasir@toko.test", Roles: []string{"kasir"}, Human: true, Outlets: []string{"OUT-01"}}
	body := func() map[string]any {
		return map[string]any{"shift": sh, "outlet": "OUT-01", "grand_total": 45000, "items": []any{map[string]any{"menu": menu, "qty": 2}}}
	}
	res, err := d.e.CreateAndSubmit(d.ctx, kasir, "penjualan", body(), true)
	if err != nil {
		t.Fatal(err)
	}
	wantMoney(t, res.Record["grand_total"], 46000)
	d.closeShift(sh)
	_, err = d.e.CreateAndSubmit(d.ctx, kasir, "penjualan", body(), true)
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Errors) == 0 || ve.Errors[0].Rule != "shift_open" {
		t.Fatalf("REST on a closed shift: %v", err)
	}
	b := body()
	b["date"] = "2026-10-29"
	_, err = d.e.Create(d.ctx, kasir, "penjualan", b)
	if err == nil {
		t.Fatal("REST draft on a closed shift saved")
	}
	if xs := d.exceptions(""); len(xs) != 0 {
		t.Fatalf("exceptions = %v", xs)
	}
}

// A void is not a sale already paid: a device cancels only what it made (other_device), and a cancel rule still
// refuses (contract 0058 §6: voids offline are same-day only).
func TestDeviceWriteCancelOtherDevice(t *testing.T) {
	d := setupDeviceApps(t)
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	k2 := d.cashier("OUT-01", "K2", "dev_00000000000000000002")
	sh := d.shift(k1, "SESI-OUT01-K1-2610-001")
	if _, err := d.paid(k1, sh, "STRK-OUT01-K1-2610-00001", d.octNow, d.menu, 1, nil); err != nil {
		t.Fatal(err)
	}
	cancel := func(a Actor, name string, at time.Time) error {
		_, err := d.e.Cancel(WithDeviceWrite(d.ctx, DeviceWrite{Model: "penjualan", Name: name, OccurredAt: at}), a, "penjualan", name)
		return err
	}
	wantStatus(t, cancel(k2, "STRK-OUT01-K1-2610-00001", d.octNow), 422, "other_device")
	// a receipt head office made is not the device's either
	ho, err := d.e.CreateAndSubmit(d.ctx, d.owner, "penjualan", map[string]any{"shift": sh, "outlet": "OUT-01",
		"items": []any{map[string]any{"menu": d.menu, "qty": 1}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, cancel(k1, str(ho.Record["name"]), d.octNow), 422, "other_device")
	// the next day the same_day_void rule refuses the device's own void: no override for a cancel
	err = cancel(k1, "STRK-OUT01-K1-2610-00001", d.octNow.Add(24*time.Hour))
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Errors) == 0 || ve.Errors[0].Rule != "same_day_void" {
		t.Fatalf("next-day void: %v", err)
	}
	if xs := d.exceptions(""); len(xs) != 0 {
		t.Fatalf("exceptions = %v", xs)
	}
	if err := cancel(k1, "STRK-OUT01-K1-2610-00001", d.octNow); err != nil {
		t.Fatalf("same-day void of its own receipt: %v", err)
	}
}

// A rule of a record a flow writes in the device write (setoran, made by tutup_sesi's flow) still refuses.
func TestDeviceWriteFlowRuleStays(t *testing.T) {
	d := setupDeviceApps(t)
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	sh := d.shift(k1, "SESI-OUT01-K1-2610-001")
	ctx := WithDeviceWrite(d.ctx, DeviceWrite{Model: "tutup_sesi", OccurredAt: d.octNow})
	_, err := d.e.CreateAndSubmit(ctx, k1, "tutup_sesi", map[string]any{"shift": sh, "counted_cash": 20000000}, true)
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Errors) == 0 || ve.Errors[0].Rule != "setoran_limit" {
		t.Fatalf("flow record rule: %v", err)
	}
	if r, _ := d.e.Get(d.ctx, d.owner, "sesi_kasir", sh); str(r["state"]) != "open" {
		t.Fatalf("shift = %v", r["state"])
	}
	if xs := d.exceptions(""); len(xs) != 0 {
		t.Fatalf("exceptions = %v", xs)
	}
}

// Money the actor may not see is never taken from the device (contract 0027), nor money of a model that is not
// named on the device (tutup_sesi's blind-close difference).
func TestDeviceWriteHiddenMoney(t *testing.T) {
	d := setupDeviceApps(t)
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	sh := d.shift(k1, "SESI-OUT01-K1-2610-001")
	res, err := d.paid(k1, sh, "STRK-OUT01-K1-2610-00001", d.octNow, d.menu, 1, map[string]any{"audit_total": 1})
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := d.e.Get(d.ctx, d.owner, "penjualan", str(res.Record["name"]))
	wantMoney(t, stored["audit_total"], 18000)
	body := map[string]any{"shift": sh, "counted_cash": 90000, "cash_difference": 0}
	ctx := WithDeviceWrite(d.ctx, DeviceWrite{Model: "tutup_sesi", OccurredAt: d.octNow, Money: d.e.DeviceMoney("tutup_sesi", body)})
	tc, err := d.e.CreateAndSubmit(ctx, k1, "tutup_sesi", body, true)
	if err != nil {
		t.Fatal(err)
	}
	close, _ := d.e.Get(d.ctx, d.owner, "tutup_sesi", str(tc.Record["name"]))
	wantMoney(t, close["cash_difference"], 90000) // counted - expected (0): the kernel's figure
	if xs := d.exceptions("edge_price_drift"); len(xs) != 0 {
		t.Fatalf("drift = %v", xs)
	}
}

// A device submit of a draft: money it sends is kept and checked; without money, a draft this device made keeps
// its stored totals, and a draft made online is recomputed at today's prices.
func TestDeviceWriteSubmitMoney(t *testing.T) {
	d := setupDeviceApps(t)
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	sh := d.shift(k1, "SESI-OUT01-K1-2610-001")
	menu := d.menuAt(20000)
	line := []any{map[string]any{"menu": menu, "qty": 1}}
	submit := func(draft, name string, money map[string]any) store.Record {
		t.Helper()
		r, err := d.e.Submit(WithDeviceWrite(d.ctx, DeviceWrite{Model: "penjualan", Name: name, OccurredAt: d.octNow, Money: money}), k1, "penjualan", draft, true)
		if err != nil {
			t.Fatal(err)
		}
		return r.Record
	}
	// online draft, then the price goes up: recomputed
	kasir := Actor{Org: "toko", ID: "kasir@toko.test", Roles: []string{"kasir"}, Human: true, Outlets: []string{"OUT-01"}}
	online, err := d.e.Create(d.ctx, kasir, "penjualan", map[string]any{"shift": sh, "outlet": "OUT-01", "items": line})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.e.Update(d.ctx, d.owner, "menu", menu, map[string]any{"price": 22000}); err != nil {
		t.Fatal(err)
	}
	wantMoney(t, submit(str(online.Record["name"]), "STRK-OUT01-K1-2610-00001", nil)["grand_total"], 22000)
	// a device draft submitted with money: kept, drift against the kernel
	mk := func(money map[string]any) string {
		t.Helper()
		r, err := d.e.Create(WithDeviceWrite(d.ctx, DeviceWrite{Model: "penjualan", OccurredAt: d.octNow, Money: money}), k1, "penjualan",
			map[string]any{"shift": sh, "outlet": "OUT-01", "items": line})
		if err != nil {
			t.Fatal(err)
		}
		return str(r.Record["name"])
	}
	wantMoney(t, submit(mk(nil), "STRK-OUT01-K1-2610-00002", map[string]any{"grand_total": 21000})["grand_total"], 21000)
	xs := d.exceptions("edge_price_drift")
	if len(xs) != 1 || xs[0].Ref != "penjualan/STRK-OUT01-K1-2610-00002" || fmt.Sprint(xs[0].Detail["kernel"]) != "22000" {
		t.Fatalf("drift = %v", xs)
	}
	// a device draft that stored the device's money keeps it at a submit without money (no new drift)
	wantMoney(t, submit(mk(map[string]any{"grand_total": 21500}), "STRK-OUT01-K1-2610-00003", nil)["grand_total"], 21500)
	if xs := d.exceptions("edge_price_drift"); len(xs) != 2 {
		t.Fatalf("drift = %v", xs)
	}
}

// Migrating a live database adds created_device to an existing table whose model becomes naming_on: device.
func TestMigrateAddsCreatedDevice(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("cp", "-r", "../../testdata/device-apps/.", dir).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	file := filepath.Join(dir, "modules", "kasir", "models", "penjualan.rk")
	orig, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	older := strings.Replace(strings.Replace(string(orig), "naming_on: device\n", "", 1), "STRK-{outlet.code}-{device.code}-{YY}{MM}-{#####}", "STRK-{#####}", 1)
	if older == string(orig) {
		t.Fatal("fixture not edited")
	}
	url := testdb.New(t)
	ctx := context.Background()
	db, err := pg.NewPool(ctx, url, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	migrate := func(src string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		reg, rep := meta.Validate(filepath.Join(dir, "apps"), filepath.Join(dir, "site"), nil, schema.Embedded())
		if rep.Errors() > 0 {
			t.Fatalf("fixture: %v", rep.All())
		}
		if _, err := store.Migrate(ctx, db, reg, false, "test"); err != nil {
			t.Fatal(err)
		}
	}
	has := func() bool {
		res, err := db.Query(ctx, `select 1 from information_schema.columns where table_name = 'r_penjualan' and column_name = 'created_device'`)
		if err != nil {
			t.Fatal(err)
		}
		return len(res.Rows) == 1
	}
	migrate(older)
	if has() {
		t.Fatal("created_device on a model not named on the device")
	}
	migrate(string(orig))
	if !has() {
		t.Fatal("created_device not added by the migration")
	}
}
