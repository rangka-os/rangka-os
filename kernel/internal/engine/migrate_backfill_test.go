package engine

import (
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
