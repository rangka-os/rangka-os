package engine

import (
	"context"
)

// Computed is the answer of Compute: the record as the model would save it, and the issues a save would raise.
type Computed struct {
	Record   map[string]any `json:"record"`
	Errors   []Issue        `json:"errors"`
	Warnings []Issue        `json:"warnings"`
}

// Compute runs defaults, computes and save rules on an unsaved record and writes nothing (contract 0034). A work
// screen uses it to show the model's own totals, tax and change before the sale is saved, so it never computes
// money itself. The caller needs create on the model and the record must pass the caller's row scope, exactly as a
// create would; rule failures come back as issues in a 200 answer because an unfinished cart is not an error.
// No number is taken, and no hook, flow, automation or event runs. The transaction is always rolled back.
func (e *Engine) Compute(ctx context.Context, a Actor, model string, input map[string]any) (*Computed, error) {
	ctx, leave := e.gate.enter(ctx) // one registry for the whole preview, as for a unit of work (reggate.go)
	defer leave()
	m, err := e.model(model)
	if err != nil {
		return nil, err
	}
	if m.IsChild {
		return nil, badRequest(model + " rows are computed through their parent document")
	}
	tx, err := e.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	o := &op{ctx: ctx, tx: tx, e: e, actor: a, reg: e.Reg(), touched: map[string]bool{}, links: map[string]map[string]any{}}
	input = e.dropHidden(a, m, input)
	rec, issues := e.normalize(m, input, nil, true)
	if len(issues) > 0 {
		return nil, validation(issues, nil)
	}
	o.prepare(m, rec, true)
	if err := o.authorize(m, "create", rec); err != nil {
		return nil, err
	}
	if err := e.Guard(ctx, a, m.Name, rec, "create"); err != nil {
		return nil, err
	}
	errs, warns, err := o.check(m, rec, "save")
	if err != nil {
		return nil, err
	}
	errs = append(errs, minRows(m, rec, nil)...)
	if errs == nil {
		errs = []Issue{}
	}
	if warns == nil {
		warns = []Issue{}
	}
	out := e.Redact(a, m, e.Output(m, rec))
	return &Computed{Record: out, Errors: errs, Warnings: warns}, nil
}
