package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
)

// Imports under multi-org and row scope (contracts 0010 and 0012).

func mokaCSV(t *testing.T, e *env) []byte {
	t.Helper()
	csv, err := os.ReadFile("../imports/testdata/moka-item-details.csv")
	if err != nil {
		t.Fatal(err)
	}
	for i, sku := range []string{"TH-ORI-10", "TH-ORI-20", "TH-ISI-5", "MN-ETM-R", "MN-EJR-R"} {
		e.must(201, "POST", "/api/v1/r/item", map[string]any{"item_name": "Barang " + sku, "sku": sku, "rate": 1000 * (i + 1)})
	}
	return csv
}

func countOutletSales(t *testing.T, e *env) int64 {
	t.Helper()
	res, err := e.s.DB.Query(context.Background(), "select count(*) from r_outlet_sales")
	if err != nil {
		t.Fatal(err)
	}
	return res.Rows[0][0].(int64)
}

func TestImportScopedOtherOutlet403(t *testing.T) {
	owner, staff := scopedEnv(t, []string{"OUT-01"}, addFnbTest)
	csv := mokaCSV(t, owner)
	const path = "/api/v1/imports/moka_item_details"

	// preview for an outlet outside the member's outlets
	assertDenied(t, staff.uploadForm(path, "moka.csv", csv, map[string]string{"outlet": "OUT-02"}), "outlet_sales", "import")
	if n := len(owner.must(200, "GET", path, nil).JSON["imports"].([]any)); n != 0 {
		t.Fatalf("a refused preview left %d entries", n)
	}

	// an entry the owner previewed for another outlet: the member can neither read nor commit it
	pv := owner.uploadForm(path, "moka.csv", csv, map[string]string{"outlet": "OUT-02"})
	if pv.Status != 200 {
		t.Fatalf("owner preview: %d %s", pv.Status, pv.Body)
	}
	id := pv.JSON["id"].(string)
	assertDenied(t, staff.do("GET", path+"/"+id, nil), "outlet_sales", "import")
	assertDenied(t, staff.do("POST", path+"/"+id+"/commit", nil), "outlet_sales", "import")
	if l := staff.must(200, "GET", path, nil).JSON["imports"].([]any); len(l) != 0 {
		t.Fatalf("member lists another outlet's entries: %v", l)
	}
	if n := countOutletSales(t, owner); n != 0 {
		t.Fatalf("refused commit wrote %d record(s)", n)
	}
	if st := owner.must(200, "GET", path+"/"+id, nil).JSON["status"]; st != "previewed" {
		t.Fatalf("entry status after refused commit: %v", st)
	}

	// the member's own outlet works end to end
	own := staff.uploadForm(path, "moka.csv", csv, map[string]string{"outlet": "OUT-01"})
	if own.Status != 200 || len(own.JSON["errors"].([]any)) != 0 {
		t.Fatalf("own preview: %d %s", own.Status, own.Body)
	}
	if cm := staff.must(200, "POST", path+"/"+own.JSON["id"].(string)+"/commit", nil); cm.JSON["status"] != "committed" {
		t.Fatalf("own commit: %s", cm.Body)
	}
	if n := countOutletSales(t, owner); n != 1 {
		t.Fatalf("own commit wrote %d record(s)", n)
	}
}

// TestImportCommitGuardsEveryRow: the create guard in CreateMany refuses the whole run when one row is for an
// outlet outside the actor's row scope; nothing is written.
func TestImportCommitGuardsEveryRow(t *testing.T) {
	owner, _ := scopedEnv(t, []string{"OUT-01"}, addFnbTest)
	a := engine.Actor{Org: "kopi-senja", ID: "staff", UserName: "staff", Roles: []string{"outlet_staff"}, Outlets: []string{"OUT-01"}}
	inputs := []map[string]any{{"date": "2026-11-01", "outlet": "OUT-01"}, {"date": "2026-11-02", "outlet": "OUT-02"}}
	_, err := owner.s.Engine.CreateMany(context.Background(), a, "outlet_sales", inputs, engine.ManyOpts{})
	var pe *engine.PermissionError
	if !errors.As(err, &pe) || pe.Model != "outlet_sales" || pe.Action != "create" {
		t.Fatalf("err = %v, want permission denied create on outlet_sales", err)
	}
	if n := countOutletSales(t, owner); n != 0 {
		t.Fatalf("refused run wrote %d record(s)", n)
	}
	if _, err := owner.s.Engine.CreateMany(context.Background(), a, "outlet_sales", inputs[:1], engine.ManyOpts{}); err != nil {
		t.Fatal(err)
	}
	if n := countOutletSales(t, owner); n != 1 {
		t.Fatalf("own-outlet run wrote %d record(s)", n)
	}
}

func TestImportOfOtherOrg404(t *testing.T) {
	e := newEnv(t, false, addFnbTest)
	ctx := context.Background()
	if err := auth.EnsureOrgCode(ctx, e.s.DB, "pt_beta", "PT Beta", "BETA"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.EnsureUser(ctx, e.s.DB, "ceo@saga.co.id", "", "rangka-demo", "pt_beta", "owner", ""); err != nil {
		t.Fatal(err)
	}
	e.login()
	csv := mokaCSV(t, e)
	outlet := e.must(201, "POST", "/api/v1/r/outlet", map[string]any{"outlet_name": "Outlet Dago"}).JSON["name"].(string)
	const path = "/api/v1/imports/moka_item_details"
	pv := e.uploadForm(path, "moka.csv", csv, map[string]string{"outlet": outlet})
	if pv.Status != 200 {
		t.Fatalf("preview: %d %s", pv.Status, pv.Body)
	}
	id := pv.JSON["id"].(string)
	e.must(200, "POST", "/auth/org", map[string]any{"org": "pt_beta"})
	e.must(404, "GET", path+"/"+id, nil)
	e.must(404, "POST", path+"/"+id+"/commit", nil)
	if l := e.must(200, "GET", path, nil).JSON["imports"].([]any); len(l) != 0 {
		t.Fatalf("pt_beta lists kopi-senja entries: %v", l)
	}
	if n := countOutletSales(t, e); n != 0 {
		t.Fatalf("records written: %d", n)
	}
	e.must(200, "POST", "/auth/org", map[string]any{"org": "kopi-senja"})
	e.must(200, "POST", path+"/"+id+"/commit", nil)
}

// plainOutletImport adds an import without per: outlet whose rows name their outlet in a column.
func plainOutletImport(apps, site string) {
	addFnbTest(apps, site)
	p := filepath.Join(apps, "fnb_test", "imports", "outlet_sales_plain.rk")
	if err := os.WriteFile(p, []byte(importSchema+"import.json\nname: outlet_sales_plain\nlabel: {id: Penjualan per baris}\nmodel: outlet_sales\nsource: csv\nheader_row: 1\nmap:\n  date: Date\n  outlet: Outlet\n  note: Note\n"), 0o644); err != nil {
		panic(err)
	}
}

// TestImportScopedPreviewRowsOfOtherOutlet: an entry without per: outlet whose planned rows include another
// outlet is not readable by a scoped member (403 import), and is left out of their list.
func TestImportScopedPreviewRowsOfOtherOutlet(t *testing.T) {
	owner, staff := scopedEnv(t, []string{"OUT-01"}, plainOutletImport)
	const path = "/api/v1/imports/outlet_sales_plain"
	csv := []byte("Date,Outlet,Note\n2026-11-01,OUT-01,pagi\n2026-11-01,OUT-02,rahasia outlet lain\n")
	pv := owner.uploadForm(path, "mix.csv", csv, nil)
	if pv.Status != 200 || len(pv.JSON["errors"].([]any)) != 0 {
		t.Fatalf("owner preview: %d %s", pv.Status, pv.Body)
	}
	id := pv.JSON["id"].(string)
	r := staff.do("GET", path+"/"+id, nil)
	assertDenied(t, r, "outlet_sales", "import")
	if strings.Contains(string(r.Body), "rahasia") {
		t.Fatalf("preview leaked: %s", r.Body)
	}
	if l := staff.must(200, "GET", path, nil).JSON["imports"].([]any); len(l) != 0 {
		t.Fatalf("member lists the entry: %v", l)
	}
	// an entry with only the member's outlet stays readable
	own := owner.uploadForm(path, "own.csv", []byte("Date,Outlet,Note\n2026-11-02,OUT-01,siang\n"), nil)
	staff.must(200, "GET", path+"/"+own.JSON["id"].(string), nil)
	if l := staff.must(200, "GET", path, nil).JSON["imports"].([]any); len(l) != 1 {
		t.Fatalf("member list: %v", l)
	}
}
