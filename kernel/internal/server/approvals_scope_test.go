package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// Approvals under multi-org and row scope (contracts 0007 and 0010).

// outletApprovalPolicy holds every sales invoice submit for an outlet_staff approver.
func outletApprovalPolicy(apps, _ string) {
	p := filepath.Join(apps, "sales", "models", "sales_invoice.rk")
	b, err := os.ReadFile(p)
	if err != nil {
		panic(err)
	}
	b = append(b, []byte("approvals:\n  - name: outlet\n    on: submit\n    when: \"grand_total > 0\"\n    approvers: [outlet_staff]\n    label: {id: Persetujuan outlet}\n")...)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		panic(err)
	}
}

// heldSubmit creates a draft invoice for outlet as u and submits it; the submit waits for approval.
func heldSubmit(t *testing.T, u *env, outlet string) string {
	t.Helper()
	draft := u.must(201, "POST", "/api/v1/r/sales_invoice", map[string]any{"customer": "CUST-0012", "outlet": outlet,
		"items": []any{map[string]any{"item": "BRG-0044", "qty": 2}}}).JSON["name"].(string)
	held := u.must(202, "POST", "/api/v1/r/sales_invoice/"+draft+"/submit", map[string]any{"force": true}).JSON
	return get(held, "approval", "id").(string)
}

func approvalIDs(r *resp) map[string]bool {
	out := map[string]bool{}
	for _, x := range rowsOf(r) {
		out[x.(map[string]any)["id"].(string)] = true
	}
	return out
}

// TestApprovalScopedOtherOutlet: an outlet approver does not see or decide another outlet's request: not in
// the list, 403 on GET, approve and reject, and no SSE event; their own outlet's request works.
func TestApprovalScopedOtherOutlet(t *testing.T) {
	owner, staff := scopedEnv(t, []string{"OUT-01"}, outletApprovalPolicy)
	if _, err := auth.EnsureUser(context.Background(), owner.s.DB, "rep@kopisenja.id", "Sales", "rahasia-123", "kopi-senja", "sales_rep", "USR-003"); err != nil {
		t.Fatal(err)
	}
	rep := owner.userEnv("rep@kopisenja.id", "rahasia-123")
	events := approvalEvents(t, staff)
	time.Sleep(200 * time.Millisecond) // the stream is subscribed before the requests are made

	other := heldSubmit(t, rep, "OUT-02")
	own := heldSubmit(t, rep, "OUT-01")

	// SSE: the first approval event the member gets is their own outlet's (events keep their order)
	if ev := waitApprovalEvent(t, events, "pending"); ev["id"] != own {
		t.Fatalf("approval event of another outlet delivered: %v", ev)
	}

	l := staff.must(200, "GET", "/api/v1/approvals?state=pending", nil)
	if ids := approvalIDs(l); ids[other] || !ids[own] || l.JSON["total"] != 1.0 {
		t.Fatalf("scoped list: %s", l.Body)
	}
	if ids := approvalIDs(owner.must(200, "GET", "/api/v1/approvals?state=pending", nil)); !ids[other] || !ids[own] {
		t.Fatalf("owner list lacks requests: %v", ids)
	}
	assertDenied(t, staff.do("GET", "/api/v1/approvals/"+other, nil), "sales_invoice", "read")
	assertDenied(t, staff.do("POST", "/api/v1/approvals/"+other+"/approve", nil), "sales_invoice", "approve")
	assertDenied(t, staff.do("POST", "/api/v1/approvals/"+other+"/reject", map[string]any{"reason": "bukan outlet saya"}), "sales_invoice", "reject")
	if st := get(owner.must(200, "GET", "/api/v1/approvals/"+other, nil).JSON, "state"); st != "pending" {
		t.Fatalf("other outlet's request changed: %v", st)
	}

	// MCP follows the same rules
	res := rpc(staff, 11, "tools/call", map[string]any{"name": "rangka_approvals_list", "arguments": map[string]any{"state": "pending"}})
	if data, _ := get(res, "structuredContent", "data").([]any); len(data) != 1 || get(data[0], "id") != own {
		t.Fatalf("MCP list: %v", res)
	}
	res = rpc(staff, 12, "tools/call", map[string]any{"name": "rangka_approve", "arguments": map[string]any{"id": other, "decision": "approve"}})
	if res["isError"] != true || get(res, "structuredContent", "error", "code") != "permission_denied" {
		t.Fatalf("MCP approve of another outlet's request: %v", res)
	}

	// the member's own outlet
	staff.must(200, "GET", "/api/v1/approvals/"+own, nil)
	if d := staff.must(200, "POST", "/api/v1/approvals/"+own+"/approve", nil).JSON; get(d, "approval", "state") != "approved" {
		t.Fatalf("approve own outlet: %v", d)
	}
}

// TestApprovalOfOtherOrg404: a request of kopi-senja is not found from pt_beta, over REST and MCP.
func TestApprovalOfOtherOrg404(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(apps, "sales", "models", "sales_invoice.rk")
		b, _ := os.ReadFile(p)
		b = append(b, []byte("approvals:\n  - name: manager\n    on: submit\n    when: \"grand_total > 0\"\n    approvers: [sales_manager]\n    label: {id: Persetujuan manajer}\n")...)
		os.WriteFile(p, b, 0o644)
	})
	ctx := context.Background()
	db := e.s.DB
	if err := auth.EnsureOrgCode(ctx, db, "pt_beta", "PT Beta", "BETA"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.EnsureUser(ctx, db, "ceo@saga.co.id", "", "rangka-demo", "pt_beta", "owner", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.EnsureUser(ctx, db, "rep@kopisenja.id", "Sales", "rahasia-123", "kopi-senja", "sales_rep", "USR-003"); err != nil {
		t.Fatal(err)
	}
	e.login()
	id := heldSubmit(t, e.userEnv("rep@kopisenja.id", "rahasia-123"), "OUT-01")
	e.must(200, "GET", "/api/v1/approvals/"+id, nil)
	e.must(200, "POST", "/auth/org", map[string]any{"org": "pt_beta"})
	e.must(404, "GET", "/api/v1/approvals/"+id, nil)
	e.must(404, "POST", "/api/v1/approvals/"+id+"/approve", nil)
	e.must(404, "POST", "/api/v1/approvals/"+id+"/reject", map[string]any{"reason": "salah org"})
	if l := e.must(200, "GET", "/api/v1/approvals", nil); len(rowsOf(l)) != 0 {
		t.Fatalf("pt_beta lists kopi-senja requests: %s", l.Body)
	}
	res := rpc(e, 13, "tools/call", map[string]any{"name": "rangka_approve", "arguments": map[string]any{"id": id, "decision": "approve"}})
	if res["isError"] != true || get(res, "structuredContent", "error", "code") != "not_found" {
		t.Fatalf("MCP approve from another org: %v", res)
	}
	e.must(200, "POST", "/auth/org", map[string]any{"org": "kopi-senja"})
	if st := get(e.must(200, "GET", "/api/v1/approvals/"+id, nil).JSON, "state"); st != "pending" {
		t.Fatalf("request changed: %v", st)
	}
}
