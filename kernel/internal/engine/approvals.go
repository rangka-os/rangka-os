package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Document approvals (contract 0007): a verb whose policy matches is held as pending requests bound to the
// record's snapshot; approving the last one runs the verb as the requester.

// snapshotSkip are record keys left out of a snapshot (contract §3).
var snapshotSkip = map[string]bool{"name": true, "docstatus": true, "status": true, "created_by": true, "created_at": true,
	"modified_at": true, "idx": true, "parent": true, "parent_field": true}

// Snapshot is the SHA-256 (hex) of the canonical JSON of a stored record: the model's fields and x_ fields,
// child rows as lists of their fields, keys sorted (contract §3).
func Snapshot(m *meta.Model, rec store.Record) string {
	v := map[string]any{}
	for _, f := range m.Fields {
		if snapshotSkip[f.Name] {
			continue
		}
		if f.Type == "table" {
			list := []any{}
			for _, row := range rows(rec[f.Name]) {
				list = append(list, snapshotRow(row))
			}
			v[f.Name] = list
			continue
		}
		v[f.Name] = rec[f.Name]
	}
	for k, x := range rec {
		if strings.HasPrefix(k, "x_") {
			v[k] = x
		}
	}
	b, _ := json.Marshal(v) // map keys are sorted
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// snapshotRow keeps a child row's fields (stored rows carry only fields, x_ fields and the system keys).
func snapshotRow(row map[string]any) map[string]any {
	out := map[string]any{}
	for k, x := range row {
		if !snapshotSkip[k] {
			out[k] = x
		}
	}
	return out
}

// CanDecide reports whether a may approve or reject ap now: a pending request, a person, and owner or one of
// the request's approver roles.
func CanDecide(a Actor, ap *store.Approval) bool {
	if ap == nil || ap.State != "pending" || !a.Human {
		return false
	}
	for _, r := range a.Roles {
		if r == "owner" {
			return true
		}
		for _, x := range ap.Approvers {
			if r == x {
				return true
			}
		}
	}
	return false
}

// ListApprovals lists the requests of the caller's organisation.
func (e *Engine) ListApprovals(ctx context.Context, a Actor, f store.ApprovalFilter) ([]store.Approval, int, error) {
	f.Org = a.Org
	return store.ListApprovals(ctx, e.DB, f)
}

// GetApproval loads one request of the caller's organisation.
func (e *Engine) GetApproval(ctx context.Context, a Actor, id string) (*store.Approval, error) {
	ap, err := store.GetApproval(ctx, e.DB, a.Org, id, false)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("approval " + id)
	}
	return ap, err
}

// Decision is the outcome of Approve or Reject.
type Decision struct {
	Approval store.Approval `json:"approval"`
	Record   store.Record   `json:"record,omitempty"` // when the verb ran
	Action   map[string]any `json:"action,omitempty"` // action hook result
	Flow     *FlowResult    `json:"flow,omitempty"`
}

func approvalLabel(ap *store.Approval) string {
	if s := str(ap.Label["id"]); s != "" {
		return s
	}
	if s := str(ap.Label["en"]); s != "" {
		return s
	}
	return ap.Policy
}

// hold creates or finds the requests verb needs on rec. A policy is satisfied by an approved request for the
// same snapshot; a pending one for the same snapshot is reused; otherwise a request is inserted, already
// approved when the actor may decide it (self-approval). It returns the pending and all requests of the call.
func (o *op) hold(m *meta.Model, rec store.Record, snapshot, verb string, args map[string]any) (pending []store.Approval, all []store.Approval, err error) {
	pols := m.ApprovalsFor(verb)
	if len(pols) == 0 {
		return nil, nil, nil
	}
	a := o.actor
	name := str(rec["name"])
	existing, _, err := store.ListApprovals(o.ctx, o.tx, store.ApprovalFilter{Org: a.Org, Model: m.Name, Name: name, Verb: verb, Limit: 500})
	if err != nil {
		return nil, nil, err
	}
	for _, p := range pols {
		if p.When != "" {
			v, err := o.scope(m, rec).eval(p.When)
			if err == nil && !cel.Truthy(v) {
				continue
			} // a when that cannot be evaluated requires approval (fail closed)
		}
		var found *store.Approval
		for i := range existing {
			x := &existing[i]
			if x.Policy != p.Name || x.Snapshot != snapshot {
				continue
			}
			if x.State == "approved" || (x.State == "pending" && found == nil) {
				found = x
			}
		}
		if found != nil {
			all = append(all, *found)
			if found.State == "pending" {
				pending = append(pending, *found)
			}
			continue
		}
		var label map[string]any
		if len(p.Label) > 0 {
			label = map[string]any{}
			for k, v := range p.Label {
				label[k] = v
			}
		}
		approvers := append([]string{}, p.Approvers...)
		roles := append([]string{}, a.Roles...)
		ap := store.Approval{Org: a.Org, Model: m.Name, Name: name, Verb: verb, Policy: p.Name, Label: label, Approvers: approvers,
			State: "pending", Snapshot: snapshot, Args: args, RequestedBy: a.ID,
			Requester: map[string]any{"id": a.ID, "user": a.UserName, "roles": roles}}
		if CanDecide(a, &ap) {
			now := o.e.Now()
			ap.State, ap.DecidedBy, ap.DecidedAt = "approved", a.ID, &now
		}
		if err := store.InsertApproval(o.ctx, o.tx, &ap); err != nil {
			return nil, nil, err
		}
		kind := "approval.requested"
		if ap.State == "approved" {
			kind = "approval.approved"
		}
		if err := o.approvalEvent(&ap, kind, nil); err != nil {
			return nil, nil, err
		}
		all = append(all, ap)
		if ap.State == "pending" {
			pending = append(pending, ap)
		}
	}
	return pending, all, nil
}

// approvalEvent audits a request's change (kind approval.*) and publishes the SSE approval event.
func (o *op) approvalEvent(ap *store.Approval, kind string, extra map[string]any) error {
	if err := store.Notify(o.ctx, o.tx, store.Event{Type: "approval", Org: ap.Org, Model: ap.Model, Name: ap.Name, ID: ap.ID, State: ap.State}); err != nil {
		return err
	}
	data := map[string]any{"id": ap.ID, "policy": ap.Policy, "verb": ap.Verb, "requested_by": ap.RequestedBy}
	for k, v := range extra {
		data[k] = v
	}
	target := ap.Model + "/" + ap.Name
	if ap.Model == "" {
		target = ap.Verb
	}
	return store.Audit(o.ctx, o.tx, ap.Org, o.actor.ID, kind, target, ap.Policy+" ("+ap.Verb+") "+ap.State, data)
}

// voidApprovals voids the pending requests of a record (one verb, or all when verb is empty).
func (o *op) voidApprovals(model, name, verb string) error {
	return o.voidExcept(model, name, verb, "")
}

func (o *op) voidExcept(model, name, verb, exceptID string) error {
	voided, err := store.VoidVerbApprovals(o.ctx, o.tx, o.actor.Org, model, name, verb, exceptID)
	if err != nil {
		return err
	}
	for i := range voided {
		if err := o.approvalEvent(&voided[i], "approval.void", nil); err != nil {
			return err
		}
	}
	return nil
}

var errApprovalVoid = conflict("approval_void", "Dokumen berubah setelah diajukan; ajukan ulang")

// decide loads and locks a request and applies the guards shared by Approve and Reject.
func (o *op) decide(id string) (*store.Approval, error) {
	a := o.actor
	if !a.Human {
		return nil, &StatusError{403, "human_required", "Persetujuan harus dilakukan oleh orang yang masuk lewat aplikasi"}
	}
	ap, err := store.GetApproval(o.ctx, o.tx, a.Org, id, true)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("approval " + id)
	}
	if err != nil {
		return nil, err
	}
	if ap.State == "void" {
		return nil, errApprovalVoid
	}
	if ap.State != "pending" {
		return nil, conflict("not_pending", "Permintaan persetujuan ini sudah diputuskan")
	}
	if !CanDecide(a, ap) {
		return nil, &StatusError{403, "permission_denied", "Kamu tidak termasuk penyetuju " + approvalLabel(ap)}
	}
	return ap, nil
}

// requesterActor rebuilds the person who asked for the verb from a request.
func requesterActor(org string, r map[string]any) Actor {
	a := Actor{Org: org, ID: str(r["id"]), UserName: str(r["user"]), Human: true}
	switch rs := r["roles"].(type) {
	case []any:
		for _, x := range rs {
			a.Roles = append(a.Roles, str(x))
		}
	case []string:
		a.Roles = append(a.Roles, rs...)
	}
	return a
}

// Approve approves a pending request. When it was the last pending request of its (record, verb), the verb
// runs in the same transaction as the requester; a verb error rolls everything back and is returned.
func (e *Engine) Approve(ctx context.Context, a Actor, id string) (*Decision, error) {
	var d *Decision
	var stale *store.Approval
	err := e.run(ctx, a, func(o *op) error {
		ap, err := o.decide(id)
		if err != nil {
			return err
		}
		if ap.Model != "" {
			m, err := e.model(ap.Model)
			if err != nil {
				return err
			}
			rec, err := o.load(m, ap.Name)
			if err != nil && !IsNotFound(err) {
				return err
			}
			if rec == nil || Snapshot(m, rec) != ap.Snapshot {
				stale = ap
				return errApprovalVoid
			}
		}
		now := e.Now()
		if err := store.DecideApproval(o.ctx, o.tx, a.Org, ap.ID, "approved", a.ID, "", now); err != nil {
			return err
		}
		ap.State, ap.DecidedBy, ap.DecidedAt = "approved", a.ID, &now
		if err := o.approvalEvent(ap, "approval.approved", nil); err != nil {
			return err
		}
		d = &Decision{Approval: *ap}
		left, _, err := store.ListApprovals(o.ctx, o.tx, store.ApprovalFilter{Org: a.Org, State: "pending", Model: ap.Model, Name: ap.Name, Verb: ap.Verb, Limit: 1})
		if err != nil {
			return err
		}
		if len(left) > 0 {
			return nil
		}
		return o.runApproved(ap, d)
	})
	if stale != nil && errors.Is(err, errApprovalVoid) {
		// the record changed behind the engine's back: void its requests in a transaction of their own
		if verr := e.run(ctx, a, func(o *op) error { return o.voidApprovals(stale.Model, stale.Name, "") }); verr != nil {
			return nil, verr
		}
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}

// runApproved runs the request's verb as its requester inside o's transaction.
func (o *op) runApproved(ap *store.Approval, d *Decision) error {
	ro := &op{ctx: o.ctx, tx: o.tx, e: o.e, actor: requesterActor(o.actor.Org, ap.Requester), touched: o.touched, links: map[string]map[string]any{}}
	switch {
	case strings.HasPrefix(ap.Verb, "flow:"):
		return &StatusError{501, "not_implemented", "flow approvals are not implemented yet"}
	case ap.Verb == "submit":
		force, _ := ap.Args["force"].(bool)
		res, err := ro.submit(ap.Model, ap.Name, force)
		if err != nil {
			return err
		}
		if !res.Held {
			d.Record = res.Record
		}
	case ap.Verb == "cancel":
		res, err := ro.cancel(ap.Model, ap.Name)
		if err != nil {
			return err
		}
		if !res.Held {
			d.Record = res.Record
		}
	default:
		args, _ := ap.Args["args"].(map[string]any)
		out, err := ro.runAction(ap.Model, ap.Name, ap.Verb, args, ActionOpts{})
		if err != nil {
			return err
		}
		switch {
		case out.Held:
		case out.Queued:
			d.Action = map[string]any{"queued": true, "job_id": out.JobID}
		default:
			d.Action = out.Result
		}
	}
	return nil
}

// Reject rejects a pending request with a reason; the other pending requests of the same (record, verb) become
// void. The record is left as it is.
func (e *Engine) Reject(ctx context.Context, a Actor, id, reason string) (*Decision, error) {
	var d *Decision
	err := e.run(ctx, a, func(o *op) error {
		ap, err := o.decide(id)
		if err != nil {
			return err
		}
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return badRequest("Alasan penolakan wajib diisi")
		}
		now := e.Now()
		if err := store.DecideApproval(o.ctx, o.tx, a.Org, ap.ID, "rejected", a.ID, reason, now); err != nil {
			return err
		}
		ap.State, ap.DecidedBy, ap.DecidedAt, ap.Reason = "rejected", a.ID, &now, reason
		if err := o.approvalEvent(ap, "approval.rejected", map[string]any{"reason": reason}); err != nil {
			return err
		}
		if ap.Model != "" {
			if err := o.voidExcept(ap.Model, ap.Name, ap.Verb, ap.ID); err != nil {
				return err
			}
		}
		d = &Decision{Approval: *ap}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return d, nil
}
