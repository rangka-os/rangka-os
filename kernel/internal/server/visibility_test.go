package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const crewModel = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json\n" +
	"name: crew\nnaming: CRW-{####}\nlabel: {id: Kru}\ntitle_field: crew_name\nfields:\n" +
	"  - {name: crew_name, type: data, label: {id: Nama}, in_list: true}\n" +
	"  - {name: pay, type: currency, label: {id: Upah}, in_list: true, hidden_for: [sales_rep]}\n" +
	"permissions:\n  - {role: sales_rep, read: true, export: true}\n"

const crewPayDashboard = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/dashboard.json\n" +
	"name: crew_pay\nlabel: {id: Upah kru}\nroles: [sales_manager]\nwidgets:\n" +
	"  - {name: crew_n, type: number, label: {id: Jumlah}, model: crew, aggregate: count, span: 3}\n"

// Contract 0027 through HTTP: meta, record, export and dashboards follow hidden_for and roles:.
func TestHiddenForOverHTTP(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) {
		for p, c := range map[string]string{
			filepath.Join(apps, "crm", "models", "crew.rk"):         crewModel,
			filepath.Join(apps, "crm", "dashboards", "crew_pay.rk"): crewPayDashboard,
		} {
			if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	})
	e.login()
	name := get(e.must(201, "POST", "/api/v1/r/crew", map[string]any{"crew_name": "Budi", "pay": 250000}).JSON, "name").(string)
	rep := repEnv(t, e)

	m := rep.must(200, "GET", "/api/v1/meta", nil).JSON
	for _, f := range get(m, "models", "crew", "fields").([]any) {
		if get(f, "name") == "pay" {
			t.Fatal("meta describes a hidden field to the rep")
		}
	}
	if d, _ := m["dashboards"].(map[string]any); d["crew_pay"] != nil {
		t.Fatal("meta lists a dashboard the rep may not open")
	}
	if r := rep.must(200, "GET", "/api/v1/r/crew/"+name, nil).JSON; r["pay"] != nil {
		t.Fatalf("record shows pay: %v", r)
	}
	csv := string(rep.must(200, "GET", "/api/v1/export/r/crew", nil).Body)
	if strings.Contains(csv, "Upah") || strings.Contains(csv, "250000") {
		t.Fatalf("export shows pay: %s", csv)
	}
	rep.must(403, "GET", "/api/v1/dashboards/crew_pay", nil)
	// the owner sees everything
	if r := e.must(200, "GET", "/api/v1/r/crew/"+name, nil).JSON; r["pay"] == nil {
		t.Fatalf("owner record: %v", r)
	}
	e.must(200, "GET", "/api/v1/dashboards/crew_pay", nil)
}
