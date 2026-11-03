package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/imports"
)

const importSchema = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/"

// addFnbTest adds a sku field to core's item and a small F&B app with the Moka import (contract 0012 §1).
func addFnbTest(apps, site string) {
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	item := filepath.Join(apps, "core", "models", "item.rk")
	b, err := os.ReadFile(item)
	must(err)
	must(os.WriteFile(item, append(b, []byte("  - name: sku\n    type: data\n    label: {id: SKU}\n")...), 0o644))
	files := map[string]string{
		"app.rk": importSchema + "app.json\nname: fnb_test\nversion: 1.0.0\ntitle: {id: FnB, en: FnB}\nrequires: {rangka: '>=0.4', core: ^1.0}\nmenu:\n  - group: {id: Data}\n    items: [outlet_sales]\n",
		"models/outlet_sales.rk": importSchema + `model.json
name: outlet_sales
label: {id: Penjualan outlet}
naming: OS-{YYYY}-{####}
title_field: date
fields:
  - name: date
    type: date
    label: {id: Tanggal}
    required: true
  - name: outlet
    type: link
    to: outlet
    label: {id: Outlet}
  - name: note
    type: data
    label: {id: Catatan}
  - name: lines
    type: table
    of: outlet_sales_line
    label: {id: Baris}
`,
		"models/outlet_sales_line.rk": importSchema + `model.json
name: outlet_sales_line
is_child: true
fields:
  - name: item
    type: link
    to: item
    label: {id: Barang}
  - name: qty
    type: float
    label: {id: Qty}
  - name: gross
    type: currency
    label: {id: Penjualan kotor}
  - name: discount
    type: currency
    label: {id: Diskon}
  - name: net
    type: currency
    label: {id: Penjualan bersih}
  - name: refund
    type: currency
    label: {id: Retur}
`,
		"imports/moka_item_details.rk": importSchema + `import.json
name: moka_item_details
label: {id: Penjualan Moka (detail item)}
model: outlet_sales
source: csv
header_row: 1
per: outlet
key: [Date, Receipt Number, Item, Variant, Modifiers]
group: [Date]
map:
  date: Date
  note: "='Moka ' + row['Date']"
lines:
  field: lines
  map:
    item: {value: SKU, match: sku}
    qty: Quantity
    gross: Gross Sales
    discount: Discounts
    net: Net Sales
    refund: Refunds
`,
	}
	for name, content := range files {
		p := filepath.Join(apps, "fnb_test", name)
		must(os.MkdirAll(filepath.Dir(p), 0o755))
		must(os.WriteFile(p, []byte(content), 0o644))
	}
	// the site's plan and app list must include the new app
	for _, p := range []string{filepath.Join(site, "features.rk"), filepath.Join(site, "config.rk")} {
		b, err := os.ReadFile(p)
		must(err)
		must(os.WriteFile(p, []byte(strings.Replace(string(b), "apps: [core, crm, sales]", "apps: [core, crm, sales, fnb_test]", 1)), 0o644))
	}
}

// uploadForm posts a multipart form with a file part and extra fields.
func (e *env) uploadForm(path, fileName string, data []byte, fields map[string]string) *resp {
	e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	if fileName != "" {
		fw, _ := mw.CreateFormFile("file", fileName)
		fw.Write(data)
	}
	mw.Close()
	req, _ := http.NewRequest("POST", e.ts.URL+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	c := &http.Client{Jar: e.jar}
	res, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	r := &resp{Status: res.StatusCode, Header: res.Header, Body: b}
	json.Unmarshal(b, &r.JSON)
	return r
}

func TestImportsMoka(t *testing.T) {
	e := newEnv(t, false, addFnbTest)
	e.login()
	m := e.must(200, "GET", "/api/v1/meta", nil).JSON
	if get(m, "imports", "moka_item_details", "model") != "outlet_sales" || get(m, "imports", "moka_item_details", "per") != "outlet" ||
		get(m, "imports", "moka_item_details", "source") != "csv" || get(m, "imports", "moka_item_details", "app") != "fnb_test" {
		t.Fatalf("meta imports: %v", m["imports"])
	}
	csv, err := os.ReadFile("../imports/testdata/moka-item-details.csv")
	if err != nil {
		t.Fatal(err)
	}
	outlet := e.must(201, "POST", "/api/v1/r/outlet", map[string]any{"outlet_name": "Outlet Dago"}).JSON["name"]
	for i, sku := range []string{"TH-ORI-10", "TH-ORI-20", "TH-ISI-5", "MN-ETM-R", "MN-EJR-R"} {
		e.must(201, "POST", "/api/v1/r/item", map[string]any{"item_name": "Barang " + sku, "sku": sku, "rate": 1000 * (i + 1)})
	}
	form := map[string]string{"outlet": outlet.(string)}

	// no file
	if r := e.uploadForm("/api/v1/imports/moka_item_details", "", nil, form); r.Status != 400 || get(r.JSON, "error", "code") != "file_required" || get(r.JSON, "error", "message") != "Pilih file dulu" {
		t.Fatalf("no file: %d %s", r.Status, r.Body)
	}

	// a body over the upload limit is refused with 413 before the file is read
	big := make([]byte, imports.MaxBytes+2<<20)
	if r := e.uploadForm("/api/v1/imports/moka_item_details", "big.csv", big, form); r.Status != 413 || get(r.JSON, "error", "code") != "too_large" || get(r.JSON, "error", "message") != imports.ErrTooBig.Error() {
		t.Fatalf("too large: %d %.200s", r.Status, r.Body)
	}

	// preview, commit
	pv := e.uploadForm("/api/v1/imports/moka_item_details", "moka.csv", csv, form)
	if pv.Status != 200 || pv.JSON["rows"] != float64(8) || pv.JSON["duplicate"] != false || len(pv.JSON["errors"].([]any)) != 0 {
		t.Fatalf("preview: %d %s", pv.Status, pv.Body)
	}
	id := pv.JSON["id"].(string)
	e.must(200, "GET", "/api/v1/imports/moka_item_details/"+id, nil)
	if l := e.must(200, "GET", "/api/v1/imports/moka_item_details", nil).JSON["imports"].([]any); len(l) != 1 {
		t.Fatalf("list: %v", l)
	}
	cm := e.must(200, "POST", "/api/v1/imports/moka_item_details/"+id+"/commit", nil)
	if cm.JSON["status"] != "committed" {
		t.Fatalf("commit: %s", cm.Body)
	}
	recs := e.must(200, "GET", "/api/v1/r/outlet_sales", nil).JSON["data"].([]any)
	if len(recs) != 1 {
		t.Fatalf("outlet_sales: %v", recs)
	}
	rec := e.must(200, "GET", "/api/v1/r/outlet_sales/"+recs[0].(map[string]any)["name"].(string), nil).JSON
	if n := len(rec["lines"].([]any)); n != 8 {
		t.Fatalf("lines = %d: %v", n, rec)
	}

	// the same file again is a duplicate and cannot be committed
	dup := e.uploadForm("/api/v1/imports/moka_item_details", "moka.csv", csv, form)
	if dup.Status != 200 || dup.JSON["duplicate"] != true {
		t.Fatalf("duplicate: %d %s", dup.Status, dup.Body)
	}
	r := e.must(409, "POST", "/api/v1/imports/moka_item_details/"+dup.JSON["id"].(string)+"/commit", nil)
	if get(r.JSON, "error", "code") != "already_imported" {
		t.Fatalf("second commit: %s", r.Body)
	}
	if n := len(e.must(200, "GET", "/api/v1/r/outlet_sales", nil).JSON["data"].([]any)); n != 1 {
		t.Fatalf("outlet_sales after 409: %d", n)
	}

	// an overlapping export (the same day plus a new one): the known rows are skipped, one new record
	more := string(csv) + "03/11/2026,08:01,Outlet Dago,DGO-0101,Tahu,Tahu Original,Isi 10,,TH-ORI-10,1,25000,0,0,25000,0,0,Dine In,Cash,Kasir 1,Kasir 1\n" +
		"03/11/2026,08:01,Outlet Dago,DGO-0101,Minuman,Es Jeruk,Regular,,MN-EJR-R,1,10000,0,0,10000,0,0,Dine In,Cash,Kasir 1,Kasir 1\n"
	ov := e.uploadForm("/api/v1/imports/moka_item_details", "moka-2.csv", []byte(more), form)
	if ov.Status != 200 || ov.JSON["rows"] != float64(10) || ov.JSON["skipped"] != float64(8) || ov.JSON["duplicate"] != false || len(ov.JSON["errors"].([]any)) != 0 {
		t.Fatalf("overlap preview: %d %s", ov.Status, ov.Body)
	}
	oc := e.must(200, "POST", "/api/v1/imports/moka_item_details/"+ov.JSON["id"].(string)+"/commit", nil)
	if recs, _ := oc.JSON["records"].([]any); len(recs) != 1 {
		t.Fatalf("overlap commit: %s", oc.Body)
	}
	if n := len(e.must(200, "GET", "/api/v1/r/outlet_sales", nil).JSON["data"].([]any)); n != 2 {
		t.Fatalf("outlet_sales after overlap: %d", n)
	}
	// list entries carry no preview records
	for _, it := range e.must(200, "GET", "/api/v1/imports/moka_item_details", nil).JSON["imports"].([]any) {
		if _, ok := it.(map[string]any)["preview"]; ok {
			t.Fatalf("list entry with preview: %v", it)
		}
	}

	// a cell that is not a number is reported with row, column and message
	lines := strings.Split(string(csv), "\n")
	cols := strings.Split(lines[2], ",") // spreadsheet row 3
	cols[9] = "dua"                      // Quantity
	lines[2] = strings.Join(cols, ",")
	// another outlet, since rows already imported for Outlet Dago are skipped before they are checked
	other := e.must(201, "POST", "/api/v1/r/outlet", map[string]any{"outlet_name": "Outlet Braga"}).JSON["name"].(string)
	bad := e.uploadForm("/api/v1/imports/moka_item_details", "bad.csv", []byte(strings.Join(lines, "\n")), map[string]string{"outlet": other})
	errs, _ := bad.JSON["errors"].([]any)
	if bad.Status != 200 || len(errs) == 0 {
		t.Fatalf("bad: %d %s", bad.Status, bad.Body)
	}
	first := errs[0].(map[string]any)
	if first["row"] != float64(3) || first["column"] != "Quantity" || first["message"] != `Kolom "Quantity" harus angka, isinya "dua"` {
		t.Fatalf("row error: %v", first)
	}
	e.must(422, "POST", "/api/v1/imports/moka_item_details/"+bad.JSON["id"].(string)+"/commit", nil)

	// a token (not a person) may not commit
	e.token = e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "ai"}).JSON["token"].(string)
	r = e.must(403, "POST", "/api/v1/imports/moka_item_details/"+bad.JSON["id"].(string)+"/commit", nil)
	if get(r.JSON, "error", "code") != "permission_denied" || get(r.JSON, "error", "message") != "Impor hanya bisa dijalankan oleh pengguna" {
		t.Fatalf("token commit: %s", r.Body)
	}
}
