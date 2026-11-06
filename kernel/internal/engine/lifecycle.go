package engine

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// SystemKeys are record keys the client cannot set.
var SystemKeys = map[string]bool{"name": true, "docstatus": true, "amended_from": true, "created_by": true, "created_at": true, "modified_at": true,
	"status": true, "idx": true, "parent": true, "parent_field": true}

// normalize keeps known fields (and x_ company fields) of input, coerced to their types. Missing fields stay
// missing so defaults can apply.
//
// client marks a write from outside the kernel (REST create and update, imports): read_only fields are not
// writable there, on the document and on its child rows. A client-sent read_only value is dropped, so a new
// record or row gets its default and a stored one keeps its value: a row keeps the read_only values of the
// stored row of the same name (base), and a row whose name is not among the stored rows is a new row (its
// name is dropped, so defaults apply). Hooks, automations and flows write read_only fields (client false).
func (e *Engine) normalize(m *meta.Model, input map[string]any, base store.Record, client bool) (store.Record, []Issue) {
	rec := store.Record{}
	for k, v := range base {
		rec[k] = v
	}
	var issues []Issue
	for k, v := range input {
		f := m.Field(k)
		if f == nil {
			if strings.HasPrefix(k, "x_") {
				rec[k] = v
			}
			continue
		}
		if SystemKeys[k] && f == nil {
			continue
		}
		if f.MoneyOf() != "" {
			continue // IDR twins are computed by the kernel; a client-sent value is ignored
		}
		if client && f.ReadOnly {
			continue // the stored value (base) or the default stays
		}
		if f.Type == "table" {
			cm := e.Reg().Models[f.Of]
			if cm == nil {
				continue
			}
			stored := map[string]map[string]any{}
			for _, sr := range rows(base[k]) {
				if n := str(sr["name"]); n != "" {
					stored[n] = sr
				}
			}
			var out []any
			list, _ := v.([]any)
			if v != nil && list == nil {
				issues = append(issues, Issue{Field: k, Code: "bad_value", Message: f.Label.Text() + " harus berupa daftar baris"})
				continue
			}
			for _, rv := range list {
				row, _ := rv.(map[string]any)
				if row == nil {
					continue
				}
				nr, is := e.normalize(cm, row, nil, client)
				if n := str(row["name"]); n != "" {
					if sr, ok := stored[n]; client && ok {
						for _, cf := range cm.Fields {
							if cf.ReadOnly && cf.Type != "table" {
								if v, ok := sr[cf.Name]; ok {
									nr[cf.Name] = v
								}
							}
						}
						nr["name"] = n
					} else if !client {
						nr["name"] = n
					}
				}
				issues = append(issues, is...)
				out = append(out, map[string]any(nr))
			}
			if out == nil {
				out = []any{}
			}
			rec[k] = out
			continue
		}
		c, err := store.Coerce(f, v)
		if err != nil {
			issues = append(issues, Issue{Field: k, Code: "bad_value", Message: err.Error()})
			continue
		}
		rec[k] = c
	}
	return rec, issues
}

// prepare applies defaults (newOnly: only for a new record or new child rows), fills zero values and computes.
func (o *op) prepare(m *meta.Model, rec store.Record, isNew bool) {
	s := o.scope(m, rec)
	if isNew {
		s.applyDefaults()
	}
	for _, f := range m.Fields {
		if f.Type != "table" {
			continue
		}
		cm := o.e.Reg().Models[f.Of]
		if cm == nil {
			continue
		}
		for _, row := range rows(rec[f.Name]) {
			cs := o.scope(cm, row)
			cs.parent = s
			if isNew || str(row["name"]) == "" {
				cs.applyDefaults()
			}
			fill(cm, row)
		}
	}
	fill(m, rec)
	s.compute()
	o.settleMoney(m, rec)
}

// check runs rules (on save, or on submit = submit + save rules), required fields, min_rows (submit; a
// client save checks it in create and update), link targets and select options.
func (o *op) check(m *meta.Model, rec store.Record, on string) (errs, warns []Issue, err error) {
	s := o.scope(m, rec)
	var req []Issue
	for _, f := range m.Fields {
		v := rec[f.Name]
		if f.Required && missing(v) {
			lbl := f.Label.Text()
			if lbl == "" {
				lbl = f.Name
			}
			req = append(req, Issue{Field: f.Name, Code: "required", Message: lbl + " wajib diisi"})
		}
	}
	if on == "submit" {
		errs = append(errs, minRows(m, rec, nil)...)
	}
	errs = append(req, errs...)
	for _, r := range m.Rules {
		if r.On != on && !(on == "submit" && r.On == "save") {
			continue
		}
		v, e := s.eval(r.Check)
		if e != nil {
			continue // a rule that cannot be evaluated does not block (prototype)
		}
		if !cel.Truthy(v) {
			is := Issue{Rule: r.Name, Code: "rule", Message: r.Message.Text()}
			if r.Level == "warn" {
				warns = append(warns, is)
			} else {
				errs = append(errs, is)
			}
		}
	}
	more, err := o.checkValues(m, rec, "")
	errs = append(errs, more...)
	return append(errs, s.checkMoney("", "", "", map[string]bool{})...), warns, err
}

// minRows reports the table fields of rec with fewer rows than their min_rows. only limits the check to
// some table fields (nil: all of them).
func minRows(m *meta.Model, rec store.Record, only func(field string) bool) []Issue {
	var errs []Issue
	for _, f := range m.Fields {
		if f.Type != "table" || f.MinRows <= 0 || (only != nil && !only(f.Name)) {
			continue
		}
		if len(rows(rec[f.Name])) < f.MinRows {
			errs = append(errs, Issue{Field: f.Name, Code: "min_rows", Message: fmt.Sprintf("Tambahkan minimal %d baris %s", f.MinRows, strings.ToLower(labelOr(f)))})
		}
	}
	return errs
}

func (o *op) checkValues(m *meta.Model, rec store.Record, prefix string) ([]Issue, error) {
	var errs []Issue
	for _, f := range m.Fields {
		v := str(rec[f.Name])
		switch f.Type {
		case "link":
			if v == "" {
				continue
			}
			tm := o.e.Reg().Model(f.To)
			if tm == nil {
				continue
			}
			ok, err := store.Exists(o.ctx, o.tx, o.actor.Org, tm.Name, v)
			if err != nil {
				return nil, err
			}
			if !ok {
				errs = append(errs, Issue{Field: prefix + f.Name, Code: "link_not_found", Message: fmt.Sprintf("%s: %s %s tidak ditemukan", labelOr(f), tm.Label.Text(), v)})
			}
		case "select":
			if v != "" && f.Option(v) == nil {
				errs = append(errs, Issue{Field: prefix + f.Name, Code: "bad_option", Message: fmt.Sprintf("%s: pilihan %q tidak ada", labelOr(f), v)})
			}
		case "table":
			cm := o.e.Reg().Models[f.Of]
			if cm == nil {
				continue
			}
			for i, row := range rows(rec[f.Name]) {
				var req []Issue
				for _, cf := range cm.Fields {
					if cf.Required && missing(row[cf.Name]) {
						req = append(req, Issue{Field: fmt.Sprintf("%s%s.%d.%s", prefix, f.Name, i, cf.Name), Code: "required",
							Message: fmt.Sprintf("%s baris %d: %s wajib diisi", labelOr(f), i+1, labelOr(cf))})
					}
				}
				errs = append(errs, req...)
				more, err := o.checkValues(cm, row, fmt.Sprintf("%s%s.%d.", prefix, f.Name, i))
				if err != nil {
					return nil, err
				}
				errs = append(errs, more...)
			}
		}
	}
	return errs, nil
}

func labelOr(f *meta.Field) string {
	if l := f.Label.Text(); l != "" {
		return l
	}
	return f.Name
}

var uniqRe = regexp.MustCompile(`^r_[a-z0-9_]+?_([a-z0-9_]+)_uniq$`)

// dbIssue turns a unique violation into a validation error.
func dbIssue(m *meta.Model, err error) error {
	var pe *pg.Error
	if errors.As(err, &pe) && pe.Code == "23505" {
		field := ""
		if mm := uniqRe.FindStringSubmatch(pe.Constraint); mm != nil {
			field = strings.TrimPrefix(pe.Constraint, store.TableName(m.Name)+"_")
			field = strings.TrimSuffix(field, "_uniq")
		}
		msg := "Nilai ini sudah dipakai"
		if f := m.Field(field); f != nil {
			msg = labelOr(f) + " sudah dipakai oleh data lain"
		}
		if pe.Constraint == store.TableName(m.Name)+"_pkey" {
			field, msg = "name", "Nama ini sudah dipakai"
		}
		return &ValidationError{Errors: []Issue{{Field: field, Code: "unique", Message: msg}}, Warnings: []Issue{}}
	}
	return err
}

func validation(errs, warns []Issue) *ValidationError {
	if errs == nil {
		errs = []Issue{}
	}
	if warns == nil {
		warns = []Issue{}
	}
	return &ValidationError{Errors: errs, Warnings: warns}
}

// Output decorates a record for clients: derived status for submittable models.
func (e *Engine) Output(m *meta.Model, rec store.Record) store.Record {
	if st := Status(m, rec, e.Today()); st != "" {
		rec["status"] = st
	}
	return rec
}

// CreateOpts tune Create.
type CreateOpts struct {
	KeepName  bool // use input["name"] (seeding, imports)
	Docstatus int64
	authorize bool // check create on the new record (public entry points; hooks run inside an allowed op)
	Guard     bool // check the new record against the actor's row scope (public Create, not automations)
	client    bool // a client write (public Create, imports): read_only fields are dropped, min_rows is checked
}

// Result of a write: the record and any warnings. Held is set when the verb waits for approval (0007);
// Approvals then lists every request of the call.
type Result struct {
	Record    store.Record
	Warnings  []Issue
	Held      bool             `json:"-"`
	Approvals []store.Approval `json:"-"`
}

// appendOnly refuses a write to a ledger row (contract 0026): it is never edited or deleted, by anyone, and only
// flows, hooks and seed create it; the correction is a reversing row written when the source document is cancelled.
func appendOnly(m *meta.Model) error {
	return conflict("append_only", "Baris "+strings.ToLower(m.Label.Text())+" dicatat otomatis dari dokumen sumbernya dan tidak bisa dibuat, diubah, atau dihapus manual. Untuk koreksi, batalkan dokumen sumbernya.")
}

// Create saves a new record: defaults, compute, save rules, name (DRAFT-xxxxx for submittable models).
// The actor needs create, decided on the new record after defaults.
func (e *Engine) Create(ctx context.Context, a Actor, model string, input map[string]any) (*Result, error) {
	var res *Result
	err := e.run(ctx, a, func(o *op) error {
		var err error
		res, err = o.create(model, e.dropHidden(a, e.Reg().Model(model), input), CreateOpts{authorize: true, Guard: true, client: true})
		return err
	})
	return e.redactResult(a, model, res), err
}

func (o *op) create(model string, input map[string]any, opts CreateOpts) (*Result, error) {
	m, err := o.e.model(model)
	if err != nil {
		return nil, err
	}
	if m.IsChild {
		return nil, badRequest(model + " rows are saved through their parent document")
	}
	if m.AppendOnly && (opts.client || o.manualRun) {
		return nil, appendOnly(m)
	}
	rec, issues := o.e.normalize(m, input, nil, opts.client)
	if len(issues) > 0 {
		return nil, validation(issues, nil)
	}
	o.prepare(m, rec, true)
	if opts.authorize {
		if err := o.authorize(m, "create", rec); err != nil {
			return nil, err
		}
	}
	errs, warns, err := o.check(m, rec, "save")
	if err != nil {
		return nil, err
	}
	if opts.client { // a client cannot save a document with fewer rows than min_rows; a hook's draft may start empty
		errs = append(errs, minRows(m, rec, nil)...)
	}
	if len(errs) > 0 {
		return nil, validation(errs, warns)
	}
	if opts.Guard {
		if err := o.e.Guard(o.ctx, o.actor, m.Name, rec, "create"); err != nil {
			return nil, err
		}
	}
	rec["created_by"] = o.actor.ID
	switch {
	case opts.KeepName && str(input["name"]) != "":
		rec["name"] = str(input["name"])
	case m.IsSubmittable:
		rec["name"] = "DRAFT-" + store.RandomName(5)
	default:
		n, err := o.nextName(m)
		if err != nil {
			return nil, err
		}
		rec["name"] = n
	}
	if m.IsSubmittable {
		rec["docstatus"] = opts.Docstatus
		if opts.KeepName {
			rec["amended_from"] = str(input["amended_from"])
		}
	}
	if err := store.Insert(o.ctx, o.tx, o.e.Reg(), o.actor.Org, m, rec); err != nil {
		return nil, dbIssue(m, err)
	}
	if err := o.event(m, rec, "created"); err != nil {
		return nil, err
	}
	if err := o.automations(m, nil, rec, "created"); err != nil {
		return nil, err
	}
	if ran, err := o.recordFlows(m, nil, rec, "created"); err != nil {
		return nil, err
	} else if ran { // a flow may have written the record: answer with what is stored now
		if rec, err = o.load(m, str(rec["name"])); err != nil {
			return nil, err
		}
	}
	return &Result{Record: o.e.Output(m, rec), Warnings: warns}, nil
}

// Get loads a record the actor may read.
func (e *Engine) Get(ctx context.Context, a Actor, model, name string) (store.Record, error) {
	m, err := e.model(model)
	if err != nil {
		return nil, err
	}
	rec, err := store.Get(ctx, e.DB, e.Reg(), a.Org, m, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound(model + " " + name)
	}
	if err != nil {
		return nil, err
	}
	e.refreshClock(ctx, a, m, rec, true, nil)
	rec = e.Output(m, rec)
	if err := e.authorize(ctx, e.DB, nil, a, m.Name, "read", rec); err != nil {
		return nil, err
	}
	if err := e.Guard(ctx, a, m.Name, rec, "read"); err != nil {
		return nil, err
	}
	return e.Redact(a, m, rec), nil
}

// lockLoad loads a record with its row locked until the transaction ends (select … for update). Submit,
// cancel, update and actions use it, so a hook writing the same row in another transaction (an allocation
// raising paid_amount) is either seen or waits; the full-record write that follows cannot lose it.
//
// The record's advisory lock (approvals' lockRecord) is taken first, so every path locks in the same order,
// advisory lock then row: a re-submit of a held document racing its Approve (which locks the record, then the
// approval, then the row) queues instead of deadlocking.
func (o *op) lockLoad(m *meta.Model, name string) (store.Record, error) {
	if err := o.lockRecord(m.Name, name); err != nil {
		return nil, err
	}
	rec, err := store.GetForUpdate(o.ctx, o.tx, o.e.Reg(), o.actor.Org, m, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound(m.Name + " " + name)
	}
	return rec, err
}

func (o *op) load(m *meta.Model, name string) (store.Record, error) {
	rec, err := store.Get(o.ctx, o.tx, o.e.Reg(), o.actor.Org, m, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound(m.Name + " " + name)
	}
	return rec, err
}

// Update merges patch into a draft (or non-submittable) record, recomputes and runs save rules. The actor
// needs write on the stored record and on the record after the patch.
func (e *Engine) Update(ctx context.Context, a Actor, model, name string, patch map[string]any) (*Result, error) {
	var res *Result
	err := e.run(ctx, a, func(o *op) error {
		var err error
		res, err = o.update(model, name, e.dropHidden(a, e.Reg().Model(model), patch), true, true)
		return err
	})
	return e.redactResult(a, model, res), err
}

// update merges patch into a record. authorize checks write on the stored and the patched record (public
// Update, flow steps); guard marks the public Update: both records are checked against the actor's row scope,
// read_only fields of the patch are dropped and a table sent in the patch must keep min_rows rows.
// Automations and hooks pass false for both.
func (o *op) update(model, name string, patch map[string]any, authorize, guard bool) (*Result, error) {
	m, err := o.e.model(model)
	if err != nil {
		return nil, err
	}
	if m.AppendOnly {
		return nil, appendOnly(m)
	}
	cur, err := o.lockLoad(m, name)
	if err != nil {
		return nil, err
	}
	if authorize {
		if err := o.authorize(m, "write", o.e.Output(m, copyRecord(cur))); err != nil {
			return nil, err
		}
	}
	if guard {
		if err := o.e.Guard(o.ctx, o.actor, m.Name, cur, "update"); err != nil {
			return nil, err
		}
	}
	if m.IsSubmittable && toInt(cur["docstatus"]) != 0 {
		return nil, conflict("immutable", "Dokumen yang sudah diajukan tidak bisa diubah; batalkan lalu ubah (amend)")
	}
	rec, issues := o.e.normalize(m, patch, cur, guard)
	if len(issues) > 0 {
		return nil, validation(issues, nil)
	}
	o.prepare(m, rec, false)
	if authorize { // write must also hold after the patch (no handing a record to someone else)
		if err := o.authorize(m, "write", o.e.Output(m, copyRecord(rec))); err != nil {
			return nil, err
		}
	}
	errs, warns, err := o.check(m, rec, "save")
	if err != nil {
		return nil, err
	}
	if guard { // a client patch that sends a table must leave at least min_rows rows in it
		errs = append(errs, minRows(m, rec, func(f string) bool { _, sent := patch[f]; return sent })...)
	}
	if len(errs) > 0 {
		return nil, validation(errs, warns)
	}
	if guard {
		if err := o.e.Guard(o.ctx, o.actor, m.Name, rec, "update"); err != nil {
			return nil, err
		}
	}
	if err := store.Update(o.ctx, o.tx, o.e.Reg(), o.actor.Org, m, name, rec); err != nil {
		return nil, dbIssue(m, err)
	}
	if err := o.voidApprovals(m.Name, name, ""); err != nil {
		return nil, err
	}
	if err := o.event(m, rec, "updated"); err != nil {
		return nil, err
	}
	if err := o.automations(m, cur, rec, "updated"); err != nil {
		return nil, err
	}
	if ran, err := o.recordFlows(m, cur, rec, "updated"); err != nil {
		return nil, err
	} else if ran { // a flow may have written the record: answer with what is stored now
		if rec, err = o.load(m, name); err != nil {
			return nil, err
		}
	}
	return &Result{Record: o.e.Output(m, rec), Warnings: warns}, nil
}

// Delete removes a record (not a submitted one).
func (e *Engine) Delete(ctx context.Context, a Actor, model, name string) error {
	return e.run(ctx, a, func(o *op) error {
		m, err := o.e.model(model)
		if err != nil {
			return err
		}
		if m.AppendOnly {
			return appendOnly(m)
		}
		cur, err := o.lockLoad(m, name) // advisory lock, then row: the same order as Approve
		if err != nil {
			return err
		}
		if err := o.authorize(m, "delete", o.e.Output(m, copyRecord(cur))); err != nil {
			return err
		}
		if err := e.Guard(ctx, a, m.Name, cur, "delete"); err != nil {
			return err
		}
		if m.IsSubmittable && toInt(cur["docstatus"]) == 1 {
			return conflict("immutable", "Dokumen yang sudah diajukan tidak bisa dihapus; batalkan dulu")
		}
		// A cancelled document with an amended copy is the copy's history (amended_from points at it), and apps key
		// uniqueness on that chain; deleting it, even as owner, would orphan the copy and free its keys.
		if m.IsSubmittable && toInt(cur["docstatus"]) == 2 {
			copies, err := store.Find(o.ctx, o.tx, o.e.Reg(), o.actor.Org, m, map[string]any{"amended_from": name}, store.FindOpts{})
			if err != nil {
				return err
			}
			if len(copies) > 0 {
				return conflict("has_amendments", "Dokumen ini sudah diubah menjadi "+str(copies[0]["name"])+"; dokumen batal yang punya salinan tidak bisa dihapus")
			}
		}
		if err := store.Delete(o.ctx, o.tx, o.e.Reg(), o.actor.Org, m, name); err != nil {
			return err
		}
		if err := o.voidApprovals(m.Name, name, ""); err != nil {
			return err
		}
		return o.event(m, cur, "deleted")
	})
}

// Submit validates a draft with submit rules, names it with the naming pattern and makes it immutable.
// Warnings block unless force is set. A matching approval policy holds the draft instead (Result.Held).
func (e *Engine) Submit(ctx context.Context, a Actor, model, name string, force bool) (*Result, error) {
	var res *Result
	err := e.run(ctx, a, func(o *op) error {
		var err error
		res, err = o.submit(model, name, force)
		return err
	})
	return e.redactResult(a, model, res), err
}

func (o *op) submit(model, name string, force bool) (*Result, error) {
	e, a := o.e, o.actor
	m, err := e.model(model)
	if err != nil {
		return nil, err
	}
	if m.AppendOnly { // validation refuses append_only with is_submittable; kept for a registry built otherwise
		return nil, appendOnly(m)
	}
	if !m.IsSubmittable {
		return nil, badRequest(model + " is not submittable")
	}
	rec, err := o.lockLoad(m, name)
	if err != nil {
		return nil, err
	}
	if err := o.authorize(m, "submit", e.Output(m, copyRecord(rec))); err != nil {
		return nil, err
	}
	if err := o.guard(m, rec, "submit"); err != nil {
		return nil, err
	}
	if toInt(rec["docstatus"]) != 0 {
		return nil, conflict("not_draft", "Hanya draf yang bisa diajukan")
	}
	if af := str(rec["amended_from"]); af != "" { // before any approval request is held
		if err := o.oneLiveAmendment(m, af, name); err != nil {
			return nil, err
		}
	}
	stored := cloneRecord(rec) // snapshot and approval when: read the stored record (0007 §2, §3)
	o.prepare(m, rec, false)
	errs, warns, err := o.check(m, rec, "submit")
	if err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return nil, validation(errs, warns)
	}
	if len(warns) > 0 && !force {
		v := validation(nil, warns)
		v.NeedsForce = true
		return nil, v
	}
	pending, all, err := o.hold(m, stored, Snapshot(m, stored), "submit", map[string]any{"force": force})
	if err != nil {
		return nil, err
	}
	if len(pending) > 0 {
		return &Result{Record: e.Output(m, rec), Warnings: warns, Held: true, Approvals: all}, nil
	}
	newName := name
	if strings.HasPrefix(name, "DRAFT-") {
		if af := str(rec["amended_from"]); af != "" {
			newName, err = o.amendName(m, af)
		} else {
			newName, err = o.nextName(m)
		}
		if err != nil {
			return nil, err
		}
	}
	rec["name"], rec["docstatus"] = newName, int64(1)
	if err := store.Update(o.ctx, o.tx, e.Reg(), a.Org, m, name, rec); err != nil {
		return nil, dbIssue(m, err)
	}
	if newName != name {
		if err := store.RenameRecordAttachments(o.ctx, o.tx, a.Org, m.Name, name, newName); err != nil {
			return nil, err
		}
		if err := store.RenameRecordApprovals(o.ctx, o.tx, a.Org, m.Name, name, newName); err != nil {
			return nil, err
		}
	}
	if err := o.runHooks(m, rec, m.OnSubmit); err != nil {
		return nil, err
	}
	if err := o.event(m, rec, "submitted"); err != nil {
		return nil, err
	}
	if err := o.automations(m, rec, rec, "submitted"); err != nil {
		return nil, err
	}
	if _, err := o.recordFlows(m, stored, rec, "submitted"); err != nil {
		return nil, err
	}
	return &Result{Record: e.Output(m, rec), Warnings: warns}, nil
}

// oneLiveAmendment keeps the amendments of a document a single chain, so one business document has one live
// version. Amend (self == "") is refused once the document has any copy (amended_from = it), whatever the copy's
// state: a draft or submitted copy is the live one, and a cancelled copy is where the chain goes on (amend that
// copy instead). Submitting a draft copy (self) is refused when another copy of the same document was ever
// submitted (docstatus 1 or 2), which only chains branched before this rule can have. The original is locked
// first, so two amends or submits at the same time see each other.
func (o *op) oneLiveAmendment(m *meta.Model, original, self string) error {
	if _, err := store.GetForUpdate(o.ctx, o.tx, o.e.Reg(), o.actor.Org, m, original); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	copies, err := store.Find(o.ctx, o.tx, o.e.Reg(), o.actor.Org, m, map[string]any{"amended_from": original}, store.FindOpts{})
	if err != nil {
		return err
	}
	for _, c := range copies {
		cn, ds := str(c["name"]), toInt(c["docstatus"])
		switch {
		case cn == self:
			continue
		case self == "" && ds == 2:
			return conflict("already_amended", "Dokumen "+original+" sudah diubah menjadi "+cn+" yang juga dibatalkan; ubah (amend) "+cn+", bukan dokumen ini")
		case self == "" || ds != 0:
			return conflict("already_amended", "Dokumen "+original+" sudah diubah menjadi "+cn+"; lanjutkan atau batalkan salinan itu dulu")
		}
	}
	return nil
}

func (o *op) amendName(m *meta.Model, base string) (string, error) {
	for i := 1; i < 1000; i++ {
		n := fmt.Sprintf("%s-%d", base, i)
		ok, err := store.Exists(o.ctx, o.tx, o.actor.Org, m.Name, n)
		if err != nil {
			return "", err
		}
		if !ok {
			return n, nil
		}
	}
	return "", conflict("naming", "too many amendments of "+base)
}

// Cancel turns a submitted document into a cancelled one (docstatus 2), unless an approval policy holds it.
func (e *Engine) Cancel(ctx context.Context, a Actor, model, name string) (*Result, error) {
	var res *Result
	err := e.run(ctx, a, func(o *op) error {
		var err error
		res, err = o.cancel(model, name)
		return err
	})
	return e.redactResult(a, model, res), err
}

func (o *op) cancel(model, name string) (*Result, error) {
	e, a := o.e, o.actor
	m, err := e.model(model)
	if err != nil {
		return nil, err
	}
	if m.AppendOnly { // validation refuses append_only with is_submittable; kept for a registry built otherwise
		return nil, appendOnly(m)
	}
	if !m.IsSubmittable {
		return nil, badRequest(model + " is not submittable")
	}
	rec, err := o.lockLoad(m, name)
	if err != nil {
		return nil, err
	}
	if err := o.authorize(m, "cancel", e.Output(m, copyRecord(rec))); err != nil {
		return nil, err
	}
	if err := o.guard(m, rec, "cancel"); err != nil {
		return nil, err
	}
	if toInt(rec["docstatus"]) != 1 {
		return nil, conflict("not_submitted", "Hanya dokumen yang sudah diajukan yang bisa dibatalkan")
	}
	pending, all, err := o.hold(m, rec, Snapshot(m, rec), "cancel", map[string]any{})
	if err != nil {
		return nil, err
	}
	if len(pending) > 0 {
		return &Result{Record: e.Output(m, rec), Held: true, Approvals: all}, nil
	}
	before := copyRecord(rec)
	rec["docstatus"] = int64(2)
	if err := store.Update(o.ctx, o.tx, e.Reg(), a.Org, m, name, rec); err != nil {
		return nil, err
	}
	if err := o.runHooks(m, rec, m.OnCancel); err != nil {
		return nil, err
	}
	if err := o.event(m, rec, "cancelled"); err != nil {
		return nil, err
	}
	if err := o.automations(m, rec, rec, "cancelled"); err != nil {
		return nil, err
	}
	if _, err := o.recordFlows(m, before, rec, "cancelled"); err != nil {
		return nil, err
	}
	return &Result{Record: e.Output(m, rec)}, nil
}

// Amend copies a cancelled document into a new draft with amended_from set; read-only stored values
// (like paid_amount) start again from zero.
func (e *Engine) Amend(ctx context.Context, a Actor, model, name string) (*Result, error) {
	var res *Result
	err := e.run(ctx, a, func(o *op) error {
		var err error
		res, err = o.amend(model, name)
		return err
	})
	return e.redactResult(a, model, res), err
}

func (o *op) amend(model, name string) (*Result, error) {
	e, a := o.e, o.actor
	m, err := e.model(model)
	if err != nil {
		return nil, err
	}
	if m.AppendOnly { // validation refuses append_only with is_submittable; kept for a registry built otherwise
		return nil, appendOnly(m)
	}
	if !m.IsSubmittable {
		return nil, badRequest(model + " is not submittable")
	}
	old, err := o.load(m, name)
	if err != nil {
		return nil, err
	}
	if err := o.authorize(m, "amend", e.Output(m, copyRecord(old))); err != nil {
		return nil, err
	}
	if err := e.Guard(o.ctx, a, m.Name, old, "amend"); err != nil {
		return nil, err
	}
	if toInt(old["docstatus"]) != 2 {
		return nil, conflict("not_cancelled", "Hanya dokumen yang dibatalkan yang bisa di-amend")
	}
	if err := o.oneLiveAmendment(m, name, ""); err != nil {
		return nil, err
	}
	rec := store.Record{}
	for k, v := range old {
		rec[k] = v
	}
	for _, f := range m.Fields {
		if f.ReadOnly && f.Compute == "" {
			rec[f.Name] = store.Zero(f)
		}
		if f.Type == "table" {
			var copied []any
			for _, row := range rows(old[f.Name]) {
				nr := map[string]any{}
				for k, v := range row {
					if k != "name" && k != "parent" && k != "parent_field" && k != "idx" {
						nr[k] = v
					}
				}
				copied = append(copied, nr)
			}
			if copied == nil {
				copied = []any{}
			}
			rec[f.Name] = copied
		}
	}
	rec["name"], rec["docstatus"], rec["amended_from"] = "DRAFT-"+store.RandomName(5), int64(0), name
	rec["created_by"] = a.ID
	o.prepare(m, rec, false)
	if err := store.Insert(o.ctx, o.tx, e.Reg(), a.Org, m, rec); err != nil {
		return nil, err
	}
	if err := o.event(m, rec, "created"); err != nil {
		return nil, err
	}
	return &Result{Record: e.Output(m, rec)}, nil
}

// copyRecord is a shallow copy (permission checks decorate it with the derived status).
func copyRecord(r store.Record) store.Record {
	out := make(store.Record, len(r)+1)
	for k, v := range r {
		out[k] = v
	}
	return out
}

// runHooks runs on_submit/on_cancel hooks; core hooks without an implementation are recorded as skipped.
func (o *op) runHooks(m *meta.Model, rec store.Record, hooks []string) error {
	for _, h := range hooks {
		fn := o.e.Hooks[h]
		if fn == nil {
			if err := store.Audit(o.ctx, o.tx, o.actor.Org, o.actor.ID, "hook.skipped", m.Name+"/"+str(rec["name"]), "hook "+h+" is not implemented in this kernel", nil); err != nil {
				return err
			}
			continue
		}
		if _, err := callHook(fn, &HookCtx{op: o, Model: m, Record: rec}); err != nil {
			return fmt.Errorf("hook %s: %w", h, err)
		}
	}
	return nil
}
