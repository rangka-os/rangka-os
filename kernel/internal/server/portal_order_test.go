package server

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// Order pages (contract 0073 §9): a guest orders from a table's QR link without signing in.

func orderPageEnv(t *testing.T) *env {
	t.Helper()
	return newEnvFrom(t, func(dir string) (string, string) {
		src := filepath.Join(testdb.RepoRoot(), "kernel", "testdata", "order-page")
		if err := exec.Command("cp", "-r", src+"/.", dir).Run(); err != nil {
			t.Fatal(err)
		}
		return filepath.Join(dir, "apps"), filepath.Join(dir, "site")
	})
}

func TestPortalOrderPage(t *testing.T) {
	e := orderPageEnv(t)
	e.login()
	meja := e.must(201, "POST", "/api/v1/r/meja", map[string]any{"nama": "A3"}).JSON["name"].(string)
	meja2 := e.must(201, "POST", "/api/v1/r/meja", map[string]any{"nama": "B1"}).JSON["name"].(string)
	kopi := e.must(201, "POST", "/api/v1/r/menu", map[string]any{"nama": "Kopi susu", "harga": 18000, "grup": "Minuman", "modal": 7000}).JSON["name"].(string)
	roti := e.must(201, "POST", "/api/v1/r/menu", map[string]any{"nama": "Roti bakar", "harga": 15000, "grup": "Makanan"}).JSON["name"].(string)
	habis := e.must(201, "POST", "/api/v1/r/menu", map[string]any{"nama": "Es teh", "harga": 8000, "habis": true}).JSON["name"].(string)
	off := e.must(201, "POST", "/api/v1/r/menu", map[string]any{"nama": "Menu lama", "harga": 1000, "dijual": false}).JSON["name"].(string)

	// a link names the anchor; a long life (365 days by default)
	link := e.must(201, "POST", "/api/v1/portal/pesan/links", map[string]any{"record": meja}).JSON
	path := link["path"].(string)
	if !strings.HasPrefix(link["expires_at"].(string), "2027-") {
		t.Fatalf("expires_at = %v, want a year on", link["expires_at"])
	}
	e.must(404, "POST", "/api/v1/portal/pesan/links", map[string]any{"record": "MN-999"})

	c := e.customer()
	shell := c.must(200, "GET", path, nil)
	if !strings.Contains(string(shell.Body), "/portal/order.js") {
		t.Fatalf("shell does not load order.js")
	}
	d := c.must(200, "GET", path+"/data", nil).JSON
	if get(d, "page", "type") != "order_page" || get(d, "anchor", "title") != "A3" {
		t.Fatalf("data = %v", d)
	}
	menu := d["menu"].([]any)
	names := map[string]map[string]any{}
	for _, m := range menu {
		row := m.(map[string]any)
		names[row["name"].(string)] = row
		if len(row) != 5 || row["modal"] != nil {
			t.Fatalf("menu row carries more than name, title, price, group, sold_out: %v", row)
		}
	}
	if names[off] != nil || names[kopi] == nil || names[habis]["sold_out"] != true || names[kopi]["group"] != "Minuman" {
		t.Fatalf("menu = %v", menu)
	}

	// an order: the price comes from the model, the anchor from the link
	o := c.must(201, "POST", path+"/order", map[string]any{"guest": "Budi", "note": "", "meja": meja2,
		"lines": []any{map[string]any{"item": kopi, "qty": 2, "note": "kurang manis", "harga": 1}, map[string]any{"item": roti, "qty": 1}}}).JSON["order"].(map[string]any)
	name := o["name"].(string)
	if o["status"] != "baru" || o["label"] != "Menunggu kasir" || o["can_cancel"] != true {
		t.Fatalf("order = %v", o)
	}
	rec := e.must(200, "GET", "/api/v1/r/pesanan/"+name, nil).JSON
	if rec["meja"] != meja || rec["tamu"] != "Budi" || rec["total"] != float64(51000) || !strings.HasPrefix(rec["created_by"].(string), "portal:pl_") {
		t.Fatalf("stored order = %v", rec)
	}

	// refusals
	bad := path[:len(path)-1] + map[bool]string{true: "B", false: "A"}[strings.HasSuffix(path, "A")]
	c.must(404, "POST", bad+"/order", map[string]any{"lines": []any{map[string]any{"item": kopi, "qty": 1}}})
	c.must(404, "GET", strings.Replace(path, "/pesan/", "/tagihan/", 1)+"/data", nil)
	link2 := e.must(201, "POST", "/api/v1/portal/pesan/links", map[string]any{"record": meja2}).JSON["path"].(string)
	if r := c.do("POST", link2+"/order", map[string]any{"lines": []any{map[string]any{"item": off, "qty": 1}}}); r.Status != 409 || get(r.JSON, "error", "code") != "order_item_unavailable" {
		t.Fatalf("item off the menu: %d %s", r.Status, r.Body)
	}
	if r := c.do("POST", link2+"/order", map[string]any{"lines": []any{map[string]any{"item": habis, "qty": 1}}}); r.Status != 409 || get(r.JSON, "error", "code") != "order_item_sold_out" {
		t.Fatalf("sold out: %d %s", r.Status, r.Body)
	}
	if r := c.do("POST", link2+"/order", map[string]any{"lines": []any{map[string]any{"item": kopi, "qty": 1.5}}}); r.Status != 400 || get(r.JSON, "error", "code") != "order_qty" {
		t.Fatalf("qty 1.5: %d %s", r.Status, r.Body)
	}
	if r := c.do("POST", link2+"/order", map[string]any{"lines": []any{map[string]any{"item": kopi, "qty": 1}}}); r.Status != 429 {
		t.Fatalf("fourth send on one link: %d, want 429", r.Status)
	}
	link3 := e.must(201, "POST", "/api/v1/portal/pesan/links", map[string]any{"record": meja2}).JSON["path"].(string)
	many := []any{}
	for i := 0; i < 31; i++ {
		many = append(many, map[string]any{"item": kopi, "qty": 1})
	}
	c.must(400, "POST", link3+"/order", map[string]any{"lines": many})
	c.must(400, "POST", link3+"/order", map[string]any{"lines": []any{}})
	c.must(400, "POST", link3+"/order", map[string]any{"guest": strings.Repeat("x", 41), "lines": []any{map[string]any{"item": kopi, "qty": 1}}})

	// status: only this link's own orders
	d = c.must(200, "GET", path+"/data?mine="+name+",PS-99999", nil).JSON
	if mine := d["orders"].([]any); len(mine) != 1 || get(mine, 0, "name") != name {
		t.Fatalf("mine = %v", d["orders"])
	}
	if mine := c.must(200, "GET", link3+"/data?mine="+name, nil).JSON["orders"].([]any); len(mine) != 0 {
		t.Fatalf("another table sees the order: %v", mine)
	}

	// cancel: another table's link cannot; the own link can while open
	link4 := e.must(201, "POST", "/api/v1/portal/pesan/links", map[string]any{"record": meja2}).JSON["path"].(string)
	c.must(404, "POST", link4+"/order/"+name+"/cancel", nil)
	got := c.must(200, "POST", path+"/order/"+name+"/cancel", nil).JSON["order"].(map[string]any)
	if got["status"] != "batal" || got["can_cancel"] != false {
		t.Fatalf("cancelled = %v", got)
	}
	c.must(409, "POST", path+"/order/"+name+"/cancel", nil)

	// QR sheet: rotates the anchor's links
	sheet := e.must(200, "POST", "/api/v1/portal/pesan/qr", map[string]any{"records": []any{meja}}).JSON["data"].([]any)
	if len(sheet) != 1 || !strings.HasPrefix(get(sheet, 0, "qr").(string), "data:image/png;base64,") || get(sheet, 0, "title") != "A3" {
		t.Fatalf("sheet = %v", sheet)
	}
	c.must(410, "GET", path+"/data", nil)
	c.must(200, "GET", get(sheet, 0, "path").(string)+"/data", nil)
	e.must(404, "POST", "/api/v1/portal/nope/qr", map[string]any{})
	c.must(401, "POST", "/api/v1/portal/pesan/qr", map[string]any{})
}
