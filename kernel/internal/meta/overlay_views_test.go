package meta

import (
	"os"
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
	copyDir(t, testdb.Catalogue("sites/kopi-senja"), dir+"/site")
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

// add applies after a replacing layout / column list: `after` is placed in the replacement, and naming a field
// only the old layout had is an error.
func TestOverlayAddAfterReplacement(t *testing.T) {
	site := viewsSite(t, map[string]string{"custom/deal.rk": rkHint + `overlay.json
model: deal
views:
  form:
    layout:
      - section: {id: Utama}
        fields: [deal_name, value]
    add:
      - {field: notes, after: deal_name}
  list:
    columns: [value, deal_name]
    add:
      - {field: stage, after: value}
`})
	reg, rep := validateViews(t, site)
	if rep.Errors() > 0 {
		t.Fatalf("errors: %v", rep.All())
	}
	views := asMap(reg.Model("deal").Raw["views"])
	if got := strings.Join(strList(asMap(asList(asMap(views["form"])["layout"])[0])["fields"]), ","); got != "deal_name,notes,value" {
		t.Errorf("form fields = %s", got)
	}
	if got := strings.Join(strList(asMap(views["list"])["columns"]), ","); got != "value,stage,deal_name" {
		t.Errorf("list columns = %s", got)
	}

	bad := viewsSite(t, map[string]string{"custom/deal.rk": rkHint + `overlay.json
model: deal
views:
  form:
    layout:
      - section: {id: Utama}
        fields: [deal_name, value]
    add:
      - {field: notes, after: customer}
  list:
    columns: [value, deal_name]
    add:
      - {field: stage, after: expected_close}
`})
	_, rep = validateViews(t, bad)
	var got []string
	for _, it := range rep.All() {
		if it.Level == "error" {
			got = append(got, it.Code+" "+it.Where)
		}
	}
	want := "unknown_field views/form/add/0/after,unknown_field views/list/add/0/after"
	if strings.Join(got, ",") != want {
		t.Errorf("errors = %v", got)
	}
}

// an add naming a field the replacing layout / columns already hold must not place it twice
func TestOverlayAddSkipsPlacedField(t *testing.T) {
	site := viewsSite(t, map[string]string{"custom/deal.rk": rkHint + `overlay.json
model: deal
fields:
  - {name: x_tier, type: text, label: {id: Tingkat}}
views:
  form:
    layout:
      - section: {id: Utama}
        fields: [deal_name, x_tier, value]
    add:
      - {field: x_tier, after: deal_name}
  list:
    columns: [value, x_tier, deal_name]
    add:
      - {field: x_tier, after: value}
`})
	reg, rep := validateViews(t, site)
	if rep.Errors() > 0 {
		t.Fatalf("errors: %v", rep.All())
	}
	views := asMap(reg.Model("deal").Raw["views"])
	if got := strings.Join(strList(asMap(asList(asMap(views["form"])["layout"])[0])["fields"]), ","); got != "deal_name,x_tier,value" {
		t.Errorf("form fields = %s", got)
	}
	if got := strings.Join(strList(asMap(views["list"])["columns"]), ","); got != "value,x_tier,deal_name" {
		t.Errorf("list columns = %s", got)
	}
}

// TestOverlayLabelsMerge: contract 0049 §5, the company's word for a model (page title, the create button, widget
// titles) and for fields of the app model replace the app's labels; names stay, an unknown field is reported.
func TestOverlayLabelsMerge(t *testing.T) {
	site := viewsSite(t, map[string]string{"custom/deal.rk": rkHint + `overlay.json
model: deal
label: {id: Mobil, en: Car}
labels:
  value: {id: Tarif harian}
`})
	reg, rep := validateViews(t, site)
	if rep.Errors() > 0 {
		t.Fatalf("errors: %v", rep.All())
	}
	m := reg.Model("deal")
	if m == nil || m.Label["id"] != "Mobil" || m.Label["en"] != "Car" || m.Name != "deal" {
		t.Fatalf("model label %+v", m)
	}
	var got Label
	for _, f := range m.Fields {
		if f.Name == "value" {
			got = f.Label
		}
	}
	if got["id"] != "Tarif harian" {
		t.Fatalf("field label %+v", got)
	}

	site = viewsSite(t, map[string]string{"custom/deal.rk": rkHint + `overlay.json
model: deal
labels:
  tarif: {id: Tarif}
`})
	_, rep = validateViews(t, site)
	found := false
	for _, it := range rep.All() {
		found = found || (it.Code == "unknown_field" && it.Where == "labels/tarif")
	}
	if !found {
		t.Fatalf("unknown field label not reported: %v", rep.All())
	}
}

// Contract 0049: custom/app.<app>.rk renames an app's menu group by its Indonesian text; the app file is unchanged.
func TestAppOverlayMenuLabels(t *testing.T) {
	reg, rep := ValidateOpts(Options{AppsDir: testdb.Catalogue("apps"), SiteDir: "../../testdata/overlay-site"}, nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("metadata errors: %v", rep.All())
	}
	a := reg.App["crm"]
	g := asMap(asList(a.Raw["menu"])[0])
	if l := asMap(g["group"]); l["id"] != "Pelanggan dan peluang" || l["en"] != "Customers and deals" {
		t.Errorf("crm menu group = %v, want the company's label", l)
	}
	if len(asList(g["items"])) == 0 {
		t.Error("the group lost its items")
	}
	if orig := asMap(asMap(asList(asMap(a.Doc.Data)["menu"])[0])["group"]); orig["id"] != "CRM" {
		t.Errorf("app.rk data changed: %v", orig)
	}
	if reg.Overlays["app.crm"] != nil || reg.AppOverlays["crm"] == nil {
		t.Error("custom/app.crm.rk must load as an app overlay, not a model overlay")
	}
}

// Audit menu-labels F3: an app upgrade that changes a group's words leaves a stale key; the site still loads (warning
// unknown_group) and the menu keeps the app's label.
func TestAppOverlayStaleGroupIsWarning(t *testing.T) {
	site := t.TempDir()
	if err := os.CopyFS(site, os.DirFS("../../testdata/overlay-site")); err != nil {
		t.Fatal(err)
	}
	stale := "app: crm\nmenu_labels:\n  CRM lama: {id: Pelanggan}\n"
	if err := os.WriteFile(filepath.Join(site, "custom", "app.crm.rk"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, rep := ValidateOpts(Options{AppsDir: testdb.Catalogue("apps"), SiteDir: site}, nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("a stale menu label must not stop the site: %v", rep.All())
	}
	warned := false
	for _, x := range rep.All() {
		if x.Code == "unknown_group" && x.Level == "warn" {
			warned = true
		}
	}
	if !warned {
		t.Error("no unknown_group warning for a stale key")
	}
	if g := asMap(asMap(asList(reg.App["crm"].Raw["menu"])[0])["group"]); g["id"] != "CRM" {
		t.Errorf("crm group = %v, want the app's own label", g)
	}
}

// Audit overlay label N1: the IDR twin of a relabelled money field follows the new word, unless labels names the twin.
func TestOverlayLabelsMoneyTwin(t *testing.T) {
	site := t.TempDir()
	files := map[string]string{
		"config.rk": "locale: id-ID\ncurrency: IDR\ntimezone: Asia/Jakarta\napps: [billing]\n",
		"custom/bill.rk": rkHint + `overlay.json
model: bill
labels:
  total: {id: Tarif, en: Rate}
  tax: {id: PPN}
  tax_idr: {id: PPN rupiah}
`,
	}
	for p, body := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(site, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(site, p), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg, rep := ValidateOpts(Options{AppsDir: "../../testdata/money-apps", SiteDir: site}, nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("errors: %v", rep.All())
	}
	m := reg.Model("bill")
	label := func(n string) Label {
		if f := m.Field(n); f != nil {
			return f.Label
		}
		return nil
	}
	if l := label("total_idr"); l["id"] != "Tarif (IDR)" || l["en"] != "Rate (IDR)" {
		t.Errorf("total_idr label %v, want the new word with (IDR)", l)
	}
	if l := label("tax_idr"); l["id"] != "PPN rupiah" {
		t.Errorf("tax_idr label %v, want its own overlay label", l)
	}
	if l := label("grand_total_idr"); l["id"] != "Total akhir (IDR)" {
		t.Errorf("grand_total_idr label %v, want the app's", l)
	}
}
