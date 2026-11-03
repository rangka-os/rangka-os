package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/money"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

const moneyHead = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/"

// moneyRegistry loads a temp app "billing" with a bill header (money total) and a bill_line child (money amount).
func moneyRegistry(t *testing.T) *meta.Registry {
	t.Helper()
	return load(t, moneyAppsDir(t))
}

// moneyAppsDir writes the moneyRegistry apps to a temp dir.
func moneyAppsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"billing/app.rk":              moneyHead + "app.json\nname: billing\nversion: 1.0.0\ntitle: {id: Tagihan}\n",
		"billing/models/bill.rk":      moneyHead + "model.json\nname: bill\nlabel: {id: Tagihan}\nfields:\n  - {name: currency, type: data}\n  - {name: rate, type: float}\n  - {name: total, type: money, currency_field: currency}\n  - {name: items, type: table, of: bill_line}\n",
		"billing/models/bill_line.rk": moneyHead + "model.json\nname: bill_line\nis_child: true\nlabel: {id: Baris}\nfields:\n  - {name: amount, type: money, currency_field: currency}\n",
	}
	for p, s := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// moneyRegistryWithOverlay is moneyRegistry plus a site overlay adding money field x_fee to bill.
func moneyRegistryWithOverlay(t *testing.T) *meta.Registry {
	t.Helper()
	apps := moneyAppsDir(t)
	site := t.TempDir()
	overlay := moneyHead + "overlay.json\nmodel: bill\nfields:\n  - {name: x_fee, type: money, currency_field: currency, label: {id: Biaya}}\n"
	if err := os.MkdirAll(filepath.Join(site, "custom"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(site, "custom", "bill.rk"), []byte(overlay), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, rep := meta.Validate(apps, site, nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("metadata errors: %v", rep.All())
	}
	return reg
}

func TestMoneyColumns(t *testing.T) {
	reg := moneyRegistry(t)
	cols := map[string]string{}
	for _, c := range ModelTable(reg.Models["bill"]).Columns {
		cols[c.Name] = c.Type
	}
	if cols["total"] != "numeric(20,4)" || cols["total_idr"] != "bigint" {
		t.Errorf("columns = %v", cols)
	}
	if SQLType("money") != "numeric(20,4)" {
		t.Errorf("SQLType(money) = %q", SQLType("money"))
	}
	// a plain currency field keeps numeric
	if SQLType("currency") != "numeric" {
		t.Errorf("SQLType(currency) = %q", SQLType("currency"))
	}
}

func TestCoerceMoney(t *testing.T) {
	f := &meta.Field{Name: "total", Type: "money"}
	str := func(v any) string {
		t.Helper()
		got, err := Coerce(f, v)
		if err != nil {
			t.Fatalf("Coerce(%v): %v", v, err)
		}
		return got.(money.Decimal).String()
	}
	if got := str("1250.50"); got != "1250.5" {
		t.Errorf("string: %s", got)
	}
	if got := str(0.1); got != "0.1" {
		t.Errorf("float: %s", got)
	}
	if got := str(int64(7)); got != "7" {
		t.Errorf("int64: %s", got)
	}
	if got := str(3); got != "3" {
		t.Errorf("int: %s", got)
	}
	if got := str(""); got != "0" {
		t.Errorf("empty: %s", got)
	}
	if got := str(nil); got != "0" {
		t.Errorf("nil: %s", got)
	}
	d, _ := money.Parse("5")
	if got := str(d); got != "5" {
		t.Errorf("decimal: %s", got)
	}
	// messages are Bahasa Indonesia with the field label (the name when there is none)
	if _, err := Coerce(f, "1.23456"); err == nil || err.Error() != "total maksimal 4 angka desimal" {
		t.Errorf("too many decimals: %v", err)
	}
	if _, err := Coerce(f, "abc"); err == nil || err.Error() != "total maksimal 4 angka desimal" {
		t.Errorf("junk: %v", err)
	}
	labelled := &meta.Field{Name: "total", Type: "money", Label: meta.Label{"id": "Total akhir"}}
	if _, err := Coerce(labelled, "1.23456"); err == nil || err.Error() != "Total akhir maksimal 4 angka desimal" {
		t.Errorf("labelled too many decimals: %v", err)
	}
	if _, err := Coerce(labelled, int64(1)<<62); err == nil || err.Error() != "Total akhir terlalu besar" || !errors.Is(err, money.ErrRange) {
		t.Errorf("too large: %v (is ErrRange: %v)", err, errors.Is(err, money.ErrRange))
	}
	if _, err := Coerce(labelled, "1.2345"); errors.Is(err, money.ErrRange) {
		t.Errorf("a valid amount is no range error: %v", err)
	}
	if _, err := Coerce(f, true); err == nil {
		t.Error("bool should fail")
	}
	if v := Zero(f); !v.(money.Decimal).IsZero() {
		t.Errorf("Zero = %v", v)
	}
	if got := sqlValue(f, d); got != "5" {
		t.Errorf("sqlValue = %v", got)
	}
}

func TestMoneyRoundTrip(t *testing.T) {
	url := testdb.New(t)
	ctx := context.Background()
	db, err := pg.NewPool(ctx, url, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reg := moneyRegistry(t)
	if _, err := Migrate(ctx, db, reg, false, "test"); err != nil {
		t.Fatal(err)
	}
	bill, line := reg.Models["bill"], reg.Models["bill_line"]
	total, _ := Coerce(bill.Field("total"), "9999999999999999.9999")
	amount, _ := Coerce(line.Field("amount"), "0.0001")
	r := Record{"name": "B-1", "created_by": "u", "currency": "USD", "rate": 1.0, "total": total, "total_idr": int64(1234),
		"items": []any{map[string]any{"amount": amount}}}
	if err := Insert(ctx, db, reg, "org1", bill, r); err != nil {
		t.Fatal(err)
	}
	got, err := Get(ctx, db, reg, "org1", bill, "B-1")
	if err != nil {
		t.Fatal(err)
	}
	if s := got["total"].(money.Decimal).String(); s != "9999999999999999.9999" {
		t.Errorf("total = %s", s)
	}
	if v, ok := got["total_idr"].(int64); !ok || v != 1234 {
		t.Errorf("total_idr = %#v", got["total_idr"])
	}
	items := got["items"].([]any)
	if s := items[0].(Record)["amount"].(money.Decimal).String(); s != "0.0001" {
		t.Errorf("amount = %s", s)
	}
	if _, leaked := got["rk_text__total"]; leaked {
		t.Error("alias key leaked into record")
	}
	all, err := All(ctx, db, reg, "org1", bill, true)
	if err != nil || len(all) != 1 || all[0]["total"].(money.Decimal).String() != "9999999999999999.9999" {
		t.Errorf("All = %v, %v", all, err)
	}
	// a NULL money column reads back as zero
	if _, err := db.Exec(ctx, `update r_bill set total = null where name = 'B-1'`); err != nil {
		t.Fatal(err)
	}
	got, _ = Get(ctx, db, reg, "org1", bill, "B-1")
	if !got["total"].(money.Decimal).IsZero() {
		t.Errorf("null total = %v", got["total"])
	}
	// migrating again changes nothing
	res, err := Migrate(ctx, db, reg, false, "test")
	if err != nil || len(res.Statements) != 0 {
		t.Errorf("second migrate: %+v %v", res, err)
	}
}

// TestTwinZero: an IDR twin is whole rupiah everywhere, so its zero value is int64 like the bigint column.
func TestTwinZero(t *testing.T) {
	reg := moneyRegistry(t)
	tw := reg.Models["bill"].Field("total_idr")
	if tw == nil || tw.MoneyOf() != "total" {
		t.Fatalf("twin = %+v", tw)
	}
	if v := Zero(tw); v != int64(0) {
		t.Errorf("Zero(twin) = %#v, want int64(0)", v)
	}
	if v := Zero(&meta.Field{Name: "x", Type: "currency"}); v != float64(0) {
		t.Errorf("Zero(currency) = %#v, want float64(0)", v)
	}
}

// TestCoerceDecimalIntoOtherTypes: a decimal into int rounds half away from zero, into a float type converts
// exactly as far as float64 allows, into text gives the canonical string.
func TestCoerceDecimalIntoOtherTypes(t *testing.T) {
	d := func(s string) money.Decimal {
		v, err := money.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		typ, in string
		want    any
	}{
		{"int", "2.5", int64(3)},
		{"int", "-2.5", int64(-3)},
		{"int", "2.4999", int64(2)},
		{"rating", "4.5", int64(5)},
		{"float", "0.3", 0.3},
		{"currency", "-1250.5", -1250.5},
		{"percent", "42.9", 42.9},
		{"data", "1250.50", "1250.5"},
		{"text", "0", "0"},
	}
	for _, c := range cases {
		got, err := Coerce(&meta.Field{Name: "x", Type: c.typ}, d(c.in))
		if err != nil || got != c.want {
			t.Errorf("Coerce(%s, %s) = %#v, %v; want %#v", c.typ, c.in, got, err, c.want)
		}
	}
}

// TestFromRowMoneyText: money text that is not canonical numeric (a column left double precision after a
// retype) is read exactly, rounded to 4 dp half away from zero; text that is no number is a read error.
func TestFromRowMoneyText(t *testing.T) {
	bill := moneyRegistry(t).Models["bill"]
	for in, want := range map[string]string{"1e+15": "1000000000000000", "0.30000000000000004": "0.3", "-1.23455": "-1.2346", "1250.5000": "1250.5"} {
		r, err := fromRow(bill, map[string]any{"name": "B-1", moneyAlias + "total": in})
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if got := r["total"].(money.Decimal).String(); got != want {
			t.Errorf("%s read as %s, want %s", in, got, want)
		}
	}
	for _, bad := range []string{"NaN", "abc", "1e+30"} {
		if _, err := fromRow(bill, map[string]any{"name": "B-1", moneyAlias + "total": bad}); err == nil {
			t.Errorf("%s: want a read error", bad)
		}
	}
}

// TestOverlayMoneyRoundTrip: an x_ money field added by a company overlay lives in the x JSONB column and reads
// back as the same exact decimal.
func TestOverlayMoneyRoundTrip(t *testing.T) {
	url := testdb.New(t)
	ctx := context.Background()
	db, err := pg.NewPool(ctx, url, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reg := moneyRegistryWithOverlay(t)
	if _, err := Migrate(ctx, db, reg, false, "test"); err != nil {
		t.Fatal(err)
	}
	bill := reg.Models["bill"]
	if f := bill.Field("x_fee"); f == nil || f.Type != "money" {
		t.Fatalf("x_fee = %+v", f)
	}
	fee, err := Coerce(bill.Field("x_fee"), "9999999999999999.9999")
	if err != nil {
		t.Fatal(err)
	}
	r := Record{"name": "B-1", "created_by": "u", "currency": "USD", "rate": 1.0, "x_fee": fee, "items": []any{}}
	if err := Insert(ctx, db, reg, "org1", bill, r); err != nil {
		t.Fatal(err)
	}
	got, err := Get(ctx, db, reg, "org1", bill, "B-1")
	if err != nil {
		t.Fatal(err)
	}
	d, ok := got["x_fee"].(money.Decimal)
	if !ok || d.String() != "9999999999999999.9999" {
		t.Errorf("x_fee = %#v", got["x_fee"])
	}
}
