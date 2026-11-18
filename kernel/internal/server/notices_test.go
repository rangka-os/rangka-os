package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// noticeEvents streams /api/v1/events for u and sends the data of every notice event on the channel.
func noticeEvents(t *testing.T, u *env) <-chan map[string]any {
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
			} else if strings.HasPrefix(line, "data: ") && ev == "notice" {
				var m map[string]any
				json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m)
				ch <- m
			}
		}
	}()
	return ch
}

// approvalEnv is the v0 catalogue with a manager approval on sales invoices over Rp 1.000.000, a sales manager
// (USR-002), two sales reps (USR-003 asks, USR-004 does not) and the owner; it returns their signed-in envs.
func approvalEnv(t *testing.T, mutate func(apps, site string)) (e, owner, mgr, rep, rep2 *env) {
	t.Helper()
	e = newEnv(t, false, func(apps, site string) {
		p := filepath.Join(apps, "sales", "models", "sales_invoice.rk")
		b, _ := os.ReadFile(p)
		b = append(b, []byte("approvals:\n  - name: manager\n    on: submit\n    when: \"grand_total > 1000000\"\n    approvers: [sales_manager]\n    label: {id: Persetujuan manajer}\n")...)
		os.WriteFile(p, b, 0o644)
		if mutate != nil {
			mutate(apps, site)
		}
	})
	ctx := context.Background()
	for _, u := range [][]string{{"manager@kopisenja.id", "Manajer", "sales_manager", "USR-002"}, {"rep@kopisenja.id", "Sari", "sales_rep", "USR-003"},
		{"rep2@kopisenja.id", "Budi", "sales_rep", "USR-004"}} {
		if _, err := auth.EnsureUser(ctx, e.s.DB, u[0], u[1], "rahasia-123", "kopi-senja", u[2], u[3]); err != nil {
			t.Fatal(err)
		}
	}
	return e, e.userEnv("ceo@saga.co.id", "rangka-demo"), e.userEnv("manager@kopisenja.id", "rahasia-123"),
		e.userEnv("rep@kopisenja.id", "rahasia-123"), e.userEnv("rep2@kopisenja.id", "rahasia-123")
}

func notices(t *testing.T, u *env, q string) (items []any, unread float64) {
	t.Helper()
	r := u.must(200, "GET", "/api/v1/notices"+q, nil).JSON
	items, _ = r["data"].([]any)
	unread, _ = r["unread"].(float64)
	return items, unread
}

func waitNotice(t *testing.T, ch <-chan map[string]any, kind string) map[string]any {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case m := <-ch:
			if m["kind"] == kind {
				return m
			}
		case <-deadline:
			t.Fatalf("no notice event of kind %q", kind)
		}
	}
}

// Contract 0066 §5 and §4.1: a held submit tells the approvers (and the owner) in their inbox and over SSE, never the
// requester; a decision tells the requester; read marks are per person; a removed member's inbox goes with them.
func TestNoticesApprovals(t *testing.T) {
	e, owner, mgr, rep, rep2 := approvalEnv(t, nil)
	mgrEvents, repEvents := noticeEvents(t, mgr), noticeEvents(t, rep)
	draft := rep.must(201, "POST", "/api/v1/r/sales_invoice", map[string]any{"customer": "CUST-0012", "outlet": "OUT-01",
		"items": []any{map[string]any{"item": "BRG-0044", "qty": 4}}}).JSON["name"].(string)
	id := get(rep.must(202, "POST", "/api/v1/r/sales_invoice/"+draft+"/submit", map[string]any{"force": true}).JSON, "approval", "id").(string)

	ev := waitNotice(t, mgrEvents, "kernel.approval.requested")
	if ev["title"] != draft+" menunggu persetujuanmu" || ev["text"] != "Persetujuan manajer · diajukan oleh Sari" || ev["link"] != "/app/#/approvals" ||
		ev["severity"] != "warn" || ev["read"] != false {
		t.Fatalf("approver's SSE notice: %v", ev)
	}
	items, unread := notices(t, mgr, "")
	if len(items) != 1 || unread != 1 || get(items[0].(map[string]any), "id") != ev["id"] || get(items[0].(map[string]any), "kind") != "kernel.approval.requested" {
		t.Fatalf("approver's inbox: %v (unread %v)", items, unread)
	}
	if _, has := items[0].(map[string]any)["data"]; has {
		t.Fatalf("data is not part of an inbox item: %v", items[0])
	}
	if items, _ := notices(t, owner, ""); len(items) != 1 {
		t.Fatalf("the owner hears of every request: %v", items)
	}
	for _, u := range []*env{rep, rep2} {
		if items, unread := notices(t, u, ""); len(items) != 0 || unread != 0 {
			t.Fatalf("the requester and other reps get nothing: %v", items)
		}
	}
	// a second submit of the same draft reuses the request: no second notice
	rep.must(202, "POST", "/api/v1/r/sales_invoice/"+draft+"/submit", map[string]any{"force": true})
	if items, _ := notices(t, mgr, ""); len(items) != 1 {
		t.Fatalf("one notice per request: %v", items)
	}

	// read marks: one id, then all; they do not touch the owner's copy
	r := mgr.must(200, "POST", "/api/v1/notices/read", map[string]any{"ids": []string{ev["id"].(string)}}).JSON
	if r["marked"] != 1.0 || r["unread"] != 0.0 {
		t.Fatalf("read: %v", r)
	}
	if items, unread := notices(t, mgr, "?unread=1"); len(items) != 0 || unread != 0 {
		t.Fatalf("unread after read: %v", items)
	}
	if _, unread := notices(t, owner, ""); unread != 1 {
		t.Fatalf("the owner's copy stays unread: %v", unread)
	}
	owner.must(200, "POST", "/api/v1/notices/read", nil)
	if _, unread := notices(t, owner, ""); unread != 0 {
		t.Fatalf("read all: %v", unread)
	}

	// the decision tells the requester, with the reason and a link to the record
	mgr.must(200, "POST", "/api/v1/approvals/"+id+"/reject", map[string]any{"reason": "Diskon terlalu besar"})
	dec := waitNotice(t, repEvents, "kernel.approval.decided")
	if dec["title"] != draft+" ditolak: Diskon terlalu besar" || dec["severity"] != "warn" || dec["link"] != "/app/#/r/sales_invoice/"+draft ||
		dec["text"] != "Persetujuan manajer · oleh Manajer" {
		t.Fatalf("decided: %v", dec)
	}
	if items, _ := notices(t, mgr, ""); len(items) != 1 {
		t.Fatalf("the approver is not told of their own decision: %v", items)
	}

	// a members:write token reads no inbox
	tok := owner.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "tim", "scopes": []string{"members:write"}}).JSON["token"].(string)
	if r := (&env{t: t, s: e.s, ts: e.ts, token: tok}).do("GET", "/api/v1/notices", nil); r.Status != 403 || get(r.JSON, "error", "code") != "scope_limited" {
		t.Fatalf("members:write token: %d %s", r.Status, r.Body)
	}

	// removing the member removes their inbox (0043 §4)
	ctx := pg.WithOrg(context.Background(), "kopi-senja")
	res, err := e.s.DB.Query(ctx, "select id from rk_users where email = 'manager@kopisenja.id'")
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.RemoveMember(ctx, e.s.DB, "kopi-senja", res.Rows[0][0].(int64)); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.s.DB.Query(ctx, "select count(*) from rk_notices n join rk_users u on u.id = n.user_id where u.email = 'manager@kopisenja.id'"); res.Rows[0][0] != int64(0) {
		t.Fatalf("a removed member's notices stay: %v", res.Rows)
	}
}

// Contract 0066 §1.3: notify steps without notice: reach people. A role target tells that role (and nobody else); a
// target that resolves to field values tells the members those values name (by email or user record) and drops and
// counts the rest. The implicit kind is app.<module>.<flow>.
func TestNoticesFromNotifySteps(t *testing.T) {
	e, owner, mgr, rep, rep2 := approvalEnv(t, func(apps, site string) {
		os.WriteFile(filepath.Join(apps, "crm", "flows", "tell_managers.rk"), []byte(`name: tell_managers
label: {id: Kabar untuk manajer}
steps:
  - notify: sales_manager
    channel: in_app
    template: {id: 'Cek pipeline minggu ini'}
  - for_each: "[1, 2]"
    as: x
    do:
      - notify: sales_manager
        template: {id: 'Butir {x}'}
  - for_each: "['USR-002', 'orang@luar.id']"
    as: who
    do:
      - notify: who
        channel: whatsapp
        template: {id: 'Untuk {who}'}
`), 0o644)
	})
	owner.must(200, "POST", "/api/v1/flows/tell_managers/run", map[string]any{})
	items, unread := notices(t, mgr, "")
	if len(items) != 4 || unread != 4 || items[0].(map[string]any)["text"] != "Untuk USR-002" {
		t.Fatalf("the role hears each step, once per for_each item: %v", items)
	}
	first := items[3].(map[string]any)
	if first["kind"] != "app.crm.tell_managers" || first["title"] != "Kabar untuk manajer" || first["text"] != "Cek pipeline minggu ini" || first["severity"] != "info" {
		t.Fatalf("implicit notice: %v", first)
	}
	for _, u := range []*env{owner, rep, rep2} {
		if items, _ := notices(t, u, ""); len(items) != 0 {
			t.Fatalf("only the role is told: %v", items)
		}
	}

	// morning_follow_up: notify: owner names the activities' owner field (user records USR-002, USR-003, …)
	owner.must(200, "POST", "/api/v1/flows/morning_follow_up/run", map[string]any{})
	if items, _ := notices(t, mgr, ""); len(items) != 5 || items[0].(map[string]any)["kind"] != "app.crm.morning_follow_up" {
		t.Fatalf("USR-002 is told: %v", items)
	}
	for _, u := range []*env{owner, rep} { // their activities are done or not due yet
		if items, _ := notices(t, u, ""); len(items) != 0 {
			t.Fatalf("owner: is a field here, not the owner role: %v", items)
		}
	}
	// an address outside the organisation is dropped and counted in the audit entry
	res, err := e.s.DB.Query(pg.WithOrg(context.Background(), "kopi-senja"), "select data->>'dropped' from rk_audit where kind = 'notify.whatsapp' order by id")
	if err != nil || len(res.Rows) != 2 || res.Rows[0][0] != nil || res.Rows[1][0] != "1" {
		t.Fatalf("audit: %v %v", res, err)
	}
}
