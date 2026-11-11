package engine

import (
	"errors"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// heldRef is a record that has live approval requests (pending, or approved and unused) whose snapshot matches
// the record as stored now.
type heldRef struct {
	m    *meta.Model
	name string
	snap string
	ids  []string
}

// linksTo reports whether rm links model from a field or from a child row.
func linksTo(reg *meta.Registry, rm *meta.Model, model string) bool {
	for _, f := range rm.Fields {
		switch {
		case f.Type == "link" && f.To == model:
			return true
		case f.Type == "table":
			if cm := reg.Model(f.Of); cm != nil {
				for _, cf := range cm.Fields {
					if cf.Type == "link" && cf.To == model {
						return true
					}
				}
			}
		}
	}
	return false
}

// heldReferrers finds the records of models linking model that have live requests made for their current
// state. Submit calls it before the link rename, so restampReferrers can carry those requests over: the rename
// corrects a name, it is not a change of the document someone approves (audit kernel fix 1111 O1).
func (o *op) heldReferrers(model string) ([]heldRef, error) {
	reg := o.e.Reg()
	var models []string
	byName := map[string]*meta.Model{}
	for _, rm := range reg.InstalledModels() {
		if linksTo(reg, rm, model) {
			models = append(models, rm.Name)
			byName[rm.Name] = rm
		}
	}
	live, err := store.LiveApprovals(o.ctx, o.tx, o.actor.Org, models)
	if err != nil {
		return nil, err
	}
	var out []heldRef
	at := map[string]int{}
	for _, ap := range live {
		key := ap.Model + "\x00" + ap.Name + "\x00" + ap.Snapshot
		if i, ok := at[key]; ok {
			out[i].ids = append(out[i].ids, ap.ID)
			continue
		}
		rm := byName[ap.Model]
		rec, err := store.Get(o.ctx, o.tx, reg, o.actor.Org, rm, ap.Name)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return nil, err
		}
		if Snapshot(rm, rec) != ap.Snapshot {
			continue // made for an older state: Approve voids it, the rename must not revive it
		}
		at[key] = len(out)
		out = append(out, heldRef{m: rm, name: ap.Name, snap: ap.Snapshot, ids: []string{ap.ID}})
	}
	return out, nil
}

// restampReferrers gives the requests found by heldReferrers the snapshot of their record after the rename.
func (o *op) restampReferrers(refs []heldRef) error {
	for _, r := range refs {
		rec, err := store.Get(o.ctx, o.tx, o.e.Reg(), o.actor.Org, r.m, r.name)
		if err != nil {
			return err
		}
		if err := store.RestampApprovals(o.ctx, o.tx, o.actor.Org, r.ids, r.snap, Snapshot(r.m, rec)); err != nil {
			return err
		}
	}
	return nil
}

// renameSelfLinks applies the link rename to the submitted record in memory: a field or child row of m that
// links m itself and held the draft name now holds the final one, as the stored row does, so on_submit hooks,
// flows and the caller see what is stored (audit kernel fix 1111 O3).
func renameSelfLinks(reg *meta.Registry, m *meta.Model, rec store.Record, from, to string) {
	for _, f := range m.Fields {
		switch {
		case f.Type == "link" && f.To == m.Name:
			if str(rec[f.Name]) == from {
				rec[f.Name] = to
			}
		case f.Type == "table":
			cm := reg.Model(f.Of)
			if cm == nil {
				continue
			}
			for _, row := range rows(rec[f.Name]) {
				for _, cf := range cm.Fields {
					if cf.Type == "link" && cf.To == m.Name && str(row[cf.Name]) == from {
						row[cf.Name] = to
					}
				}
			}
		}
	}
}
