package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// userEnv is an env for another user: its own cookie jar, signed in through /auth/login.
func (e *env) userEnv(email, password string) *env {
	e.t.Helper()
	u := &env{t: e.t, s: e.s, ts: e.ts}
	u.jar, _ = cookiejar.New(nil)
	u.must(200, "POST", "/auth/login", map[string]any{"email": email, "password": password})
	return u
}

// approvalEvents streams /api/v1/events for u and sends every approval event's data on the channel.
func approvalEvents(t *testing.T, u *env) <-chan map[string]any {
	t.Helper()
	req, _ := http.NewRequest("GET", u.ts.URL+"/api/v1/events", nil)
	res, err := (&http.Client{Jar: u.jar}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	ch := make(chan map[string]any, 32)
	go func() {
		sc := bufio.NewScanner(res.Body)
		ev := ""
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "event: ") {
				ev = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") && ev == "approval" {
				var m map[string]any
				json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m)
				ch <- m
			}
		}
	}()
	return ch
}

func waitApprovalEvent(t *testing.T, ch <-chan map[string]any, state string) map[string]any {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case m := <-ch:
			if m["state"] == state {
				return m
			}
		case <-deadline:
			t.Fatalf("no approval event with state %q", state)
		}
	}
}

func TestApprovalsREST(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(apps, "sales", "models", "sales_invoice.rk")
		b, _ := os.ReadFile(p)
		b = append(b, []byte("approvals:\n  - name: manager\n    on: submit\n    when: \"grand_total > 1000000\"\n    approvers: [sales_manager]\n    label: {id: Persetujuan manajer}\n")...)
		os.WriteFile(p, b, 0o644)
	})
	ctx := context.Background()
	for _, u := range [][]string{{"manager@kopisenja.id", "Manajer", "sales_manager", "USR-002"}, {"rep@kopisenja.id", "Sales", "sales_rep", "USR-003"}} {
		if _, err := auth.EnsureUser(ctx, e.s.DB, u[0], u[1], "rahasia-123", "kopi-senja", u[2], u[3]); err != nil {
			t.Fatal(err)
		}
	}
	rep, mgr := e.userEnv("rep@kopisenja.id", "rahasia-123"), e.userEnv("manager@kopisenja.id", "rahasia-123")
	items := func(qty int) map[string]any {
		return map[string]any{"items": []any{map[string]any{"item": "BRG-0044", "qty": qty}}}
	}
	draft := rep.must(201, "POST", "/api/v1/r/sales_invoice", map[string]any{"customer": "CUST-0012", "outlet": "OUT-01",
		"items": items(4)["items"]}).JSON["name"].(string)

	// a held submit answers 202 with the pending request
	held := rep.must(202, "POST", "/api/v1/r/sales_invoice/"+draft+"/submit", map[string]any{"force": true}).JSON
	id := get(held, "approval", "id").(string)
	if get(held, "approval", "state") != "pending" || get(held, "approval", "can_decide") != false ||
		get(held, "record", "docstatus") != 0.0 || len(held["approvals"].([]any)) != 1 {
		t.Fatalf("held: %v", held)
	}
	list := mgr.must(200, "GET", "/api/v1/approvals?state=pending&model=sales_invoice&name="+draft, nil).JSON
	if list["total"] != 1.0 || len(list["data"].([]any)) != 1 || get(list, "data", 0, "can_decide") != true || get(list, "data", 0, "id") != id {
		t.Fatalf("list: %v", list)
	}
	if r := rep.do("POST", "/api/v1/approvals/"+id+"/approve", map[string]any{}); r.Status != 403 || get(r.JSON, "error", "code") != "permission_denied" {
		t.Fatalf("rep approve: %d %s", r.Status, r.Body)
	}
	if r := mgr.do("POST", "/api/v1/approvals/"+id+"/reject", map[string]any{}); r.Status != 400 || get(r.JSON, "error", "code") != "bad_request" {
		t.Fatalf("reject without reason: %d %s", r.Status, r.Body)
	}
	tok := mgr.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "ci"}).JSON["token"].(string)
	api := &env{t: t, s: e.s, ts: e.ts, token: tok}
	if r := api.do("POST", "/api/v1/approvals/"+id+"/approve", map[string]any{}); r.Status != 403 || get(r.JSON, "error", "code") != "human_required" {
		t.Fatalf("token approve: %d %s", r.Status, r.Body)
	}

	// editing the draft voids the request; approving it is a conflict
	rep.must(200, "PATCH", "/api/v1/r/sales_invoice/"+draft, items(5))
	if st := mgr.must(200, "GET", "/api/v1/approvals/"+id, nil).JSON["state"]; st != "void" {
		t.Fatalf("state after edit: %v", st)
	}
	if r := mgr.do("POST", "/api/v1/approvals/"+id+"/approve", map[string]any{}); r.Status != 409 || get(r.JSON, "error", "code") != "approval_void" {
		t.Fatalf("approve void: %d %s", r.Status, r.Body)
	}

	// resubmit, then approve: the verb runs and SSE carries both states
	events := approvalEvents(t, mgr)
	time.Sleep(100 * time.Millisecond)
	held = rep.must(202, "POST", "/api/v1/r/sales_invoice/"+draft+"/submit", map[string]any{"force": true}).JSON
	id = get(held, "approval", "id").(string)
	if ev := waitApprovalEvent(t, events, "pending"); ev["id"] != id || ev["model"] != "sales_invoice" || ev["name"] != draft {
		t.Fatalf("pending event: %v", ev)
	}
	done := mgr.must(200, "POST", "/api/v1/approvals/"+id+"/approve", map[string]any{}).JSON
	if get(done, "approval", "state") != "approved" || get(done, "record", "docstatus") != 1.0 {
		t.Fatalf("approve: %v", done)
	}
	if ev := waitApprovalEvent(t, events, "approved"); ev["id"] != id {
		t.Fatalf("approved event: %v", ev)
	}
	final := get(done, "record", "name").(string)
	if got := rep.must(200, "GET", "/api/v1/r/sales_invoice/"+final, nil).JSON; got["docstatus"] != 1.0 {
		t.Fatalf("final record: %v", got)
	}

	r := mgr.must(404, "GET", "/api/v1/approvals/apr_NOPE", nil)
	if get(r.JSON, "error", "code") != "not_found" || strings.Contains(get(r.JSON, "error", "message").(string), "no such endpoint") {
		t.Fatalf("missing approval: %s", r.Body)
	}
}
