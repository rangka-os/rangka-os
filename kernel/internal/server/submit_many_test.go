package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// POST /api/v1/r/{model}/submit submits several drafts, each on its own: one is submitted, one is held for
// approval, an unknown name fails, and a retry answers not_draft for the one already submitted (card t_35afb0bc).
func TestSubmitMany(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(apps, "sales", "models", "sales_invoice.rk")
		b, _ := os.ReadFile(p)
		b = append(b, []byte("approvals:\n  - name: manager\n    on: submit\n    when: \"grand_total > 1000000\"\n    approvers: [sales_manager]\n    label: {id: Persetujuan manajer}\n")...)
		os.WriteFile(p, b, 0o644)
	})
	if _, err := auth.EnsureUser(context.Background(), e.s.DB, "rep@kopisenja.id", "Sales", "rahasia-123", "kopi-senja", "sales_rep", "USR-003"); err != nil {
		t.Fatal(err)
	}
	rep := e.userEnv("rep@kopisenja.id", "rahasia-123")
	draft := func(qty int) string {
		return rep.must(201, "POST", "/api/v1/r/sales_invoice", map[string]any{"customer": "CUST-0012", "outlet": "OUT-01",
			"items": []any{map[string]any{"item": "BRG-0044", "qty": qty}}}).JSON["name"].(string)
	}
	small, big := draft(1), draft(40)
	out := rep.must(200, "POST", "/api/v1/r/sales_invoice/submit", map[string]any{"names": []any{small, big, "DRAFT-TIDAKADA", small}, "force": true}).JSON
	res, _ := out["results"].([]any)
	if len(res) != 3 || out["submitted"] != 1.0 || out["held"] != 1.0 || out["failed"] != 1.0 {
		t.Fatalf("submit many: %v", out)
	}
	if get(res[0], "name") != small || get(res[0], "status") != 200.0 || get(res[0], "new_name") == small || get(res[0], "new_name") == nil {
		t.Fatalf("first: %v", res[0])
	}
	if get(res[1], "status") != 202.0 || get(res[1], "approval", "state") != "pending" {
		t.Fatalf("second: %v", res[1])
	}
	if get(res[2], "status") != 404.0 || get(res[2], "error", "code") != "not_found" {
		t.Fatalf("third: %v", res[2])
	}
	got := rep.must(200, "GET", "/api/v1/r/sales_invoice/"+get(res[0], "new_name").(string), nil).JSON
	if got["docstatus"] != 1.0 {
		t.Fatalf("submitted record: %v", got)
	}
	again := rep.must(200, "POST", "/api/v1/r/sales_invoice/submit", map[string]any{"names": []any{get(res[0], "new_name")}}).JSON
	if get(again, "results", 0, "status") != 409.0 || get(again, "results", 0, "error", "code") != "not_draft" {
		t.Fatalf("retry: %v", again)
	}
	if r := rep.do("POST", "/api/v1/r/sales_invoice/submit", map[string]any{"names": []any{}}); r.Status != 400 {
		t.Fatalf("no names: %d", r.Status)
	}
}
