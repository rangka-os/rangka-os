package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
)

func writeUnitApp(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, s := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestChildReadsParent: a child row's compute and default read the document's fields as parent (contract 0069 §2),
// a link as a link (parent.outlet.code); a row without a document reads parent as an unknown name (zero).
func TestChildReadsParent(t *testing.T) {
	dir := writeUnitApp(t, map[string]string{
		"shop/app.rk":           unitHead + "app.json\nname: shop\nversion: 1.0.0\ntitle: {id: Toko}\n",
		"shop/models/outlet.rk": unitHead + "model.json\nname: outlet\nlabel: {id: Outlet}\nfields:\n  - {name: code, type: data}\n",
		"shop/models/sale.rk": unitHead + "model.json\nname: sale\nlabel: {id: Jual}\nfields:\n  - {name: outlet, type: link, to: outlet}\n" +
			"  - {name: lines, type: table, of: sale_line}\n  - {name: total, type: currency, compute: 'lines.map(l, l.amount).sum()'}\n",
		"shop/models/sale_line.rk": unitHead + "model.json\nname: sale_line\nis_child: true\nlabel: {id: Baris}\nfields:\n" +
			"  - {name: promo_outlet, type: data}\n  - {name: qty, type: float}\n" +
			"  - {name: here, type: data, default: parent.outlet}\n" +
			"  - {name: code, type: data, compute: parent.outlet.code}\n" +
			"  - {name: amount, type: currency, compute: \"promo_outlet == '' || promo_outlet == parent.outlet ? qty * 800 : qty * 1000\"}\n",
	})
	reg, rep := meta.Validate(dir, "", nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("metadata errors: %v", rep.All())
	}
	e := New(nil, reg)
	links := map[string]map[string]any{"outlet/RIAU": {"name": "RIAU", "code": "RIA"}, "outlet/DAGO": {"name": "DAGO", "code": "DGO"}}
	for outlet, want := range map[string]float64{"RIAU": 1600, "DAGO": 2000} {
		ln := map[string]any{"promo_outlet": "RIAU", "qty": 2.0}
		doc := map[string]any{"outlet": outlet, "lines": []any{ln}}
		s := e.newScope(context.Background(), nil, Actor{}, reg.Models["sale"], doc, links)
		cs := e.newScope(context.Background(), nil, Actor{}, reg.Models["sale_line"], ln, links)
		cs.parent = s
		cs.applyDefaults()
		s.compute()
		if got := asNum(ln["amount"]); got != want {
			t.Errorf("%s: amount = %v, want %v", outlet, ln["amount"], want)
		}
		if ln["here"] != outlet {
			t.Errorf("%s: default here = %v", outlet, ln["here"])
		}
		if wantCode := map[string]string{"RIAU": "RIA", "DAGO": "DGO"}[outlet]; ln["code"] != wantCode {
			t.Errorf("%s: code = %v, want %s", outlet, ln["code"], wantCode)
		}
	}
	alone := map[string]any{"promo_outlet": "RIAU", "qty": 1.0}
	e.newScope(context.Background(), nil, Actor{}, reg.Models["sale_line"], alone, links).compute()
	if got := asNum(alone["amount"]); got != 0 {
		t.Errorf("a row without its document: amount = %v, want 0 (parent is not a name)", alone["amount"])
	}
}

// TestParentValidation: parent is a name only in a child model's field expressions, and its members must be fields
// of every document with a table of the model (both validators, contract 0069 §2).
func TestParentValidation(t *testing.T) {
	dir := writeUnitApp(t, map[string]string{
		"shop/app.rk": unitHead + "app.json\nname: shop\nversion: 1.0.0\ntitle: {id: Toko}\n",
		"shop/models/sale.rk": unitHead + "model.json\nname: sale\nlabel: {id: Jual}\nfields:\n  - {name: outlet, type: data}\n" +
			"  - {name: lines, type: table, of: sale_line}\n  - {name: bad, type: data, compute: parent.outlet}\n",
		"shop/models/quote.rk": unitHead + "model.json\nname: quote\nlabel: {id: Penawaran}\nfields:\n  - {name: lines, type: table, of: sale_line}\n",
		"shop/models/sale_line.rk": unitHead + "model.json\nname: sale_line\nis_child: true\nlabel: {id: Baris}\nfields:\n" +
			"  - {name: here, type: data, compute: parent.outlet}\n  - {name: ok, type: data, compute: parent.name}\n",
	})
	_, rep := meta.Validate(dir, "", nil, schema.Embedded())
	var got []string
	for _, is := range rep.All() {
		got = append(got, is.Code+" "+is.Message)
	}
	all := strings.Join(got, "\n")
	if !strings.Contains(all, "`parent` in `parent.outlet` is not a field") {
		t.Errorf("a document's parent.outlet should be unknown_field; got:\n%s", all)
	}
	if !strings.Contains(all, "`parent.outlet`: quote has no field \"outlet\"") {
		t.Errorf("a member missing on one of the documents should be unknown_field; got:\n%s", all)
	}
	if strings.Contains(all, "parent.name") {
		t.Errorf("parent.name (a system field) is fine; got:\n%s", all)
	}
}

func asNum(v any) float64 {
	f, _ := strconv.ParseFloat(fmt.Sprint(v), 64)
	return f
}
