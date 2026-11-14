package engine

import (
	"errors"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// heldRef is a record that has live approval requests (pending, or approved and unused) whose snapshot matches
// the record as stored before the link rename; want is the snapshot it has once the rename changed only its links.
type heldRef struct {
	m    *meta.Model
	name string
	snap string
	want string
	ids  []string
}

// linksTo reports whether rm links model from a field (x_ custom links included) or from a child row.
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

// heldReferrers finds the records linking the draft from of model that have live requests made for their current
// state, and the snapshot each will have once only its links point at to. Submit calls it before the link
// rename, so restampReferrers can carry those requests over: the rename corrects a name, it is not a change of
// the document someone approves (audit kernel fix 1111 O1). A record that does not link the draft is left out.
func (o *op) heldReferrers(model, from, to string) ([]heldRef, error) {
	reg := o.registry()
	// only the requests of records that link the draft now: a referrer model may hold many live requests for
	// records that have nothing to do with this draft (card t_6e1fa9ae)
	var live []store.Approval
	byName := map[string]*meta.Model{}
	for _, rm := range reg.InstalledModels() {
		if rm.IsChild || !linksTo(reg, rm, model) {
			continue // requests are made for documents, never for child rows
		}
		byName[rm.Name] = rm
		names, err := store.RecordsLinking(o.ctx, o.tx, reg, o.actor.Org, rm, model, from)
		if err != nil {
			return nil, err
		}
		aps, err := store.LiveApprovalsOf(o.ctx, o.tx, o.actor.Org, rm.Name, names)
		if err != nil {
			return nil, err
		}
		live = append(live, aps...)
	}
	var out []heldRef
	at := map[string]int{}
	for _, ap := range live {
		key := ap.Model + "\x00" + ap.Name + "\x00" + ap.Snapshot
		if i, ok := at[key]; ok {
			if i >= 0 {
				out[i].ids = append(out[i].ids, ap.ID)
			}
			continue
		}
		at[key] = -1
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
		if !renameLinksIn(reg, rm, rec, model, from, to) {
			continue // does not link the draft: the rename leaves it as it is
		}
		at[key] = len(out)
		out = append(out, heldRef{m: rm, name: ap.Name, snap: ap.Snapshot, want: Snapshot(rm, rec), ids: []string{ap.ID}})
	}
	return out, nil
}

// restampReferrers moves the requests found by heldReferrers to the record's snapshot after the rename, only when
// that snapshot is exactly the one the rename alone gives. The record was read without a lock: a write another
// transaction committed meanwhile changes the snapshot, and its requests are then left to be voided (audit
// kernel 1111b F1: an approval must never carry content the approver did not see).
func (o *op) restampReferrers(refs []heldRef) error {
	for _, r := range refs {
		rec, err := store.Get(o.ctx, o.tx, o.registry(), o.actor.Org, r.m, r.name)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return err
		}
		if Snapshot(r.m, rec) != r.want {
			continue
		}
		if err := store.RestampApprovals(o.ctx, o.tx, o.actor.Org, r.ids, r.snap, r.want); err != nil {
			return err
		}
	}
	return nil
}

// renameSelfLinks applies the link rename to the submitted record in memory: a field or child row of m that
// links m itself and held the draft name now holds the final one, as the stored row does, so on_submit hooks,
// flows and the caller see what is stored (audit kernel fix 1111 O3).
func renameSelfLinks(reg *meta.Registry, m *meta.Model, rec store.Record, from, to string) {
	renameLinksIn(reg, m, rec, m.Name, from, to)
}

// renameLinksIn sets every link of rm's record rec to model that holds from (fields, x_ custom links, child rows)
// to to, as store.RenameRecordLinks does in the database. It reports whether anything changed.
func renameLinksIn(reg *meta.Registry, rm *meta.Model, rec store.Record, model, from, to string) bool {
	changed := false
	for _, f := range rm.Fields {
		switch {
		case f.Type == "link" && f.To == model:
			if str(rec[f.Name]) == from {
				rec[f.Name], changed = to, true
			}
		case f.Type == "table":
			cm := reg.Model(f.Of)
			if cm == nil {
				continue
			}
			for _, row := range rows(rec[f.Name]) {
				for _, cf := range cm.Fields {
					if cf.Type == "link" && cf.To == model && str(row[cf.Name]) == from {
						row[cf.Name], changed = to, true
					}
				}
			}
		}
	}
	return changed
}

// forgetLinks updates the unit of work's link cache for the rename of model's record from to to: the record under
// its draft name is dropped, and cached records that link it get the new name in place (link fields, x_ links,
// child rows), as RenameRecordLinks did in the database. Nothing else is read again: the computes a hook
// recomputes later in the submit (stock cogs set on a row) read linked records as the submit read them, never a
// commit another transaction made meanwhile, so a value like delivery_note.credit_over_seen keeps meaning "the
// approval policy matched" (card t_48e24a91; audit credit-race 1: dropping a whole referrer model's entries let a
// customer with a link back to deliveries be read again).
func (o *op) forgetLinks(model, from, to string) {
	reg := o.registry()
	for k, out := range o.links {
		client := strings.HasPrefix(k, "client:")
		lm, ln, _ := strings.Cut(strings.TrimPrefix(k, "client:"), "/")
		if lm == model && ln == from {
			delete(o.links, k)
			continue
		}
		rm := reg.Model(lm)
		if rm == nil || !linksTo(reg, rm, model) {
			continue
		}
		if client { // a client filter's view is built per call; none is kept by a submit
			delete(o.links, k)
			continue
		}
		s := o.scope(rm, nil)
		for _, f := range rm.Fields {
			switch {
			case f.Type == "link" && f.To == model:
				if l, ok := out[f.Name].(*cel.Link); ok && l.Name == from {
					out[f.Name] = s.link(model, to)
				}
			case f.Type == "table":
				cm := reg.Model(f.Of)
				if cm == nil {
					continue
				}
				l, _ := out[f.Name].([]any)
				for _, row := range rows(l) {
					for _, cf := range cm.Fields {
						if cf.Type == "link" && cf.To == model && str(row[cf.Name]) == from {
							row[cf.Name] = to
						}
					}
				}
			}
		}
	}
}
