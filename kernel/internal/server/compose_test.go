package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Contract 0032 end to end on testdata/config-modules: a composed site (config.modules, no apps) installs the closure
// of its modules, /meta says who brings each one in and draws one menu group per module, and adding or removing a
// module is an ordinary change set: added modules migrate on apply, a removal that would hide rows is raised to high
// at preview, revert brings the rows back, and a module still required by another stays installed.
func TestConfigModulesEndToEnd(t *testing.T) {
	e := newEnvFrom(t, func(dir string) (apps, site string) {
		if err := exec.Command("cp", "-r", "../../testdata/config-modules/.", dir).Run(); err != nil {
			t.Fatal(err)
		}
		// a second approver, so approvable_by tells medium (owner + approvers) from high (owner only)
		f, err := os.OpenFile(filepath.Join(dir, "site", "config.rk"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString("ai: {approvers: [admin]}\n"); err != nil {
			t.Fatal(err)
		}
		f.Close()
		return filepath.Join(dir, "apps"), filepath.Join(dir, "site")
	})
	e.login()
	m := e.must(200, "GET", "/api/v1/meta", nil).JSON
	if got := strings.Join(toStrings(m["installed"]), ","); got != "contacts,invoicing,payments,site" {
		t.Fatalf("installed = %s", got)
	}
	by := map[string]string{}
	for _, x := range m["modules"].([]any) {
		by[get(x, "name").(string)] = strings.Join(toStrings(get(x, "installed_by")), ",")
	}
	if by["invoicing"] != "config" || by["payments"] != "config" || by["contacts"] != "" {
		t.Errorf("installed_by = %v", by)
	}
	var groups []string
	for _, g := range m["menu"].([]any) {
		groups = append(groups, get(g, "module").(string)+":"+strings.Join(toStrings(get(g, "items")), "+"))
	}
	if got := strings.Join(groups, ","); got != "payments:payment,invoicing:invoice,contacts:customer,site:x_sewa" {
		t.Errorf("menu = %s", got)
	}
	if get(m, "site", "config", "modules", "payments") != "^1.0" {
		t.Errorf("site.config.modules as written: %v", get(m, "site", "config", "modules"))
	}

	// the catalogue reads the same resolution: invoicing comes from config, pipeline is outside plan sewa
	cat := e.must(200, "GET", "/api/v1/catalogue", nil).JSON
	if inv := catalogueEntry(t, cat["modules"], "invoicing"); get(inv, "installed_by", 0) != "config" || inv["in_plan"] != true {
		t.Errorf("catalogue invoicing: %v", inv)
	}
	if pl := catalogueEntry(t, cat["modules"], "pipeline"); pl["installed"] != false || pl["in_plan"] != false {
		t.Errorf("catalogue pipeline: %v", pl)
	}

	run := func(title string, patch []any) map[string]any {
		t.Helper()
		cs := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": title, "ops": []any{
			map[string]any{"op": "patch", "path": "config.rk", "patch": patch}}}).JSON
		id := cs["id"].(string)
		if v := e.must(200, "POST", "/api/v1/changesets/"+id+"/validate", nil).JSON; v["status"] != "validated" {
			t.Fatalf("%s: validate %v", title, v["validation"])
		}
		pv := e.must(200, "POST", "/api/v1/changesets/"+id+"/preview", nil).JSON
		pv["proposed_risk"] = cs["risk"]
		return pv
	}
	apply := func(pv map[string]any) {
		t.Helper()
		id := pv["id"].(string)
		e.must(200, "POST", "/api/v1/changesets/"+id+"/approve", map[string]any{"note": "ok"})
		if ap := e.must(200, "POST", "/api/v1/changesets/"+id+"/apply", nil).JSON; ap["status"] != "applied" {
			t.Fatalf("apply: %v", ap)
		}
	}

	// the plan allows stock? no: sewa allows invoicing and payments only, so adding stock fails module_not_in_plan
	bad := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Tambah stok", "ops": []any{map[string]any{"op": "patch", "path": "config.rk",
		"patch": []any{map[string]any{"op": "add", "path": "/modules/stock", "value": "^1.0"}}}}}).JSON
	if v := e.must(200, "POST", "/api/v1/changesets/"+bad["id"].(string)+"/validate", nil).JSON; v["status"] != "invalid" ||
		get(v, "validation", "errors", 0, "code") != "module_not_in_plan" {
		t.Fatalf("stock outside the plan: %v", v["validation"])
	}

	// add stock with a plan without module keys (dasar extended → none): medium, the preview lists the closure, apply migrates
	add := run("Tambah stok", []any{
		map[string]any{"op": "remove", "path": "/plan"},
		map[string]any{"op": "add", "path": "/modules/stock", "value": "^1.0"},
		map[string]any{"op": "add", "path": "/modules/contacts", "value": "^1.0"},
	})
	if got := strings.Join(toStrings(add["approvable_by"]), ","); got != "admin,owner" {
		t.Fatalf("add stock: approvable_by %v, want [admin owner]", add["approvable_by"])
	}
	if add["proposed_risk"] != "medium" || add["risk"] != "medium" || strings.Join(toStrings(get(add, "preview", "modules", "added")), ",") != "stock" ||
		!strings.Contains(string(mustJSON(get(add, "preview", "migrations"))), "stock_item") {
		t.Fatalf("add stock: risk %v/%v preview %v", add["proposed_risk"], add["risk"], add["preview"])
	}
	apply(add)
	e.must(201, "POST", "/api/v1/r/stock_item", map[string]any{"item_name": "Molen"})
	if by := e.must(200, "GET", "/api/v1/meta", nil).JSON; get(by, "models", "stock_item") == nil {
		t.Fatal("stock_item not in /meta after apply")
	}

	// contacts leaves config.modules but invoicing still requires it: low, and the preview says so
	keep := run("Lepas kontak dari daftar", []any{map[string]any{"op": "remove", "path": "/modules/contacts"}})
	if keep["risk"] != "low" || len(toStrings(get(keep, "preview", "modules", "removed"))) != 0 ||
		!strings.Contains(string(mustJSON(get(keep, "preview", "notes"))), "contacts tetap terpasang: dibutuhkan oleh") {
		t.Fatalf("kept contacts: risk %v preview %v", keep["risk"], keep["preview"])
	}

	// removing stock while stock_item holds a row: medium when proposed, high at preview, owner approves
	rm := run("Lepas stok", []any{map[string]any{"op": "remove", "path": "/modules/stock"}})
	// stock_item is not submittable: no "submitted documents" note (audit 14 Nov L1, store.CountSubmittedTable)
	if rm["proposed_risk"] != "medium" || rm["risk"] != "high" || get(rm, "preview", "modules", "rows", "stock_item") != 1.0 ||
		get(rm, "preview", "modules", "submitted") != nil || strings.Contains(string(mustJSON(get(rm, "preview", "notes"))), "sudah diajukan") {
		t.Fatalf("remove stock: risk %v/%v preview %v", rm["proposed_risk"], rm["risk"], rm["preview"])
	}
	// the preview raised the risk, so approvable_by follows it: owner only (contract 0045 §3)
	if got := strings.Join(toStrings(rm["approvable_by"]), ","); got != "owner" {
		t.Fatalf("remove stock: approvable_by %v after preview raised risk to high, want [owner]", rm["approvable_by"])
	}
	apply(rm)
	if m := e.must(200, "GET", "/api/v1/meta", nil).JSON; get(m, "models", "stock_item") != nil {
		t.Fatal("stock_item still in /meta after removal")
	}
	e.must(404, "GET", "/api/v1/r/stock_item", nil)

	// revert: the module is back with its row (the table was kept)
	e.must(200, "POST", "/api/v1/changesets/"+rm["id"].(string)+"/revert", nil)
	list := e.must(200, "GET", "/api/v1/r/stock_item", nil).JSON
	if n := len(list["data"].([]any)); n != 1 {
		t.Fatalf("stock_item rows after revert = %d, want 1: %v", n, list)
	}
}
