package server

import (
	"context"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// A flow's idempotency: is enforced (contract 0002 §A.2): the same key runs the flow once, an empty key fails the
// run, and a flow a connector event starts may be run by hand only by the owner (card t_6271ac78, audit rel uang B).
func TestFlowIdempotencyAndConnectorRun(t *testing.T) {
	e, _ := runtimeEnv(t)
	ev := map[string]any{"message_id": "wamid.IDEM1", "from": "6281277770001", "profile_name": "Sari", "text": "halo"}
	e.must(200, "POST", "/api/v1/flows/whatsapp_to_lead/run", map[string]any{"event": ev})
	again := e.must(200, "POST", "/api/v1/flows/whatsapp_to_lead/run", map[string]any{"event": ev}).JSON
	if again["skipped"] != true {
		t.Fatalf("second run with the same key: %v", again)
	}
	if n := leadsByPhone(t, e, "6281277770001"); n != 1 {
		t.Fatalf("leads after the same key twice = %d", n)
	}
	acts := 0
	for _, r := range rowsOf(e.must(200, "GET", "/api/v1/r/activity?limit=500", nil)) {
		if get(r, "notes") == "halo" {
			acts++
		}
	}
	if acts != 1 {
		t.Fatalf("activities after the same key twice = %d", acts)
	}
	noKey := map[string]any{"from": "6281277770002", "profile_name": "Tono", "text": "tanpa id"}
	if r := e.do("POST", "/api/v1/flows/whatsapp_to_lead/run", map[string]any{"event": noKey}); r.Status != 422 {
		t.Fatalf("run without a key: %d %s", r.Status, r.Body)
	}
	for _, bad := range []any{[]any{}, map[string]any{}, 1.5, true} {
		ev := map[string]any{"message_id": bad, "from": "6281277770002", "profile_name": "Tono", "text": "kunci aneh"}
		if r := e.do("POST", "/api/v1/flows/whatsapp_to_lead/run", map[string]any{"event": ev}); r.Status != 422 {
			t.Fatalf("run with key %#v: %d %s", bad, r.Status, r.Body)
		}
	}
	if n := leadsByPhone(t, e, "6281277770002"); n != 0 {
		t.Fatalf("a run without its key wrote %d leads", n)
	}

	if _, err := auth.EnsureUser(context.Background(), e.s.DB, "manajer@example.test", "Manajer", "rangka-demo", "kopi-senja", "sales_manager", ""); err != nil {
		t.Fatal(err)
	}
	e.loginAs("manajer@example.test")
	other := map[string]any{"message_id": "wamid.IDEM2", "from": "6281277770003", "profile_name": "Palsu", "text": "palsu"}
	if r := e.do("POST", "/api/v1/flows/whatsapp_to_lead/run", map[string]any{"event": other}); r.Status != 403 {
		t.Fatalf("a non-owner ran a connector flow by hand: %d %s", r.Status, r.Body)
	}
}
