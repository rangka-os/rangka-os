package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Contract 0036 §3: GET /api/v1/catalogue lists every module, app and connector on disk with plain words, for
// people who may propose change sets; MCP rangka_catalogue_list gives the same answer.

func catalogueEntry(t *testing.T, list any, name string) map[string]any {
	t.Helper()
	for _, x := range list.([]any) {
		if m := x.(map[string]any); m["name"] == name {
			return m
		}
	}
	t.Fatalf("no catalogue entry %q in %v", name, list)
	return nil
}

func TestCatalogueShapes(t *testing.T) {
	// config.connectors switches xendit on before any flow uses it
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "config.rk")
		b, _ := os.ReadFile(p)
		if err := os.WriteFile(p, append(b, []byte("connectors: [xendit]\n")...), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	e.login()
	cat := e.must(200, "GET", "/api/v1/catalogue", nil).JSON
	if len(cat["modules"].([]any)) != 0 { // the v0 catalogue has 0.4 units only: each is listed once, as an app
		t.Fatalf("modules: %v", cat["modules"])
	}
	crm := catalogueEntry(t, cat["apps"], "crm")
	for _, k := range []string{"name", "kind", "title", "summary", "category", "icon", "version", "installed", "installed_by", "requires",
		"required_by", "models", "in_plan", "plan", "source"} {
		if _, ok := crm[k]; !ok {
			t.Errorf("app entry has no %s: %v", k, crm)
		}
	}
	if crm["kind"] != "app" || crm["installed"] != true || crm["source"] != "directory" || crm["category"] != "other" ||
		get(crm, "installed_by", 0) != "config" || get(crm, "requires", 0) != "core" {
		t.Errorf("crm: %v", crm)
	}
	core := catalogueEntry(t, cat["apps"], "core")
	if !strings.Contains(strings.Join(anyStrings(core["required_by"]), ","), "crm") {
		t.Errorf("core required_by: %v", core["required_by"])
	}
	for _, m := range crm["models"].([]any) {
		if get(m, "name") == nil || get(m, "label") == nil {
			t.Errorf("model entry: %v", m)
		}
	}

	wa := catalogueEntry(t, cat["connectors"], "whatsapp_business")
	for _, k := range []string{"name", "label", "summary", "category", "icon", "vendor", "docs", "version", "company", "money", "hosts",
		"provides", "ports_selected", "used_by", "auth", "state", "errors_24h", "last_event_at"} {
		if _, ok := wa[k]; !ok {
			t.Errorf("connector entry has no %s: %v", k, wa)
		}
	}
	// installed through a flow and notify; auth fields are never set before the secrets API of contract 0017
	if wa["state"] != "needs_auth" || get(wa, "used_by", 0) != "whatsapp_to_lead" || wa["company"] != false ||
		get(wa, "auth", 0, "name") != "access_token" || get(wa, "auth", 0, "set") != false || get(wa, "auth", 0, "secret") != true {
		t.Errorf("whatsapp_business: %v", wa)
	}
	if !strings.Contains(strings.Join(anyStrings(wa["hosts"]), ","), "graph.facebook.com") {
		t.Errorf("hosts: %v", wa["hosts"])
	}
	if x := catalogueEntry(t, cat["connectors"], "xendit"); x["installed"] != true || x["state"] == "available" {
		t.Errorf("xendit via config.connectors: %v", x)
	}
	if m := catalogueEntry(t, cat["connectors"], "moka"); m["installed"] != false || m["state"] != "available" {
		t.Errorf("moka: %v", m)
	}
	// config.connectors also counts as installed in /meta and /system/status
	if get(e.must(200, "GET", "/api/v1/meta", nil).JSON, "connectors", "xendit") == nil {
		t.Error("/meta connectors has no xendit")
	}
}

func anyStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// A sales_rep may not propose change sets, so the catalogue answers 403; so does a plain API token.
// A delegated assistant token for the owner may read it, over REST and MCP.
func TestCatalogueReadRule(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	rep := repEnv(t, e)
	if r := rep.do("GET", "/api/v1/catalogue", nil); r.Status != 403 || get(r.JSON, "error", "code") != "forbidden" {
		t.Fatalf("sales_rep: %d %s", r.Status, r.Body)
	}
	tok := e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "cli"}).JSON["token"].(string)
	if r := (&env{t: t, ts: e.ts, token: tok}).do("GET", "/api/v1/catalogue", nil); r.Status != 403 {
		t.Fatalf("api token: %d %s", r.Status, r.Body)
	}
	ai := assistantEnv(t, e, 10*time.Minute)
	ai.must(200, "GET", "/api/v1/catalogue", nil)
	res := rpc(ai, 3, "tools/call", map[string]any{"name": "rangka_catalogue_list", "arguments": map[string]any{}})
	if res["isError"] == true || !strings.Contains(get(res, "content", 0, "text").(string), "whatsapp_business") {
		t.Fatalf("assistant MCP: %v", res)
	}
}

// The answer holds no secret values, even when the secrets driver (env) has them.
func TestCatalogueHoldsNoSecret(t *testing.T) {
	const secret = "s3cret-value-never-shown"
	for _, k := range []string{"WHATSAPP_BUSINESS_ACCESS_TOKEN", "WHATSAPP_ACCESS_TOKEN", "WHATSAPP_TOKEN", "XENDIT_SECRET_KEY", "XENDIT_API_KEY"} {
		t.Setenv(k, secret)
	}
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "config.rk")
		b, _ := os.ReadFile(p)
		os.WriteFile(p, append(b, []byte("connectors: [xendit]\n")...), 0o644)
	})
	e.login()
	r := e.must(200, "GET", "/api/v1/catalogue", nil)
	if strings.Contains(string(r.Body), secret) || strings.Contains(string(r.Body), `"value"`) {
		t.Fatalf("catalogue leaks a secret: %s", r.Body)
	}
	for _, x := range r.JSON["connectors"].([]any) {
		for _, f := range get(x, "auth").([]any) {
			for k := range f.(map[string]any) {
				if k != "name" && k != "label" && k != "secret" && k != "required" && k != "set" {
					t.Errorf("auth field key %q in %v", k, f)
				}
			}
		}
	}
}
