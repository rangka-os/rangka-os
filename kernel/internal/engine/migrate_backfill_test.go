package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// upgradeApps copies the pilot apps twice: old as they are, new with the edits (file under apps → old → new).
func upgradeApps(t *testing.T, edits map[string][2]string) (old, upd string) {
	t.Helper()
	dir := t.TempDir()
	old, upd = filepath.Join(dir, "old"), filepath.Join(dir, "new")
	for _, d := range []string{old, upd} {
		if err := exec.Command("cp", "-r", pilotApps, d).Run(); err != nil {
			t.Fatal(err)
		}
	}
	for file, e := range edits {
		p := filepath.Join(upd, file)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), e[0]) {
			t.Fatalf("%s has no %q", file, e[0])
		}
		if err := os.WriteFile(p, []byte(strings.Replace(string(b), e[0], e[1], 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return old, upd
}

var backfillEdits = map[string][2]string{
	"stock/models/recipe.rk": {"  - {name: active, type: check, default: 'true'}\n",
		"  - {name: active, type: check, default: 'true'}\n" +
			"  - {name: active_product, type: data, unique: true, compute: \"active ? product : ''\"}\n" +
			"  - {name: checked, type: check, default: 'true'}\n" +
			"  - {name: note, type: data, default: \"'baru'\"}\n" +
			"  - {name: stamped, type: date, compute: 'today()'}\n"},
	"stock/models/recipe_line.rk": {"  - {name: qty, type: float, required: true}\n",
		"  - {name: qty, type: float, required: true}\n  - {name: double_qty, type: float, compute: 'qty * 2'}\n"},
	"outlet_sales/models/outlet_sales.rk": {"fields:\n", "fields:\n  - {name: outlet_copy, type: data, compute: outlet}\n"},
}

func (p *pilot) upgrade(apps string) (*store.MigrateResult, error) {
	p.t.Helper()
	reg, rep := meta.Validate(apps, pilotSite, nil, schema.Embedded())
	if rep.Errors() > 0 {
		p.t.Fatal(rep.All())
	}
	res, err := store.Migrate(p.ctx, p.e.DB, reg, false, "test")
	if err == nil {
		p.e.reg.Store(reg)
	}
	return res, err
}

// TestMigrateBackfillsNewColumns: a column added by migrate gets, for the records already there (submitted
// documents and child rows included), the value of its compute, or its literal default; a compute that reads the
// clock is left empty (its value at upgrade time would be wrong for every old record).
func TestMigrateBackfillsNewColumns(t *testing.T) {
	old, upd := upgradeApps(t, backfillEdits)
	p := setupPilotApps(t, old)
	m := p.masters()
	p.receipt(m, m.outB, 100, 12000)
	on := p.create("recipe", map[string]any{"product": m.nasi, "lines": []any{map[string]any{"material": m.beras, "qty": 0.1}}})
	off := p.create("recipe", map[string]any{"product": m.nasi, "active": false, "lines": []any{map[string]any{"material": m.beras, "qty": 0.2}}})
	sale := p.doc("outlet_sales", map[string]any{"outlet": m.outB, "date": "2026-10-20", "lines": []any{map[string]any{"item": m.nasi, "qty": 10}}})

	res, err := p.upgrade(upd)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !strings.Contains(strings.Join(res.Statements, ";"), `add column "active_product"`) {
		t.Fatalf("statements: %v", res.Statements)
	}
	r := p.get("recipe", on)
	if r["active_product"] != m.nasi || r["checked"] != true || r["note"] != "baru" {
		t.Fatalf("active recipe after upgrade: active_product %v checked %v note %v", r["active_product"], r["checked"], r["note"])
	}
	if res, err := p.e.DB.Query(p.ctx, "select count(*) from r_recipe where stamped is not null"); err != nil || res.Rows[0][0].(int64) != 0 {
		t.Fatalf("a compute that reads the clock was backfilled: %v %v", res, err)
	}
	if l := rows(r["lines"])[0]; l["double_qty"] != 0.2 {
		t.Fatalf("child compute after upgrade: %v", l["double_qty"])
	}
	if r := p.get("recipe", off); r["active_product"] != "" || r["checked"] != true {
		t.Fatalf("inactive recipe after upgrade: %v %v", r["active_product"], r["checked"])
	}
	if s := p.get("outlet_sales", sale); s["outlet_copy"] != m.outB || s["docstatus"] != int64(1) {
		t.Fatalf("submitted document after upgrade: %v %v", s["outlet_copy"], s["docstatus"])
	}
	// the unique index now holds: a second active recipe for the product is refused
	if _, err := p.e.Create(p.ctx, p.a, "recipe", map[string]any{"product": m.nasi, "lines": []any{map[string]any{"material": m.beras, "qty": 1}}}); err == nil {
		t.Fatal("a second active recipe was accepted after the upgrade")
	}
	// a second migrate has nothing to add and leaves the values alone
	if res, err := p.upgrade(upd); err != nil || len(res.Statements) != 0 {
		t.Fatalf("second migrate: %v %v", res, err)
	}
}

// TestMigrateBackfillUniqueClash: when the backfilled values break a unique field, migrate fails, names the
// records that clash, and applies nothing.
func TestMigrateBackfillUniqueClash(t *testing.T) {
	old, upd := upgradeApps(t, backfillEdits)
	p := setupPilotApps(t, old)
	m := p.masters()
	r1 := p.create("recipe", map[string]any{"product": m.nasi, "lines": []any{map[string]any{"material": m.beras, "qty": 0.1}}})
	r2 := p.create("recipe", map[string]any{"product": m.nasi, "lines": []any{map[string]any{"material": m.beras, "qty": 0.2}}})
	_, err := p.upgrade(upd)
	if err == nil {
		t.Fatal("migrate accepted two active recipes for one product")
	}
	for _, want := range []string{"recipe.active_product", r1, r2, m.nasi} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}
	res, err := p.e.DB.Query(p.ctx, "select count(*) from information_schema.columns where table_name = 'r_recipe' and column_name = 'active_product'")
	if err != nil || res.Rows[0][0].(int64) != 0 {
		t.Fatalf("the refused migration left the column: %v %v", res, err)
	}
}

// TestMigrateBackfillFrozenDocuments: on a submitted document only the new fields are computed, from the values
// stored when it was submitted, and a new compute that reads another record (a link member) is left empty there:
// the linked record may have changed since (audit: a packing factor changed after submit doubled the unit cost).
// A draft gets it, as its next save would.
func TestMigrateBackfillFrozenDocuments(t *testing.T) {
	old, upd := upgradeApps(t, map[string][2]string{
		"procurement/models/goods_receipt_item.rk": {"  - {name: unit_cost, type: currency, compute: 'stock_qty > 0 ? round(amount / stock_qty) : 0'}\n",
			"  - {name: unit_cost, type: currency, compute: 'stock_qty > 0 ? round(amount / stock_qty) : 0'}\n" +
				"  - {name: stock_qty_new, type: float, compute: \"packing == '' ? qty : qty * packing.factor\"}\n" +
				"  - {name: unit_new, type: currency, compute: 'stock_qty > 0 ? round(amount / stock_qty) : 0'}\n" +
				"  - {name: day_new, type: date, compute: 'today ()'}\n" +
				"  - {name: amount_v2, type: currency, compute: 'round(qty * rate)'}\n" +
				"  - {name: factor_v2, type: float, compute: \"packing == '' ? 1.0 : packing.factor\"}\n" +
				"  - {name: stock_qty_v3, type: float, compute: 'qty * factor_v2'}\n"},
		"procurement/models/goods_receipt.rk": {"  - {name: items, type: table, of: goods_receipt_item, min_rows: 1}\n",
			"  - {name: items, type: table, of: goods_receipt_item, min_rows: 1}\n" +
				"  - {name: total_v2, type: currency, compute: 'items.map(i, i.amount_v2).sum()'}\n" +
				"  - {name: qty_v3, type: float, compute: 'items.map(i, i.stock_qty_v3).sum()'}\n"},
	})
	p := setupPilotApps(t, old)
	m := p.masters()
	pack := p.create("uom_conversion", map[string]any{"item": m.beras, "uom": m.karung, "factor": 50})
	in := map[string]any{"vendor": m.vend, "outlet": m.outA, "items": []any{map[string]any{"item": m.beras, "qty": 2, "packing": pack, "rate": 600000}}}
	gr := p.doc("goods_receipt", in)
	draft := p.create("goods_receipt", in)
	if _, err := p.e.Update(p.ctx, p.a, "uom_conversion", pack, map[string]any{"factor": 25}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.upgrade(upd); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	l := rows(p.get("goods_receipt", gr)["items"])[0]
	if l["stock_qty"] != 100.0 || rp(l["unit_new"]) != 12000 || l["stock_qty_new"] != 0.0 {
		t.Fatalf("submitted line: stock_qty %v unit_new %v stock_qty_new %v, want 100 12000 0 (empty)", l["stock_qty"], l["unit_new"], l["stock_qty_new"])
	}
	res, err := p.e.DB.Query(p.ctx, "select count(*) from r_goods_receipt_item where stock_qty_new is not null and parent = $1", gr)
	if err != nil || res.Rows[0][0].(int64) != 0 {
		t.Fatalf("a link-reading compute was written on a submitted document: %v %v", res, err)
	}
	if d := rows(p.get("goods_receipt", draft)["items"])[0]; d["stock_qty_new"] != 50.0 {
		t.Fatalf("draft line stock_qty_new = %v, want 50 (factor today)", d["stock_qty_new"])
	}
	// a document compute over a new child compute sees the child values (audit R2-1), on both documents
	for _, n := range []string{gr, draft} {
		if g := p.get("goods_receipt", n); rp(g["total_v2"]) != 1200000 {
			t.Fatalf("%s total_v2 = %v, want 1200000", n, g["total_v2"])
		}
	}
	// a compute over a new field left empty is left empty too, also in the document (audit R2-2)
	res, err = p.e.DB.Query(p.ctx, `select (select count(*) from r_goods_receipt_item where parent = $1 and (factor_v2 is not null or stock_qty_v3 is not null)),
		(select count(*) from r_goods_receipt where name = $1 and qty_v3 is not null), (select qty_v3 from r_goods_receipt where name = $2)`, gr, draft)
	if err != nil || res.Rows[0][0].(int64) != 0 || res.Rows[0][1].(int64) != 0 || fmt.Sprint(res.Rows[0][2]) != "50" {
		t.Fatalf("transitive empty: %v %v", res.Rows, err)
	}
	if res, err := p.e.DB.Query(p.ctx, "select count(*) from r_goods_receipt_item where day_new is not null"); err != nil || res.Rows[0][0].(int64) != 0 {
		t.Fatalf("today () with a space was backfilled: %v %v", res, err)
	}
}

func TestBackfillVolatile(t *testing.T) {
	for expr, want := range map[string]bool{"today()": true, "today ()": true, "now( )": true, "tax.ppn_rate": true, "org .name": true,
		"user.name": true, "active ? product : ''": false, "qty * 2": false, "todays_total": false, "taxable": false} {
		if got := store.Volatile(expr); got != want {
			t.Errorf("Volatile(%q) = %v, want %v", expr, got, want)
		}
	}
}
