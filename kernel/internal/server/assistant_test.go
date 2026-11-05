package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/changeset"
)

// assistantEnv signs in the owner and returns an env that calls with an assistant token minted for them.
func assistantEnv(t *testing.T, e *env, ttl time.Duration) *env {
	t.Helper()
	e.login()
	id, err := auth.Authenticate(context.Background(), e.s.Auth.DB, "ceo@saga.co.id", "rangka-demo")
	if err != nil {
		t.Fatal(err)
	}
	u, err := auth.LoadUser(context.Background(), e.s.Auth.DB, id, "kopi-senja")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := e.s.Auth.MintAssistantToken(context.Background(), u, ttl)
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, s: e.s, ts: e.ts, token: tok}
}

// rpcRaw posts one JSON-RPC request to /mcp and returns the whole response.
func rpcRaw(e *env, method string, params any) map[string]any {
	e.t.Helper()
	return e.must(200, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}).JSON
}

func TestAssistantTokenReadsAsPerson(t *testing.T) {
	e := newEnv(t, false, nil)
	ai := assistantEnv(t, e, 10*time.Minute)
	own := e.must(200, "GET", "/api/v1/r/sales_invoice", nil).JSON
	got := ai.must(200, "GET", "/api/v1/r/sales_invoice", nil).JSON
	if got["total"] != own["total"] || own["total"] == nil {
		t.Fatalf("assistant total %v, owner total %v", got["total"], own["total"])
	}
	q := call(ai, "rangka_query", map[string]any{"model": "deal", "filter": "stage == 'won'", "limit": 5})
	if q["total"] != 1.0 || get(q, "data", 0, "name") != "DEAL-00008" {
		t.Fatalf("query: %v", q)
	}
	// the assistant does not see the approve tool
	for _, tl := range rpc(ai, 2, "tools/list", nil)["tools"].([]any) {
		if get(tl, "name") == "rangka_approve" {
			t.Fatal("rangka_approve listed for an assistant")
		}
	}
}

func TestAssistantCannotApproveApplyOrWrite(t *testing.T) {
	e := newEnv(t, false, nil)
	ai := assistantEnv(t, e, 10*time.Minute)
	cs := e.must(201, "POST", "/api/v1/changesets", addRegion()).JSON
	id := cs["id"].(string)
	e.must(200, "POST", "/api/v1/changesets/"+id+"/validate", nil)
	e.must(200, "POST", "/api/v1/changesets/"+id+"/preview", nil)
	forbidden := func(method, path string, body any) {
		t.Helper()
		r := ai.do(method, path, body)
		if r.Status != 403 || get(r.JSON, "error", "code") != "ai_forbidden" || get(r.JSON, "error", "message") != auth.AIForbiddenMessage {
			t.Fatalf("%s %s = %d %s, want 403 ai_forbidden", method, path, r.Status, r.Body)
		}
	}
	forbidden("POST", "/api/v1/r/lead", map[string]any{"lead_name": "Dari Raka", "phone": "+62811"})
	forbidden("PATCH", "/api/v1/r/lead/LEAD-00031", map[string]any{"lead_name": "Ganti"})
	forbidden("DELETE", "/api/v1/r/lead/LEAD-00031", nil)
	forbidden("POST", "/api/v1/r/sales_invoice/INV-2026-00043/submit", map[string]any{})
	forbidden("POST", "/api/v1/r/lead/LEAD-00031/actions/convert", map[string]any{})
	forbidden("POST", "/api/v1/changesets", addRegion())
	for _, verb := range []string{"approve", "apply", "revert", "reject"} {
		forbidden("POST", "/api/v1/changesets/"+id+"/"+verb, map[string]any{})
	}
	forbidden("POST", "/api/v1/approvals/apr_ANY/approve", map[string]any{})
	forbidden("POST", "/api/v1/tokens", map[string]any{"name": "raka"})
	// nothing changed: the lead is still there and the change set still waits for a person
	e.must(200, "GET", "/api/v1/r/lead/LEAD-00031", nil)
	if st := e.must(200, "GET", "/api/v1/changesets/"+id, nil).JSON["status"]; st != "previewed" {
		t.Fatalf("change set status %v", st)
	}
	// over MCP: approving is refused as a JSON-RPC error
	r := rpcRaw(ai, "tools/call", map[string]any{"name": "rangka_approve", "arguments": map[string]any{"id": "apr_ANY", "decision": "approve"}})
	if get(r, "error", "data", "code") != "ai_forbidden" || get(r, "error", "message") != auth.AIForbiddenMessage {
		t.Fatalf("mcp approve: %v", r)
	}
}

func TestAssistantProposalIsMarked(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "config.rk")
		b, _ := os.ReadFile(p)
		os.WriteFile(p, []byte(strings.Replace(string(b), "auto_apply: never", "auto_apply: low_risk", 1)), 0o644)
	})
	ai := assistantEnv(t, e, 10*time.Minute)
	// the author the arguments name is ignored: the kernel sets it from the token
	cs := call(ai, "rangka_changeset_propose", map[string]any{"title": "Warna grafik", "author": map[string]any{"kind": "user", "id": "someone@else.id"},
		"ops": []any{map[string]any{"op": "patch", "path": "themes/kopi_senja.rk",
			"patch": []any{map[string]any{"op": "replace", "path": "/color/chart-1", "value": "#0055cc"}}}}})
	if cs["source"] != "ai" || get(cs, "author", "kind") != "ai" || get(cs, "author", "id") != "ceo@saga.co.id" || cs["risk"] != "low" {
		t.Fatalf("propose: source %v author %v risk %v", cs["source"], cs["author"], cs["risk"])
	}
	id := cs["id"].(string)
	call(ai, "rangka_changeset_validate", map[string]any{"id": id})
	if p := call(ai, "rangka_changeset_preview", map[string]any{"id": id}); p["status"] != "previewed" {
		t.Fatalf("preview: %v", p["status"])
	}
	if r := ai.do("POST", "/api/v1/changesets/"+id+"/apply", nil); r.Status != 403 || get(r.JSON, "error", "code") != "ai_forbidden" {
		t.Fatalf("apply over REST: %d %s", r.Status, r.Body)
	}
	// the service refuses too, whatever route reaches it
	_, err := e.s.Changes.Apply(context.Background(), changeset.Actor{ID: "ceo@saga.co.id", Org: "kopi-senja", Roles: []string{"owner"}, Kind: "ai", Assistant: true}, id)
	if ce, ok := err.(*changeset.Error); !ok || ce.Code != "ai_forbidden" {
		t.Fatalf("service apply: %v", err)
	}
	got := e.must(200, "GET", "/api/v1/changesets/"+id, nil).JSON
	if got["status"] != "previewed" || got["source"] != "ai" {
		t.Fatalf("stored: status %v source %v", got["status"], got["source"])
	}
	// a person's proposal carries no source
	if own := e.must(201, "POST", "/api/v1/changesets", addRegion()).JSON; own["source"] != nil {
		t.Fatalf("person's change set has source %v", own["source"])
	}
}

func TestAssistantTokenDropsWithMembership(t *testing.T) {
	e := newEnv(t, false, nil)
	ai := assistantEnv(t, e, 10*time.Minute)
	ai.must(200, "GET", "/api/v1/r/lead", nil)
	id, err := auth.Authenticate(context.Background(), e.s.Auth.DB, "ceo@saga.co.id", "rangka-demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.RemoveMember(context.Background(), e.s.Auth.DB, "kopi-senja", id); err != nil {
		t.Fatal(err)
	}
	ai.must(401, "GET", "/api/v1/r/lead", nil)
}

func TestAssistantTokenExpires(t *testing.T) {
	e := newEnv(t, false, nil)
	ai := assistantEnv(t, e, -time.Second)
	ai.must(401, "GET", "/api/v1/r/lead", nil)
	if r := ai.do("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}); r.Status != 401 {
		t.Fatalf("mcp with an expired token: %d", r.Status)
	}
}

// A change set is filed under the proposer's organisation: a body org naming another tenant is refused for
// people and assistants alike, and nothing is stored.
func TestProposeOrgMismatch(t *testing.T) {
	e := newEnv(t, false, nil)
	ai := assistantEnv(t, e, 10*time.Minute)
	count := func() int {
		t.Helper()
		res, err := e.s.Auth.DB.Query(context.Background(), "select count(*) from rk_changesets")
		if err != nil {
			t.Fatal(err)
		}
		return int(res.Rows[0][0].(int64))
	}
	before := count()
	res := rpc(ai, 3, "tools/call", map[string]any{"name": "rangka_changeset_propose", "arguments": map[string]any{
		"title": "Tambah wilayah", "org": "toko-lain", "ops": get(addRegion(), "ops")}})
	if res["isError"] != true || get(res, "structuredContent", "error", "code") != "org_mismatch" {
		t.Fatalf("mcp propose for another org: %v", res)
	}
	other := addRegion()
	other["org"] = "toko-lain"
	if r := e.do("POST", "/api/v1/changesets", other); r.Status != 403 || get(r.JSON, "error", "code") != "org_mismatch" {
		t.Fatalf("rest propose for another org: %d %s", r.Status, r.Body)
	}
	if n := count(); n != before {
		t.Fatalf("change sets stored: %d, want %d", n, before)
	}
	for _, org := range []string{"kopi-senja", ""} {
		body := addRegion()
		body["org"] = org
		if cs := e.must(201, "POST", "/api/v1/changesets", body).JSON; cs["org"] != "kopi-senja" {
			t.Fatalf("org %q: stored under %v", org, cs["org"])
		}
	}
	if cs := call(ai, "rangka_changeset_propose", map[string]any{"title": "Tambah wilayah", "org": "kopi-senja", "ops": get(addRegion(), "ops")}); cs["org"] != "kopi-senja" {
		t.Fatalf("mcp same org: %v", cs["org"])
	}
}
