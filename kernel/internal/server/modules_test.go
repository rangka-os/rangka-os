package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The contract 0001 layout end to end: /meta lists apps and modules separately with each model's module,
// and a change set editing modules/<m>/models/<x>.rk validates, previews and applies.
func TestModulesAndApps(t *testing.T) {
	var root string
	e := newEnvFrom(t, func(dir string) (apps, site string) {
		root = dir
		if err := exec.Command("cp", "-r", "../../testdata/modules-apps/.", dir).Run(); err != nil {
			t.Fatal(err)
		}
		return filepath.Join(dir, "apps"), filepath.Join(dir, "site")
	})
	e.login()
	m := e.must(200, "GET", "/api/v1/meta", nil).JSON
	var apps, mods []string
	for _, a := range m["apps"].([]any) {
		apps = append(apps, get(a, "name").(string))
	}
	for _, x := range m["modules"].([]any) {
		mods = append(mods, get(x, "name").(string))
	}
	if strings.Join(apps, ",") != "crm" || strings.Join(mods, ",") != "contacts,pipeline" {
		t.Fatalf("apps %v modules %v", apps, mods)
	}
	if got := mustJSON(get(m, "apps", 0, "modules")); string(got) != `["contacts","pipeline"]` {
		t.Errorf("crm modules = %s", got)
	}
	if get(m, "modules", 1, "requires", "contacts") != "^1.0" || get(m, "models", "deal", "module") != "pipeline" || get(m, "models", "customer", "module") != "contacts" {
		t.Fatalf("meta: modules %v deal.module %v", m["modules"], get(m, "models", "deal", "module"))
	}
	if strings.Join(toStrings(m["installed"]), ",") != "contacts,pipeline,crm" {
		t.Errorf("installed = %v", m["installed"])
	}
	if f := e.must(200, "GET", "/api/v1/files/modules/pipeline/models/deal.rk", nil).JSON; f["kind"] != "model" || f["layer"] != 5.0 {
		t.Fatalf("file: %v", f)
	}

	// a new field in a module's model: proposed (high: an app author's file), validated, previewed, applied
	cs := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Tambah sumber di peluang", "ops": []any{map[string]any{
		"op": "patch", "path": "modules/pipeline/models/deal.rk", "patch": []any{map[string]any{"op": "add", "path": "/fields/-",
			"value": map[string]any{"name": "source", "type": "data", "label": map[string]any{"id": "Sumber"}}}}}}}).JSON
	id := cs["id"].(string)
	if cs["risk"] != "high" || get(cs, "layers", 0) != 5.0 {
		t.Fatalf("proposed: %v", cs)
	}
	if v := e.must(200, "POST", "/api/v1/changesets/"+id+"/validate", nil).JSON; v["status"] != "validated" {
		t.Fatalf("validate: %v", v)
	}
	pv := e.must(200, "POST", "/api/v1/changesets/"+id+"/preview", nil).JSON
	if pv["status"] != "previewed" || get(pv, "preview", "files", 0, "path") != "modules/pipeline/models/deal.rk" ||
		!strings.Contains(string(mustJSON(get(pv, "preview", "migrations"))), "source") {
		t.Fatalf("preview: %v", pv["preview"])
	}
	e.must(200, "POST", "/api/v1/changesets/"+id+"/approve", map[string]any{"note": "ok"})
	if ap := e.must(200, "POST", "/api/v1/changesets/"+id+"/apply", nil).JSON; ap["status"] != "applied" {
		t.Fatalf("apply: %v", ap)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "modules", "pipeline", "models", "deal.rk")); !strings.Contains(string(b), "source") {
		t.Fatal("module file not written")
	}
	d := e.must(201, "POST", "/api/v1/r/deal", map[string]any{"deal_name": "Grinder", "customer": "CUST-0001", "value": 5000000, "source": "whatsapp"}).JSON
	if d["source"] != "whatsapp" {
		t.Fatalf("new field not stored: %v", d)
	}

	// a change set that breaks a module reports the module path
	bad := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Salah", "ops": []any{map[string]any{
		"op": "patch", "path": "modules/pipeline/module.rk", "patch": []any{map[string]any{"op": "replace", "path": "/requires/contacts", "value": "^2.0"}}}}}).JSON
	v := e.must(200, "POST", "/api/v1/changesets/"+bad["id"].(string)+"/validate", nil).JSON
	if v["status"] != "invalid" || get(v, "validation", "errors", 0, "code") != "version_mismatch" || get(v, "validation", "errors", 0, "file") != "modules/pipeline/module.rk" {
		t.Fatalf("invalid: %v", v["validation"])
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}
