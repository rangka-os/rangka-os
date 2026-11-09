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
	if want := []string{"payments", "invoicing", "contacts"}; !slices.Equal(mods, want) {
		t.Fatalf("menu groups = %v, want %v (config.modules order, then required ones)", mods, want)
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
