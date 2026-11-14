package server

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// POST /api/v1/edge/events (contract 0057 §4, 0006 §3) over testdata/device-apps: penjualan (submittable) and
// sesi_kasir named on the device, outlets OUT-01 (code OUT01) and OUT-02 (OUT02).

type edgeEnv struct {
	owner, dev *env
	kasir      int64 // a kasir of OUT-01
	menu       string
}

func newEdgeEnv(t *testing.T) *edgeEnv {
	t.Helper()
	owner := newEnvFrom(t, func(dir string) (string, string) {
		if out, err := exec.Command("cp", "-r", "../../testdata/device-apps/.", dir).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return filepath.Join(dir, "apps"), filepath.Join(dir, "site")
	})
	owner.login()
	kasir := edgeMember(owner, "kasir@toko.test", "OUT-01")
	menu := owner.must(201, "POST", "/api/v1/r/menu", map[string]any{"menu_name": "Kopi susu", "price": 18000}).JSON["name"].(string)
	dev, _ := owner.pair("OUT-01", "Kasir 1")
	dev.hdr = http.Header{"Rangka-Edge-Sync": {"v1"}}
	return &edgeEnv{owner: owner, dev: dev, kasir: kasir, menu: menu}
}

// edgeMember adds a kasir with a password, limited to outlets.
func edgeMember(e *env, email string, outlets ...string) int64 {
	e.t.Helper()
	ctx := context.Background()
	id, err := auth.EnsureUser(ctx, e.s.DB, email, "Kasir "+email, "rahasia-123", "kopi-senja", "kasir", "")
	if err != nil {
		e.t.Fatal(err)
	}
	if err := auth.SetOutlets(ctx, e.s.DB, "kopi-senja", id, outlets); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// ev is one event by actor at occurred.
func ev(key string, seq int, kind string, actor int64, occurred time.Time, payload map[string]any) map[string]any {
	return map[string]any{"key": key, "kind": kind, "seq": seq, "occurred_at": occurred.UTC().Format(time.RFC3339), "actor": actor, "payload": payload}
}

// write is a screen.write event of the env's kasir at fixedNow.
func (x *edgeEnv) write(key string, seq int, payload map[string]any) map[string]any {
	return ev(key, seq, "screen.write", x.kasir, fixedNow(), payload)
}

func (x *edgeEnv) shiftEv(key string, seq int, name string) map[string]any {
	return x.write(key, seq, map[string]any{"screen": "jual", "model": "sesi_kasir", "op": "create", "name": name,
		"body": map[string]any{"outlet": "OUT-01", "opening_cash": 100000}})
}

func (x *edgeEnv) saleBody(shift string, qty int) map[string]any {
	return map[string]any{"shift": shift, "outlet": "OUT-01", "items": []any{map[string]any{"menu": x.menu, "qty": qty}}}
}

// paidSaleEv creates and submits a sale in one event.
func (x *edgeEnv) paidSaleEv(key string, seq int, shift, name string, qty int) map[string]any {
	return x.write(key, seq, map[string]any{"screen": "jual", "model": "penjualan", "op": "create", "name": name, "submit": true,
		"body": x.saleBody(shift, qty)})
}

// send posts events and returns the results.
func (x *edgeEnv) send(events ...map[string]any) []any {
	x.owner.t.Helper()
	r := x.dev.must(200, "POST", "/api/v1/edge/events", map[string]any{"events": events})
	res, _ := r.JSON["results"].([]any)
	if len(res) != len(events) {
		x.owner.t.Fatalf("results %s", r.Body)
	}
	return res
}

func wantResult(t *testing.T, res any, key, status, code string) {
	t.Helper()
	if get(res, "key") != key || get(res, "status") != status || (code != "" && get(res, "code") != code) {
		t.Fatalf("result = %v, want %s %s %s", res, key, status, code)
	}
	if status == "rejected" {
		if m, _ := get(res, "message").(string); m == "" {
			t.Fatalf("rejected without message: %v", res)
		}
	}
}

func (x *edgeEnv) count(model string) float64 {
	return x.owner.must(200, "GET", "/api/v1/r/"+model, nil).JSON["total"].(float64)
}

func TestEventsCreateSubmitSale(t *testing.T) {
	x := newEdgeEnv(t)
	res := x.send(
		x.shiftEv("k-1", 1, "SESI-OUT01-K1-2610-001"),
		x.paidSaleEv("k-2", 2, "SESI-OUT01-K1-2610-001", "STRK-OUT01-K1-2610-00001", 1),
		x.write("k-3", 3, map[string]any{"screen": "jual", "model": "penjualan", "op": "create", "body": x.saleBody("SESI-OUT01-K1-2610-001", 2)}),
		x.write("k-4", 4, map[string]any{"model": "penjualan", "op": "submit", "name": "STRK-OUT01-K1-2610-00002", "draft": "k-3"}),
	)
	wantResult(t, res[0], "k-1", "accepted", "")
	if get(res[0], "ref") != "SESI-OUT01-K1-2610-001" || get(res[0], "record", "name") != "SESI-OUT01-K1-2610-001" {
		t.Fatalf("shift = %v", res[0])
	}
	wantResult(t, res[1], "k-2", "accepted", "")
	if get(res[1], "ref") != "STRK-OUT01-K1-2610-00001" || get(res[1], "record", "docstatus") != 1.0 || get(res[1], "record", "grand_total") != 18000.0 {
		t.Fatalf("paid sale = %v", res[1])
	}
	wantResult(t, res[2], "k-3", "accepted", "")
	draft, _ := get(res[2], "ref").(string)
	if draft == "" || get(res[2], "record", "docstatus") != 0.0 {
		t.Fatalf("draft = %v", res[2])
	}
	wantResult(t, res[3], "k-4", "accepted", "")
	if get(res[3], "ref") != "STRK-OUT01-K1-2610-00002" || get(res[3], "record", "docstatus") != 1.0 {
		t.Fatalf("submitted draft = %v", res[3])
	}
	// a draft name the device got online works too; an unknown draft key is payload_invalid
	res = x.send(
		x.write("k-5", 5, map[string]any{"model": "penjualan", "op": "create", "body": x.saleBody("SESI-OUT01-K1-2610-001", 1)}),
	)
	online, _ := get(res[0], "ref").(string)
	res = x.send(
		x.write("k-6", 6, map[string]any{"model": "penjualan", "op": "submit", "name": "STRK-OUT01-K1-2610-00003", "draft": online}),
		x.write("k-7", 7, map[string]any{"model": "penjualan", "op": "submit", "name": "STRK-OUT01-K1-2610-00004", "draft": "no-such-key"}),
	)
	wantResult(t, res[0], "k-6", "accepted", "")
	wantResult(t, res[1], "k-7", "rejected", "payload_invalid")
}

func TestEventsDuplicate(t *testing.T) {
	x := newEdgeEnv(t)
	batch := []map[string]any{x.shiftEv("k-1", 1, "SESI-OUT01-K1-2610-001"), x.paidSaleEv("k-2", 2, "SESI-OUT01-K1-2610-001", "STRK-OUT01-K1-2610-00001", 1)}
	x.send(batch...)
	res := x.send(batch...)
	wantResult(t, res[0], "k-1", "duplicate", "")
	wantResult(t, res[1], "k-2", "duplicate", "")
	if get(res[1], "ref") != "STRK-OUT01-K1-2610-00001" || get(res[1], "record", "name") != "STRK-OUT01-K1-2610-00001" {
		t.Fatalf("duplicate = %v", res[1])
	}
	if n := x.count("penjualan"); n != 1 {
		t.Fatalf("%v sales, want 1", n)
	}
	// a rejected key replayed answers the same rejection
	bad := ev("k-3", 3, "sale.recorded", x.kasir, fixedNow(), map[string]any{})
	first := x.send(bad)[0]
	wantResult(t, first, "k-3", "rejected", "unknown_kind")
	again := x.send(bad)[0]
	wantResult(t, again, "k-3", "rejected", "unknown_kind")
	if get(again, "message") != get(first, "message") {
		t.Fatalf("replayed rejection message %v, first %v", get(again, "message"), get(first, "message"))
	}
	// an engine refusal replayed keeps its message too; the answer carries the trimmed key
	dup := x.paidSaleEv(" k-4 ", 4, "SESI-OUT01-K1-2610-001", "STRK-OUT01-K1-2610-00001", 1)
	first = x.send(dup)[0]
	wantResult(t, first, "k-4", "rejected", "duplicate_name")
	again = x.send(dup)[0]
	wantResult(t, again, "k-4", "rejected", "duplicate_name")
	if get(again, "message") != get(first, "message") {
		t.Fatalf("replayed refusal message %v, first %v", get(again, "message"), get(first, "message"))
	}
}

func TestEventsKeyConflict(t *testing.T) {
	x := newEdgeEnv(t)
	x.send(x.shiftEv("k-1", 1, "SESI-OUT01-K1-2610-001"), x.paidSaleEv("k-2", 2, "SESI-OUT01-K1-2610-001", "STRK-OUT01-K1-2610-00001", 1))
	res := x.send(x.paidSaleEv("k-2", 2, "SESI-OUT01-K1-2610-001", "STRK-OUT01-K1-2610-00001", 3))
	wantResult(t, res[0], "k-2", "rejected", "key_conflict")
	rec := x.owner.must(200, "GET", "/api/v1/r/penjualan/STRK-OUT01-K1-2610-00001", nil).JSON
	if rec["grand_total"] != 18000.0 {
		t.Fatalf("record changed: %v", rec)
	}
}

func TestEventsBatchOrder(t *testing.T) {
	x := newEdgeEnv(t)
	res := x.send(
		x.shiftEv("k-1", 1, "SESI-OUT01-K1-2610-001"),
		x.write("k-2", 2, map[string]any{"model": "penjualan", "op": "create", "name": "STRK-OUT01-K1-2610-00001", "submit": true,
			"body": map[string]any{"shift": "SESI-OUT01-K1-2610-001", "outlet": "OUT-01"}}), // no lines: min_rows
		x.shiftEv("k-3", 3, "SESI-OUT01-K1-2610-002"),
	)
	wantResult(t, res[0], "k-1", "accepted", "")
	wantResult(t, res[1], "k-2", "rejected", "validation_failed")
	wantResult(t, res[2], "k-3", "accepted", "")
}

func TestEventsActorNotInOutlet(t *testing.T) {
	x := newEdgeEnv(t)
	other := edgeMember(x.owner, "dago@toko.test", "OUT-02")
	e := x.shiftEv("k-1", 1, "SESI-OUT01-K1-2610-001")
	e["actor"] = other
	unknown := x.shiftEv("k-2", 2, "SESI-OUT01-K1-2610-001")
	unknown["actor"] = 987654
	// a desk role (no desk: false) and the owner never unlock a device, so they never press its buttons (0057 §2)
	ctx := context.Background()
	desk, err := auth.EnsureUser(ctx, x.owner.s.DB, "kantor@toko.test", "Kantor", "rahasia-123", "kopi-senja", "head_office", "")
	if err != nil {
		t.Fatal(err)
	}
	q, err := x.owner.s.DB.Query(ctx, "select id from rk_users where email = 'ceo@saga.co.id'")
	if err != nil || len(q.Rows) != 1 {
		t.Fatalf("owner id: %v", err)
	}
	owner := q.Rows[0][0].(int64)
	for _, id := range []int64{desk, owner} {
		if err := auth.SetOutlets(ctx, x.owner.s.DB, "kopi-senja", id, []string{"OUT-01"}); err != nil {
			t.Fatal(err)
		}
	}
	deskEv := x.shiftEv("k-3", 3, "SESI-OUT01-K1-2610-001")
	deskEv["actor"] = desk
	ownerEv := x.shiftEv("k-4", 4, "SESI-OUT01-K1-2610-001")
	ownerEv["actor"] = owner
	res := x.send(e, unknown, deskEv, ownerEv)
	wantResult(t, res[0], "k-1", "rejected", "actor_not_allowed")
	wantResult(t, res[1], "k-2", "rejected", "actor_not_allowed")
	wantResult(t, res[2], "k-3", "rejected", "actor_not_allowed")
	wantResult(t, res[3], "k-4", "rejected", "actor_not_allowed")
	if n := x.count("sesi_kasir"); n != 0 {
		t.Fatalf("%v shifts", n)
	}
}

func TestEventsUnknownKind(t *testing.T) {
	x := newEdgeEnv(t)
	res := x.send(
		ev("k-1", 1, "sale.recorded", x.kasir, fixedNow(), map[string]any{}),
		ev("k-2", 2, "print.done", x.kasir, fixedNow(), map[string]any{"job": "p1"}),
		ev("k-3", 3, "device.read", x.kasir, fixedNow(), map[string]any{"raw": "x"}),
		x.write("k-4", 4, map[string]any{"model": "penjualan", "op": "explode"}),
	)
	wantResult(t, res[0], "k-1", "rejected", "unknown_kind")
	wantResult(t, res[1], "k-2", "accepted", "")
	wantResult(t, res[2], "k-3", "accepted", "")
	wantResult(t, res[3], "k-4", "rejected", "payload_invalid")
}

func TestEventsVersionHeader(t *testing.T) {
	x := newEdgeEnv(t)
	x.dev.hdr = nil
	r := x.dev.must(400, "POST", "/api/v1/edge/events", map[string]any{"events": []any{}})
	sup, _ := get(r.JSON, "error", "supported").([]any)
	if errCodeOf(r) != "unsupported_sync_version" || len(sup) != 1 || sup[0] != "v1" {
		t.Fatalf("no header: %s", r.Body)
	}
	x.dev.hdr = http.Header{"Rangka-Edge-Sync": {"v2"}}
	x.dev.must(400, "POST", "/api/v1/edge/events", map[string]any{"events": []any{}})
}

func TestEventsTooOld(t *testing.T) {
	x := newEdgeEnv(t)
	old := ev("k-1", 1, "screen.write", x.kasir, fixedNow().Add(-31*24*time.Hour), map[string]any{"model": "sesi_kasir", "op": "create",
		"name": "SESI-OUT01-K1-2609-001", "body": map[string]any{"outlet": "OUT-01", "opening_cash": 1}})
	ahead := ev("k-2", 2, "print.done", x.kasir, fixedNow().Add(10*time.Minute), map[string]any{})
	res := x.send(old, ahead)
	wantResult(t, res[0], "k-1", "rejected", "occurred_at_too_old")
	wantResult(t, res[1], "k-2", "rejected", "payload_invalid")
}

func TestEventsCancelVoid(t *testing.T) {
	x := newEdgeEnv(t)
	res := x.send(
		x.shiftEv("k-1", 1, "SESI-OUT01-K1-2610-001"),
		x.paidSaleEv("k-2", 2, "SESI-OUT01-K1-2610-001", "STRK-OUT01-K1-2610-00001", 1),
		x.write("k-3", 3, map[string]any{"model": "penjualan", "op": "cancel", "name": "STRK-OUT01-K1-2610-00001"}),
	)
	wantResult(t, res[2], "k-3", "accepted", "")
	if get(res[2], "ref") != "STRK-OUT01-K1-2610-00001" {
		t.Fatalf("cancel = %v", res[2])
	}
	rec := x.owner.must(200, "GET", "/api/v1/r/penjualan/STRK-OUT01-K1-2610-00001", nil).JSON
	if rec["docstatus"] != 2.0 {
		t.Fatalf("not cancelled: %v", rec)
	}
}

func TestEventsMaxItems(t *testing.T) {
	x := newEdgeEnv(t)
	var events []map[string]any
	for i := 1; i <= 101; i++ {
		events = append(events, ev(fmt.Sprintf("k-%d", i), i, "print.done", x.kasir, fixedNow(), map[string]any{}))
	}
	r := x.dev.must(400, "POST", "/api/v1/edge/events", map[string]any{"events": events})
	if errCodeOf(r) != "too_many_events" {
		t.Fatalf("101 events: %s", r.Body)
	}
	x.send(events[:100]...)
}

func TestEventsAuditViaDevice(t *testing.T) {
	x := newEdgeEnv(t)
	at := fixedNow().Add(-2 * time.Hour)
	e := x.paidSaleEv("k-2", 2, "SESI-OUT01-K1-2610-001", "STRK-OUT01-K1-2610-00001", 1)
	e["occurred_at"] = at.UTC().Format(time.RFC3339)
	x.send(x.shiftEv("k-1", 1, "SESI-OUT01-K1-2610-001"), e)
	res, err := x.owner.s.DB.Query(context.Background(), "select kind, data from rk_audit where target = 'penjualan/STRK-OUT01-K1-2610-00001'")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) == 0 {
		t.Fatal("no audit rows")
	}
	for _, r := range res.Rows {
		d, _ := r[1].(map[string]any)
		if d["via"] != "device" || d["device"] != "K1" || d["occurred_at"] != at.UTC().Format(time.RFC3339) {
			t.Fatalf("%v audit data = %v", r[0], d)
		}
	}
}

// Who may send: a paired device (cookie or token) or a PIN session on one; not a full session, not a revoked device.
func TestEventsPrincipal(t *testing.T) {
	x := newEdgeEnv(t)
	x.owner.hdr = http.Header{"Rangka-Edge-Sync": {"v1"}}
	x.owner.must(401, "POST", "/api/v1/edge/events", map[string]any{"events": []any{}})
	// a PIN session on the paired browser
	withPin(x.owner, "kasir@toko.test", "1357")
	x.dev.must(200, "POST", "/auth/device/unlock", unlockBody(x.kasir, "1357"))
	wantResult(t, x.send(x.shiftEv("k-1", 1, "SESI-OUT01-K1-2610-001"))[0], "k-1", "accepted", "")
	// rangka-edge with its token
	code := x.owner.pairingCode("OUT-01")
	reg := x.owner.device().must(201, "POST", "/api/v1/edge/register", map[string]any{"pairing_code": code, "device": map[string]any{"name": "Edge"}})
	edge := x.owner.device()
	edge.token, edge.hdr = reg.JSON["token"].(string), http.Header{"Rangka-Edge-Sync": {"v1"}}
	r := edge.must(200, "POST", "/api/v1/edge/events", map[string]any{"events": []any{x.shiftEv("k-2", 1, "SESI-OUT01-K2-2610-001")}})
	wantResult(t, get(r.JSON, "results", 0), "k-2", "accepted", "")
	// revoked
	x.owner.must(204, "DELETE", "/api/v1/devices/"+reg.JSON["device_id"].(string), nil)
	if r := edge.must(401, "POST", "/api/v1/edge/events", map[string]any{"events": []any{}}); errCodeOf(r) != "device_revoked" {
		t.Fatalf("revoked: %s", r.Body)
	}
}
