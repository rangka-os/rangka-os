package meta

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

const overlaySite = "../../testdata/overlay-site"

func TestOrgConfigFallsBackToSite(t *testing.T) {
	reg, rep := ValidateOpts(Options{AppsDir: testdb.Catalogue("apps"), SiteDir: overlaySite}, nil, schema.Embedded())
	if !onlyDeprecations(rep) { // catalogue-v0 is the 0.4 layout (contract 0001)
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
		t.Errorf("OrgConfig(pt_alpha, org.npwp) = %v, want nil (org: replaces the site's, never merged)", got)
	}
	if got := (&Registry{}).OrgConfig("pt_alpha", "org.legal_name"); got != nil {
		t.Errorf("no site: %v", got)
	}
}

func mergeRegistry(site, org map[string]any) *Registry {
	return &Registry{
		Site: &Site{Config: site},
		Orgs: map[string]*OrgDoc{"bebas": {ID: "bebas", Config: org}, "kosong": {ID: "kosong"}},
	}
}

func TestOrgConfigDeepMerge(t *testing.T) {
	site := map[string]any{"tax": map[string]any{"ppn_rate": 12, "dpp_ratio": "11/12", "pkp": true}, "n": 1}
	reg := mergeRegistry(site, map[string]any{"tax": map[string]any{"ppn_rate": 11}})
	for path, want := range map[string]any{"tax.pkp": true, "tax.ppn_rate": 11, "tax.dpp_ratio": "11/12", "n": 1} {
		if got := reg.OrgConfig("bebas", path); got != want {
			t.Errorf("OrgConfig(bebas, %s) = %v, want %v", path, got, want)
		}
	}
	merged := AsMap(reg.OrgConfig("bebas", "tax"))
	if len(merged) != 3 || merged["ppn_rate"] != 11 || merged["pkp"] != true {
		t.Fatalf("merged tax = %v", merged)
	}
	merged["pkp"] = false
	merged["extra"] = 1
	if tax := AsMap(site["tax"]); tax["pkp"] != true || tax["ppn_rate"] != 12 || len(tax) != 3 {
		t.Errorf("site tax mutated: %v", tax)
	}
	if tax := AsMap(reg.Orgs["bebas"].Config["tax"]); len(tax) != 1 || tax["ppn_rate"] != 11 {
		t.Errorf("org tax mutated: %v", tax)
	}
}

func TestOrgConfigMergeTypeMismatchAndFallback(t *testing.T) {
	site := map[string]any{"a": map[string]any{"x": 1}, "b": "scalar", "deep": map[string]any{"l1": map[string]any{"k": 1, "j": 2}}}
	reg := mergeRegistry(site, map[string]any{"a": "flat", "b": map[string]any{"y": 2}, "deep": map[string]any{"l1": map[string]any{"k": 9}}})
	if got := reg.OrgConfig("bebas", "a"); got != "flat" {
		t.Errorf("org scalar over site map = %v", got)
	}
	if got := AsMap(reg.OrgConfig("bebas", "b")); len(got) != 1 || got["y"] != 2 {
		t.Errorf("org map over site scalar = %v", got)
	}
	if reg.OrgConfig("bebas", "deep.l1.k") != 9 || reg.OrgConfig("bebas", "deep.l1.j") != 2 {
		t.Errorf("nested merge: %v", reg.OrgConfig("bebas", "deep"))
	}
	if got := reg.OrgConfig("nope", "b"); got != "scalar" {
		t.Errorf("unknown org = %v", got)
	}
	if got := reg.OrgConfig("kosong", "a"); !reflect.DeepEqual(got, site["a"]) {
		t.Errorf("org without the key = %v", got)
	}
}

func TestOrgConfigReplacesOrgAndNumbering(t *testing.T) {
	site := map[string]any{
		"org":       map[string]any{"legal_name": "PT Induk", "npwp": "1"},
		"numbering": map[string]any{"sales_invoice": "SI-{#####}"},
	}
	reg := mergeRegistry(site, map[string]any{
		"org":       map[string]any{"legal_name": "PT Bebas"},
		"numbering": map[string]any{"invoice": map[string]any{"pattern": "INV/{ORG}/{YY}/{####}"}},
	})
	if got := reg.OrgConfig("bebas", "org.npwp"); got != nil {
		t.Errorf("org.npwp inherited: %v", got)
	}
	if got := reg.OrgConfig("bebas", "org.legal_name"); got != "PT Bebas" {
		t.Errorf("org.legal_name = %v", got)
	}
	if n := AsMap(reg.OrgConfig("bebas", "numbering")); len(n) != 1 || n["invoice"] == nil || n["sales_invoice"] != nil {
		t.Errorf("numbering = %v, want the org's map whole", n)
	}
	if got := reg.OrgConfig("kosong", "org.npwp"); got != nil {
		t.Errorf("listed org without its own org: = %v, want nil (never the site's)", got)
	}
	if got := reg.OrgConfig("nope", "org.npwp"); got != "1" {
		t.Errorf("unlisted org = %v, want the site's", got)
	}
}

func TestOrgIdentity(t *testing.T) {
	site := map[string]any{
		"org": map[string]any{"legal_name": "PT Induk", "npwp": "1", "pkp": true},
		"tax": map[string]any{"pkp": true},
	}
	reg := mergeRegistry(site, map[string]any{"tax": map[string]any{"pkp": false}})
	reg.Orgs["bebas"].Name, reg.Orgs["bebas"].Code = "CV Bebas", "BBS"
	if got := reg.OrgConfig("bebas", "org.pkp"); got != nil {
		t.Errorf("OrgConfig(bebas, org.pkp) = %v, want nil (not the site's true)", got)
	}
	if got := reg.OrgConfig("bebas", "org"); got != nil {
		t.Errorf("OrgConfig(bebas, org) = %v, want nil", got)
	}
	want := map[string]any{"legal_name": "CV Bebas", "name": "CV Bebas", "code": "BBS", "pkp": false}
	if got := reg.OrgIdentity("bebas"); !reflect.DeepEqual(got, want) {
		t.Errorf("OrgIdentity(bebas) = %v, want %v", got, want)
	}
	// kosong sets no tax: pkp falls back to the site's tax.pkp, never the site's org.pkp/npwp
	want = map[string]any{"legal_name": "", "name": "", "code": "", "pkp": true}
	if got := reg.OrgIdentity("kosong"); !reflect.DeepEqual(got, want) {
		t.Errorf("OrgIdentity(kosong) = %v, want %v", got, want)
	}
	want = map[string]any{"legal_name": "PT Induk", "npwp": "1", "pkp": true}
	if got := reg.OrgIdentity("nope"); !reflect.DeepEqual(got, want) {
		t.Errorf("OrgIdentity(unlisted) = %v, want %v", got, want)
	}
	reg.Orgs["bebas"].Config["org"] = map[string]any{"legal_name": "CV Bebas Jaya", "npwp": "2", "pkp": true}
	id := reg.OrgIdentity("bebas")
	if id["legal_name"] != "CV Bebas Jaya" || id["npwp"] != "2" || id["pkp"] != true {
		t.Errorf("own org: = %v", id)
	}
	id["npwp"] = "x"
	reg.OrgIdentity("nope")["npwp"] = "x"
	if reg.OrgIdentity("bebas")["npwp"] != "2" || AsMap(site["org"])["npwp"] != "1" {
		t.Errorf("OrgIdentity returned shared maps")
	}
}

func TestOrgConfigReturnsCopies(t *testing.T) {
	site := map[string]any{"tax": map[string]any{"pkp": true}, "list": []any{1}}
	reg := mergeRegistry(site, nil)
	AsMap(reg.OrgConfig("kosong", "tax"))["pkp"] = false
	AsMap(reg.OrgConfig("nope", "tax"))["pkp"] = false
	reg.OrgConfig("bebas", "list").([]any)[0] = 9
	if AsMap(site["tax"])["pkp"] != true || site["list"].([]any)[0] != 1 {
		t.Errorf("site config mutated through OrgConfig: %v", site)
	}
}

func writeRole(t *testing.T, body string) *Registry {
	t.Helper()
	dir := t.TempDir()
	apps := filepath.Join(dir, "apps")
	if err := os.CopyFS(apps, os.DirFS(testdb.Catalogue("apps"))); err != nil {
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
	_, rep := ValidateOpts(Options{AppsDir: testdb.Catalogue("apps"), SiteDir: dir}, nil, schema.Embedded())
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
