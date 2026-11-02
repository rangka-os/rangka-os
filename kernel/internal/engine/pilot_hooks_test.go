package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// The pilot hooks run against a self-contained catalogue (testdata/pilot), trimmed from the rangka-apps S8
// Model reference to the fields the hooks read and write.
const (
	pilotApps = "testdata/pilot/apps"
	pilotSite = "testdata/pilot/site"
)

type pilot struct {
	t   *testing.T
	e   *Engine
	a   Actor
	ctx context.Context
}

func setupPilot(t *testing.T) *pilot {
	t.Helper()
	url := testdb.New(t)
	ctx := context.Background()
	reg, rep := meta.Validate(pilotApps, pilotSite, nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("pilot catalogue errors: %v", rep.All())
	}
	db, err := pg.NewPool(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := store.Migrate(ctx, db, reg, false, "test"); err != nil {
		t.Fatal(err)
	}
	e := New(db, reg)
	e.Now = FixedNow
	return &pilot{t: t, e: e, ctx: ctx, a: Actor{Org: "pilot", ID: "owner@pilot.test", UserName: "USR-001", Roles: []string{"owner"}, Human: true}}
}

// create makes a record and fails the test on error; it returns the record's name.
func (p *pilot) create(model string, in map[string]any) string {
	p.t.Helper()
	r, err := p.e.Create(p.ctx, p.a, model, in)
	if err != nil {
		p.t.Fatalf("create %s: %v", model, err)
	}
	return str(r.Record["name"])
}

// submit submits a draft (force) and fails the test on error; it returns the submitted name.
func (p *pilot) submit(model, name string) string {
	p.t.Helper()
	r, err := p.e.Submit(p.ctx, p.a, model, name, true)
	if err != nil {
		p.t.Fatalf("submit %s %s: %v", model, name, err)
	}
	return str(r.Record["name"])
}

// doc creates and submits a document.
func (p *pilot) doc(model string, in map[string]any) string {
	p.t.Helper()
	return p.submit(model, p.create(model, in))
}

func (p *pilot) get(model, name string) store.Record {
	p.t.Helper()
	r, err := p.e.Get(p.ctx, p.a, model, name)
	if err != nil {
		p.t.Fatalf("get %s %s: %v", model, name, err)
	}
	return r
}

func (p *pilot) cancel(model, name string) error {
	_, err := p.e.Cancel(p.ctx, p.a, model, name)
	return err
}

func (p *pilot) action(model, name, action string, args map[string]any) map[string]any {
	p.t.Helper()
	r, err := p.e.RunAction(p.ctx, p.a, model, name, action, args, ActionOpts{})
	if err != nil {
		p.t.Fatalf("action %s on %s %s: %v", action, model, name, err)
	}
	return r.Result
}

// opened is the record an action result opens.
func opened(res map[string]any) string { return str(meta.AsMap(res["open"])["name"]) }

// wantCode asserts a StatusError with a status and code.
func wantCode(t *testing.T, err error, status int, code string) {
	t.Helper()
	var se *StatusError
	if !errors.As(err, &se) || se.Status != status || se.Code != code {
		t.Fatalf("want %d %s, got %v", status, code, err)
	}
}

// money reads a currency value as whole rupiah.
func money(v any) int64 { return moneyIn(v) }

// master creates the shared master data: uoms Kg and Karung, items, two outlets, a customer and a vendor.
type masters struct {
	kg, karung, porsi         string
	beras, nasi, jasa, minyak string
	outA, outB, outC          string
	cust, cust2, vend         string
}

func (p *pilot) masters() masters {
	var m masters
	m.kg = p.create("uom", map[string]any{"uom_name": "Kg"})
	m.karung = p.create("uom", map[string]any{"uom_name": "Karung"})
	m.porsi = p.create("uom", map[string]any{"uom_name": "Porsi"})
	m.beras = p.create("item", map[string]any{"item_name": "Beras", "kind": "material", "stock_uom": m.kg, "standard_cost": 11000})
	m.minyak = p.create("item", map[string]any{"item_name": "Minyak", "kind": "material", "stock_uom": m.kg})
	m.nasi = p.create("item", map[string]any{"item_name": "Nasi goreng", "kind": "product", "stock_uom": m.porsi, "rate": 25000})
	m.jasa = p.create("item", map[string]any{"item_name": "Inspeksi NDT", "kind": "service", "stock_uom": m.kg, "rate": 1000000})
	m.outA = p.create("outlet", map[string]any{"outlet_name": "Gudang pusat"})
	m.outB = p.create("outlet", map[string]any{"outlet_name": "Cabang Dago"})
	m.outC = p.create("outlet", map[string]any{"outlet_name": "Cabang Braga"})
	m.cust = p.create("customer", map[string]any{"customer_name": "PT Baja Prima", "credit_limit": 1000000000})
	m.cust2 = p.create("customer", map[string]any{"customer_name": "PT Lain", "credit_limit": 1000000000})
	m.vend = p.create("vendor", map[string]any{"vendor_name": "CV Tani Makmur"})
	return m
}

func TestMoneyRounding(t *testing.T) {
	cases := []struct {
		in   float64
		want int64
	}{{2.5, 3}, {-2.5, -3}, {2.4999999999, 3}, {12999.17, 12999}, {0.1 * 13000 * 30, 39000}, {-0.5, -1}}
	for _, c := range cases {
		if got := roundHalfAway(c.in); got != c.want {
			t.Errorf("roundHalfAway(%v) = %d, want %d", c.in, got, c.want)
		}
	}
	if mulRound(0.1*300, 13000) != 390000 || divRound(1950000, 150) != 13000 || moneyIn(11100000.0) != 11100000 || moneyIn("1250000") != 1250000 {
		t.Error("money helpers")
	}
}

// TestPilotSetSubmitted: hooks write read-only fields of submitted documents, dependent computed fields
// follow, and editable fields are refused.
func TestPilotSetSubmitted(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	inv := p.create("sales_invoice", map[string]any{"customer": m.cust, "ppn_applies": false, "items": []any{map[string]any{"item": m.jasa, "qty": 1}}})
	// a stand-in hook (the real ones arrive in the next tasks) that pays 400.000 on submit
	p.e.Hooks["ledger.post_receivable"] = func(h *HookCtx) (map[string]any, error) {
		if _, err := h.SetSubmitted("sales_invoice", str(h.Record["name"]), map[string]any{"paid_amount": moneyOut(400000)}); err != nil {
			return nil, err
		}
		if _, err := h.SetSubmitted("sales_invoice", str(h.Record["name"]), map[string]any{"notes": "x"}); err == nil {
			t.Error("SetSubmitted accepted a field that does not exist")
		}
		if _, err := h.SetSubmitted("sales_invoice", str(h.Record["name"]), map[string]any{"po_number": "x"}); err == nil {
			t.Error("SetSubmitted accepted an editable field")
		}
		if _, err := h.SetSubmitted("customer", str(h.Record["customer"]), map[string]any{"outstanding": moneyOut(600000)}); err != nil {
			return nil, err
		}
		locked, err := h.Lock("customer", str(h.Record["customer"]))
		if err != nil || money(locked["outstanding"]) != 600000 {
			t.Errorf("lock after write: %v %v", locked["outstanding"], err)
		}
		return nil, nil
	}
	name := p.submit("sales_invoice", inv)
	got := p.get("sales_invoice", name)
	if money(got["paid_amount"]) != 400000 || money(got["outstanding"]) != 600000 || got["status"] != "partly" {
		t.Fatalf("invoice after SetSubmitted: paid %v outstanding %v status %v", got["paid_amount"], got["outstanding"], got["status"])
	}
	if c := p.get("customer", m.cust); money(c["outstanding"]) != 600000 {
		t.Fatalf("customer outstanding = %v", c["outstanding"])
	}
}
