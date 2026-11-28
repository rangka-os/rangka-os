package engine

import (
	"context"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

const queueDashboard = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/dashboard.json
name: antrean
label: {id: Antrean}
cache: 5m
widgets:
  - {name: setuju_daftar, type: list, source: approvals, label: {id: Menunggu persetujuanmu}, limit: 5}
  - {name: setuju_jumlah, type: number, source: approvals, label: {id: Persetujuan menunggu}}
  - {name: kecuali_daftar, type: list, source: exceptions, label: {id: Pengecualian}, limit: 1}
  - {name: kecuali_jumlah, type: number, source: exceptions, label: {id: Pengecualian terbuka}}
`

// Widgets with source: approvals and exceptions show only what the viewer may act on, and a viewer who may act on
// nothing of the kind gets a hidden widget (never someone else's work).
func TestDashboardQueueWidgets(t *testing.T) {
	e, owner := approvalSetupFiles(t, map[string]string{"crm/dashboards/antrean.rk": queueDashboard}, managerPolicy)
	ctx := context.Background()
	heldInvoice(t, e)
	for _, k := range []string{"negative_stock", "edge_price_drift"} {
		if err := store.RecordException(ctx, e.DB, store.Exception{Org: "kopi-senja", Outlet: "OUT-01", Device: "dev_x", Kind: k, Ref: "BRG-0044"}); err != nil {
			t.Fatal(err)
		}
	}
	board := func(a Actor) map[string]any {
		t.Helper()
		d, _, err := e.Dashboard(ctx, a, "antrean", 0)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	// the owner decides everything: one request, two open exceptions (the list is cut at its limit, the total is not)
	d := board(owner)
	if w := widgetOut(t, d, "setuju_jumlah"); w["value"] != 1 || w["count"] != 1 || w["error"] != nil {
		t.Fatalf("owner approvals number: %v", w)
	}
	w := widgetOut(t, d, "setuju_daftar")
	rows, _ := w["rows"].([]any)
	if len(rows) != 1 || w["total"] != 1 {
		t.Fatalf("owner approvals list: %v", w)
	}
	row := rows[0].(map[string]any)
	if row["model"] != "sales_invoice" || row["can_decide"] != true || row["requested_by"] != "rep@x" || row["id"] == "" {
		t.Fatalf("approval row: %v", row)
	}
	if sum, _ := row["summary"].(map[string]any); sum == nil {
		t.Fatalf("a listed approval carries its summary: %v", row)
	}
	w = widgetOut(t, d, "kecuali_daftar")
	if rows, _ := w["rows"].([]any); len(rows) != 1 || w["total"] != 2 {
		t.Fatalf("exceptions list (limit 1, total 2): %v", w)
	}
	if w := widgetOut(t, d, "kecuali_jumlah"); w["value"] != 2 {
		t.Fatalf("exceptions number: %v", w)
	}

	// the approver sees the request, not the exceptions; the requester decides nothing, so sees nothing
	d = board(mgr)
	if w := widgetOut(t, d, "setuju_jumlah"); w["value"] != 1 {
		t.Fatalf("approver number: %v", w)
	}
	if w := widgetOut(t, d, "kecuali_jumlah"); w["hidden"] != true {
		t.Fatalf("exceptions are the owner's and head office's: %v", w)
	}
	d = board(rep)
	if w := widgetOut(t, d, "setuju_jumlah"); w["value"] != 0 {
		t.Fatalf("a requester who cannot decide has nothing waiting: %v", w)
	}
	if w := widgetOut(t, d, "setuju_daftar"); w["total"] != 0 {
		t.Fatalf("empty list: %v", w)
	}

	// head office sees the exceptions; a token (not human) sees neither queue, even as owner
	hq := Actor{Org: "kopi-senja", ID: "hq@x", Roles: []string{"head_office"}, Human: true}
	if w := widgetOut(t, board(hq), "kecuali_jumlah"); w["value"] != 2 {
		t.Fatalf("head office exceptions: %v", w)
	}
	tok := owner
	tok.Human = false
	d = board(tok)
	for _, n := range []string{"setuju_daftar", "setuju_jumlah", "kecuali_daftar", "kecuali_jumlah"} {
		if w := widgetOut(t, d, n); w["hidden"] != true {
			t.Fatalf("%s for a token: %v", n, w)
		}
	}

	// the dashboard has cache: 5m, but a queue changes without a model write: a decision shows at once
	ap := pendingFor(t, e, owner, "sales_invoice", "")
	if len(ap) != 1 {
		t.Fatalf("pending: %v", ap)
	}
	if _, err := e.Approve(ctx, mgr, ap[0].ID); err != nil {
		t.Fatal(err)
	}
	if w := widgetOut(t, board(owner), "setuju_jumlah"); w["value"] != 0 {
		t.Fatalf("after approval the queue is empty (never cached): %v", w)
	}
}
