package changeset

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/schema"
)

func TestApplyPatch(t *testing.T) {
	doc := map[string]any{"name": "deal", "fields": []any{map[string]any{"name": "a"}, map[string]any{"name": "b"}}, "label": map[string]any{"id": "x"}}
	out, err := ApplyPatch(doc, []map[string]any{
		{"op": "add", "path": "/fields/-", "value": map[string]any{"name": "c"}},
		{"op": "add", "path": "/fields/0", "value": map[string]any{"name": "z"}},
		{"op": "remove", "path": "/fields/2"},
		{"op": "replace", "path": "/label/id", "value": "y"},
		{"op": "copy", "from": "/label", "path": "/title"},
		{"op": "move", "from": "/title", "path": "/caption"},
		{"op": "test", "path": "/caption/id", "value": "y"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"name": "deal", "fields": []any{map[string]any{"name": "z"}, map[string]any{"name": "a"}, map[string]any{"name": "c"}},
		"label": map[string]any{"id": "y"}, "caption": map[string]any{"id": "y"}}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("got %v", out)
	}
	if doc["label"].(map[string]any)["id"] != "x" {
		t.Error("input was modified")
	}
	if _, err := ApplyPatch(doc, []map[string]any{{"op": "replace", "path": "/nope", "value": 1}}); err == nil {
		t.Error("replace of a missing key accepted")
	}
	if _, err := ApplyPatch(doc, []map[string]any{{"op": "test", "path": "/name", "value": "lead"}}); err == nil {
		t.Error("failed test accepted")
	}
}

func TestAssess(t *testing.T) {
	deal := map[string]any{"name": "deal", "fields": []any{map[string]any{"name": "value", "type": "currency"}, map[string]any{"name": "notes", "type": "text"}}}
	inv := map[string]any{"name": "sales_invoice", "is_submittable": true, "fields": []any{map[string]any{"name": "notes", "type": "text"}}}
	cfg := map[string]any{"apps": []any{"core"}, "tax": map[string]any{"ppn_rate": 12}, "theme": "a"}
	ovl := map[string]any{"model": "sales_invoice", "fields": []any{map[string]any{"name": "x_po", "type": "data"}}}
	files := map[string]map[string]any{"apps/crm/models/deal.rk": deal, "apps/sales/models/sales_invoice.rk": inv, "config.rk": cfg,
		"custom/sales_invoice.rk": ovl, "themes/kopi_senja.rk": {"name": "kopi_senja"}, "connectors/whatsapp_business.rk": {"name": "w"},
		"apps/crm/dashboards/old.rk": {"name": "old"}}
	before := func(p string) (map[string]any, bool) { m, ok := files[p]; return m, ok }
	with := func(p string, m map[string]any) func(string) (map[string]any, bool) {
		return func(q string) (map[string]any, bool) {
			if q == p {
				return m, true
			}
			return before(q)
		}
	}
	model := func(n string) map[string]any { return map[string]map[string]any{"sales_invoice": inv, "deal": deal}[n] }
	cases := []struct {
		name  string
		ops   []Op
		after func(string) (map[string]any, bool)
		risk  string
		layer int
	}{
		{"edit an app model", []Op{{Op: "patch", Path: "apps/crm/models/deal.rk"}}, with("apps/crm/models/deal.rk", deal), "high", 5},
		{"new dashboard", []Op{{Op: "create", Path: "apps/crm/dashboards/x.rk"}}, with("apps/crm/dashboards/x.rk", map[string]any{"name": "x"}), "low", 5},
		{"edit a dashboard file", []Op{{Op: "patch", Path: "apps/crm/dashboards/old.rk"}}, before, "high", 5},
		{"new model", []Op{{Op: "create", Path: "apps/crm/models/visit.rk"}}, with("apps/crm/models/visit.rk", map[string]any{"name": "visit"}), "medium", 5},
		{"new model with money", []Op{{Op: "create", Path: "apps/crm/models/visit.rk"}}, with("apps/crm/models/visit.rk", map[string]any{"name": "visit", "fields": []any{map[string]any{"name": "fee", "type": "currency"}}}), "high", 5},
		{"new model with money type", []Op{{Op: "create", Path: "apps/crm/models/visit.rk"}}, with("apps/crm/models/visit.rk", map[string]any{"name": "visit", "fields": []any{map[string]any{"name": "fee", "type": "money", "currency_field": "cur"}}}), "high", 5},
		{"new overlay", []Op{{Op: "create", Path: "custom/deal.rk"}}, with("custom/deal.rk", map[string]any{"model": "deal", "fields": []any{map[string]any{"name": "x_r", "type": "data"}}}), "medium", 4},
		{"overlay money field", []Op{{Op: "replace", Path: "custom/sales_invoice.rk"}}, with("custom/sales_invoice.rk", map[string]any{"model": "sales_invoice", "fields": []any{ovl["fields"].([]any)[0], map[string]any{"name": "x_fee", "type": "float", "money": true}}}), "high", 4},
		{"overlay stock field", []Op{{Op: "create", Path: "custom/deal.rk"}}, with("custom/deal.rk", map[string]any{"model": "deal", "fields": []any{map[string]any{"name": "x_q", "type": "float", "stock": true}}}), "high", 4},
		{"change overlay field of submittable", []Op{{Op: "replace", Path: "custom/sales_invoice.rk"}}, with("custom/sales_invoice.rk", map[string]any{"model": "sales_invoice", "fields": []any{map[string]any{"name": "x_po", "type": "int"}}}), "high", 4},
		{"overlay views", []Op{{Op: "replace", Path: "custom/sales_invoice.rk"}}, with("custom/sales_invoice.rk", map[string]any{"model": "sales_invoice", "fields": ovl["fields"], "views": map[string]any{"list": map[string]any{"add": []any{map[string]any{"field": "x_po"}}}}}), "low", 4},
		{"theme", []Op{{Op: "replace", Path: "themes/kopi_senja.rk"}}, before, "low", 6},
		{"system", []Op{{Op: "patch", Path: "system.rk"}}, before, "high", 2},
		{"delete", []Op{{Op: "delete", Path: "custom/sales_invoice.rk"}}, before, "high", 4},
		{"role", []Op{{Op: "create", Path: "apps/crm/roles/viewer.rk"}}, before, "high", 5},
		{"connector", []Op{{Op: "replace", Path: "connectors/whatsapp_business.rk"}}, before, "medium", 3},
		{"config apps", []Op{{Op: "patch", Path: "config.rk"}}, with("config.rk", map[string]any{"apps": []any{"core", "crm"}, "tax": cfg["tax"], "theme": "a"}), "medium", 4},
		{"config tax", []Op{{Op: "patch", Path: "config.rk"}}, with("config.rk", map[string]any{"apps": cfg["apps"], "tax": map[string]any{"ppn_rate": 11}, "theme": "a"}), "high", 4},
		{"config theme", []Op{{Op: "patch", Path: "config.rk"}}, with("config.rk", map[string]any{"apps": cfg["apps"], "tax": cfg["tax"], "theme": "b"}), "low", 4},
		{"features", []Op{{Op: "patch", Path: "features.rk"}}, before, "medium", 4},
	}
	for _, c := range cases {
		a := Assess(c.ops, before, c.after, model)
		if a.Risk != c.risk || len(a.Layers) != 1 || a.Layers[0] != c.layer {
			t.Errorf("%s: risk %s layers %v (%v), want %s %d", c.name, a.Risk, a.Layers, a.Reasons, c.risk, c.layer)
		}
	}
}

// A patched 0.4 app.rk keeps declaring 0.4 app.json; a 0.5 app (with modules) and other kinds declare 0.5.
func TestPatchKeepsLegacyAppHeader(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "apps", "crm"), 0o755)
	os.MkdirAll(filepath.Join(dir, "apps", "shop"), 0o755)
	os.WriteFile(filepath.Join(dir, "apps", "crm", "app.rk"), []byte("# yaml-language-server: $schema="+schema.Legacy+"app.json\nname: crm\nversion: 1.0.0\ntitle: {id: CRM}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "apps", "shop", "app.rk"), []byte("# yaml-language-server: $schema="+schema.Base+"app.json\nname: shop\nversion: 1.0.0\ntitle: {id: Toko}\nmodules: {contacts: ^1.0}\n"), 0o644)
	r := roots{apps: filepath.Join(dir, "apps")}
	patch := []map[string]any{{"op": "replace", "path": "/title/id", "value": "Baru"}}
	out, err := r.result([]Op{{Op: "patch", Path: "apps/crm/app.rk", Patch: patch}, {Op: "patch", Path: "apps/shop/app.rk", Patch: patch}})
	if err != nil {
		t.Fatal(err)
	}
	if got := *out["apps/crm/app.rk"]; !strings.HasPrefix(got, "# yaml-language-server: $schema="+schema.Legacy+"app.json\n") || !strings.Contains(got, "Baru") {
		t.Errorf("legacy app header: %q", got)
	}
	if got := *out["apps/shop/app.rk"]; !strings.HasPrefix(got, "# yaml-language-server: $schema="+schema.Base+"app.json\n") {
		t.Errorf("0.5 app header: %q", got)
	}
}

func TestAssessUbah(t *testing.T) {
	deal := map[string]any{"name": "deal", "fields": []any{map[string]any{"name": "notes", "type": "text"}}}
	inv := map[string]any{"name": "sales_invoice", "is_submittable": true}
	files := map[string]map[string]any{
		"dashboards/crm_overview.rk": {"name": "crm_overview"},
		"custom/deal.rk":             {"model": "deal", "views": map[string]any{"list": map[string]any{"columns": []any{"a"}}}},
		"layouts/crm_first.rk":       {"name": "crm_first"},
	}
	before := func(p string) (map[string]any, bool) { m, ok := files[p]; return m, ok }
	with := func(p string, m map[string]any) func(string) (map[string]any, bool) {
		return func(q string) (map[string]any, bool) {
			if q == p {
				return m, true
			}
			return before(q)
		}
	}
	model := func(n string) map[string]any { return map[string]map[string]any{"deal": deal, "sales_invoice": inv}[n] }
	views := map[string]any{"form": map[string]any{"layout": []any{"a"}}}
	appr := []any{map[string]any{"name": "a1"}}
	opts := map[string]any{"stage": []any{map[string]any{"value": "x", "label": "X"}}}
	cases := []struct {
		name  string
		ops   []Op
		after func(string) (map[string]any, bool)
		risk  string
		layer int
	}{
		{"create dashboard", []Op{{Op: "create", Path: "dashboards/new_one.rk"}}, with("dashboards/new_one.rk", map[string]any{"name": "n"}), "low", 6},
		{"patch dashboard", []Op{{Op: "patch", Path: "dashboards/crm_overview.rk"}}, with("dashboards/crm_overview.rk", map[string]any{"name": "crm_overview", "widgets": []any{}}), "low", 6},
		{"new views-only overlay", []Op{{Op: "create", Path: "custom/invoice_x.rk"}}, with("custom/invoice_x.rk", map[string]any{"model": "deal", "views": views}), "low", 4},
		{"new overlay with fields", []Op{{Op: "create", Path: "custom/invoice_x.rk"}}, with("custom/invoice_x.rk", map[string]any{"model": "deal", "fields": []any{map[string]any{"name": "x_a", "type": "data"}}}), "medium", 4},
		{"approvals added to existing overlay", []Op{{Op: "patch", Path: "custom/deal.rk"}}, with("custom/deal.rk", map[string]any{"model": "deal", "views": files["custom/deal.rk"]["views"], "approvals": appr}), "medium", 4},
		{"new overlay with approvals", []Op{{Op: "create", Path: "custom/x.rk"}}, with("custom/x.rk", map[string]any{"model": "deal", "approvals": appr}), "medium", 4},
		{"options on non-submittable", []Op{{Op: "create", Path: "custom/deal2.rk"}}, with("custom/deal2.rk", map[string]any{"model": "deal", "options": opts}), "medium", 4},
		{"options on existing overlay", []Op{{Op: "patch", Path: "custom/deal.rk"}}, with("custom/deal.rk", map[string]any{"model": "deal", "views": files["custom/deal.rk"]["views"], "options": opts}), "medium", 4},
		{"options on submittable", []Op{{Op: "create", Path: "custom/sales_invoice.rk"}}, with("custom/sales_invoice.rk", map[string]any{"model": "sales_invoice", "options": opts}), "high", 4},
		{"new flow", []Op{{Op: "create", Path: "apps/crm/flows/x.rk"}}, with("apps/crm/flows/x.rk", map[string]any{"name": "x", "on": "deal.updated"}), "medium", 5},
		{"new plain model", []Op{{Op: "create", Path: "apps/crm/models/x.rk"}}, with("apps/crm/models/x.rk", map[string]any{"name": "x", "fields": []any{map[string]any{"name": "t", "type": "data"}}}), "medium", 5},
		{"layout patch", []Op{{Op: "patch", Path: "layouts/crm_first.rk"}}, with("layouts/crm_first.rk", map[string]any{"name": "crm_first", "nav": map[string]any{}}), "low", 6},
		{"delete dashboard", []Op{{Op: "delete", Path: "dashboards/crm_overview.rk"}}, before, "high", 6},
	}
	for _, c := range cases {
		a := Assess(c.ops, before, c.after, model)
		if a.Risk != c.risk || len(a.Layers) != 1 || a.Layers[0] != c.layer {
			t.Errorf("%s: risk %s layers %v (%v), want %s %d", c.name, a.Risk, a.Layers, a.Reasons, c.risk, c.layer)
		}
	}
	if Layer("dashboards/x.rk") != 6 {
		t.Error("dashboards layer")
	}
}
