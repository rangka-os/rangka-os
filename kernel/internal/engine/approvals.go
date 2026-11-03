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

// rowSkip are child row keys left out of a snapshot (contract §3).
var rowSkip = map[string]bool{"name": true, "idx": true, "parent": true, "parent_field": true}

// Snapshot is the SHA-256 (hex) of the canonical JSON of a stored record: every declared field of the model and
// the x_ fields, child rows as lists of their fields, keys sorted (contract §3). System keys (name, docstatus,
// the derived status, created_*, modified_at) are not declared fields and so stay out; a real field is never
// skipped.
func Snapshot(m *meta.Model, rec store.Record) string {
	v := map[string]any{}
	for _, f := range m.Fields {
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
	return hashJSON(v)
}

// hashJSON is the SHA-256 (hex) of v's JSON (map keys sorted). Stored values always marshal; a failure is a bug.
func hashJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic("engine: approval snapshot is not JSON: " + err.Error())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// argsKey is the canonical JSON of a request's args; an approval only matches a call with equal args.
func argsKey(args map[string]any) string {
	if args == nil {
		args = map[string]any{}
	}
	b, err := json.Marshal(args)
	if err != nil {
		panic("engine: approval args are not JSON: " + err.Error())
	}
	return string(b)
}

// snapshotRow keeps a child row's fields (stored rows carry only fields, x_ fields and the system keys).
func snapshotRow(row map[string]any) map[string]any {
	out := map[string]any{}
	for k, x := range row {
		if !rowSkip[k] {
			out[k] = x
		}
	}
	return out
}

// cloneRecord copies a record and its child rows, so the stored values survive prepare.
func cloneRecord(rec store.Record) store.Record {
	out := store.Record{}
	for k, v := range rec {
		if l, ok := v.([]any); ok {
			c := make([]any, len(l))
			for i, x := range l {
				if row, ok := x.(map[string]any); ok {
					nr := map[string]any{}
					for rk, rv := range row {
						nr[rk] = rv
					}
					c[i] = nr
				} else {
					c[i] = x
				}
			}
			v = c
		}
		out[k] = v
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

// hold creates or finds the requests verb needs on rec (the stored record). Requests are bound to the
// snapshot and the args. Inside Approve's verb run an approved request satisfies its policy. On a direct call
// approved requests are single-use: they are reused only while another matching policy still waits (so an
// approved policy is not asked again); when they alone would let the verb run, fresh requests are made. A
// pending request with the same snapshot and args is reused; otherwise a request is inserted, already approved
// when the actor may decide it (self-approval). It returns the pending and all requests of the call.
func (o *op) hold(m *meta.Model, rec store.Record, snapshot, verb string, args map[string]any) (pending []store.Approval, all []store.Approval, err error) {
	var pols []meta.Approval
	for _, p := range m.ApprovalsFor(verb) {
		if p.When != "" {
			v, err := o.scope(m, rec).eval(p.When)
			if err == nil && !cel.Truthy(v) {
				continue
			} // a when that cannot be evaluated requires approval (fail closed)
		}
		pols = append(pols, p)
	}
	if len(pols) == 0 {
		return nil, nil, nil
	}
	a := o.actor
	name := str(rec["name"])
	if err := o.lockRecord(m.Name, name); err != nil {
		return nil, nil, err
	}
	key := argsKey(args)
	existing, _, err := store.ListApprovals(o.ctx, o.tx, store.ApprovalFilter{Org: a.Org, Model: m.Name, Name: name, Verb: verb, Limit: 500})
	if err != nil {
		return nil, nil, err
	}
	approved := map[string]*store.Approval{}
	waiting := map[string]*store.Approval{}
	for i := range existing {
		x := &existing[i]
		if x.Snapshot != snapshot || argsKey(x.Args) != key {
			continue
		}
		switch x.State {
		case "approved":
			if approved[x.Policy] == nil {
				approved[x.Policy] = x
			}
		case "pending":
			if waiting[x.Policy] == nil {
				waiting[x.Policy] = x
			}
		}
	}
	useApproved := o.approving
	if !useApproved {
		for _, p := range pols {
			if approved[p.Name] == nil {
				useApproved = true // some policy still waits: do not ask approved ones again
				break
			}
		}
	}
	for _, p := range pols {
		if x := approved[p.Name]; useApproved && x != nil {
			all = append(all, *x)
			continue
		}
		if x := waiting[p.Name]; x != nil {
			all = append(all, *x)
			pending = append(pending, *x)
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
			Requester: map[string]any{"id": a.ID, "user": a.UserName, "roles": roles, "human": a.Human}}
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

// lockRecord serialises approval work on one record until the transaction ends (hold, approve, reject).
// Transactions are READ COMMITTED: without it two approvers could each see the other's request pending, or a
// double submit could insert two requests for one policy. The lock is reentrant within a transaction.
func (o *op) lockRecord(model, name string) error {
	_, err := o.tx.Exec(o.ctx, `select pg_advisory_xact_lock(hashtext($1))`, o.actor.Org+"/"+model+"/"+name)
	return err
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
	ap, err := store.GetApproval(o.ctx, o.tx, a.Org, id, false)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("approval " + id)
	}
	if err != nil {
		return nil, err
	}
	if !isFlowApproval(ap) { // the record lock before the row lock, so decisions on one record queue
		if err := o.lockRecord(ap.Model, ap.Name); err != nil {
			return nil, err
		}
	}
	if ap, err = store.GetApproval(o.ctx, o.tx, a.Org, id, true); err != nil {
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
	if h, ok := r["human"].(bool); ok { // whether the requester was a person (rows without the key: a person)
		a.Human = h
	}
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
		flow := isFlowApproval(ap)
		if !flow {
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
		if flow { // a flow request stands alone: no snapshot of a record, no sibling requests
			return o.runApproved(ap, d)
		}
		left, _, err := store.ListApprovals(o.ctx, o.tx, store.ApprovalFilter{Org: a.Org, State: "pending", Model: ap.Model, Name: ap.Name, Verb: ap.Verb, Limit: 500})
		if err != nil {
			return err
		}
		waiting, key := false, argsKey(ap.Args)
		for i := range left {
			x := &left[i]
			if x.Snapshot != ap.Snapshot {
				// an older request for another state of the record can never run: void it
				v, err := store.VoidApproval(o.ctx, o.tx, a.Org, x.ID)
				if err != nil {
					return err
				}
				if err := o.approvalEvent(v, "approval.void", nil); err != nil {
					return err
				}
				continue
			}
			if argsKey(x.Args) == key { // a request with other args is another call
				waiting = true
			}
		}
		if waiting {
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
	ro := &op{ctx: o.ctx, tx: o.tx, e: o.e, actor: requesterActor(o.actor.Org, ap.Requester), touched: o.touched, links: map[string]map[string]any{},
		approving: true}
	switch {
	case isFlowApproval(ap):
		return ro.resumeFlow(ap, d)
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

func isFlowApproval(ap *store.Approval) bool { return strings.HasPrefix(ap.Verb, "flow:") }

// resumeFlow runs the remaining top-level steps of an approved flow request as o's actor (the flow's original
// actor), with the saved variables and the step's as: set to the decision.
func (o *op) resumeFlow(ap *store.Approval, d *Decision) error {
	name := strings.TrimPrefix(ap.Verb, "flow:")
	f := o.e.Reg().Flows[name]
	if f == nil || !o.e.Reg().IsInstalled(f.App) {
		return notFound("flow " + name)
	}
	vars := map[string]any{}
	for k, v := range meta.AsMap(ap.Args["vars"]) {
		vars[k] = v
	}
	if as := str(ap.Args["as"]); as != "" {
		vars[as] = map[string]any{"state": "approved", "by": ap.DecidedBy, "at": *ap.DecidedAt}
	}
	out := newFlowResult(name)
	if err := o.e.flowIn(o, f, meta.AsMap(ap.Args["event"]), int(toInt(ap.Args["resume"])), vars, out); err != nil {
		return err
	}
	d.Flow = out
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
		if isFlowApproval(ap) {
			f := strings.TrimPrefix(ap.Verb, "flow:")
			if err := store.Audit(o.ctx, o.tx, a.Org, a.ID, "flow.rejected", "flow/"+f, approvalLabel(ap)+": "+reason, map[string]any{"approval": ap.ID, "reason": reason}); err != nil {
				return err
			}
		} else if err := o.voidExcept(ap.Model, ap.Name, ap.Verb, ap.ID); err != nil {
			return err
		}
		d = &Decision{Approval: *ap}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return d, nil
}
