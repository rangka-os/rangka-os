package server

import (
	"os"
	"path/filepath"
	"testing"
)

const accountModel = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json\n" +
	"name: x_akun\nnaming: AKN-{####}\nlabel: {id: Akun}\ntitle_field: akun_name\nfields:\n" +
	"  - {name: akun_name, type: data, label: {id: Nama}, in_list: true}\n" +
	"  - {name: credit_limit, type: currency, label: {id: Batas kredit}, default: '1000', read_only_for: [sales_rep]}\n" +
	"  - {name: credit_hold, type: check, label: {id: Tahan kredit}, read_only_for: [sales_rep]}\n" +
	"permissions:\n  - {role: sales_rep, read: true, create: true, write: true}\n"

// read_only_for (card t_6c114aa0): a role listed there reads the field but a client write by it neither sets it on
// create (the default applies) nor changes it on update; /meta shows it read-only to that role; the owner writes it.
func TestReadOnlyForOverHTTP(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) {
		if err := os.WriteFile(filepath.Join(apps, "crm", "models", "x_akun.rk"), []byte(accountModel), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	e.login()
	rep := repEnv(t, e)

	for _, f := range get(rep.must(200, "GET", "/api/v1/meta", nil).JSON, "models", "x_akun", "fields").([]any) {
		if n := get(f, "name"); (n == "credit_limit" || n == "credit_hold") && get(f, "read_only") != true {
			t.Fatalf("meta: %s is not read-only for the rep: %v", n, f)
		}
	}
	created := rep.must(201, "POST", "/api/v1/r/x_akun", map[string]any{"akun_name": "Toko A", "credit_limit": 99000000, "credit_hold": true}).JSON
	name := created["name"].(string)
	if created["credit_limit"] != 1000.0 || created["credit_hold"] == true {
		t.Fatalf("the rep set read-only fields on create: limit %v hold %v", created["credit_limit"], created["credit_hold"])
	}
	up := rep.must(200, "PATCH", "/api/v1/r/x_akun/"+name, map[string]any{"akun_name": "Toko A2", "credit_limit": 50000000, "credit_hold": true}).JSON
	if up["akun_name"] != "Toko A2" || up["credit_limit"] != 1000.0 || up["credit_hold"] == true {
		t.Fatalf("the rep changed read-only fields: %v", up)
	}
	if r := rep.must(200, "GET", "/api/v1/r/x_akun/"+name, nil).JSON; r["credit_limit"] != 1000.0 {
		t.Fatalf("the rep still reads the field: %v", r)
	}
	// the owner decides them
	own := e.must(200, "PATCH", "/api/v1/r/x_akun/"+name, map[string]any{"credit_limit": 5000000, "credit_hold": true}).JSON
	if own["credit_limit"] != 5000000.0 || own["credit_hold"] != true {
		t.Fatalf("owner: %v", own)
	}
	// and a later save by the rep keeps them
	again := rep.must(200, "PATCH", "/api/v1/r/x_akun/"+name, map[string]any{"credit_limit": 0, "credit_hold": false}).JSON
	if again["credit_limit"] != 5000000.0 || again["credit_hold"] != true {
		t.Fatalf("the rep cleared the owner's decision: %v", again)
	}
}
