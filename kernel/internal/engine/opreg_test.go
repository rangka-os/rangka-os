package engine

import (
	"context"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/rk"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// reloadDuring starts SetRegistry(reg) in the background, returns once it is waiting on the gate, and gives a func
// that waits for the swap to finish (card t_ae2bdfa5).
func reloadDuring(t *testing.T, e *Engine, reg *meta.Registry) func() {
	t.Helper()
	done := make(chan struct{})
	go func() { e.SetRegistry(reg); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.gate.mu.Lock()
		waiting := e.gate.swapping
		e.gate.mu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("SetRegistry never reached the gate")
		}
		time.Sleep(time.Millisecond)
	}
	return func() {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("SetRegistry did not finish after the unit of work ended")
		}
	}
}

func pilotReg(t *testing.T) *meta.Registry {
	t.Helper()
	reg, rep := meta.Validate(pilotApps, pilotSite, nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatal(rep.All())
	}
	return reg
}

// A reload waits until the running unit of work ends: inside it the engine's registry stays the one it started
// with, at every read (also the ones that do not go through the op), and a unit of work started inside it does
// not wait on the pending swap.
func TestReloadWaitsForUnitOfWork(t *testing.T) {
	e, a := setup(t)
	reg1, rep := meta.Validate(testdb.Catalogue("apps"), testdb.Catalogue("sites/kopi-senja"), nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("metadata errors: %v", rep.All())
	}
	var wait func()
	err := e.run(context.Background(), a, func(o *op) error {
		reg0 := o.registry()
		wait = reloadDuring(t, e, reg1)
		time.Sleep(20 * time.Millisecond)
		if e.Reg() != reg0 || o.registry() != reg0 {
			t.Error("the registry changed inside a running unit of work")
		}
		r := &flowRun{e: e, o: o}
		if r.registry() != reg0 {
			t.Error("flow run without its own registry does not follow the op")
		}
		nested := make(chan error, 1)
		go func() { nested <- e.run(o.ctx, a, func(*op) error { return nil }) }()
		select {
		case err := <-nested:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("a unit of work started inside another waited on the pending reload")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	wait()
	if e.Reg() != reg1 {
		t.Fatal("SetRegistry did not swap after the unit of work")
	}
}

// Audit t_ae2bdfa5 F1: a reload that drops stock_move while a sale is submitted no longer submits it without moves.
func TestReloadDuringSubmitKeepsStockMoves(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	p.receipt(m, m.outA, 100, 12000)
	p.create("recipe", map[string]any{"product": m.nasi, "yield_qty": 1, "lines": []any{map[string]any{"material": m.beras, "qty": 0.1}}})
	sale := p.create("outlet_sales", map[string]any{"outlet": m.outA, "date": "2026-10-30", "lines": []any{map[string]any{"item": m.nasi, "qty": 10}}})
	reg0 := p.e.Reg()
	reg1 := pilotReg(t)
	delete(reg1.Models, "stock_move")
	var wait func()
	var submitted string
	err := p.e.run(context.Background(), p.a, func(o *op) error {
		wait = reloadDuring(t, p.e, reg1)
		r, err := o.submit("outlet_sales", sale, true)
		if err == nil {
			submitted = str(r.Record["name"])
		}
		return err
	})
	wait()
	p.e.SetRegistry(reg0)
	if err != nil {
		t.Fatal(err)
	}
	if mv := p.moves(submitted); len(mv) != 1 {
		t.Errorf("sale %s submitted with %d stock moves, want 1", submitted, len(mv))
	}
}

// Audit t_ae2bdfa5 F2: a reload that changes a line formula while a document is created does not compute the line
// with the new formula.
func TestReloadDuringCreateKeepsFormula(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	reg0 := p.e.Reg()
	reg1 := pilotReg(t)
	reg1.Models["outlet_sales_line"].Field("net").Compute = "gross"
	var wait func()
	var rec map[string]any
	err := p.e.run(context.Background(), p.a, func(o *op) error {
		wait = reloadDuring(t, p.e, reg1)
		r, err := o.create("outlet_sales", map[string]any{"outlet": m.outA, "date": "2026-10-30", "lines": []any{map[string]any{"item": m.nasi, "qty": 2, "discount": 5000}}}, CreateOpts{})
		if err == nil {
			rec = r.Record
		}
		return err
	})
	wait()
	p.e.SetRegistry(reg0)
	if err != nil {
		t.Fatal(err)
	}
	if net := rp(rows(rec["lines"])[0]["net"]); net != 45000 {
		t.Errorf("line net = %d, want 45000 (gross - discount, the registry the create started with)", net)
	}
}

// Audit t_ae2bdfa5 F3: the row scope of a registry is computed from that registry, also its link targets.
func TestRowScopeInUsesItsRegistry(t *testing.T) {
	p := setupPilot(t)
	m := p.masters()
	reg0 := p.e.Reg()
	kasir := &rk.Doc{Data: map[string]any{"scope": map[string]any{"field": "outlet", "from": "user.outlets"}}}
	reg0.Roles["kasir_pin"] = kasir
	a := Actor{Org: "pilot", ID: "k@pilot.test", Roles: []string{"kasir_pin"}, Outlets: []string{m.outB}, Human: true}
	outA := p.get("outlet", m.outA)
	if p.e.guardIn(reg0, p.ctx, a, "outlet", outA, "update") == nil {
		t.Fatal("a cashier scoped to outlet B may update outlet A")
	}
	reg1 := pilotReg(t)
	reg1.Roles["kasir_pin"] = kasir
	for n, x := range reg1.Models {
		if x.Field("outlet") != nil {
			delete(reg1.Models, n)
		}
	}
	p.e.SetRegistry(reg1)
	after := p.e.guardIn(reg0, p.ctx, a, "outlet", outA, "update")
	p.e.SetRegistry(reg0)
	if after == nil {
		t.Error("guardIn on reg0 changed its answer after the engine's registry was swapped")
	}
}
