package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
)

// GET /api/v1/edge/config and /api/v1/edge/data/<set> (contract 0057 §7, kernel side of 0006 §4) over
// testdata/device-apps: the jual counter (catalogue menu, available stok_menu per outlet) bound to kasir.

func (x *edgeEnv) sys() engine.Actor {
	return engine.Actor{Org: "kopi-senja", ID: "system", System: true}
}

func (x *edgeEnv) create(model string, input map[string]any) string {
	x.owner.t.Helper()
	res, err := x.owner.s.Engine.Create(context.Background(), x.sys(), model, input)
	if err != nil {
		x.owner.t.Fatal(err)
	}
	return res.Record["name"].(string)
}

// pull reads every page of a data set and returns its items and the number of pages.
func (x *edgeEnv) pull(set string) ([]map[string]any, int) {
	x.owner.t.Helper()
	var items []map[string]any
	cursor, pages := "", 0
	for {
		path := "/api/v1/edge/data/" + set
		if cursor != "" {
			path += "?cursor=" + url.QueryEscape(cursor)
		}
		r := x.dev.must(200, "GET", path, nil)
		pages++
		list, _ := r.JSON["items"].([]any)
		for _, it := range list {
			items = append(items, it.(map[string]any))
		}
		if r.JSON["done"] == true {
			return items, pages
		}
		cursor, _ = r.JSON["next_cursor"].(string)
		if cursor == "" || pages > 50 {
			x.owner.t.Fatalf("page %d of %s without next_cursor: %s", pages, set, r.Body)
		}
	}
}

func TestEdgeConfig(t *testing.T) {
	x := newEdgeEnv(t)
	r := x.dev.must(200, "GET", "/api/v1/edge/config", nil)
	cfg := r.JSON
	v, _ := cfg["version"].(string)
	if v == "" || r.Header.Get("ETag") != `"`+v+`"` {
		t.Fatalf("version %q etag %q", v, r.Header.Get("ETag"))
	}
	if get(cfg, "outlet", "code") != "OUT01" || get(cfg, "outlet", "name") != "Batununggal" || get(cfg, "outlet", "timezone") != "Asia/Jakarta" {
		t.Fatalf("outlet = %v", cfg["outlet"])
	}
	if get(cfg, "device", "code") != "K1" || get(cfg, "device", "id") == "" {
		t.Fatalf("device = %v", cfg["device"])
	}
	if get(cfg, "sync", "push_max_items") != 100.0 || get(cfg, "sync", "pull_interval_s") != 60.0 {
		t.Fatalf("sync = %v", cfg["sync"])
	}
	screens := strs(cfg["screens"])
	if !slices.Contains(screens, "jual") || !slices.Contains(screens, "jual_online") || slices.Contains(screens, "dapur") {
		t.Fatalf("screens = %v (only kasir members on OUT-01)", screens)
	}
	sets := strs(cfg["data_sets"])
	for _, want := range []string{"menu", "stok_menu", "tax", "staff"} {
		if !slices.Contains(sets, want) {
			t.Fatalf("data_sets = %v, want %s", sets, want)
		}
	}
	if slices.Contains(sets, "penjualan") || slices.Contains(sets, "sesi_kasir") {
		t.Fatalf("data_sets = %v: documents are not reference data", sets)
	}
	// the same answer with If-None-Match is 304; a stale version is the full answer
	x.dev.hdr.Set("If-None-Match", `"`+v+`"`)
	if r := x.dev.do("GET", "/api/v1/edge/config", nil); r.Status != http.StatusNotModified || len(r.Body) != 0 {
		t.Fatalf("If-None-Match = %d %s", r.Status, r.Body)
	}
	x.dev.hdr.Set("If-None-Match", `"cfg_old"`)
	x.dev.must(200, "GET", "/api/v1/edge/config", nil)
	x.dev.hdr.Del("If-None-Match")
	// a dapur member on the outlet adds the dapur screen and changes the version
	edgeMemberRole(x.owner, "dapur@toko.test", "dapur", "OUT-01")
	cfg2 := x.dev.must(200, "GET", "/api/v1/edge/config", nil).JSON
	if !slices.Contains(strs(cfg2["screens"]), "dapur") || cfg2["version"] == v {
		t.Fatalf("after dapur member: %v", cfg2)
	}
	// without Rangka-Edge-Sync: v1 the kernel says which versions it speaks (0006 §1)
	x.dev.hdr.Del("Rangka-Edge-Sync")
	if r := x.dev.do("GET", "/api/v1/edge/config", nil); r.Status != 400 || get(r.JSON, "error", "code") != "unsupported_sync_version" {
		t.Fatalf("no sync header = %d %s", r.Status, r.Body)
	}
	// a set the config does not name is 404
	x.dev.hdr.Set("Rangka-Edge-Sync", "v1")
	x.dev.must(404, "GET", "/api/v1/edge/data/penjualan", nil)
}

func TestEdgeDataPaging(t *testing.T) {
	x := newEdgeEnv(t)
	for i := 0; i < 599; i++ { // plus the env's Kopi susu: 600
		x.create("menu", map[string]any{"menu_name": fmt.Sprintf("Menu %03d", i), "price": 1000 + i})
	}
	first := x.dev.must(200, "GET", "/api/v1/edge/data/menu", nil).JSON
	if n := len(first["items"].([]any)); n != 500 || first["done"] != false || first["next_cursor"] == nil {
		t.Fatalf("first page: %d items, done %v, next %v", n, first["done"], first["next_cursor"])
	}
	it := get(first, "items", 0).(map[string]any)
	if it["name"] == "" || it["deleted"] != false || get(it, "row", "price") == nil || get(it, "row", "name") != it["name"] {
		t.Fatalf("item = %v", it)
	}
	second := x.dev.must(200, "GET", "/api/v1/edge/data/menu?cursor="+url.QueryEscape(first["next_cursor"].(string)), nil).JSON
	if n := len(second["items"].([]any)); n != 100 || second["done"] != true {
		t.Fatalf("second page: %d items, done %v", n, second["done"])
	}
	items, pages := x.pull("menu")
	seen := map[any]bool{}
	for _, it := range items {
		seen[it["name"]] = true
	}
	if pages != 2 || len(items) != 600 || len(seen) != 600 {
		t.Fatalf("pull: %d pages, %d items, %d distinct", pages, len(items), len(seen))
	}
	x.dev.must(400, "GET", "/api/v1/edge/data/menu?cursor=%25%25bad", nil)
}

func TestEdgeDataOutletScoped(t *testing.T) {
	x := newEdgeEnv(t)
	x.create("stok_menu", map[string]any{"menu": x.menu, "outlet": "OUT-01", "qty": 7})
	x.create("stok_menu", map[string]any{"menu": x.menu, "outlet": "OUT-02", "qty": 3})
	items, _ := x.pull("stok_menu")
	if len(items) != 1 || get(items[0], "row", "outlet") != "OUT-01" || get(items[0], "row", "qty") != 7.0 {
		t.Fatalf("stok_menu = %v", items)
	}
	// a field hidden from a staff role on the outlet never leaves the kernel (contract 0027)
	x.create("menu", map[string]any{"menu_name": "Es teh", "price": 8000, "cost": 2000})
	menu, _ := x.pull("menu")
	if len(menu) != 2 {
		t.Fatalf("menu = %v", menu)
	}
	for _, it := range menu {
		if _, has := it["row"].(map[string]any)["cost"]; has {
			t.Fatalf("hidden cost sent: %v", it)
		}
	}
	tax, _ := x.pull("tax")
	if len(tax) != 1 || get(tax[0], "row", "ppn_rate") == nil {
		t.Fatalf("tax = %v", tax)
	}
}

func TestEdgeStaffNoEmail(t *testing.T) {
	x := newEdgeEnv(t)
	ctx := context.Background()
	if err := x.owner.s.Auth.SetPIN(ctx, "kopi-senja", x.kasir, "2580"); err != nil {
		t.Fatal(err)
	}
	other := edgeMember(x.owner, "kasir2@toko.test", "OUT-02")
	if err := x.owner.s.Auth.SetPIN(ctx, "kopi-senja", other, "2580"); err != nil {
		t.Fatal(err)
	}
	items, _ := x.pull("staff")
	if len(items) != 1 {
		t.Fatalf("staff = %v (only OUT-01 members)", items)
	}
	row := items[0]["row"].(map[string]any)
	if row["id"] != float64(x.kasir) || row["name"] != "Kasir kasir@toko.test" || row["role"] != "kasir" || len(row) != 3 {
		t.Fatalf("staff row = %v", row)
	}
	if items[0]["name"] != fmt.Sprint(x.kasir) || items[0]["deleted"] != false {
		t.Fatalf("staff item = %v", items[0])
	}
}

func TestEdgeDataNeedsDevice(t *testing.T) {
	x := newEdgeEnv(t)
	x.owner.hdr = http.Header{"Rangka-Edge-Sync": {"v1"}}
	for _, p := range []string{"/api/v1/edge/config", "/api/v1/edge/data/menu"} {
		if r := x.owner.do("GET", p, nil); r.Status != 401 {
			t.Fatalf("session %s = %d %s", p, r.Status, r.Body)
		}
	}
	anon := x.owner.device()
	anon.hdr = http.Header{"Rangka-Edge-Sync": {"v1"}}
	anon.must(401, "GET", "/api/v1/edge/config", nil)
	// a revoked device is told so
	id := x.owner.must(200, "GET", "/api/v1/devices", nil).JSON["items"].([]any)[0].(map[string]any)["id"].(string)
	x.owner.must(204, "DELETE", "/api/v1/devices/"+id, nil)
	if r := x.dev.do("GET", "/api/v1/edge/data/menu", nil); r.Status != 401 || get(r.JSON, "error", "code") != "device_revoked" {
		t.Fatalf("revoked = %d %s", r.Status, r.Body)
	}
}

// edgeMemberRole adds a member with role, limited to outlets.
func edgeMemberRole(e *env, email, role string, outlets ...string) int64 {
	e.t.Helper()
	ctx := context.Background()
	id, err := auth.EnsureUser(ctx, e.s.DB, email, "Staf "+email, "rahasia-123", "kopi-senja", role, "")
	if err != nil {
		e.t.Fatal(err)
	}
	if err := auth.SetOutlets(ctx, e.s.DB, "kopi-senja", id, outlets); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func strs(v any) []string {
	var out []string
	l, _ := v.([]any)
	for _, x := range l {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}
