package meta

import (
	"slices"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/schema"
)

// Contract 0032: a site names modules in config.modules. The installed set is their closure (requires first), the
// plan's modules allow them, /meta says who brings each one in, and modules no app brings in get a menu group.
func TestConfigModules(t *testing.T) {
	reg, rep := ValidateOpts(Options{AppsDir: "../../testdata/config-modules/apps", SiteDir: "../../testdata/config-modules/site"}, nil, schema.Embedded())
	if n := len(rep.All()); n != 0 {
		t.Fatalf("config-modules: %d problem(s), first %+v", n, rep.All()[0])
	}
	if got, want := reg.ConfigModules(), []string{"payments", "invoicing"}; !slices.Equal(got, want) {
		t.Errorf("ConfigModules = %v, want %v (file order)", got, want)
	}
	// invoicing first (sorted), with contacts before it; payments after; the site unit last
	if got, want := reg.AppOrder(), []string{"contacts", "invoicing", "payments", SiteUnit}; !slices.Equal(got, want) {
		t.Errorf("install order = %v, want %v", got, want)
	}
	if reg.IsInstalled("crm") || reg.IsInstalled("pipeline") || reg.IsInstalled("stock") {
		t.Error("units outside the composition are installed")
	}
	if got := reg.InstalledBy("invoicing"); !slices.Equal(got, []string{"config"}) {
		t.Errorf("InstalledBy(invoicing) = %v", got)
	}
	if got := reg.InstalledBy("contacts"); len(got) != 0 {
		t.Errorf("InstalledBy(contacts) = %v, want none (required only)", got)
	}
	if got := reg.InstalledBy("stock"); len(got) != 0 {
		t.Errorf("InstalledBy(stock) = %v, want none", got)
	}
	allowed := reg.PlanModules()
	for _, m := range []string{"contacts", "invoicing", "payments"} {
		if !allowed[m] {
			t.Errorf("plan sewa should allow %s (extends dasar): %v", m, allowed)
		}
	}
	if allowed["pipeline"] || allowed["crm"] {
		t.Errorf("plan allows too much: %v", allowed)
	}
	menu := reg.DefaultMenu()
	var mods []string
	for _, g := range menu {
		mods = append(mods, g.Module)
	}
	if want := []string{"payments", "invoicing", "contacts", SiteUnit}; !slices.Equal(mods, want) {
		t.Fatalf("menu groups = %v, want %v (config.modules order, then required ones, then the company group)", mods, want)
	}
	if !slices.Equal(menu[1].Items, []string{"invoice"}) || menu[1].Group["id"] != "Tagihan" {
		t.Errorf("invoicing group = %+v (invoice_line is a child table)", menu[1])
	}
}

// An app-only site keeps contract 0001: no default menu, and config.apps still decides.
func TestNoComposition(t *testing.T) {
	reg, _ := ValidateOpts(Options{AppsDir: "../../testdata/modules-apps/apps", SiteDir: "../../testdata/modules-apps/site"}, nil, schema.Embedded())
	if reg.Composed() || reg.DefaultMenu() != nil {
		t.Error("an app-only site has no composition")
	}
	if got := reg.InstalledBy("contacts"); !slices.Equal(got, []string{"crm"}) {
		t.Errorf("InstalledBy(contacts) = %v, want [crm]", got)
	}
	if got := reg.InstalledBy("crm"); !slices.Equal(got, []string{"config"}) {
		t.Errorf("InstalledBy(crm) = %v, want [config]", got)
	}
}

func TestBrokenConfigModulesMatchPython(t *testing.T) {
	golden(t, "testdata/broken-config-modules.expected.txt", Options{AppsDir: "testdata/broken-config-modules/apps", SiteDir: "testdata/broken-config-modules/site"})
}

// Contract 0045 §5: company tables get their own last menu group, for app-only and composed sites alike.
func TestSiteMenuGroup(t *testing.T) {
	reg, rep := ValidateOpts(Options{AppsDir: "../../testdata/modules-apps/apps", SiteDir: "../../testdata/site-module"}, nil, schema.Embedded())
	if n := len(rep.All()); n != 0 {
		t.Fatalf("site-module: %d problem(s), first %+v", n, rep.All()[0])
	}
	if reg.Composed() {
		t.Fatal("site-module is meant to be apps-only")
	}
	var want []string
	for _, n := range reg.App[SiteUnit].Models {
		if m := reg.Models[n]; m != nil && !m.IsChild {
			want = append(want, n)
		}
	}
	slices.Sort(want)
	if len(want) == 0 {
		t.Fatal("site-module has no company model")
	}
	menu := reg.DefaultMenu()
	if len(menu) != 1 {
		t.Fatalf("menu = %+v, want only the site group", menu)
	}
	g := menu[len(menu)-1]
	if g.Module != SiteUnit || g.Group["id"] != "Perusahaan" || g.Group["en"] != "Company" {
		t.Errorf("site group = %+v", g)
	}
	if !slices.Equal(g.Items, want) || g.Dashboards == nil || len(g.Dashboards) != 0 {
		t.Errorf("site group items = %v dashboards = %v, want %v and []", g.Items, g.Dashboards, want)
	}
}

// A composed site keeps its module groups and gets the company group after them.
func TestSiteMenuGroupComposed(t *testing.T) {
	reg, rep := ValidateOpts(Options{AppsDir: "../../testdata/config-modules/apps", SiteDir: "../../testdata/config-modules/site"}, nil, schema.Embedded())
	if n := len(rep.All()); n != 0 {
		t.Fatalf("config-modules: %d problem(s), first %+v", n, rep.All()[0])
	}
	menu := reg.DefaultMenu()
	if len(menu) < 2 {
		t.Fatalf("menu = %+v", menu)
	}
	g := menu[len(menu)-1]
	if g.Module != SiteUnit || g.Group["id"] != "Perusahaan" || !slices.Equal(g.Items, []string{"x_sewa"}) {
		t.Errorf("last group = %+v, want the site group with x_sewa", g)
	}
	for _, o := range menu[:len(menu)-1] {
		if o.Module == SiteUnit {
			t.Errorf("site group is not last: %+v", menu)
		}
	}
}
