package engine

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/money"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// ---------- money: whole rupiah as int64, exact through internal/money

// moneyIn reads a stored amount as whole rupiah, rounded half away from zero: a currency field (float64 in
// the engine), an IDR twin (int64) or a money field (money.Decimal) all go through money.Rat, so no float
// arithmetic decides a rupiah. This, moneyOut and the rounding helpers below are the only places that know.
func moneyIn(v any) int64 {
	if n, ok := v.(int64); ok {
		return n
	}
	r, ok := money.Rat(v)
	if !ok {
		return 0
	}
	return ratRound(r)
}

// moneyOut is the value a hook writes into a currency, IDR-twin or money field; store.Coerce turns it into
// the field's own type (float64 for currency, int64 for a twin, money.Decimal for money).
func moneyOut(n int64) any { return n }

// ratRound rounds a rational to whole rupiah, half away from zero.
func ratRound(r *big.Rat) int64 {
	n, err := money.ToIDR(money.FromInt(1), r)
	if err != nil {
		return 0
	}
	return n
}

// qtyRat is the exact rational of a quantity or factor (its shortest decimal form).
func qtyRat(q float64) *big.Rat {
	r, ok := money.Rat(q)
	if !ok {
		return new(big.Rat)
	}
	return r
}

// mulRound is round(qty × amount) for a quantity and a whole-rupiah amount.
func mulRound(qty float64, amount int64) int64 {
	return ratRound(new(big.Rat).Mul(qtyRat(qty), new(big.Rat).SetInt64(amount)))
}

// divRound is round(value / qty) (qty != 0).
func divRound(value int64, qty float64) int64 {
	return ratRound(new(big.Rat).Quo(new(big.Rat).SetInt64(value), qtyRat(qty)))
}

// percentOff is round(amount × (1 − pct/100)).
func percentOff(amount int64, pct float64) int64 {
	f := new(big.Rat).Sub(big.NewRat(1, 1), new(big.Rat).Quo(qtyRat(pct), big.NewRat(100, 1)))
	return ratRound(new(big.Rat).Mul(new(big.Rat).SetInt64(amount), f))
}

func qtyOf(v any) float64 { return num(v) }

// ---------- record access for hooks

// Find returns the records of model whose fields equal eq (by name, with child rows).
func (h *HookCtx) Find(model string, eq map[string]any) ([]store.Record, error) {
	m := h.op.e.Reg().Model(model)
	if m == nil {
		return nil, notFound("model " + model)
	}
	return store.Find(h.op.ctx, h.op.tx, h.op.e.Reg(), h.op.actor.Org, m, eq, store.FindOpts{WithChildren: true})
}

// FindForUpdate is Find with the rows locked until the transaction ends.
func (h *HookCtx) FindForUpdate(model string, eq map[string]any) ([]store.Record, error) {
	m := h.op.e.Reg().Model(model)
	if m == nil {
		return nil, notFound("model " + model)
	}
	return store.Find(h.op.ctx, h.op.tx, h.op.e.Reg(), h.op.actor.Org, m, eq, store.FindOpts{WithChildren: true, ForUpdate: true})
}

// Lock locks a record (select … for update) and returns its stored values.
func (h *HookCtx) Lock(model, name string) (store.Record, error) {
	m := h.op.e.Reg().Model(model)
	if m == nil {
		return nil, notFound("model " + model)
	}
	rec, err := store.GetForUpdate(h.op.ctx, h.op.tx, h.op.e.Reg(), h.op.actor.Org, m, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound(model + " " + name)
	}
	return rec, err
}

// LockKey serialises hooks on a key that has no row yet (outlet+item of a first stock balance).
func (h *HookCtx) LockKey(key string) error {
	return store.LockKey(h.op.ctx, h.op.tx, h.op.actor.Org+"|"+key)
}

// SetSubmitted writes hook-maintained read-only fields (paid_amount, outstanding, system_qty, cogs, …) of a
// submitted or non-submittable record, which update refuses or would re-validate. fields maps a field name
// to its value; for a table field, a map of row name → {child field: value}. Only read_only fields without
// compute are accepted. Computed fields that read a changed field are recomputed (only those, so values
// depending on links that changed since submit stay as submitted). No rules run and no automations fire;
// the record event and audit entry are written as for an update.
func (h *HookCtx) SetSubmitted(model, name string, fields map[string]any) (store.Record, error) {
	return h.setSubmitted(model, name, fields, nil)
}

// setSubmitted is SetSubmitted that also accepts the editable fields named in allow (quotation.mark_outcome
// sets outcome and lost_reason on a submitted quotation).
func (h *HookCtx) setSubmitted(model, name string, fields map[string]any, allow []string) (store.Record, error) {
	o := h.op
	m := o.e.Reg().Model(model)
	if m == nil {
		return nil, notFound("model " + model)
	}
	var rec store.Record
	if h.Model != nil && h.Model.Name == model && str(h.Record["name"]) == name {
		rec = h.Record
	} else {
		r, err := o.load(m, name)
		if err != nil {
			return nil, err
		}
		rec = r
	}
	if m.IsSubmittable && toInt(rec["docstatus"]) == 0 {
		return nil, fmt.Errorf("SetSubmitted %s %s: a draft is changed with Update", model, name)
	}
	writable := func(f *meta.Field) bool {
		if f.ReadOnly && f.Compute == "" {
			return true
		}
		for _, a := range allow {
			if a == f.Name && f.Compute == "" {
				return true
			}
		}
		return false
	}
	changed := map[string]bool{}
	for k, v := range fields {
		f := m.Field(k)
		if f == nil {
			return nil, fmt.Errorf("SetSubmitted %s: no field %s", model, k)
		}
		if f.Type == "table" {
			cm := o.e.Reg().Models[f.Of]
			byRow, _ := v.(map[string]map[string]any)
			for rowName, vals := range byRow {
				var row map[string]any
				for _, r := range rows(rec[k]) {
					if str(r["name"]) == rowName {
						row = r
					}
				}
				if row == nil {
					return nil, fmt.Errorf("SetSubmitted %s %s: no row %s in %s", model, name, rowName, k)
				}
				rowChanged := map[string]bool{}
				for ck, cv := range vals {
					cf := cm.Field(ck)
					if cf == nil || !writable(cf) {
						return nil, fmt.Errorf("SetSubmitted %s.%s: %s is not a read-only field", model, k, ck)
					}
					c, err := store.Coerce(cf, cv)
					if err != nil {
						return nil, err
					}
					row[ck] = c
					rowChanged[ck] = true
				}
				o.recomputeDependents(cm, row, rowChanged)
			}
			changed[k] = true
			continue
		}
		if !writable(f) {
			return nil, fmt.Errorf("SetSubmitted %s: %s is not a read-only field", model, k)
		}
		c, err := store.Coerce(f, v)
		if err != nil {
			return nil, err
		}
		rec[k] = c
		changed[k] = true
	}
	o.recomputeDependents(m, rec, changed)
	if err := store.Update(o.ctx, o.tx, o.e.Reg(), o.actor.Org, m, name, rec); err != nil {
		return nil, err
	}
	delete(o.links, model+"/"+name)
	if err := o.event(m, rec, "updated"); err != nil {
		return nil, err
	}
	return rec, nil
}

// recomputeDependents recomputes, in compute order, the computed fields that read a changed field (directly
// or through another recomputed field).
func (o *op) recomputeDependents(m *meta.Model, rec store.Record, changed map[string]bool) {
	if len(changed) == 0 {
		return
	}
	s := o.scope(m, rec)
	for _, f := range computeOrder(m) {
		refs, _ := cel.FreeVars(f.Compute)
		hit := false
		for _, r := range refs {
			if r.Kind == "ref" && changed[r.Head] {
				hit = true
			}
		}
		if !hit {
			continue
		}
		v, err := s.eval(f.Compute)
		c := store.Zero(f)
		if err == nil && v != nil {
			if cv, err := store.Coerce(f, v); err == nil {
				c = cv
			}
		}
		rec[f.Name] = c
		changed[f.Name] = true
	}
}

// has reports whether a model has a field (hooks tolerate modules that do not declare optional fields).
func has(m *meta.Model, field string) bool { return m != nil && m.Field(field) != nil }

func refuse(code, msg string) error { return conflict(code, msg) }
