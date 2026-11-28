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
	if got := workOf(t, m); got != "jual* jual_online " {
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

// PATCH with _if (contract 0030 §2 queue): the update runs only when the locked record still holds the values.
func TestConditionalPatch(t *testing.T) {
	owner, _, _ := screensEnv(t)
	tk := owner.must(201, "POST", "/api/v1/r/tiket_dapur", map[string]any{"table_no": "Meja 7"}).JSON["name"].(string)
	r := owner.must(200, "PATCH", "/api/v1/r/tiket_dapur/"+tk, map[string]any{"status": "cooking", "_if": map[string]any{"status": "new"}}).JSON
	if r["status"] != "cooking" {
		t.Fatalf("first step: %v", r)
	}
	// a second device still shows "new": its step is refused, the status does not go back
	c := owner.must(409, "PATCH", "/api/v1/r/tiket_dapur/"+tk, map[string]any{"status": "cooking", "_if": map[string]any{"status": "new"}})
	if get(c.JSON, "error", "code") != "precondition_failed" {
		t.Fatalf("stale step: %s", c.Body)
	}
	owner.must(400, "PATCH", "/api/v1/r/tiket_dapur/"+tk, map[string]any{"status": "ready", "_if": map[string]any{"ghost": 1}})
	owner.must(400, "PATCH", "/api/v1/r/tiket_dapur/"+tk, map[string]any{"status": "ready", "_if": "status"})
	// a number compares by value whatever its JSON form
	m := owner.must(201, "POST", "/api/v1/r/menu", map[string]any{"menu_name": "Teh", "price": 5000}).JSON["name"].(string)
	owner.must(200, "PATCH", "/api/v1/r/menu/"+m, map[string]any{"price": 6000, "_if": map[string]any{"price": 5000.0}})
	if s := owner.must(200, "GET", "/api/v1/r/tiket_dapur/"+tk, nil).JSON; s["status"] != "cooking" {
		t.Fatalf("after refusals: %v", s)
	}
}

// _if on a table field (contract 0077 §1): {row name: {child field: value}}; a stored row not named is a conflict.
func TestConditionalPatchRows(t *testing.T) {
	owner, _, _ := screensEnv(t)
	m := owner.must(201, "POST", "/api/v1/r/menu", map[string]any{"menu_name": "Soto", "price": 20000}).JSON["name"].(string)
	tk := owner.must(201, "POST", "/api/v1/r/tiket_dapur", map[string]any{"table_no": "Meja 3",
		"items": []any{map[string]any{"menu": m, "qty": 1}, map[string]any{"menu": m, "qty": 2, "held": true}}}).JSON
	name := tk["name"].(string)
	rows := tk["items"].([]any)
	r0, r1 := rows[0].(map[string]any), rows[1].(map[string]any)
	n0, n1 := r0["name"].(string), r1["name"].(string)
	patch := func(st0 string, want0 string) map[string]any {
		a := map[string]any{"name": n0, "menu": m, "qty": 1, "line_status": st0}
		b := map[string]any{"name": n1, "menu": m, "qty": 2, "held": true, "line_status": "new"}
		return map[string]any{"items": []any{a, b}, "_if": map[string]any{"items": map[string]any{
			n0: map[string]any{"line_status": want0}, n1: map[string]any{"line_status": "new", "held": true}}}}
	}
	got := owner.must(200, "PATCH", "/api/v1/r/tiket_dapur/"+name, patch("in_progress", "new")).JSON
	if s := got["items"].([]any)[0].(map[string]any)["line_status"]; s != "in_progress" {
		t.Fatalf("row step: %v", got["items"])
	}
	// a second tablet still shows the first row as new: refused, nothing changes
	c := owner.must(409, "PATCH", "/api/v1/r/tiket_dapur/"+name, patch("ready", "new"))
	if get(c.JSON, "error", "code") != "precondition_failed" {
		t.Fatalf("stale row step: %s", c.Body)
	}
	// a row that no longer exists, and malformed preconditions
	owner.must(409, "PATCH", "/api/v1/r/tiket_dapur/"+name, map[string]any{"table_no": "Meja 4",
		"_if": map[string]any{"items": map[string]any{"ghost-row": map[string]any{"line_status": "new"}}}})
	owner.must(400, "PATCH", "/api/v1/r/tiket_dapur/"+name, map[string]any{"table_no": "Meja 4", "_if": map[string]any{"items": "new"}})
	owner.must(400, "PATCH", "/api/v1/r/tiket_dapur/"+name, map[string]any{"table_no": "Meja 4",
		"_if": map[string]any{"items": map[string]any{n0: map[string]any{"ghost": 1}}}})
	after := owner.must(200, "GET", "/api/v1/r/tiket_dapur/"+name, nil).JSON
	if after["table_no"] != "Meja 3" || after["items"].([]any)[0].(map[string]any)["line_status"] != "in_progress" {
		t.Fatalf("after refusals: %v", after)
	}
	// a row another device added after this one loaded the record: a PATCH naming only the rows it knew is refused,
	// so rewriting the table never deletes the new row
	cur := owner.must(200, "GET", "/api/v1/r/tiket_dapur/"+name, nil).JSON["items"].([]any)
	owner.must(200, "PATCH", "/api/v1/r/tiket_dapur/"+name, map[string]any{"items": append(append([]any{}, cur...), map[string]any{"menu": m, "qty": 3})})
	owner.must(409, "PATCH", "/api/v1/r/tiket_dapur/"+name, map[string]any{"items": cur,
		"_if": map[string]any{"items": map[string]any{n0: map[string]any{"line_status": "in_progress"}, n1: map[string]any{"held": true}}}})
	cur = owner.must(200, "GET", "/api/v1/r/tiket_dapur/"+name, nil).JSON["items"].([]any)
	if len(cur) != 3 {
		t.Fatalf("the added row was deleted: %v", cur)
	}
	n2 := cur[2].(map[string]any)["name"].(string)
	// a null expectation is the field's zero value (a row field the device never had a value for)
	owner.must(200, "PATCH", "/api/v1/r/tiket_dapur/"+name, map[string]any{"table_no": "Meja 3",
		"_if": map[string]any{"items": map[string]any{n0: map[string]any{"held": nil}, n1: map[string]any{"held": true}, n2: map[string]any{"held": nil}}}})
	// the row queue screen opens: its recall set: values are literals, not CEL (contract 0077)
	owner.must(200, "GET", "/api/v1/screens/dapur", nil)
}
