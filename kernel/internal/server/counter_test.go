package server

import (
	"context"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// The counter screen of testdata/screens (module kasir) through the record API, as the web app drives it
// (contract 0030 §2 counter, §10 on: cancel).
func TestCounterScreen(t *testing.T) {
	owner, _, _ := screensEnv(t)
	if _, err := auth.EnsureUser(context.Background(), owner.s.DB, "kasir@example.test", "Kasir", "rangka-demo", "kopi-senja", "kasir", ""); err != nil {
		t.Fatal(err)
	}
	kasir := &env{t: t, ts: owner.ts, jar: newJar()}
	kasir.must(200, "POST", "/auth/login", map[string]any{"email": "kasir@example.test", "password": "rangka-demo"})
	kopi := owner.must(201, "POST", "/api/v1/r/menu", map[string]any{"menu_name": "Kopi susu", "category": "Minuman", "price": 18000}).JSON["name"].(string)

	m := kasir.must(200, "GET", "/api/v1/meta", nil).JSON
	if got := workOf(t, m); got != "jual* " {
		t.Fatalf("kasir work: %q", got)
	}
	sc := kasir.must(200, "GET", "/api/v1/screens/jual", nil).JSON
	if get(sc, "sets", "document", "date") != fixedNow().Format("2006-01-02") || get(sc, "screen", "counter", "session", "model") != "sesi_kasir" {
		t.Fatalf("screen: %v", sc)
	}
	// open a session, sell with split payment, the change comes from cash
	shift := kasir.must(201, "POST", "/api/v1/r/sesi_kasir", map[string]any{"opening_cash": 100000}).JSON["name"].(string)
	open := kasir.must(200, "GET", "/api/v1/r/sesi_kasir?filter=cashier+%3D%3D+user.email+%26%26+state+%3D%3D+%27open%27", nil).JSON
	if open["total"] != 1.0 {
		t.Fatalf("open session lookup: %v", open)
	}
	sale := map[string]any{"shift": shift, "date": get(sc, "sets", "document", "date"),
		"items":    []any{map[string]any{"menu": kopi, "qty": 2}},
		"payments": []any{map[string]any{"method": "qris", "amount": 10000}, map[string]any{"method": "cash", "amount": 50000}}}
	d := withKey(kasir, "sale-1").must(201, "POST", "/api/v1/r/penjualan", sale).JSON
	sub := withKey(kasir, "sale-1s").must(200, "POST", "/api/v1/r/penjualan/"+d["name"].(string)+"/submit", nil).JSON
	if sub["grand_total"] != 36000.0 || sub["change_amount"] != 24000.0 {
		t.Fatalf("sale: %v", sub)
	}
	kasir.must(200, "GET", "/api/v1/r/penjualan/"+sub["name"].(string)+"/print?format=struk", nil)
	// a sale of another day may not be voided by the cashier (rule on: cancel)
	old := map[string]any{"shift": shift, "date": "2026-01-02", "items": []any{map[string]any{"menu": kopi, "qty": 1}}}
	o := kasir.must(201, "POST", "/api/v1/r/penjualan", old).JSON
	os := kasir.must(200, "POST", "/api/v1/r/penjualan/"+o["name"].(string)+"/submit", nil).JSON
	r := kasir.must(422, "POST", "/api/v1/r/penjualan/"+os["name"].(string)+"/cancel", nil)
	if get(r.JSON, "errors", 0, "rule") != "same_day_void" {
		t.Fatalf("old void: %s", r.Body)
	}
	kasir.must(200, "POST", "/api/v1/r/penjualan/"+sub["name"].(string)+"/cancel", nil)
	// close the session blind: the difference is hidden from the cashier
	c := kasir.must(201, "POST", "/api/v1/r/tutup_sesi", map[string]any{"shift": shift, "counted_cash": 126000}).JSON
	if _, seen := c["cash_difference"]; seen {
		t.Fatalf("the cashier sees the difference: %v", c)
	}
	kasir.must(200, "POST", "/api/v1/r/tutup_sesi/"+c["name"].(string)+"/submit", nil)
	if s := kasir.must(200, "GET", "/api/v1/r/sesi_kasir/"+shift, nil).JSON; s["state"] != "closed" {
		t.Fatalf("session after close: %v", s)
	}
}
