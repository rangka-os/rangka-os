package meta

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/schema"
)

const overlaySite = "../../testdata/overlay-site"

func TestOrgConfigFallsBackToSite(t *testing.T) {
	reg, rep := ValidateOpts(Options{AppsDir: appsDir, SiteDir: overlaySite}, nil, schema.Embedded())
	if len(rep.All()) != 0 {
		t.Fatalf("problems: %v", rep.All())
	}
	o := reg.Orgs["pt_alpha"]
	if o == nil || o.ID != "pt_alpha" || o.Name != "PT Alpha Inspeksi" || o.Code != "ALP" {
		t.Fatalf("Orgs[pt_alpha] = %+v", o)
	}
	if got := reg.OrgConfig("pt_alpha", "org.legal_name"); got != "PT Alpha Inspeksi" {
		t.Errorf("OrgConfig(pt_alpha, org.legal_name) = %v", got)
	}
	if got, want := reg.OrgConfig("other", "org.legal_name"), reg.SiteConfig("org.legal_name"); got != want || got != "PT Kopi Senja Nusantara" {
		t.Errorf("OrgConfig(other, org.legal_name) = %v, SiteConfig = %v", got, want)
	}
	// a key the org does not override falls back to the site, even for a known org
	if got := reg.OrgConfig("pt_alpha", "tax.ppn_rate"); got != reg.SiteConfig("tax.ppn_rate") || got == nil {
		t.Errorf("OrgConfig(pt_alpha, tax.ppn_rate) = %v", got)
	}
	if got := reg.OrgConfig("pt_alpha", "org.npwp"); got != nil {
		t.Errorf("OrgConfig(pt_alpha, org.npwp) = %v, want nil (the org's org key replaces the site's)", got)
	}
	if got := (&Registry{}).OrgConfig("pt_alpha", "org.legal_name"); got != nil {
		t.Errorf("no site: %v", got)
	}
}

func writeRole(t *testing.T, body string) *Registry {
	t.Helper()
	dir := t.TempDir()
	apps := filepath.Join(dir, "apps")
	if err := os.CopyFS(apps, os.DirFS(appsDir)); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(apps, "crm", "roles"), 0o755)
	if err := os.WriteFile(filepath.Join(apps, "crm", "roles", "outlet_staff.rk"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(apps, "", nil)
}

func TestRoleScope(t *testing.T) {
	const head = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/role.json\nname: outlet_staff\nlabel: {id: Staf outlet}\n"
	reg := writeRole(t, head+"scope: {field: [from_outlet, to_outlet], from: user.outlets}\n")
	s := reg.RoleScope("outlet_staff")
	if s == nil || !reflect.DeepEqual(s.Fields, []string{"from_outlet", "to_outlet"}) || s.From != "user.outlets" {
		t.Fatalf("RoleScope = %+v", s)
	}
	reg = writeRole(t, head+"scope: {field: outlet, from: user.outlets}\n")
	if s := reg.RoleScope("outlet_staff"); s == nil || !reflect.DeepEqual(s.Fields, []string{"outlet"}) {
		t.Fatalf("string field: RoleScope = %+v", s)
	}
	if s := reg.RoleScope("sales_rep"); s != nil {
		t.Errorf("role without scope: %+v", s)
	}
	if s := reg.RoleScope("nobody"); s != nil {
		t.Errorf("unknown role: %+v", s)
	}
}

func TestOrgSchema(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(overlaySite)); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "config.rk")
	b, _ := os.ReadFile(p)
	b = []byte(strings.Replace(string(b), "code: ALP,", "code: ALP, colour: red,", 1))
	os.WriteFile(p, b, 0o644)
	_, rep := ValidateOpts(Options{AppsDir: appsDir, SiteDir: dir}, nil, schema.Embedded())
	found := false
	for _, pr := range rep.All() {
		if pr.Code == "schema" && pr.Where == "orgs/pt_alpha" && strings.Contains(pr.Message, "colour") {
			found = true
		}
	}
	if !found {
		t.Fatalf("want a schema error at orgs/pt_alpha for colour, got %v", rep.All())
	}
}
