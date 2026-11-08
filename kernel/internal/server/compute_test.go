package server

import (
	"context"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// POST /api/v1/compute/{model} answers the record as the model would save it and writes nothing (contract 0034).
func TestComputeRecord(t *testing.T) {
	owner, _, _ := screensEnv(t)
	if _, err := auth.EnsureUser(context.Background(), owner.s.DB, "kasir@example.test", "Kasir", "rangka-demo", "kopi-senja", "kasir", ""); err != nil {
		t.Fatal(err)
	}
	kasir := &env{t: t, ts: owner.ts, jar: newJar()}
	kasir.must(200, "POST", "/auth/login", map[string]any{"email": "kasir@example.test", "password": "rangka-demo"})
	kopi := owner.must(201, "POST", "/api/v1/r/menu", map[string]any{"menu_name": "Kopi susu", "category": "Minuman", "price": 18000}).JSON["name"].(string)
	shift := kasir.must(201, "POST", "/api/v1/r/sesi_kasir", map[string]any{"opening_cash": 100000}).JSON["name"].(string)

	cart := map[string]any{"shift": shift, "items": []any{map[string]any{"menu": kopi, "qty": 2}},
		"payments": []any{map[string]any{"method": "cash", "amount": 50000}}}
	r := kasir.must(200, "POST", "/api/v1/compute/penjualan", cart).JSON
	if get(r, "record", "grand_total") != 36000.0 || get(r, "record", "change_amount") != 14000.0 ||
		get(r, "record", "items", 0, "amount") != 36000.0 || get(r, "record", "date") != fixedNow().Format("2006-01-02") {
		t.Fatalf("computed: %v", r)
	}
	if n := len(r["errors"].([]any)); n != 0 {
		t.Fatalf("errors on a valid cart: %v", r["errors"])
	}
	if _, named := r["record"].(map[string]any)["name"]; named {
		t.Fatalf("a computed record takes no name: %v", r["record"])
	}
	// an empty cart is an issue in a 200 answer, not a failure
	e := kasir.must(200, "POST", "/api/v1/compute/penjualan", map[string]any{"shift": shift}).JSON
	if get(e, "errors", 0, "field") != "items" {
		t.Fatalf("empty cart: %v", e)
	}
	// nothing was written, no number was taken
	if l := kasir.must(200, "GET", "/api/v1/r/penjualan", nil).JSON; l["total"] != 0.0 {
		t.Fatalf("compute wrote a record: %v", l)
	}
	d := kasir.must(201, "POST", "/api/v1/r/penjualan", cart).JSON
	if s := kasir.must(200, "POST", "/api/v1/r/penjualan/"+d["name"].(string)+"/submit", nil).JSON; s["name"] != "JUAL-00001" {
		t.Fatalf("the first sale after computes is %v", s["name"])
	}
	// hidden fields stay hidden (0027)
	c := kasir.must(200, "POST", "/api/v1/compute/tutup_sesi", map[string]any{"shift": shift, "counted_cash": 1000}).JSON
	if _, seen := c["record"].(map[string]any)["cash_difference"]; seen {
		t.Fatalf("the cashier sees the difference: %v", c)
	}
	kasir.must(404, "POST", "/api/v1/compute/nope", map[string]any{})
	kasir.must(400, "POST", "/api/v1/compute/penjualan_baris", map[string]any{})
	kasir.must(422, "POST", "/api/v1/compute/penjualan", map[string]any{"items": "x"})
}
