package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
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

// ListApprovals lists the requests of the caller's organisation that the caller may see: requests on models
// the caller may read (model-level, a conditional read grant counts, as in /meta) and flow requests when the
// caller is owner or holds one of their approver roles. Kernel actors and owners see everything.
func (e *Engine) ListApprovals(ctx context.Context, a Actor, f store.ApprovalFilter) ([]store.Approval, int, error) {
	switch f.State {
	case "", "pending", "approved", "rejected", "void":
	default:
		return nil, 0, badRequest("state harus pending, approved, rejected atau void")
	}
	f.Org = a.Org
	f.Restrict, f.Models, f.FlowRoles = false, nil, nil
	if !unrestricted(a) {
		f.Restrict, f.Models, f.FlowRoles = true, e.readableModels(a), append([]string{}, a.Roles...)
	}
	if !e.approvalsNeedRecords(a, f.Models) {
		return store.ListApprovals(ctx, e.DB, f)
	}
	// Some requests need their record checked (a conditional read grant, or a row scope that narrows the
	// model): read every match, keep the visible ones, then page, so total counts visible requests only.
	limit, offset := f.Limit, f.Offset
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	var all []store.Approval
	for f.Offset = 0; ; f.Offset += 500 {
		f.Limit = 500
		page, total, err := store.ListApprovals(ctx, e.DB, f)
		if err != nil {
			return nil, 0, err
		}
		for i := range page {
			if e.approvalVisible(ctx, a, &page[i]) {
				all = append(all, page[i])
			}
		}
		if len(page) < 500 || f.Offset+len(page) >= total {
			break
		}
	}
	total := len(all)
	if offset >= len(all) {
		return []store.Approval{}, total, nil
	}
	all = all[offset:]
	if len(all) > limit {
		all = all[:limit]
	}
	return all, total, nil
}

// approvalsNeedRecords reports whether listing requests for a needs each request's record: a has a row scope
// (which also narrows flow requests, see flowScope), or only a conditional (if:) read grant on one of models.
func (e *Engine) approvalsNeedRecords(a Actor, models []string) bool {
	if e.RowScope(a) != nil {
		return true
	}
	if unrestricted(a) {
		return false
	}
	for _, name := range models {
		if pm := e.permModel(name); pm != nil && !e.decide(a, pm, "read").all {
			return true
		}
	}
	return false
}

// approvalVisible reports whether a may see a listed request: a flow request already passed the SQL filter
// (approver roles) and must pass flowScope; a request on a record needs read on the record and the record inside
// a's row scope (GetApproval's rule).
func (e *Engine) approvalVisible(ctx context.Context, a Actor, ap *store.Approval) bool {
	if isFlowApproval(ap) {
		return e.flowScope(ctx, e.DB, a, ap, "read") == nil
	}
	_, err := e.Get(ctx, a, ap.Model, ap.Name)
	return err == nil || IsNotFound(err)
}

// readableModels lists the models a may read at model level (a child model follows its parent).
func (e *Engine) readableModels(a Actor) []string {
	out := []string{}
	for name := range e.Reg().Models {
		if pm := e.permModel(name); pm != nil && e.decide(a, pm, "read").maybe() {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// GetApproval loads one request of the caller's organisation. A request on a record needs read on that record
// (403 permission_denied); a flow request is visible to owner and its approver roles only (404 otherwise).
func (e *Engine) GetApproval(ctx context.Context, a Actor, id string) (*store.Approval, error) {
	ap, err := store.GetApproval(ctx, e.DB, a.Org, id, false)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("approval " + id)
	}
	if err != nil {
		return nil, err
	}
	if unrestricted(a) {
		return ap, nil
	}
	if isFlowApproval(ap) {
		if !hasAnyRole(a, ap.Approvers) {
			return nil, notFound("approval " + id)
		}
		if err := e.flowScope(ctx, e.DB, a, ap, "read"); err != nil {
			return nil, err
		}
		return ap, nil
	}
	if _, err = e.Get(ctx, a, ap.Model, ap.Name); IsNotFound(err) { // Get checks read on the stored record
		err = e.Authorize(ctx, a, ap.Model, "read", nil)
	}
	if err != nil {
		return nil, err
	}
	return ap, nil
}

func hasAnyRole(a Actor, roles []string) bool {
	for _, r := range roles {
		if hasRole(a, r) {
			return true
		}
	}
	return false
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
// snapshot and the args, and approved requests are single-use: once the verb has run with them they are marked
// used and never satisfy a policy again. Inside Approve's verb run an unused approved request satisfies its
// policy. On a direct call unused approved requests are reused only while another matching policy still waits
// (so an approved policy is not asked again); when they alone would let the verb run, fresh requests are made.
// A pending request with the same snapshot and args is reused; otherwise a request is inserted, already approved
// when the actor may decide it (self-approval). When nothing waits, the verb runs now: every unused approved
// request of the call is marked used. It returns the pending and all requests of the call.
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
	var unused []string // unused approved requests of this call, marked used when the verb runs
	for i := range existing {
		x := &existing[i]
		if x.Snapshot != snapshot || argsKey(x.Args) != key {
			continue
		}
		switch x.State {
		case "approved":
			if x.UsedAt != nil {
				continue // single-use: it already let the verb run once
			}
			unused = append(unused, x.ID)
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
			Requester: map[string]any{"id": a.ID, "user": a.UserName, "roles": roles, "human": a.Human, "system": a.System}}
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
		} else {
			unused = append(unused, ap.ID)
		}
	}
	if len(pending) == 0 { // the verb runs in this transaction; a failure rolls the mark back
		if err := store.MarkApprovalsUsed(o.ctx, o.tx, a.Org, unused, o.e.Now()); err != nil {
			return nil, nil, err
		}
		for i := range all {
			if all[i].UsedAt == nil {
				now := o.e.Now()
				all[i].UsedAt = &now
			}
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

// decide loads and locks a request and applies the guards shared by Approve and Reject (action is approve or
// reject): a person, one of the policy's approver roles, read on the record (S1) and the record inside the
// approver's row scope (contract 0010 §5).
func (o *op) decide(id, action string) (*store.Approval, error) {
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
	if !CanDecide(a, ap) { // approving is governed by the policy's approvers, not by model permissions
		return nil, o.e.Deny(ap.Model, "approve")
	}
	if !isFlowApproval(ap) { // but the approver must be able to read what they approve (S1)
		if err := o.authorizeRead(ap.Model, ap.Name); err != nil {
			return nil, err
		}
		if err := o.guardApproval(ap, action); err != nil {
			return nil, err
		}
	} else if err := o.e.flowScope(o.ctx, o.tx, a, ap, action); err != nil {
		return nil, err
	}
	return ap, nil
}

// flowRecord is the record a flow request was started on: the request's model and name, else the model and
// name of the flow's event. nil when the request names no record of an installed model.
func (e *Engine) flowRecord(ap *store.Approval) (*meta.Model, string) {
	model, name := ap.Model, ap.Name
	if model == "" || name == "" {
		ev := meta.AsMap(ap.Args["event"])
		model, name = str(ev["model"]), str(ev["name"])
	}
	if model == "" || name == "" {
		return nil, ""
	}
	return e.Reg().Model(model), name
}

// flowScope applies a's row scope to a flow request (contract 0010 §5, flow steps themselves are not scoped):
// nil without a row scope. A request that names no record is hidden from a scoped caller (404); one whose
// record is outside the scope is Denied(model, action); a record that is gone is not checked.
func (e *Engine) flowScope(ctx context.Context, q pg.Querier, a Actor, ap *store.Approval, action string) error {
	if e.RowScope(a) == nil {
		return nil
	}
	m, name := e.flowRecord(ap)
	if m == nil {
		return notFound("approval " + ap.ID)
	}
	rec, err := store.Get(ctx, q, e.Reg(), a.Org, m, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return e.Guard(ctx, a, m.Name, rec, action)
}

// authorizeRead checks read on a request's record as o's actor: on the stored record, or model-level when the
// record is gone (Approve then voids the request).
func (o *op) authorizeRead(model, name string) error {
	m, err := o.e.model(model)
	if err != nil {
		return err
	}
	rec, err := o.load(m, name)
	if err != nil && !IsNotFound(err) {
		return err
	}
	if rec != nil {
		rec = o.e.Output(m, copyRecord(rec))
	}
	return o.authorize(m, "read", rec)
}

// guardApproval refuses a request whose record is outside o's actor's row scope: Denied(record model, action).
// A record that is gone is not checked (Approve then voids the request).
func (o *op) guardApproval(ap *store.Approval, action string) error {
	if o.e.RowScope(o.actor) == nil {
		return nil
	}
	m, err := o.e.model(ap.Model)
	if err != nil {
		return err
	}
	rec, err := o.load(m, ap.Name)
	if IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return o.e.Guard(o.ctx, o.actor, m.Name, rec, action)
}

// requesterActor rebuilds the person who asked for the verb from a request.
func requesterActor(org string, r map[string]any) Actor {
	a := Actor{Org: org, ID: str(r["id"]), UserName: str(r["user"]), Human: true}
	if h, ok := r["human"].(bool); ok { // whether the requester was a person (rows without the key: a person)
		a.Human = h
	}
	a.System, _ = r["system"].(bool) // a kernel-started flow keeps bypassing permission checks
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
		d, stale = nil, nil // per attempt (contract 0025)
		ap, err := o.decide(id, "approve")
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
			_, err := o.runApproved(ap, d)
			return err
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
		ran, err := o.runApproved(ap, d)
		if err != nil || !ran {
			return err
		}
		if n := str(d.Record["name"]); n != "" { // submit renamed the record (and its requests)
			ap.Name, d.Approval.Name = n, n
		}
		// the verb ran: requests of the same verb with other args can no longer run on this record state
		return o.voidApprovals(ap.Model, ap.Name, ap.Verb)
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
	if d != nil && d.Record != nil { // contract 0027: the approver gets the record as they may see it
		e.Redact(a, e.Reg().Model(d.Approval.Model), d.Record)
	}
	return d, nil
}

// runApproved runs the request's verb as its requester inside o's transaction. ran is false when the verb is
// held again (a policy that newly matches waits).
func (o *op) runApproved(ap *store.Approval, d *Decision) (ran bool, err error) {
	ro := &op{ctx: o.ctx, tx: o.tx, e: o.e, actor: requesterActor(o.actor.Org, ap.Requester), touched: o.touched, links: map[string]map[string]any{},
		approving: true}
	switch {
	case isFlowApproval(ap):
		return true, ro.resumeFlow(ap, d)
	case ap.Verb == "submit":
		force, _ := ap.Args["force"].(bool)
		res, err := ro.submit(ap.Model, ap.Name, force)
		if err != nil {
			return false, err
		}
		if res.Held {
			return false, nil
		}
		d.Record = res.Record
	case ap.Verb == "cancel":
		res, err := ro.cancel(ap.Model, ap.Name)
		if err != nil {
			return false, err
		}
		if res.Held {
			return false, nil
		}
		d.Record = res.Record
	default:
		args, _ := ap.Args["args"].(map[string]any)
		out, err := ro.runAction(ap.Model, ap.Name, ap.Verb, args, ActionOpts{})
		if err != nil {
			return false, err
		}
		switch {
		case out.Held:
			return false, nil
		case out.Queued:
			d.Action = map[string]any{"queued": true, "job_id": out.JobID}
		default:
			d.Action = out.Result
		}
	}
	return true, nil
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
	// a flow a caller started by hand stays manual after its approval (contract 0026)
	prev := o.manualRun
	o.manualRun = ap.Args["manual"] == true
	err := o.e.flowIn(o, f, meta.AsMap(ap.Args["event"]), int(toInt(ap.Args["resume"])), vars, out)
	o.manualRun = prev
	if err != nil {
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
		ap, err := o.decide(id, "reject")
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

// summaryAmountFields are the fields, in order, whose value is a request's amount in its summary.
var summaryAmountFields = []string{"grand_total", "total", "net_total", "total_amount", "amount"}

// ApprovalSummary is what an approver needs to decide from the card (mobile P7, P8): the record's title (which
// follows its title link, so an invoice shows its customer and an order its supplier), its amount and currency.
// It reads the record as a (read grant, row scope, hidden fields), so it shows nothing a could not open; nil for
// flow requests and records a cannot read.
func (e *Engine) ApprovalSummary(ctx context.Context, a Actor, ap *store.Approval) map[string]any {
	if ap == nil || ap.Model == "" || ap.Name == "" || strings.HasPrefix(ap.Verb, "flow:") {
		return nil
	}
	m := e.Reg().Model(ap.Model)
	if m == nil {
		return nil
	}
	rec, err := e.Get(ctx, a, ap.Model, ap.Name)
	if err != nil {
		return nil
	}
	out := map[string]any{}
	if t := e.Title(ctx, a, m.Name, rec, 0); t != "" && t != ap.Name {
		out["title"] = t
	}
	for _, fn := range summaryAmountFields {
		f := m.Field(fn)
		if f == nil || (f.Type != "currency" && f.Type != "money") {
			continue // only money: an int "amount" may be a count of units
		}
		v, ok := rec[fn]
		if !ok || v == nil {
			continue
		}
		// the currency of the amount itself: a money field names its currency field; a currency field is rupiah
		// (a foreign-currency document keeps its grand_total in IDR, audit 2026-11-07)
		cur := "IDR"
		if f.Type == "money" {
			if cf := f.CurrencyField(); cf != "" {
				if c, ok := rec[cf].(string); ok && c != "" {
					cur = c
				}
			}
		}
		out["amount"], out["amount_label"], out["currency"] = v, f.Label.Text(), cur
		break
	}
	return out
}
