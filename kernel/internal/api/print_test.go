package api

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

func TestPrintOrg(t *testing.T) {
	dir := t.TempDir()
	cfg := `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/config.json
tax: {pkp: true}
org: {legal_name: PT Contoh Jasa Teknik, npwp: "00.000.000.0-000.000"}
orgs:
  pusat:
    name: Kantor pusat
    code: PST
    config:
      org: {legal_name: PT Contoh Pusat, pkp: false}
  cabang: {name: Cabang Contoh, code: CBG}
  bebas:
    name: Bebas PPN
    config:
      tax: {ppn_rate: 11}
`
	if err := os.WriteFile(filepath.Join(dir, "config.rk"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := meta.Load(t.TempDir(), dir, nil)
	for org, want := range map[string]map[string]any{
		"pusat":  {"name": "Kantor pusat", "code": "PST", "legal_name": "PT Contoh Pusat", "pkp": false}, // org: replaces the site's: no inherited npwp
		"cabang": {"name": "Cabang Contoh", "code": "CBG", "legal_name": "Cabang Contoh", "pkp": true},   // listed, no org:: its orgs name, no site identity, pkp from tax
		"bebas":  {"name": "Bebas PPN", "code": "", "legal_name": "Bebas PPN", "pkp": true},              // its tax: {ppn_rate: 11} merges over the site's, so tax.pkp stays
		"lain":   {"legal_name": "PT Contoh Jasa Teknik", "npwp": "00.000.000.0-000.000", "pkp": true},
	} {
		if got := printOrg(reg, org); !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", org, got, want)
		}
	}
	// printOrg copies: changing the result leaves the registry alone
	printOrg(reg, "lain")["legal_name"] = "X"
	if got := printOrg(reg, "lain")["legal_name"]; got != "PT Contoh Jasa Teknik" {
		t.Errorf("printOrg shares the config map: %v", got)
	}
}
