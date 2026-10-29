package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func rpc(e *env, id int, method string, params any) map[string]any {
	e.t.Helper()
	body := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		body["params"] = params
	}
	r := e.must(200, "POST", "/mcp", body).JSON
	if r["error"] != nil {
		e.t.Fatalf("%s: %v", method, r["error"])
	}
	return r["result"].(map[string]any)
}

func call(e *env, name string, args map[string]any) map[string]any {
	e.t.Helper()
	res := rpc(e, 7, "tools/call", map[string]any{"name": name, "arguments": args})
	if res["isError"] == true {
		e.t.Fatalf("%s: %v", name, get(res, "content", 0, "text"))
	}
	var out map[string]any
	json.Unmarshal([]byte(get(res, "content", 0, "text").(string)), &out)
	return out
}

func TestMCP(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	tok := e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "omilo"}).JSON["token"].(string)
	m := &env{t: t, ts: e.ts, token: tok}

	init := rpc(m, 1, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}})
	if init["protocolVersion"] != "2025-03-26" || get(init, "serverInfo", "name") != "rangka-os" || get(init, "capabilities", "tools") == nil {
		t.Fatalf("initialize: %v", init)
	}
	if r := m.do("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); r.Status != 202 {
		t.Fatalf("notification: %d", r.Status)
	}
	tools := rpc(m, 2, "tools/list", nil)["tools"].([]any)
	var names []string
	for _, tl := range tools {
		names = append(names, get(tl, "name").(string))
	}
	want := "rangka_meta,rangka_get_file,rangka_query,rangka_get_record,rangka_dashboard,rangka_system_status,rangka_changeset_propose,rangka_changeset_validate,rangka_changeset_preview,rangka_changeset_status"
	if strings.Join(names, ",") != want {
		t.Fatalf("tools: %s", strings.Join(names, ","))
	}
	if meta := call(m, "rangka_meta", nil); len(meta["models"].(map[string]any)) != 9 {
		t.Fatalf("meta models: %d", len(meta["models"].(map[string]any)))
	}
	if f := call(m, "rangka_get_file", map[string]any{"path": "system.rk"}); f["kind"] != "system" {
		t.Fatalf("file: %v", f)
	}
	q := call(m, "rangka_query", map[string]any{"model": "deal", "filter": "stage == 'won'", "limit": 5})
	if q["total"] != 1.0 || get(q, "data", 0, "name") != "DEAL-00008" {
		t.Fatalf("query: %v", q)
	}
	if r := call(m, "rangka_get_record", map[string]any{"model": "sales_invoice", "name": "INV-2026-00043"}); r["grand_total"] != 8061930.0 || len(r["items"].([]any)) != 2 {
		t.Fatalf("record: %v %v", r["grand_total"], r["items"])
	}
	if d := call(m, "rangka_dashboard", map[string]any{"name": "sales_overview"}); get(d, "widgets", 1, "value") != 11203230.0 {
		t.Fatalf("dashboard receivable: %v", get(d, "widgets", 1, "value"))
	}
	if st := call(m, "rangka_system_status", nil); get(st, "capabilities", "db", "ok") != true {
		t.Fatalf("status: %v", st)
	}
	cs := call(m, "rangka_changeset_propose", map[string]any{"title": "Tambah wilayah", "reason": "laporan per wilayah", "ops": get(addRegion(), "ops")})
	if f := call(m, "rangka_get_file", map[string]any{"path": "apps/crm/models/deal.rk"}); f["layer"] != 5.0 || f["parsed"] == nil || f["text"] == nil {
		t.Fatalf("get_file shape: %v", f)
	}
	id := cs["id"].(string)
	if cs["risk"] != "medium" || get(cs, "author", "kind") != "ai" {
		t.Fatalf("propose: %v", cs)
	}
	if v := call(m, "rangka_changeset_validate", map[string]any{"id": id}); v["status"] != "validated" {
		t.Fatalf("validate: %v", v)
	}
	if p := call(m, "rangka_changeset_preview", map[string]any{"id": id}); get(p, "preview", "files", 0, "path") != "custom/deal.rk" {
		t.Fatalf("preview: %v", p)
	}
	if s := call(m, "rangka_changeset_status", map[string]any{"id": id}); s["status"] != "previewed" {
		t.Fatalf("status: %v", s)
	}
	// tool errors come back as isError results; unknown tools and methods as JSON-RPC errors
	res := rpc(m, 8, "tools/call", map[string]any{"name": "rangka_query", "arguments": map[string]any{"model": "nope"}})
	if res["isError"] != true || get(res, "structuredContent", "error", "code") != "not_found" {
		t.Fatalf("expected tool error: %v", res)
	}
	if r := m.must(200, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 9, "method": "tools/call", "params": map[string]any{"name": "rangka_apply"}}).JSON; get(r, "error", "code") != -32602.0 {
		t.Fatalf("unknown tool: %v", r)
	}
	if r := m.must(200, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 10, "method": "resources/list"}).JSON; get(r, "error", "code") != -32601.0 {
		t.Fatalf("unknown method: %v", r)
	}
	// batch
	b := m.do("POST", "/mcp", []any{map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "ping"}})
	var arr []any
	json.Unmarshal(b.Body, &arr)
	if len(arr) != 2 {
		t.Fatalf("batch: %s", b.Body)
	}
}
