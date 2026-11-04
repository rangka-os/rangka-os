package meta

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

const rkHint = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/"

// viewsSite copies the catalogue's site into a temp dir and writes the given files under it.
func viewsSite(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("cp", "-r", testdb.Catalogue("sites/kopi-senja"), dir+"/site").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	for p, body := range files {
		os.MkdirAll(filepath.Dir(dir+"/site/"+p), 0o755)
		mustWrite(t, dir+"/site/"+p, []byte(body))
	}
	return dir + "/site"
}

func validateViews(t *testing.T, site string) (*Registry, *Report) {
	t.Helper()
	return ValidateOpts(Options{AppsDir: testdb.Catalogue("apps"), SiteDir: site}, nil, schema.Embedded())
}

func TestOverlayViewsMerge(t *testing.T) {
	site := viewsSite(t, map[string]string{"custom/deal.rk": rkHint + `overlay.json
model: deal
views:
  form:
    layout:
      - section: {id: Utama}
        fields: [deal_name, value]
  list:
    columns: [value, deal_name]
  kanban:
    columns: [won, prospect]
options:
  stage:
    - {value: x_review, label: {id: Tinjau}}
`})
	reg, rep := validateViews(t, site)
	if rep.Errors() > 0 {
		t.Fatalf("errors: %v", rep.All())
	}
	m := reg.Model("deal")
	views := asMap(m.Raw["views"])
	layout := asList(asMap(views["form"])["layout"])
	if len(layout) != 1 || strings.Join(strList(asMap(layout[0])["fields"]), ",") != "deal_name,value" {
		t.Errorf("form layout = %v", layout)
	}
	if got := strings.Join(strList(asMap(views["list"])["columns"]), ","); got != "value,deal_name" {
		t.Errorf("list columns = %s", got)
	}
	if got := strings.Join(strList(asMap(views["kanban"])["columns"]), ","); got != "won,prospect" {
		t.Errorf("kanban columns = %s", got)
	}
	if asMap(views["kanban"])["group_by"] != "stage" {
		t.Errorf("kanban lost group_by: %v", views["kanban"])
	}
	var opts []string
	for _, o := range asList(m.Field("stage").Raw["options"]) {
		opts = append(opts, pyStr(asMap(o)["value"]))
	}
	if len(opts) != 7 || opts[6] != "x_review" || opts[0] != "prospect" {
		t.Errorf("stage options = %v", opts)
	}
	if base := reg.App["crm"]; base == nil || strings.Contains(m.Doc.Text, "x_review") {
		t.Error("app file must not change")
	}
}

func TestSiteDashboardReplaces(t *testing.T) {
	site := viewsSite(t, map[string]string{"dashboards/crm_overview.rk": rkHint + `dashboard.json
name: crm_overview
label: {id: Ringkasan saya}
widgets:
  - {name: pipeline, type: number, label: {id: Nilai pipeline}, model: deal, aggregate: sum, field: value, span: 6}
  - {name: activities_due, type: list, label: {id: Aktivitas}, model: activity, columns: [subject], span: 6}
`})
	reg, rep := validateViews(t, site)
	if rep.Errors() > 0 {
		t.Fatalf("errors: %v", rep.All())
	}
	d := reg.Dashboard("crm_overview")
	if d == nil || len(d.Widgets) != 2 || d.App != "crm" || d.Name != "crm_overview" {
		t.Fatalf("dashboard = %+v", d)
	}
	if reg.Docs["dashboards/crm_overview.rk"] == nil {
		t.Error("site dashboard doc not registered")
	}
	if reg.BaseDashboards["crm_overview"] == nil || len(reg.BaseDashboards["crm_overview"].Widgets) == 2 {
		t.Error("the app's own dashboard must be kept apart")
	}
}

func TestOverlayViewErrors(t *testing.T) {
	site := viewsSite(t, map[string]string{
		"custom/deal.rk": rkHint + `overlay.json
model: deal
views:
  form:
    layout:
      - section: {id: Utama}
        fields: [deal_name, nope_field]
  list:
    columns: [deal_name, nope_col]
  kanban:
    columns: [won, nope]
options:
  value:
    - {value: x_a, label: {id: A}}
  stage:
    - {value: won, label: {id: Menang lagi}}
    - {value: x_b, label: {id: B}}
    - {value: x_b, label: {id: B2}}
  ghost:
    - {value: x_c, label: {id: C}}
`,
		"custom/customer.rk": rkHint + `overlay.json
model: customer
views:
  kanban:
    columns: [a]
`,
		"dashboards/nope.rk": rkHint + `dashboard.json
name: nope
label: {id: Nope}
widgets:
  - {name: w, type: number, label: {id: W}, model: deal}
`,
	})
	_, rep := validateViews(t, site)
	var got []string
	for _, it := range rep.All() {
		if it.Level == "error" {
			got = append(got, it.Code+" "+it.Where)
		}
	}
	want := []string{
		"unknown_field views/form/layout/0/fields/1",
		"unknown_field views/list/columns/1",
		"bad_option views/kanban/columns/1",
		"not_select options/value",
		"bad_option options/stage/0/value",
		"bad_option options/stage/2/value",
		"unknown_field options/ghost",
		"no_kanban views/kanban/columns",
		"unknown_dashboard name",
	}
	have := strings.Join(got, "\n")
	for _, w := range want {
		if !strings.Contains("\n"+have+"\n", "\n"+w+"\n") {
			t.Errorf("missing %q in:\n%s", w, have)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d errors, want %d:\n%s", len(got), len(want), have)
	}
}
