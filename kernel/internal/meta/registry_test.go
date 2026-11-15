package meta

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryLoadsSiteConnectors(t *testing.T) {
	conns := filepath.Join(t.TempDir(), "conns")
	writeConnector(t, filepath.Join(conns, "moka"), "moka")
	reg := LoadOpts(Options{AppsDir: "../../testdata/modules-apps/apps", SiteDir: "../../testdata/company-connector/site", ConnectorsDir: conns}, nil)
	if reg.Connectors["x_demo"] == nil || reg.Docs["connectors/x_demo.rk"] == nil {
		t.Fatalf("site connector not loaded: %v", reg.Connectors)
	}
	if reg.Connectors["moka"] == nil {
		t.Error("catalogue connector lost")
	}
	if p := reg.ConnectorPath("x_demo"); !filepath.IsAbs(p) || filepath.Base(p) != "connector.rk" {
		t.Errorf("ConnectorPath = %q", p)
	}
	if !IsCompanyConnector("x_demo") || IsCompanyConnector("moka") {
		t.Error("IsCompanyConnector")
	}
}

func writeConnector(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "name: " + name + "\nlabel: {id: X}\n"
	if err := os.WriteFile(filepath.Join(dir, "connector.rk"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
