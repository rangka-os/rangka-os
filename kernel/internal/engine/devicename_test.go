package engine

import (
	"context"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// Names made on the device (contract 0057 §3) over testdata/device-apps: penjualan (submittable) and sesi_kasir
// (not submittable) are named STRK-/SESI-{outlet.code}-{device.code}-{YY}{MM}-{#…}; outlets OUT-01 (code OUT01)
// and OUT-02 (OUT02).
type deviceEnv struct {
	t      *testing.T
	e      *Engine
	owner  Actor
	ctx    context.Context
	menu   string
	octNow time.Time
}

func setupDeviceApps(t *testing.T) *deviceEnv {
	t.Helper()
	return setupDeviceSite(t, "../../testdata/device-apps/site")
}

// setupDeviceSite is setupDeviceApps with another site folder.
func setupDeviceSite(t *testing.T, site string) *deviceEnv {
	t.Helper()
	url := testdb.New(t)
	ctx := context.Background()
	apps := "../../testdata/device-apps/apps"
	reg, rep := meta.Validate(apps, site, nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("device-apps errors: %v", rep.All())
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
	owner := Actor{Org: "toko", ID: "owner@toko.test", UserName: "USR-001", Roles: []string{"owner"}, Human: true}
	fx, err := LoadFixtures(FixturesDir(apps))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Seed(ctx, owner, fx); err != nil {
		t.Fatal(err)
	}
	r, err := e.Create(ctx, owner, "menu", map[string]any{"menu_name": "Kopi susu", "price": 18000})
	if err != nil {
		t.Fatal(err)
	}
	return &deviceEnv{t: t, e: e, owner: owner, ctx: ctx, menu: str(r.Record["name"]), octNow: FixedNow()}
}

// cashier is a kasir member of outlet on paired device code there.
func (d *deviceEnv) cashier(outlet, code, id string) Actor {
	return Actor{Org: "toko", ID: "kasir@toko.test", Roles: []string{"kasir"}, Human: true, Outlets: []string{outlet},
		Device: &DeviceRef{ID: id, Code: code, Outlet: outlet}}
}

func (d *deviceEnv) write(name string, at time.Time) context.Context {
	return WithDeviceWrite(d.ctx, DeviceWrite{Name: name, OccurredAt: at})
}

// shift opens a cashier shift on the device with the given name.
func (d *deviceEnv) shift(a Actor, name string) string {
	d.t.Helper()
	r, err := d.e.Create(d.write(name, d.octNow), a, "sesi_kasir", map[string]any{"outlet": a.Device.Outlet, "opening_cash": 100000})
	if err != nil {
		d.t.Fatalf("shift %s: %v", name, err)
	}
	return str(r.Record["name"])
}

// sale creates a draft sale in shift and submits it with the device name; it returns the submitted name.
func (d *deviceEnv) sale(a Actor, shift, outlet, name string, at time.Time) (string, error) {
	d.t.Helper()
	r, err := d.e.Create(d.write("", at), a, "penjualan", map[string]any{"shift": shift, "outlet": outlet,
		"items": []any{map[string]any{"menu": d.menu, "qty": 1}}})
	if err != nil {
		d.t.Fatalf("draft: %v", err)
	}
	res, err := d.e.Submit(d.write(name, at), a, "penjualan", str(r.Record["name"]), true)
	if err != nil {
		return "", err
	}
	return str(res.Record["name"]), nil
}

func TestDeviceNameAccepted(t *testing.T) {
	d := setupDeviceApps(t)
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	sh := d.shift(k1, "SESI-OUT01-K1-2610-001")
	if sh != "SESI-OUT01-K1-2610-001" {
		t.Fatalf("shift name %q", sh)
	}
	got, err := d.sale(k1, sh, "OUT-01", "STRK-OUT01-K1-2610-00001", d.octNow)
	if err != nil {
		t.Fatal(err)
	}
	if got != "STRK-OUT01-K1-2610-00001" {
		t.Fatalf("sale name %q", got)
	}
	if _, err := d.e.Get(d.ctx, d.owner, "penjualan", got); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceNameWrongDevice(t *testing.T) {
	d := setupDeviceApps(t)
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	sh := d.shift(k1, "SESI-OUT01-K1-2610-001")
	_, err := d.sale(k1, sh, "OUT-01", "STRK-OUT01-K2-2610-00001", d.octNow)
	wantStatus(t, err, 422, "device_name_invalid")
	// another outlet's code, and a name without a number
	_, err = d.sale(k1, sh, "OUT-01", "STRK-OUT02-K1-2610-00001", d.octNow)
	wantStatus(t, err, 422, "device_name_invalid")
	_, err = d.sale(k1, sh, "OUT-01", "STRK-OUT01-K1-2610-", d.octNow)
	wantStatus(t, err, 422, "device_name_invalid")
	// a non-submittable is checked at create
	_, err = d.e.Create(d.write("SESI-OUT01-K2-2610-001", d.octNow), k1, "sesi_kasir", map[string]any{"outlet": "OUT-01", "opening_cash": 1})
	wantStatus(t, err, 422, "device_name_invalid")
}

// R5: the record's outlet link must be the device's outlet.
func TestDeviceNameOutletMismatch(t *testing.T) {
	d := setupDeviceApps(t)
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	_, err := d.e.Create(d.write("SESI-OUT01-K1-2610-001", d.octNow), k1, "sesi_kasir", map[string]any{"outlet": "OUT-02", "opening_cash": 1})
	wantStatus(t, err, 422, "device_name_invalid")
	k1.Outlets = []string{"OUT-01", "OUT-02"}
	_, err = d.e.Create(d.write("SESI-OUT01-K1-2610-001", d.octNow), k1, "sesi_kasir", map[string]any{"outlet": "OUT-02", "opening_cash": 1})
	wantStatus(t, err, 422, "device_name_invalid")
}

// A device write of a naming_on: device model must bring the name; a write without a paired device cannot.
func TestDeviceNameMissing(t *testing.T) {
	d := setupDeviceApps(t)
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	_, err := d.e.Create(d.write("", d.octNow), k1, "sesi_kasir", map[string]any{"outlet": "OUT-01", "opening_cash": 1})
	wantStatus(t, err, 422, "device_name_invalid")
	_, err = d.e.Create(d.write("SESI-OUT01-K1-2610-001", d.octNow), d.owner, "sesi_kasir", map[string]any{"outlet": "OUT-01", "opening_cash": 1})
	wantStatus(t, err, 422, "device_name_invalid")
}

func TestDeviceNameWrongPeriod(t *testing.T) {
	d := setupDeviceApps(t)
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	sh := d.shift(k1, "SESI-OUT01-K1-2610-001")
	_, err := d.sale(k1, sh, "OUT-01", "STRK-OUT01-K1-2609-00001", d.octNow)
	wantStatus(t, err, 422, "device_name_invalid")
	// the period is the one of OccurredAt, not of the kernel's clock
	sept := time.Date(2026, 9, 30, 23, 0, 0, 0, d.e.Location())
	if got, err := d.sale(k1, sh, "OUT-01", "STRK-OUT01-K1-2609-00001", sept); err != nil || got != "STRK-OUT01-K1-2609-00001" {
		t.Fatalf("september sale: %q %v", got, err)
	}
}

func TestDeviceNameDuplicate(t *testing.T) {
	d := setupDeviceApps(t)
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	sh := d.shift(k1, "SESI-OUT01-K1-2610-001")
	if _, err := d.sale(k1, sh, "OUT-01", "STRK-OUT01-K1-2610-00001", d.octNow); err != nil {
		t.Fatal(err)
	}
	_, err := d.sale(k1, sh, "OUT-01", "STRK-OUT01-K1-2610-00001", d.octNow)
	wantStatus(t, err, 409, "duplicate_name")
	_, err = d.e.Create(d.write("SESI-OUT01-K1-2610-001", d.octNow), k1, "sesi_kasir", map[string]any{"outlet": "OUT-01", "opening_cash": 1})
	wantStatus(t, err, 409, "duplicate_name")
}

func TestDeviceNameGap(t *testing.T) {
	d := setupDeviceApps(t)
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	sh := d.shift(k1, "SESI-OUT01-K1-2610-001")
	for _, n := range []string{"STRK-OUT01-K1-2610-00001", "STRK-OUT01-K1-2610-00003"} {
		if got, err := d.sale(k1, sh, "OUT-01", n, d.octNow); err != nil || got != n {
			t.Fatalf("%s: %q %v", n, got, err)
		}
	}
	// a late 00002 fills the gap without a second exception
	if _, err := d.sale(k1, sh, "OUT-01", "STRK-OUT01-K1-2610-00002", d.octNow); err != nil {
		t.Fatal(err)
	}
	xs, err := store.ListExceptions(d.ctx, d.e.DB, "toko", "", "device_number_gap", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(xs) != 1 {
		t.Fatalf("exceptions: %+v", xs)
	}
	x := xs[0]
	if x.Outlet != "OUT-01" || x.Device != "dev_00000000000000000001" || x.Ref != "penjualan/STRK-OUT01-K1-2610-00003" ||
		toInt(x.Detail["expected"]) != 2 || toInt(x.Detail["got"]) != 3 {
		t.Fatalf("exception: %+v", x)
	}
	// each device counts on its own: K2's first number is 1
	k2 := d.cashier("OUT-01", "K2", "dev_00000000000000000002")
	sh2 := d.shift(k2, "SESI-OUT01-K2-2610-001")
	if _, err := d.sale(k2, sh2, "OUT-01", "STRK-OUT01-K2-2610-00001", d.octNow); err != nil {
		t.Fatal(err)
	}
	if xs, _ := store.ListExceptions(d.ctx, d.e.DB, "toko", "", "device_number_gap", false); len(xs) != 1 {
		t.Fatalf("exceptions after K2: %+v", xs)
	}
}

// Head office (no device write) keeps the kernel's numbering with {device.code} dropped from the pattern.
func TestNoDeviceKernelNumbering(t *testing.T) {
	d := setupDeviceApps(t)
	r, err := d.e.Create(d.ctx, d.owner, "sesi_kasir", map[string]any{"outlet": "OUT-01", "opening_cash": 100000})
	if err != nil {
		t.Fatal(err)
	}
	sh := str(r.Record["name"])
	if sh != "SESI-OUT01-2610-001" {
		t.Fatalf("shift name %q", sh)
	}
	r, err = d.e.Create(d.ctx, d.owner, "penjualan", map[string]any{"shift": sh, "outlet": "OUT-01",
		"items": []any{map[string]any{"menu": d.menu, "qty": 1}}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := d.e.Submit(d.ctx, d.owner, "penjualan", str(r.Record["name"]), true)
	if err != nil {
		t.Fatal(err)
	}
	if got := str(res.Record["name"]); got != "STRK-OUT01-2610-00001" {
		t.Fatalf("sale name %q", got)
	}
}

// Only the device write's own document takes the device name: a naming_on: device record a flow makes in the same
// unit (setoran, from tutup_sesi_on_submit) is numbered by the kernel, and a write whose Model is another model
// leaves the record to the kernel too.
func TestDeviceNameOnlyOwnDocument(t *testing.T) {
	d := setupDeviceApps(t)
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	sh := d.shift(k1, "SESI-OUT01-K1-2610-001")
	dw := WithDeviceWrite(d.ctx, DeviceWrite{Model: "tutup_sesi", OccurredAt: d.octNow})
	r, err := d.e.Create(dw, k1, "tutup_sesi", map[string]any{"shift": sh, "counted_cash": 250000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.e.Submit(dw, k1, "tutup_sesi", str(r.Record["name"]), true); err != nil {
		t.Fatalf("close shift: %v", err)
	}
	all, _, err := d.e.List(d.ctx, d.owner, "setoran", ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || str(all[0]["name"]) != "SETOR-OUT01-2610-001" {
		t.Fatalf("setoran: %v", all)
	}
	// a device write about penjualan does not name a shift
	r, err = d.e.Create(WithDeviceWrite(d.ctx, DeviceWrite{Model: "penjualan", Name: "STRK-OUT01-K1-2610-00001"}), k1, "sesi_kasir",
		map[string]any{"outlet": "OUT-01", "opening_cash": 1})
	if err != nil || str(r.Record["name"]) != "SESI-OUT01-2610-001" {
		t.Fatalf("shift: %v %v", r, err)
	}
}

// A config.numbering override of a naming_on: device model: devices and head office both follow it.
func TestDeviceNameConfigOverride(t *testing.T) {
	d := setupDeviceSite(t, "../../testdata/device-apps/site-numbering")
	k1 := d.cashier("OUT-01", "K1", "dev_00000000000000000001")
	sh := d.shift(k1, "SESI-OUT01-K1-2610-001")
	_, err := d.sale(k1, sh, "OUT-01", "STRK-OUT01-K1-2610-00001", d.octNow)
	wantStatus(t, err, 422, "device_name_invalid")
	if got, err := d.sale(k1, sh, "OUT-01", "NOTA-OUT01-K1-2610-0001", d.octNow); err != nil || got != "NOTA-OUT01-K1-2610-0001" {
		t.Fatalf("device sale: %q %v", got, err)
	}
	r, err := d.e.Create(d.ctx, d.owner, "penjualan", map[string]any{"shift": sh, "outlet": "OUT-01",
		"items": []any{map[string]any{"menu": d.menu, "qty": 1}}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := d.e.Submit(d.ctx, d.owner, "penjualan", str(r.Record["name"]), true)
	if err != nil || str(res.Record["name"]) != "NOTA-OUT01-2610-0001" {
		t.Fatalf("head office sale: %v %v", res, err)
	}
}
