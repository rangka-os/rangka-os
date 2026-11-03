package meta

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

const importDoc = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/import.json
name: moka_item_details
label: {id: Penjualan Moka}
model: outlet_sales
source: csv
header_row: 1
per: outlet
key: [Date, Receipt Number]
group: [Date]
map:
  date: Date
  note: "='Moka ' + row['Date']"
lines:
  field: lines
  map:
    item: {value: SKU, match: sku}
    qty: Quantity
`

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseImport(t *testing.T) {
	p := filepath.Join(t.TempDir(), "moka.rk")
	writeFile(t, p, importDoc)
	d, err := rk.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	im := parseImport(d, "fnb")
	if im.Name != "moka_item_details" || im.App != "fnb" || im.Model != "outlet_sales" || im.Source != "csv" ||
		im.HeaderRow != 1 || im.Per != "outlet" || im.Label["id"] != "Penjualan Moka" {
		t.Errorf("header fields: %+v", im)
	}
	if !reflect.DeepEqual(im.Key, []string{"Date", "Receipt Number"}) || !reflect.DeepEqual(im.Group, []string{"Date"}) {
		t.Errorf("key/group: %v %v", im.Key, im.Group)
	}
	if im.Map["date"] != (MapValue{Column: "Date"}) {
		t.Errorf("date: %+v", im.Map["date"])
	}
	if im.Map["note"].CEL != "'Moka ' + row['Date']" || im.Map["note"].Column != "" {
		t.Errorf("note: %+v", im.Map["note"])
	}
	if !reflect.DeepEqual(im.MapOrder, []string{"date", "note"}) {
		t.Errorf("map order: %v", im.MapOrder)
	}
	if im.Lines == nil || im.Lines.Field != "lines" || im.Lines.Map["item"] != (MapValue{Column: "SKU", Match: "sku"}) ||
		im.Lines.Map["qty"] != (MapValue{Column: "Quantity"}) || !reflect.DeepEqual(im.Lines.MapOrder, []string{"item", "qty"}) {
		t.Errorf("lines: %+v", im.Lines)
	}
}

func TestParseImportDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.rk")
	writeFile(t, p, "name: x\nmodel: m\nmap:\n  a: A\n")
	d, err := rk.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	im := parseImport(d, "")
	if im.HeaderRow != 1 || im.Source != "csv" || im.Per != "org" || im.Lines != nil || im.App != "" {
		t.Errorf("defaults: %+v", im)
	}
}

func TestLoadImports(t *testing.T) {
	root := t.TempDir()
	apps, site := filepath.Join(root, "apps"), filepath.Join(root, "site")
	writeFile(t, filepath.Join(apps, "fnb", "app.rk"), "name: fnb\nversion: 0.1.0\n")
	writeFile(t, filepath.Join(apps, "fnb", "imports", "moka.rk"), "name: moka\nmodel: m\nmap:\n  a: A\n")
	writeFile(t, filepath.Join(apps, "fnb", "imports", "other.rk"), "name: other\nmodel: m\nmap:\n  a: A\n")
	writeFile(t, filepath.Join(site, "imports", "moka.rk"), "name: moka\nmodel: m2\nmap:\n  a: B\n")
	reg := Load(apps, "", nil)
	if im := reg.Imports["moka"]; im == nil || im.App != "fnb" || im.Model != "m" {
		t.Errorf("app import: %+v", im)
	}
	reg = Load(apps, site, nil)
	if im := reg.Imports["moka"]; im == nil || im.App != "" || im.Model != "m2" {
		t.Errorf("site import should override: %+v", im)
	}
	if reg.Imports["other"] == nil || reg.Imports["other"].App != "fnb" {
		t.Errorf("other: %+v", reg.Imports["other"])
	}
}
