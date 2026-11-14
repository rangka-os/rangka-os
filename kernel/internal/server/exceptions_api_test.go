package server

import (
	"context"
	"strconv"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Head-office exceptions (contract 0058 §10).

func recordEx(t *testing.T, e *env, org, outlet, kind, ref string) {
	t.Helper()
	if err := store.RecordException(context.Background(), e.s.DB, store.Exception{Org: org, Outlet: outlet, Device: "dev_x", Kind: kind, Ref: ref,
		Detail: map[string]any{"n": 1}}); err != nil {
		t.Fatal(err)
	}
}

func openItems(e *env, path string) []any {
	e.t.Helper()
	return e.must(200, "GET", path, nil).JSON["items"].([]any)
}

func TestExceptionsListAndResolve(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	recordEx(t, e, "kopi-senja", "OUT-01", "negative_stock", "ITM-1")
	recordEx(t, e, "kopi-senja", "OUT-02", "edge_price_drift", "INV-1")
	items := openItems(e, "/api/v1/exceptions?open=1")
	if len(items) != 2 {
		t.Fatalf("open = %d", len(items))
	}
	if n := len(openItems(e, "/api/v1/exceptions?outlet=OUT-01")); n != 1 {
		t.Fatalf("outlet filter = %d", n)
	}
	if n := len(openItems(e, "/api/v1/exceptions?kind=edge_price_drift")); n != 1 {
		t.Fatalf("kind filter = %d", n)
	}
	id := int64(get(items, 0, "id").(float64))
	e.must(200, "POST", "/api/v1/exceptions/"+strconv.FormatInt(id, 10)+"/resolve", nil)
	if n := len(openItems(e, "/api/v1/exceptions?open=1")); n != 1 {
		t.Fatalf("open after resolve = %d", n)
	}
	all := openItems(e, "/api/v1/exceptions")
	if len(all) != 2 {
		t.Fatalf("all = %d", len(all))
	}
	var done map[string]any
	for _, it := range all {
		if m := it.(map[string]any); int64(m["id"].(float64)) == id {
			done = m
		}
	}
	if done["resolved_by"] != "ceo@saga.co.id" || done["resolved_at"] == nil || done["kind"] == "" || done["detail"] == nil {
		t.Fatalf("resolved row = %v", done)
	}
	e.must(404, "POST", "/api/v1/exceptions/999999/resolve", nil)
	// an API token of the owner reads but does not resolve
	tok := personToken(e, "ceo@saga.co.id", "kopi-senja")
	tok.must(200, "GET", "/api/v1/exceptions", nil)
	tok.must(403, "POST", "/api/v1/exceptions/"+strconv.FormatInt(id, 10)+"/resolve", nil)
}

func TestExceptionsForbiddenToCashier(t *testing.T) {
	e := pinEnv(t)
	e.login()
	recordEx(t, e, "kopi-senja", "OUT-01", "negative_stock", "ITM-1")
	id := staffMember(e, "kasir@kopisenja.id", "sales_rep", "OUT-01")
	// a staff member signed in fully is not head office
	b := e.device()
	b.must(200, "POST", "/auth/login", map[string]any{"email": "kasir@kopisenja.id", "password": "rahasia-123"})
	b.must(403, "GET", "/api/v1/exceptions", nil)
	b.must(403, "POST", "/api/v1/exceptions/1/resolve", nil)
	// and neither on a PIN session at a paired device
	withPin(e, "kasir@kopisenja.id", "1357")
	dev, _ := e.pair("OUT-01", "Kasir depan")
	dev.must(200, "POST", "/auth/device/unlock", unlockBody(id, "1357"))
	if v := dev.must(200, "GET", "/auth/me", nil).JSON["via"]; v != "pin" {
		t.Fatalf("via = %v", v)
	}
	dev.must(403, "GET", "/api/v1/exceptions", nil)
	dev.must(403, "POST", "/api/v1/exceptions/1/resolve", nil)
}

func TestExceptionsOrgScoped(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	ctx := context.Background()
	if err := auth.EnsureOrgCode(ctx, e.s.DB, "pt_beta", "PT Beta", "BETA"); err != nil {
		t.Fatal(err)
	}
	recordEx(t, e, "pt_beta", "X-1", "negative_stock", "ITM-9")
	recordEx(t, e, "kopi-senja", "OUT-01", "negative_stock", "ITM-1")
	items := openItems(e, "/api/v1/exceptions")
	if len(items) != 1 || get(items, 0, "ref") != "ITM-1" {
		t.Fatalf("items = %v", items)
	}
	id := dbOne(e, "select id from rk_exceptions where org_id = 'pt_beta'").(int64)
	e.must(404, "POST", "/api/v1/exceptions/"+strconv.FormatInt(id, 10)+"/resolve", nil)
	if dbOne(e, "select resolved_at from rk_exceptions where org_id = 'pt_beta'") != nil {
		t.Fatal("other org's exception was resolved")
	}
}
