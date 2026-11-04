package meta

import (
	"slices"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/schema"
)

// The site company module (contract 0016): <site>/models and <site>/flows load as the unit "site", installed after
// every app, seeing every model; catalogue units do not see it.
func TestSiteCompanyModule(t *testing.T) {
	reg, rep := ValidateOpts(Options{AppsDir: "../../testdata/modules-apps/apps", SiteDir: "../../testdata/site-module"}, nil, schema.Embedded())
	if n := len(rep.All()); n != 0 {
		t.Fatalf("site-module: %d problem(s), first %+v", n, rep.All()[0])
	}
	a := reg.App[SiteUnit]
	if a == nil || a.Kind != KindSite || a.IsApp() || a.IsModule() {
		t.Fatalf("site unit: %+v", a)
	}
	if !reg.IsInstalled(SiteUnit) || reg.AppOrder()[len(reg.AppOrder())-1] != SiteUnit {
		t.Errorf("site unit installed last: %v", reg.AppOrder())
	}
	m := reg.Model("x_kunjungan_toko")
	if m == nil || m.App != SiteUnit {
		t.Fatalf("company model: %+v", m)
	}
	if reg.Docs["models/x_kunjungan_toko.rk"] == nil || reg.Docs["flows/x_kunjungan_menang.rk"] == nil {
		t.Error("docs keys models/… and flows/… missing")
	}
	if f := reg.Flows["x_kunjungan_menang"]; f == nil || f.App != SiteUnit {
		t.Errorf("company flow: %+v", f)
	}
	var names []string
	for _, f := range asList(m.Raw["fields"]) {
		names = append(names, str(asMap(f), "name"))
	}
	if !slices.Contains(names, "x_catatan") {
		t.Errorf("overlay on the company model not merged: %v", names)
	}
	if a.DocKey("models/x.rk") != "models/x.rk" || reg.App["pipeline"].DocKey("flows/x.rk") != "modules/pipeline/flows/x.rk" {
		t.Error("DocKey")
	}

	// a site without models/ or flows/ has no site unit
	reg, _ = ValidateOpts(Options{AppsDir: "../../testdata/modules-apps/apps", SiteDir: "../../testdata/modules-apps/site"}, nil, schema.Embedded())
	if reg.App[SiteUnit] != nil || reg.IsInstalled(SiteUnit) {
		t.Error("site unit without company files")
	}
}

func TestSiteCompanyModuleBroken(t *testing.T) {
	reg, rep := ValidateOpts(Options{AppsDir: "../../testdata/modules-apps/apps", SiteDir: "../../testdata/broken-site-module"}, nil, schema.Embedded())
	var got []string
	for _, it := range rep.All() {
		got = append(got, it.File+" "+it.Code)
	}
	want := []string{
		"../../broken-site-module/models/deal.rk duplicate_model",
		"../../broken-site-module/models/kunjungan.rk company_model_name",
		"../../broken-site-module/models/kunjungan.rk unknown_model",
		"../../broken-site-module/flows/x_salah.rk unknown_event",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if reg.Models["deal"].App != "pipeline" {
		t.Error("a company model replaced the catalogue deal")
	}
}
