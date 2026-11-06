package engine

import (
	"errors"
	"net/http"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/plan"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// planError turns a plan refusal (contract 0028) into a 403 with its code (plan_limit, plan_read_only).
func planError(err error) error {
	var pe *plan.Error
	if errors.As(err, &pe) {
		return &StatusError{http.StatusForbidden, pe.Code, pe.Msg}
	}
	return err
}

// planNewRecord refuses a new outlet past the plan's outlet limit (contract 0028 §2), counting in o's transaction.
func (o *op) planNewRecord(m *meta.Model) error {
	if m.Name != "outlet" {
		return nil
	}
	if err := plan.CheckNewOutlet(o.ctx, o.tx, o.e.Reg(), o.actor.Org); err != nil {
		return planError(err)
	}
	o.e.Plan.Invalidate()
	return nil
}

// planReadOnly refuses a change by a person past the people limit, or on an outlet (or a record of an outlet) past
// the outlet limit, after a downgrade (contract 0028 §4). Reads are never refused; kernel actors are not checked.
// It reads the cached snapshot only (plan.Guard.Peek), so it never takes a database connection: before the first
// measurement, or when measuring fails, the write goes ahead (the limit checks at creation still hold).
func (e *Engine) planReadOnly(a Actor, m *meta.Model, action string, rec store.Record) error {
	if action == "read" || a.System || e.DB == nil {
		return nil
	}
	snap := e.Plan.Peek(e.DB, e.Reg(), e.Now(), e.Log)
	if snap == nil || (len(snap.ExcessOutlets) == 0 && len(snap.ExcessPeople) == 0) {
		return nil
	}
	if pe := snap.ReadOnlyPerson(a.ID); pe != nil {
		return planError(pe)
	}
	if m == nil || rec == nil || len(snap.ExcessOutlets) == 0 {
		return nil
	}
	if m.Name == "outlet" && action != "create" {
		if pe := snap.ReadOnlyOutlet(a.Org, str(rec["name"])); pe != nil {
			return planError(pe)
		}
	}
	for _, f := range m.Fields {
		if f.Type == "link" && f.To == "outlet" {
			if pe := snap.ReadOnlyOutlet(a.Org, str(rec[f.Name])); pe != nil {
				return planError(pe)
			}
		}
	}
	return nil
}

func (o *op) planReadOnly(m *meta.Model, action string, rec store.Record) error {
	return o.e.planReadOnly(o.actor, m, action, rec)
}

// planDecide refuses an approval decision by a read-only person, or on a record of a read-only outlet.
func (o *op) planDecide(ap *store.Approval) error {
	var m *meta.Model
	var rec store.Record
	if ap.Model != "" && ap.Name != "" {
		if m = o.e.Reg().Model(ap.Model); m != nil {
			if r, err := o.load(m, ap.Name); err == nil {
				rec = r
			}
		}
	}
	return o.planReadOnly(m, "write", rec)
}
