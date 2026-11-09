package meta

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// copyTree copies dir into a new temporary directory and returns it.
func copyTree(t *testing.T, dir string) string {
	t.Helper()
	out := t.TempDir()
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(out, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(out, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

const schemaHeader = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/"

// catalogueTree is modules-apps (crm on pipeline and contacts, installed by config.apps) plus an app "toko" that
// reuses contacts and brings a new module "kas".
func catalogueTree(t *testing.T) string {
	root := copyTree(t, "../../testdata/modules-apps")
	write(t, filepath.Join(root, "modules/kas/module.rk"), schemaHeader+"module.json\nname: kas\nversion: 1.0.0\ntitle: {id: Kas, en: Cash}\nrequires: {rangka: '>=0.5', contacts: ^1.0}\n")
	write(t, filepath.Join(root, "apps/toko/app.rk"), schemaHeader+"app.json\nname: toko\nversion: 1.0.0\ntitle: {id: Toko}\nsummary: {id: Kasir untuk toko.}\nrangka: '>=0.5'\nmodules: {contacts: ^1.0, kas: ^1.0}\n")
	return root
}

func appOf(c Catalogue, name string) *CatalogueApp {
	for i := range c.Apps {
		if c.Apps[i].Name == name {
			return &c.Apps[i]
		}
	}
	return nil
}

func moduleOf(c Catalogue, name string) *CatalogueModule {
	for i := range c.Modules {
		if c.Modules[i].Name == name {
			return &c.Modules[i]
		}
	}
	return nil
}

func TestCatalogueSharesAndAdds(t *testing.T) {
	root := catalogueTree(t)
	// the fixture's plan allows crm only; drop it so toko is available
	write(t, filepath.Join(root, "site/features.rk"), schemaHeader+"features.json\nplans: []\n")
	write(t, filepath.Join(root, "site/config.rk"), schemaHeader+"config.json\nlocale: id-ID\ncurrency: IDR\ntimezone: Asia/Jakarta\napps: [crm]\n")
	reg := LoadOpts(Options{AppsDir: filepath.Join(root, "apps"), SiteDir: filepath.Join(root, "site")}, nil)
	c := reg.Catalogue()

	crm := appOf(c, "crm")
	if crm == nil || crm.State != StateInstalled || !slices.Equal(crm.Modules, []string{"contacts", "pipeline"}) || len(crm.Adds) != 0 {
		t.Fatalf("crm: %+v", crm)
	}
	toko := appOf(c, "toko")
	if toko == nil || toko.State != StateAvailable {
		t.Fatalf("toko: %+v", toko)
	}
	if !slices.Equal(toko.Shares, []string{"contacts"}) || !slices.Equal(toko.Adds, []string{"kas"}) {
		t.Errorf("toko shares %v adds %v, want [contacts] [kas]", toko.Shares, toko.Adds)
	}
	if toko.Summary.Text() != "Kasir untuk toko." {
		t.Errorf("toko summary %q", toko.Summary.Text())
	}
	if m := moduleOf(c, "contacts"); m == nil || m.State != StateInstalled || !slices.Equal(m.InstalledBy, []string{"crm"}) {
		t.Errorf("contacts: %+v", m)
	}
	if m := moduleOf(c, "kas"); m == nil || m.State != StateAvailable || len(m.InstalledBy) != 0 {
		t.Errorf("kas: %+v", m)
	}
	if !c.ConnectorsRun || len(c.Connectors) != 0 {
		t.Errorf("connectors: run %v, %d listed", c.ConnectorsRun, len(c.Connectors))
	}
}

func TestCatalogueNotInPlan(t *testing.T) {
	root := catalogueTree(t) // plan dasar allows crm only
	reg := LoadOpts(Options{AppsDir: filepath.Join(root, "apps"), SiteDir: filepath.Join(root, "site")}, nil)
	c := reg.Catalogue()
	if c.Plan != "dasar" {
		t.Errorf("plan %q", c.Plan)
	}
	if toko := appOf(c, "toko"); toko == nil || toko.State != StateNotInPlan {
		t.Errorf("toko: %+v", toko)
	}
	if crm := appOf(c, "crm"); crm == nil || crm.State != StateInstalled {
		t.Errorf("crm: %+v", crm)
	}
}

func TestCatalogueWithoutSite(t *testing.T) {
	root := catalogueTree(t)
	reg := LoadOpts(Options{AppsDir: filepath.Join(root, "apps")}, nil)
	c := reg.Catalogue()
	// no site (the interview, before a business exists): nothing is installed, an app adds all its modules
	for _, a := range c.Apps {
		if a.State != StateAvailable || len(a.Shares) != 0 || !slices.Equal(a.Adds, a.Modules) {
			t.Errorf("%s: %+v", a.Name, a)
		}
	}
	if m := moduleOf(c, "contacts"); m == nil || m.State != StateAvailable || len(m.InstalledBy) != 0 {
		t.Errorf("contacts: %+v", m)
	}
}
