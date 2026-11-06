package engine

import (
	"errors"
	"net/http"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/plan"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// planError turns a plan refusal (contract 0027) into a 403 with its code (plan_limit, plan_read_only).
func planError(err error) error {
	var pe *plan.Error
	if errors.As(err, &pe) {
		return &StatusError{http.StatusForbidden, pe.Code, pe.Msg}
	}
	return err
}

// planNewRecord refuses a new outlet past the plan's outlet limit (contract 0027 §2).
func (o *op) planNewRecord(m *meta.Model) error {
	if m.Name != "outlet" || o.e.DB == nil {
		return nil
	}
	if err := plan.CheckNewOutlet(o.ctx, o.tx, o.e.DB, o.e.Reg(), o.actor.Org); err != nil {
		return planError(err)
	}
	o.e.Plan.Invalidate()
	return nil
}

// planReadOnly refuses a change by a person past the people limit, or on an outlet (or a record of an outlet) past
// the outlet limit, after a downgrade (contract 0027 §4). Reads are never refused; kernel actors are not checked.
// When the plan cannot be measured the write goes ahead (the limit checks at creation still hold).
func (o *op) planReadOnly(m *meta.Model, action string, rec store.Record) error {
	if action == "read" || o.actor.System || o.e.DB == nil {
		return nil
	}
	snap, err := o.e.Plan.Get(o.ctx, o.e.DB, o.e.Reg(), o.e.Now())
	if err != nil {
		o.e.Log("plan: %v", err)
		return nil
	}
	if len(snap.ExcessOutlets) == 0 && len(snap.ExcessPeople) == 0 {
		return nil
	}
	if pe := snap.ReadOnlyPerson(o.actor.ID); pe != nil {
		return planError(pe)
	}
	if rec == nil || len(snap.ExcessOutlets) == 0 {
		return nil
	}
	if m.Name == "outlet" && action != "create" {
		if pe := snap.ReadOnlyOutlet(o.actor.Org, str(rec["name"])); pe != nil {
			return planError(pe)
		}
	}
	for _, f := range m.Fields {
		if f.Type == "link" && f.To == "outlet" {
			if pe := snap.ReadOnlyOutlet(o.actor.Org, str(rec[f.Name])); pe != nil {
				return planError(pe)
			}
		}
	}
	return nil
}
