package changeset

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
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
	sewa := map[string]any{"name": "x_sewa", "fields": []any{map[string]any{"name": "deposit", "type": "currency"}, map[string]any{"name": "catatan", "type": "text"},
		map[string]any{"name": "baris", "type": "table", "of": "x_sewa_baris"}, map[string]any{"name": "catatan_baris", "type": "table", "of": "x_sewa_catatan"},
		map[string]any{"name": "hilang", "type": "table", "of": "x_tidak_ada"}}}
	sewaBaris := map[string]any{"name": "x_sewa_baris", "fields": []any{map[string]any{"name": "alat", "type": "data"}, map[string]any{"name": "harga", "type": "currency"}}}
	sewaCatatan := map[string]any{"name": "x_sewa_catatan", "fields": []any{map[string]any{"name": "isi", "type": "text"}}}
	invoice := map[string]any{"name": "invoice", "is_submittable": true, "fields": []any{map[string]any{"name": "catatan", "type": "text"}}}
	status := map[string]any{"name": "status", "type": "select", "options": []any{map[string]any{"value": "baru", "label": "Baru"}, map[string]any{"value": "batal", "label": "Batal"}}}
	files["custom/x_tugas.rk"] = map[string]any{"model": "x_tugas", "options": map[string]any{"status": []any{map[string]any{"value": "baru", "label": "Baru"}, map[string]any{"value": "batal", "label": "Batal"}}}}
	files["models/x_tiket.rk"] = map[string]any{"name": "x_tiket", "fields": []any{status}}
	model := func(n string) map[string]any {
		return map[string]map[string]any{"sales_invoice": inv, "deal": deal, "x_sewa": sewa, "invoice": invoice, "x_sewa_baris": sewaBaris, "x_sewa_catatan": sewaCatatan}[n]
	}
	moduleOf := func(n string) string {
		return map[string]string{"invoice": "invoicing", "deal": "crm", "x_sewa": "site"}[n]
	}
	flow := func(on string, steps ...any) map[string]any {
		return map[string]any{"name": "x_f", "on": on, "steps": steps}
	}
	cases := []struct {
		name  string
		ops   []Op
		after func(string) (map[string]any, bool)
		risk  string
		layer int
	}{
		{"edit an app model", []Op{{Op: "patch", Path: "apps/crm/models/deal.rk"}}, with("apps/crm/models/deal.rk", deal), "high", 5},
		{"new dashboard", []Op{{Op: "create", Path: "apps/crm/dashboards/x.rk"}}, with("apps/crm/dashboards/x.rk", map[string]any{"name": "x"}), "low", 5},
		{"new work screen", []Op{{Op: "create", Path: "modules/crm/screens/isi.rk"}}, with("modules/crm/screens/isi.rk", map[string]any{"name": "isi"}), "low", 5},
		{"edit a dashboard file", []Op{{Op: "patch", Path: "apps/crm/dashboards/old.rk"}}, before, "high", 5},
		{"new model", []Op{{Op: "create", Path: "apps/crm/models/visit.rk"}}, with("apps/crm/models/visit.rk", map[string]any{"name": "visit"}), "medium", 5},
		{"new model with money", []Op{{Op: "create", Path: "apps/crm/models/visit.rk"}}, with("apps/crm/models/visit.rk", map[string]any{"name": "visit", "fields": []any{map[string]any{"name": "fee", "type": "currency"}}}), "high", 5},
		{"new model with money type", []Op{{Op: "create", Path: "apps/crm/models/visit.rk"}}, with("apps/crm/models/visit.rk", map[string]any{"name": "visit", "fields": []any{map[string]any{"name": "fee", "type": "money", "currency_field": "cur"}}}), "high", 5},
		{"new overlay", []Op{{Op: "create", Path: "custom/deal.rk"}}, with("custom/deal.rk", map[string]any{"model": "deal", "fields": []any{map[string]any{"name": "x_r", "type": "data"}}}), "medium", 4},
		{"overlay money field", []Op{{Op: "replace", Path: "custom/sales_invoice.rk"}}, with("custom/sales_invoice.rk", map[string]any{"model": "sales_invoice", "fields": []any{ovl["fields"].([]any)[0], map[string]any{"name": "x_fee", "type": "float", "money": true}}}), "high", 4},
		{"overlay stock field", []Op{{Op: "create", Path: "custom/deal.rk"}}, with("custom/deal.rk", map[string]any{"model": "deal", "fields": []any{map[string]any{"name": "x_q", "type": "float", "stock": true}}}), "high", 4},
		{"change overlay field of submittable", []Op{{Op: "replace", Path: "custom/sales_invoice.rk"}}, with("custom/sales_invoice.rk", map[string]any{"model": "sales_invoice", "fields": []any{map[string]any{"name": "x_po", "type": "int"}}}), "high", 4},
		{"overlay views", []Op{{Op: "replace", Path: "custom/sales_invoice.rk"}}, with("custom/sales_invoice.rk", map[string]any{"model": "sales_invoice", "fields": ovl["fields"], "views": map[string]any{"list": map[string]any{"add": []any{map[string]any{"field": "x_po"}}}}}), "low", 4},
		// 0049 §5: the company's words for a model and its fields are display only
		{"overlay label", []Op{{Op: "replace", Path: "custom/sales_invoice.rk"}}, with("custom/sales_invoice.rk", map[string]any{"model": "sales_invoice", "fields": ovl["fields"], "label": map[string]any{"id": "Nota sewa"}, "labels": map[string]any{"notes": map[string]any{"id": "Catatan sewa"}}}), "low", 4},
		{"new overlay with only a label", []Op{{Op: "create", Path: "custom/deal.rk"}}, with("custom/deal.rk", map[string]any{"model": "deal", "label": map[string]any{"id": "Mobil"}}), "low", 4},
		// 0049: the company's words for an app's menu groups are display only
		{"new app overlay menu labels", []Op{{Op: "create", Path: "custom/app.crm.rk"}}, with("custom/app.crm.rk", map[string]any{"app": "crm", "menu_labels": map[string]any{"CRM": map[string]any{"id": "Pelanggan"}}}), "low", 4},
		// audit menu-labels F1: custom/app.rk is the overlay of a model named app, judged like any overlay
		{"overlay of a model named app with money", []Op{{Op: "create", Path: "custom/app.rk"}}, with("custom/app.rk", map[string]any{"model": "app", "fields": []any{map[string]any{"name": "x_fee", "type": "currency"}}}), "high", 4},
		{"theme", []Op{{Op: "replace", Path: "themes/kopi_senja.rk"}}, before, "low", 6},
		{"new site process", []Op{{Op: "create", Path: "processes/kopi_senja.rk"}}, with("processes/kopi_senja.rk", map[string]any{"name": "kopi_senja"}), "low", 6},
		{"replace site process", []Op{{Op: "replace", Path: "processes/kopi_senja.rk"}}, with("processes/kopi_senja.rk", map[string]any{"name": "kopi_senja"}), "low", 6},
		{"system", []Op{{Op: "patch", Path: "system.rk"}}, before, "high", 2},
		{"delete", []Op{{Op: "delete", Path: "custom/sales_invoice.rk"}}, before, "high", 4},
		{"role", []Op{{Op: "create", Path: "apps/crm/roles/viewer.rk"}}, before, "high", 5},
		{"connector", []Op{{Op: "replace", Path: "connectors/whatsapp_business.rk"}}, before, "medium", 3},
		{"config apps", []Op{{Op: "patch", Path: "config.rk"}}, with("config.rk", map[string]any{"apps": []any{"core", "crm"}, "tax": cfg["tax"], "theme": "a"}), "medium", 4},
		{"config tax", []Op{{Op: "patch", Path: "config.rk"}}, with("config.rk", map[string]any{"apps": cfg["apps"], "tax": map[string]any{"ppn_rate": 11}, "theme": "a"}), "high", 4},
		{"config connector_auth", []Op{{Op: "patch", Path: "config.rk"}}, with("config.rk", map[string]any{"apps": cfg["apps"], "tax": cfg["tax"], "theme": "a", "connector_auth": []any{"kasir"}}), "high", 4},
		{"config theme", []Op{{Op: "patch", Path: "config.rk"}}, with("config.rk", map[string]any{"apps": cfg["apps"], "tax": cfg["tax"], "theme": "b"}), "low", 4},
		{"features", []Op{{Op: "patch", Path: "features.rk"}}, before, "medium", 4},
		// contract 0045 §2b: company pieces that touch money, stock or core drafts
		{"submittable company model", []Op{{Op: "create", Path: "models/x_baru.rk"}}, with("models/x_baru.rk", map[string]any{"name": "x_baru", "is_submittable": true, "permissions": []any{}}), "high", 4},
		{"append-only company model", []Op{{Op: "create", Path: "models/x_baru.rk"}}, with("models/x_baru.rk", map[string]any{"name": "x_baru", "append_only": true, "permissions": []any{}}), "high", 4},
		{"company model moving stock", []Op{{Op: "create", Path: "models/x_baru.rk"}}, with("models/x_baru.rk", map[string]any{"name": "x_baru", "stock": map[string]any{"moves": "items"}, "permissions": []any{}}), "high", 4},
		{"flow creates a core draft", []Op{{Op: "create", Path: "flows/x_f.rk"}}, with("flows/x_f.rk", flow("deal.updated", map[string]any{"create": "invoice", "set": map[string]any{"catatan": "dari deal"}})), "high", 4},
		{"flow updates a money field of doc", []Op{{Op: "create", Path: "flows/x_f.rk"}}, with("flows/x_f.rk", flow("x_sewa.created", map[string]any{"update": "doc", "set": map[string]any{"deposit": "0"}})), "high", 4},
		{"flow money write nested in if", []Op{{Op: "create", Path: "flows/x_f.rk"}}, with("flows/x_f.rk", flow("deal.updated",
			map[string]any{"find": "x_sewa", "where": "true", "as": "s"},
			map[string]any{"if": "true", "then": []any{map[string]any{"for_each": "[1]", "as": "i", "do": []any{map[string]any{"update": "s", "set": map[string]any{"deposit": "1"}}}}}})), "high", 4},
		{"flow creates with a money field", []Op{{Op: "replace", Path: "flows/x_f.rk"}}, with("flows/x_f.rk", flow("deal.updated", map[string]any{"create": "x_sewa", "set": map[string]any{"deposit": "1"}})), "high", 4},
		{"flow updates an unbound record", []Op{{Op: "create", Path: "flows/x_f.rk"}}, with("flows/x_f.rk", flow("deal.updated", map[string]any{"update": "x", "set": map[string]any{"catatan": "a"}})), "high", 4},
		{"flow rebinds a variable in if/else", []Op{{Op: "create", Path: "flows/x_f.rk"}}, with("flows/x_f.rk", flow("deal.updated",
			map[string]any{"if": "true", "then": []any{map[string]any{"find": "x_sewa", "where": "true", "as": "r"}}, "else": []any{map[string]any{"find": "deal", "where": "true", "as": "r"}}},
			map[string]any{"update": "r", "set": map[string]any{"deposit": "1"}})), "high", 4},
		{"flow shadows a variable in for_each", []Op{{Op: "create", Path: "flows/x_f.rk"}}, with("flows/x_f.rk", flow("deal.updated",
			map[string]any{"find": "deal", "where": "true", "as": "s"},
			map[string]any{"for_each": "doc.items", "as": "s", "do": []any{map[string]any{"update": "s", "set": map[string]any{"notes": "1"}}}})), "high", 4},
		{"flow creates an unknown model", []Op{{Op: "create", Path: "flows/x_f.rk"}}, with("flows/x_f.rk", flow("deal.updated", map[string]any{"create": "x_hilang", "set": map[string]any{"a": "1"}})), "high", 4},
		{"flow sets only a text field", []Op{{Op: "create", Path: "flows/x_f.rk"}}, with("flows/x_f.rk", flow("x_sewa.updated",
			map[string]any{"update": "doc", "set": map[string]any{"catatan": "a"}},
			map[string]any{"create": "x_sewa", "set": map[string]any{"catatan": "b"}, "as": "baru"},
			map[string]any{"update": "baru", "set": map[string]any{"catatan": "c"}})), "medium", 4},
		{"flow on a non-record event updates doc", []Op{{Op: "create", Path: "flows/x_f.rk"}}, with("flows/x_f.rk", flow("x_sewa.paid", map[string]any{"update": "doc", "set": map[string]any{"catatan": "a"}})), "high", 4},
		{"flow on a submitted record updates doc text", []Op{{Op: "create", Path: "flows/x_f.rk"}}, with("flows/x_f.rk", flow("x_sewa.submitted", map[string]any{"update": "doc", "set": map[string]any{"catatan": "a"}})), "medium", 4},
		{"flow fills child rows with a money field", []Op{{Op: "create", Path: "flows/x_f.rk"}}, with("flows/x_f.rk", flow("deal.updated", map[string]any{"create": "x_sewa", "set": map[string]any{"baris": "[{'alat': 'bor', 'harga': 1}]"}})), "high", 4},
		{"flow updates child rows with a money field", []Op{{Op: "create", Path: "flows/x_f.rk"}}, with("flows/x_f.rk", flow("x_sewa.updated", map[string]any{"update": "doc", "set": map[string]any{"baris": "event.record.baris"}})), "high", 4},
		{"flow fills child rows of an unknown model", []Op{{Op: "create", Path: "flows/x_f.rk"}}, with("flows/x_f.rk", flow("deal.updated", map[string]any{"create": "x_sewa", "set": map[string]any{"hilang": "[]"}})), "high", 4},
		{"flow fills child rows with text only", []Op{{Op: "create", Path: "flows/x_f.rk"}}, with("flows/x_f.rk", flow("deal.updated", map[string]any{"create": "x_sewa", "set": map[string]any{"catatan_baris": "[{'isi': 'a'}]"}})), "medium", 4},
		{"overlay removes an option", []Op{{Op: "patch", Path: "custom/x_tugas.rk"}}, with("custom/x_tugas.rk", map[string]any{"model": "x_tugas", "options": map[string]any{"status": []any{map[string]any{"value": "baru", "label": "Baru"}}}}), "high", 4},
		{"overlay adds an option", []Op{{Op: "patch", Path: "custom/x_tugas.rk"}}, with("custom/x_tugas.rk", map[string]any{"model": "x_tugas", "options": map[string]any{"status": []any{map[string]any{"value": "baru", "label": "Baru"}, map[string]any{"value": "batal", "label": "Batal"}, map[string]any{"value": "tunda", "label": "Tunda"}}}}), "medium", 4},
		{"company model removes an option", []Op{{Op: "patch", Path: "models/x_tiket.rk"}}, with("models/x_tiket.rk", map[string]any{"name": "x_tiket", "fields": []any{map[string]any{"name": "status", "type": "select", "options": []any{map[string]any{"value": "baru", "label": "Baru"}}}}}), "high", 4},
	}
	for _, c := range cases {
		a := AssessWith(c.ops, before, c.after, Env{Model: model, ModuleOf: moduleOf})
		if a.Risk != c.risk || len(a.Layers) != 1 || a.Layers[0] != c.layer {
			t.Errorf("%s: risk %s layers %v (%v), want %s %d", c.name, a.Risk, a.Layers, a.Reasons, c.risk, c.layer)
		}
	}
	// contract 0026: switching a company model's append_only off (or on) is high risk, never auto-applied
	led := map[string]any{"name": "ledger", "append_only": true, "fields": []any{map[string]any{"name": "note", "type": "data"}}}
	files["models/ledger.rk"] = led
	off := map[string]any{"name": "ledger", "fields": led["fields"]}
	if a := Assess([]Op{{Op: "replace", Path: "models/ledger.rk"}}, before, with("models/ledger.rk", off), model); a.Risk != "high" {
		t.Errorf("append_only off: risk %s (%v), want high", a.Risk, a.Reasons)
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
		"models/x_kunjungan.rk":      {"name": "x_kunjungan", "fields": []any{map[string]any{"name": "toko", "type": "data"}}},
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
		// the site company module (contract 0016): layer 4, the company's own files
		{"new company model", []Op{{Op: "create", Path: "models/x_baru.rk"}}, with("models/x_baru.rk", map[string]any{"name": "x_baru", "fields": []any{map[string]any{"name": "t", "type": "data"}}, "permissions": []any{}}), "medium", 4},
		{"new company model with money", []Op{{Op: "create", Path: "models/x_baru.rk"}}, with("models/x_baru.rk", map[string]any{"name": "x_baru", "fields": []any{map[string]any{"name": "t", "type": "currency"}}}), "high", 4},
		{"field added to a company model", []Op{{Op: "patch", Path: "models/x_kunjungan.rk"}}, with("models/x_kunjungan.rk", map[string]any{"name": "x_kunjungan", "fields": []any{map[string]any{"name": "toko", "type": "data"}, map[string]any{"name": "kota", "type": "data"}}}), "medium", 4},
		{"company model relabelled", []Op{{Op: "patch", Path: "models/x_kunjungan.rk"}}, with("models/x_kunjungan.rk", map[string]any{"name": "x_kunjungan", "fields": []any{map[string]any{"name": "toko", "type": "data", "label": "Toko"}}}), "low", 4},
		{"company model permissions", []Op{{Op: "patch", Path: "models/x_kunjungan.rk"}}, with("models/x_kunjungan.rk", map[string]any{"name": "x_kunjungan", "fields": files["models/x_kunjungan.rk"]["fields"], "permissions": []any{map[string]any{"role": "owner"}}}), "high", 4},
		{"new company flow", []Op{{Op: "create", Path: "flows/x_a.rk"}}, with("flows/x_a.rk", map[string]any{"name": "x_a", "on": "deal.updated"}), "medium", 4},
		{"delete company model", []Op{{Op: "delete", Path: "models/x_kunjungan.rk"}}, before, "high", 4},
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

// A new model without permissions gives every role full access: still medium, with a reason that says so.
func TestAssessNewModelPermissions(t *testing.T) {
	none := func(string) (map[string]any, bool) { return nil, false }
	ops := []Op{{Op: "create", Path: "apps/crm/models/x.rk"}}
	model := func(string) map[string]any { return nil }
	after := func(m map[string]any) func(string) (map[string]any, bool) {
		return func(p string) (map[string]any, bool) { return m, p == "apps/crm/models/x.rk" }
	}
	const why = "medium: new model apps/crm/models/x.rk without permissions: every role has full access"
	a := Assess(ops, none, after(map[string]any{"name": "x"}), model)
	if a.Risk != "medium" || !slices.Contains(a.Reasons, why) {
		t.Errorf("without permissions: %s %v", a.Risk, a.Reasons)
	}
	a = Assess(ops, none, after(map[string]any{"name": "x", "permissions": []any{map[string]any{"role": "owner", "read": true}}}), model)
	if a.Risk != "medium" || slices.Contains(a.Reasons, why) {
		t.Errorf("with permissions: %s %v", a.Risk, a.Reasons)
	}
}

func TestSandboxFollowsSymlinks(t *testing.T) {
	tmp := t.TempDir()
	apps := filepath.Join(tmp, "apps")
	real := filepath.Join(tmp, "real-connectors")
	nested := filepath.Join(tmp, "shared")
	for _, d := range []string{apps, real, nested} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(real, "a.rk"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(nested, "b.rk"), []byte("b"), 0o644)
	if err := os.Symlink(nested, filepath.Join(real, "sub")); err != nil {
		t.Fatal(err)
	}
	os.Symlink(real, filepath.Join(nested, "loop")) // a cycle must not hang
	link := filepath.Join(tmp, "connectors")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	s := &Service{AppsDir: apps, ConnectorsDir: link}
	r, cleanup, _, err := s.sandbox(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, f := range []string{"a.rk", "sub/b.rk"} {
		if b, err := os.ReadFile(filepath.Join(r.connectors, f)); err != nil || len(b) != 1 {
			t.Errorf("%s not copied: %v", f, err)
		}
	}
}

// An op with base runs only while its file still has that text; without base it runs as before.
func TestResultStaleBase(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "site", "custom"), 0o755)
	f := filepath.Join(dir, "site", "custom", "deal.rk")
	orig := "name: deal\nfields: []\n"
	os.WriteFile(f, []byte(orig), 0o644)
	r := roots{apps: filepath.Join(dir, "apps"), site: filepath.Join(dir, "site")}
	op := Op{Op: "replace", Path: "custom/deal.rk", Content: "name: deal\n", Base: store.FileHash(orig)}
	if _, err := r.result([]Op{op}); err != nil {
		t.Fatalf("matching base: %v", err)
	}
	os.WriteFile(f, []byte("name: deal\nfields: [x]\n"), 0o644)
	_, err := r.result([]Op{op})
	if e, ok := err.(*Error); !ok || e.Status != 409 || e.Code != "stale_base" || !strings.Contains(e.Msg, "custom/deal.rk") {
		t.Fatalf("stale base: %v", err)
	}
	op.Base = ""
	if _, err := r.result([]Op{op}); err != nil {
		t.Fatalf("no base: %v", err)
	}
}

// Site process files (contract 0014) are change-set paths at the site level only (0042 section 8).
func TestSchemaAllowsSiteProcesses(t *testing.T) {
	for path, ok := range map[string]bool{
		"processes/kopi_senja.rk":       true,
		"processes/a.rk":                true,
		"processes/Kopi.rk":             false,
		"processes/kopi-senja.rk":       false,
		"processes/1kopi.rk":            false,
		"processes/_x.rk":               false,
		"processes/a/b.rk":              false,
		"apps/crm/processes/kopi.rk":    false,
		"modules/crm/processes/kopi.rk": false,
	} {
		doc := map[string]any{"id": newID(), "status": "proposed", "title": "Proses usaha", "author": map[string]any{"kind": "user", "id": "o@contoh.test"},
			"ops": []any{map[string]any{"op": "create", "path": path, "content": "name: x\n"}}}
		errs := schema.Embedded().Validate("changeset", schema.FromData(normalize(doc)))
		if (len(errs) == 0) != ok {
			t.Errorf("%s: accepted=%v, want %v (%v)", path, len(errs) == 0, ok, errs)
		}
	}
}

// A value meant to be written once (set_once, contract 0030 §4: clock-in time, position, a signature) stays
// evidence: a company change that makes it editable again, or a company flow that writes it, is high risk, so it
// never applies on a config.ai approver's word (audit layar-kerja C1, card t_721b9456).
func TestAssessSetOnce(t *testing.T) {
	absen := map[string]any{"name": "x_absen", "fields": []any{map[string]any{"name": "jam_masuk", "type": "datetime", "set_once": true}, map[string]any{"name": "catatan", "type": "text"}}}
	log := map[string]any{"name": "attendance_log", "fields": []any{map[string]any{"name": "clock_in", "type": "datetime", "set_once": true}, map[string]any{"name": "note", "type": "text"}}}
	files := map[string]map[string]any{"models/x_absen.rk": absen, "custom/attendance_log.rk": {"model": "attendance_log", "fields": []any{map[string]any{"name": "x_lokasi", "type": "data"}}}}
	before := func(p string) (map[string]any, bool) { m, ok := files[p]; return m, ok }
	with := func(p string, m map[string]any) func(string) (map[string]any, bool) {
		return func(q string) (map[string]any, bool) {
			if q == p {
				return m, true
			}
			return before(q)
		}
	}
	model := func(n string) map[string]any {
		return map[string]map[string]any{"attendance_log": log, "x_absen": absen}[n]
	}
	field := func(name, typ string, extra ...any) map[string]any {
		f := map[string]any{"name": name, "type": typ}
		for i := 0; i+1 < len(extra); i += 2 {
			f[extra[i].(string)] = extra[i+1]
		}
		return f
	}
	flow := func(on string, steps ...any) map[string]any {
		return map[string]any{"name": "x_f", "on": on, "steps": steps}
	}
	cases := []struct {
		name  string
		path  string
		after map[string]any
		risk  string
	}{
		{"company model drops set_once", "models/x_absen.rk", map[string]any{"name": "x_absen", "fields": []any{field("jam_masuk", "datetime"), field("catatan", "text")}}, "high"},
		{"company model changes a set_once field", "models/x_absen.rk", map[string]any{"name": "x_absen", "fields": []any{field("jam_masuk", "date", "set_once", true), field("catatan", "text")}}, "high"},
		{"company model relabels a set_once field", "models/x_absen.rk", map[string]any{"name": "x_absen", "fields": []any{field("jam_masuk", "datetime", "set_once", true, "label", map[string]any{"id": "Jam datang"}), field("catatan", "text")}}, "low"},
		{"company model changes another field", "models/x_absen.rk", map[string]any{"name": "x_absen", "fields": []any{field("jam_masuk", "datetime", "set_once", true), field("catatan", "data")}}, "medium"},
		{"overlay switches off set_once", "custom/attendance_log.rk", map[string]any{"model": "attendance_log", "fields": []any{field("x_lokasi", "data"), field("clock_in", "datetime", "set_once", false)}}, "high"},
		{"flow writes a set_once field", "flows/x_f.rk", flow("attendance_log.updated", map[string]any{"update": "doc", "set": map[string]any{"clock_in": "now()"}}), "high"},
		{"flow writes another field", "flows/x_f.rk", flow("attendance_log.updated", map[string]any{"update": "doc", "set": map[string]any{"note": "'ok'"}}), "medium"},
		// a new flow file in an app or module runs the same way (audit changeset-set-once 1)
		{"new module flow writes a set_once field", "modules/lapangan/flows/x_f.rk", flow("attendance_log.updated", map[string]any{"update": "doc", "set": map[string]any{"clock_in": "now()"}}), "high"},
		{"new app flow writes a set_once field", "apps/absen/flows/x_f.rk", flow("attendance_log.updated", map[string]any{"update": "doc", "set": map[string]any{"clock_in": "now()"}}), "high"},
		{"new module flow writes another field", "modules/lapangan/flows/x_f.rk", flow("attendance_log.updated", map[string]any{"update": "doc", "set": map[string]any{"note": "'ok'"}}), "medium"},
		// the kind is the folder after the unit's name, whatever the unit is called (audit changeset-set-once 2, 3)
		{"flow of a module named screens", "modules/screens/flows/x_f.rk", flow("attendance_log.updated", map[string]any{"update": "doc", "set": map[string]any{"clock_in": "now()"}}), "high"},
		{"flow of an app named dashboards", "apps/dashboards/flows/x_f.rk", flow("attendance_log.updated", map[string]any{"update": "doc", "set": map[string]any{"clock_in": "now()"}}), "high"},
		{"money model of a module named flows", "modules/flows/models/x_m.rk", map[string]any{"name": "x_m", "fields": []any{field("fee", "currency")}, "permissions": []any{}}, "high"},
		{"screen of a module named flows", "modules/flows/screens/x_s.rk", map[string]any{"name": "x_s"}, "low"},
	}
	for _, c := range cases {
		op := "replace"
		if _, ok := files[c.path]; !ok {
			op = "create"
		}
		a := AssessWith([]Op{{Op: op, Path: c.path}}, before, with(c.path, c.after), Env{Model: model})
		if a.Risk != c.risk {
			t.Errorf("%s: risk %s (%v), want %s", c.name, a.Risk, a.Reasons, c.risk)
		}
	}
}

// A field some roles may not change or see (read_only_for, hidden_for; contract 0027) is a permission: a flow step
// writes it whatever those roles may do (0027 §6), so a company flow that writes it, or a change to who it is limited
// for, is high and needs the owner (audit tax-fields N1, card t_2302cc49: a sales manager approved its own flow that
// set the customer's VAT collector, which only finance may change).
func TestAssessRoleLimitedFields(t *testing.T) {
	customer := map[string]any{"name": "customer", "fields": []any{
		map[string]any{"name": "vat_collector", "type": "select", "read_only_for": []any{"sales_manager", "head_office"}},
		map[string]any{"name": "phone", "type": "phone"},
		map[string]any{"name": "contacts", "type": "table", "of": "contact_person"},
		map[string]any{"name": "npwp", "type": "data", "hidden_for": []any{"outlet_staff"}},
		map[string]any{"name": "credit_limit", "type": "currency"},
		map[string]any{"name": "signed_at", "type": "datetime", "set_once": true},
	}}
	item := map[string]any{"name": "item", "fields": []any{map[string]any{"name": "item_name", "type": "data"}}}
	contact := map[string]any{"name": "contact_person", "fields": []any{map[string]any{"name": "nik", "type": "data", "hidden_for": []any{"outlet_staff"}}}}
	plain := map[string]any{"name": "note_line", "fields": []any{map[string]any{"name": "text", "type": "data"}}}
	order := map[string]any{"name": "x_order", "fields": []any{
		map[string]any{"name": "phone", "type": "data", "hidden_for": []any{"outlet_staff"}},
		map[string]any{"name": "lines", "type": "table", "of": "note_line"},
		map[string]any{"name": "catatan", "type": "text"},
	}}
	files := map[string]map[string]any{"models/x_order.rk": order}
	before := func(p string) (map[string]any, bool) { m, ok := files[p]; return m, ok }
	with := func(p string, m map[string]any) func(string) (map[string]any, bool) {
		return func(q string) (map[string]any, bool) {
			if q == p {
				return m, true
			}
			return before(q)
		}
	}
	model := func(n string) map[string]any {
		return map[string]map[string]any{"customer": customer, "contact_person": contact, "note_line": plain, "x_order": order, "item": item}[n]
	}
	flow := func(on string, steps ...any) map[string]any {
		return map[string]any{"name": "x_f", "on": on, "steps": steps}
	}
	upd := func(set map[string]any) map[string]any { return map[string]any{"update": "doc", "set": set} }
	// audit flow-locked F1: v is an item when the loop body first updates it, and the customer the body's last step
	// finds from the second pass on; judged once, the update looked like a write to an item
	rebound := func(set map[string]any) map[string]any {
		return flow("customer.updated",
			map[string]any{"find": "item", "as": "v"},
			map[string]any{"find_all": "item", "limit": 2, "as": "rows"},
			map[string]any{"for_each": "rows", "as": "r", "do": []any{
				map[string]any{"update": "v", "if": "v.name == event.name", "set": set},
				map[string]any{"find": "customer", "where": "name == event.name", "as": "v"},
			}})
	}
	cases := []struct {
		name  string
		path  string
		after map[string]any
		risk  string
	}{
		{"flow writes a read_only_for field", "flows/x_f.rk", flow("customer.updated", upd(map[string]any{"vat_collector": "'other'"})), "high"},
		{"flow creates a record with a read_only_for field", "flows/x_f.rk", flow("x_order.created", map[string]any{"create": "customer", "set": map[string]any{"vat_collector": "'government'"}}), "high"},
		{"flow writes a hidden_for field", "flows/x_f.rk", flow("x_order.updated", upd(map[string]any{"phone": "'0812'"})), "high"},
		{"flow fills rows whose model has a hidden_for field", "flows/x_f.rk", flow("customer.updated", upd(map[string]any{"contacts": "[]"})), "high"},
		{"flow writes an unlimited field", "flows/x_f.rk", flow("customer.updated", upd(map[string]any{"phone": "'0812'"})), "medium"},
		{"loop rebinds a variable to a read_only_for field's model", "flows/x_f.rk", rebound(map[string]any{"vat_collector": "'other'"}), "high"},
		{"loop rebinds a variable to a hidden_for field's model", "flows/x_f.rk", rebound(map[string]any{"npwp": "'0'"}), "high"},
		{"loop rebinds a variable to a money field's model", "flows/x_f.rk", rebound(map[string]any{"credit_limit": "0"}), "high"},
		{"loop rebinds a variable to a set_once field's model", "flows/x_f.rk", rebound(map[string]any{"signed_at": "now()"}), "high"},
		{"loop rebinds a variable, plain field", "flows/x_f.rk", rebound(map[string]any{"phone": "'0812'"}), "medium"},
		{"flow fills rows without limited fields", "flows/x_f.rk", flow("x_order.updated", upd(map[string]any{"lines": "[]"})), "medium"},
		{"company model drops a hidden_for", "models/x_order.rk", map[string]any{"name": "x_order", "fields": []any{
			map[string]any{"name": "phone", "type": "data"}, order["fields"].([]any)[1], order["fields"].([]any)[2]}}, "high"},
		{"company model widens read_only_for", "models/x_order.rk", map[string]any{"name": "x_order", "fields": []any{
			order["fields"].([]any)[0], order["fields"].([]any)[1], map[string]any{"name": "catatan", "type": "text", "read_only_for": []any{"cashier"}}}}, "high"},
		{"company model changes another field", "models/x_order.rk", map[string]any{"name": "x_order", "fields": []any{
			order["fields"].([]any)[0], order["fields"].([]any)[1], map[string]any{"name": "catatan", "type": "data"}}}, "medium"},
	}
	for _, c := range cases {
		op := "replace"
		if _, ok := files[c.path]; !ok {
			op = "create"
		}
		a := AssessWith([]Op{{Op: op, Path: c.path}}, before, with(c.path, c.after), Env{Model: model})
		if a.Risk != c.risk {
			t.Errorf("%s: risk %s (%v), want %s", c.name, a.Risk, a.Reasons, c.risk)
		}
	}
}

// changeset.json takes a work screen in an app as it does in a module (the loader reads both; audit layar-kerja C2).
func TestSchemaAllowsAppScreens(t *testing.T) {
	for path, ok := range map[string]bool{
		"apps/kasir/screens/kasir.rk":   true,
		"modules/pos/screens/kasir.rk":  true,
		"apps/kasir/screens/Kasir.rk":   false,
		"apps/kasir/screens/a/kasir.rk": false,
		"screens/kasir.rk":              false,
	} {
		doc := map[string]any{"id": newID(), "status": "proposed", "title": "Layar", "author": map[string]any{"kind": "user", "id": "o@contoh.test"},
			"ops": []any{map[string]any{"op": "create", "path": path, "content": "name: kasir\n"}}}
		errs := schema.Embedded().Validate("changeset", schema.FromData(normalize(doc)))
		if (len(errs) == 0) != ok {
			t.Errorf("%s: accepted=%v, want %v (%v)", path, len(errs) == 0, ok, errs)
		}
	}
}

// Audit menu-labels F2: a change set may write custom/app.<app>.rk (contract 0049), and only that shape.
func TestChangesetAppOverlayPath(t *testing.T) {
	for path, ok := range map[string]bool{
		"custom/app.crm.rk":              true,
		"custom/app.sewa_alat.rk":        true,
		"custom/app.rk":                  true, // the overlay of a model named app
		"custom/app..rk":                 false,
		"custom/app.x.y.rk":              false,
		"custom/app.1x.rk":               false,
		"custom/app./../system.rk":       false,
		"custom/app.crm/../../system.rk": false,
	} {
		doc := map[string]any{"id": newID(), "status": "proposed", "title": "Menu", "author": map[string]any{"kind": "user", "id": "o@contoh.test"},
			"ops": []any{map[string]any{"op": "create", "path": path, "content": "app: crm\n"}}}
		errs := schema.Embedded().Validate("changeset", schema.FromData(normalize(doc)))
		if (len(errs) == 0) != ok {
			t.Errorf("%s: accepted=%v, want %v (%v)", path, len(errs) == 0, ok, errs)
		}
		if _, app := appOverlayPath(path); app != (ok && path != "custom/app.rk") {
			t.Errorf("appOverlayPath(%s) = %v", path, app)
		}
	}
}
