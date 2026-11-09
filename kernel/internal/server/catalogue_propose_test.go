package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Contract 0036 §4: POST /api/v1/catalogue/propose writes the config.rk edit itself and returns the change set
// validated and previewed, with source catalogue; refusals are 422 with a code; sales roles and the assistant get 403.

func composedEnv(t *testing.T, noPlan bool) *env {
	t.Helper()
	return newEnvFrom(t, func(dir string) (apps, site string) {
		if err := exec.Command("cp", "-r", "../../testdata/config-modules/.", dir).Run(); err != nil {
			t.Fatal(err)
		}
		if noPlan { // no plan: every module may be installed
			p := filepath.Join(dir, "site", "config.rk")
			b, _ := os.ReadFile(p)
			if err := os.WriteFile(p, []byte(strings.Replace(string(b), "plan: sewa\n", "", 1)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return filepath.Join(dir, "apps"), filepath.Join(dir, "site")
	})
}

func TestCatalogueProposeRefusals(t *testing.T) {
	e := composedEnv(t, false)
	e.login()
	for _, c := range []struct {
		body map[string]any
		code string
	}{
		{map[string]any{}, "catalogue_nothing_to_do"},
		{map[string]any{"add": []string{"pipeline"}}, "module_not_in_plan"}, // plan sewa allows invoicing and payments
		{map[string]any{"add": []string{"invoicing"}}, "catalogue_nothing_to_do"},
		{map[string]any{"remove": []string{"pipeline"}}, "catalogue_nothing_to_do"},
		{map[string]any{"remove": []string{"contacts"}}, "catalogue_nothing_to_do"}, // invoicing brings it in
		{map[string]any{"add": []string{"nope"}}, "catalogue_unknown"},
		{map[string]any{"connect": []string{"whatsapp_business"}}, "catalogue_connect_unavailable"},
	} {
		r := e.do("POST", "/api/v1/catalogue/propose", c.body)
		if r.Status != 422 || get(r.JSON, "error", "code") != c.code {
			t.Errorf("%v: %d %s, want 422 %s", c.body, r.Status, r.Body, c.code)
		}
	}
	if r := e.do("POST", "/api/v1/catalogue/propose", map[string]any{"remove": []string{"contacts"}}); !strings.Contains(string(r.Body), "Tagihan") {
		t.Errorf("the refusal names who needs contacts: %s", r.Body)
	}
	if n := len(e.must(200, "GET", "/api/v1/changesets", nil).JSON["data"].([]any)); n != 0 {
		t.Errorf("a refusal proposed %d change set(s)", n)
	}
}

func TestCatalogueProposeForbidden(t *testing.T) {
	e := composedEnv(t, true)
	e.login()
	rep := repEnv(t, e)
	if r := rep.do("POST", "/api/v1/catalogue/propose", map[string]any{"add": []string{"stock"}}); r.Status != 403 || get(r.JSON, "error", "code") != "forbidden" {
		t.Fatalf("sales_rep: %d %s", r.Status, r.Body)
	}
	ai := assistantEnv(t, e, 10*time.Minute)
	if r := ai.do("POST", "/api/v1/catalogue/propose", map[string]any{"add": []string{"stock"}}); r.Status != 403 || get(r.JSON, "error", "code") != "ai_forbidden" {
		t.Fatalf("assistant: %d %s", r.Status, r.Body)
	}
}

// Pasang stok: medium, previewed, the closure in the preview; apply migrates. Lepas stok with a row: high.
func TestCatalogueProposeInstallAndRemove(t *testing.T) {
	e := composedEnv(t, true)
	e.login()
	cs := e.must(201, "POST", "/api/v1/catalogue/propose", map[string]any{"add": []string{"stock"}}).JSON
	if cs["title"] != "Pasang Stok" || cs["source"] != "catalogue" || cs["status"] != "previewed" || cs["risk"] != "medium" ||
		strings.Join(toStrings(get(cs, "preview", "modules", "added")), ",") != "stock" {
		t.Fatalf("pasang: %v", cs)
	}
	if p := get(cs, "ops", 0, "patch", 0); get(p, "path") != "/modules/stock" || get(p, "value") != "^1.0" || get(cs, "ops", 0, "path") != "config.rk" {
		t.Fatalf("pasang ops: %v", cs["ops"])
	}
	id := cs["id"].(string)
	e.must(200, "POST", "/api/v1/changesets/"+id+"/approve", map[string]any{"note": "ok"})
	if ap := e.must(200, "POST", "/api/v1/changesets/"+id+"/apply", nil).JSON; ap["status"] != "applied" {
		t.Fatalf("apply: %v", ap)
	}
	if c := catalogueEntry(t, e.must(200, "GET", "/api/v1/catalogue", nil).JSON["modules"], "stock"); c["installed"] != true || get(c, "installed_by", 0) != "config" {
		t.Fatalf("stock after apply: %v", c)
	}
	e.must(201, "POST", "/api/v1/r/stock_item", map[string]any{"item_name": "Molen"})

	rm := e.must(201, "POST", "/api/v1/catalogue/propose", map[string]any{"remove": []string{"stock"}}).JSON
	if rm["title"] != "Lepas Stok" || rm["status"] != "previewed" || rm["risk"] != "high" || get(rm, "preview", "modules", "rows", "stock_item") != 1.0 {
		t.Fatalf("lepas with a row: risk %v status %v preview %v", rm["risk"], rm["status"], rm["preview"])
	}
}

// A site that names neither apps nor modules installs its plan's apps: adding a module keeps them by writing apps.
func TestCatalogueProposeKeepsDefaultApps(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "config.rk")
		b, _ := os.ReadFile(p)
		var out []string
		for _, l := range strings.Split(string(b), "\n") {
			if !strings.HasPrefix(l, "apps:") {
				out = append(out, l)
			}
		}
		os.WriteFile(p, []byte(strings.Join(out, "\n")), 0o644)
	})
	e.login()
	r := e.do("POST", "/api/v1/catalogue/propose", map[string]any{"remove": []string{"crm"}})
	if r.Status != 201 {
		t.Fatalf("lepas crm: %d %s", r.Status, r.Body)
	}
	if p := get(r.JSON, "ops", 0, "patch", 0); get(p, "op") != "add" || get(p, "path") != "/apps" || strings.Contains(strings.Join(toStrings(get(p, "value")), ","), "crm") ||
		!strings.Contains(strings.Join(toStrings(get(p, "value")), ","), "sales") {
		t.Fatalf("apps written without crm and with the rest: %v", r.JSON["ops"])
	}
}
