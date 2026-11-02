package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// scopeEngine builds an engine (no database) over a tiny app: outlet, sales_invoice (link outlet),
// stock_transfer (links from_outlet, to_outlet), customer (no outlet), and two scoped roles.
func scopeEngine(t *testing.T) *Engine {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "apps", "shop")
	files := map[string]string{
		"app.rk": "name: shop\nversion: 1.0.0\ntitle: {id: Toko}\n",
		"models/outlet.rk": "name: outlet\nnaming: OUT-{##}\nlabel: {id: Outlet}\nfields:\n" +
			"  - {name: outlet_name, type: data}\n",
		"models/sales_invoice.rk": "name: sales_invoice\nlabel: {id: Faktur}\nfields:\n" +
			"  - {name: outlet, type: link, to: outlet}\n  - {name: grand_total, type: currency}\n",
		"models/stock_transfer.rk": "name: stock_transfer\nlabel: {id: Transfer}\nfields:\n" +
			"  - {name: from_outlet, type: link, to: outlet}\n  - {name: to_outlet, type: link, to: outlet}\n",
		"models/customer.rk":      "name: customer\nlabel: {id: Pelanggan}\nfields:\n  - {name: customer_name, type: data}\n",
		"roles/outlet_staff.rk":   "name: outlet_staff\nlabel: {id: Staf outlet}\nscope: {field: outlet, from: user.outlets}\n",
		"roles/transfer_staff.rk": "name: transfer_staff\nlabel: {id: Staf gudang}\nscope: {field: [from_outlet, to_outlet], from: user.outlets}\n",
		"roles/nobody_staff.rk":   "name: nobody_staff\nlabel: {id: Tanpa outlet}\nscope: {field: outlet, from: '[]'}\n",
		"roles/clerk.rk":          "name: clerk\nlabel: {id: Kasir}\n",
	}
	for p, body := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg := meta.Load(filepath.Dir(dir), "", nil)
	if reg.Models["stock_transfer"] == nil || reg.RoleScope("outlet_staff") == nil {
		t.Fatalf("test registry did not load: models %v", reg.Models)
	}
	return New(nil, reg)
}

func visible(t *testing.T, e *Engine, a Actor, model string, rec store.Record) bool {
	t.Helper()
	s := e.RowScope(a)
	if s == nil {
		t.Fatal("RowScope = nil for a scoped actor")
	}
	return s.Visible(e.Reg().Models[model], rec)
}

var staff = Actor{Org: "o", ID: "staff@example.test", Roles: []string{"outlet_staff"}, Outlets: []string{"OUT-01"}}

func TestVisibleByField(t *testing.T) {
	e := scopeEngine(t)
	if !visible(t, e, staff, "sales_invoice", store.Record{"name": "INV-1", "outlet": "OUT-01"}) {
		t.Error("own outlet's invoice hidden")
	}
	if visible(t, e, staff, "sales_invoice", store.Record{"name": "INV-2", "outlet": "OUT-02"}) {
		t.Error("other outlet's invoice visible")
	}
	if visible(t, e, staff, "sales_invoice", store.Record{"name": "INV-3", "outlet": ""}) {
		t.Error("invoice without outlet visible")
	}
	err := e.Guard(context.Background(), staff, "sales_invoice", store.Record{"name": "INV-2", "outlet": "OUT-02"}, "read")
	var pe *PermissionError
	if !errors.As(err, &pe) || pe.Model != "sales_invoice" || pe.Action != "read" {
		t.Errorf("Guard = %v", err)
	}
	if err := e.Guard(context.Background(), staff, "sales_invoice", store.Record{"name": "INV-1", "outlet": "OUT-01"}, "update"); err != nil {
		t.Errorf("Guard own = %v", err)
	}
}

func TestVisibleTargetModelByName(t *testing.T) {
	e := scopeEngine(t)
	if !visible(t, e, staff, "outlet", store.Record{"name": "OUT-01"}) || visible(t, e, staff, "outlet", store.Record{"name": "OUT-02"}) {
		t.Error("outlet visibility by name")
	}
}

func TestUnscopedModelVisible(t *testing.T) {
	e := scopeEngine(t)
	if !visible(t, e, staff, "customer", store.Record{"name": "CUST-1"}) {
		t.Error("customer hidden from a scoped member")
	}
}

func TestEmptyFromHidesAll(t *testing.T) {
	e := scopeEngine(t)
	none := staff
	none.Outlets = nil
	if visible(t, e, none, "sales_invoice", store.Record{"outlet": "OUT-01"}) || visible(t, e, none, "outlet", store.Record{"name": "OUT-01"}) {
		t.Error("no outlets: scoped models visible")
	}
	if !visible(t, e, none, "customer", store.Record{"name": "CUST-1"}) {
		t.Error("no outlets: customer hidden")
	}
	lit := Actor{Org: "o", ID: "x", Roles: []string{"nobody_staff"}, Outlets: []string{"OUT-01"}}
	if visible(t, e, lit, "sales_invoice", store.Record{"outlet": "OUT-01"}) {
		t.Error("from: [] shows records")
	}
	bad := Actor{Org: "o", ID: "x", Roles: []string{"outlet_staff"}}
	e.Reg().Roles["outlet_staff"].Data.(map[string]any)["scope"] = map[string]any{"field": "outlet", "from": "user.nope +"}
	if s := e.RowScope(bad); s == nil || len(s.Values) != 0 {
		t.Errorf("bad from: %+v (want empty values, fail closed)", s)
	}
}

func TestTransferVisibleFromEitherSide(t *testing.T) {
	e := scopeEngine(t)
	a := Actor{Org: "o", ID: "w", Roles: []string{"transfer_staff"}, Outlets: []string{"OUT-02"}}
	for _, c := range []struct {
		from, to string
		want     bool
	}{{"OUT-02", "OUT-01", true}, {"OUT-01", "OUT-02", true}, {"OUT-01", "OUT-03", false}} {
		if got := visible(t, e, a, "stock_transfer", store.Record{"from_outlet": c.from, "to_outlet": c.to}); got != c.want {
			t.Errorf("%s→%s visible = %v, want %v", c.from, c.to, got, c.want)
		}
	}
	if !visible(t, e, a, "outlet", store.Record{"name": "OUT-02"}) || visible(t, e, a, "outlet", store.Record{"name": "OUT-01"}) {
		t.Error("outlet is the target of from_outlet/to_outlet")
	}
}

func TestNoScopeRoleIsNil(t *testing.T) {
	e := scopeEngine(t)
	for _, roles := range [][]string{{"clerk"}, {"owner"}, nil} {
		if s := e.RowScope(Actor{Org: "o", ID: "c", Roles: roles, Outlets: []string{"OUT-01"}}); s != nil {
			t.Errorf("roles %v: RowScope = %+v", roles, s)
		}
	}
	if err := e.Guard(context.Background(), Actor{Roles: []string{"clerk"}}, "sales_invoice", store.Record{"outlet": "OUT-02"}, "read"); err != nil {
		t.Errorf("Guard without scope = %v", err)
	}
	if k := scopeKey(nil); k != "" {
		t.Errorf("scopeKey(nil) = %q", k)
	}
	if k := scopeKey(&RowScope{Fields: []string{"outlet"}, Values: map[string]bool{"OUT-02": true, "OUT-01": true}}); k != "outlet=OUT-01,OUT-02" {
		t.Errorf("scopeKey = %q", k)
	}
	if k := scopeKey(&RowScope{Fields: []string{"outlet"}, Values: map[string]bool{}}); k == "" {
		t.Error("an empty scope shares the unscoped cache key")
	}
}
