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
func (e *Engine) normalize(m *meta.Model, input map[string]any, base store.Record) (store.Record, []Issue) {
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
		if f.Type == "table" {
			cm := e.Reg().Models[f.Of]
			if cm == nil {
				continue
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
				nr, is := e.normalize(cm, row, nil)
				if n := str(row["name"]); n != "" {
					nr["name"] = n
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
			if isNew || str(row["name"]) == "" {
				cs.applyDefaults()
			}
			fill(cm, row)
		}
	}
	fill(m, rec)
	s.compute()
}

// check runs rules (on save, or on submit = submit + save rules), required fields, min_rows (submit),
// link targets and select options.
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
		if f.Type == "table" && f.MinRows > 0 && on == "submit" && len(rows(v)) < f.MinRows {
			errs = append(errs, Issue{Field: f.Name, Code: "min_rows", Message: fmt.Sprintf("Tambahkan minimal %d baris %s", f.MinRows, strings.ToLower(f.Label.Text()))})
		}
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
	return append(errs, more...), warns, err
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
}

// Result of a write: the record and any warnings.
type Result struct {
	Record   store.Record
	Warnings []Issue
}

// Create saves a new record: defaults, compute, save rules, name (DRAFT-xxxxx for submittable models).
func (e *Engine) Create(ctx context.Context, a Actor, model string, input map[string]any) (*Result, error) {
	var res *Result
	err := e.run(ctx, a, func(o *op) error {
		var err error
		res, err = o.create(model, input, CreateOpts{})
		return err
	})
	return res, err
}

func (o *op) create(model string, input map[string]any, opts CreateOpts) (*Result, error) {
	m, err := o.e.model(model)
	if err != nil {
		return nil, err
	}
	if m.IsChild {
		return nil, badRequest(model + " rows are saved through their parent document")
	}
	rec, issues := o.e.normalize(m, input, nil)
	if len(issues) > 0 {
		return nil, validation(issues, nil)
	}
	o.prepare(m, rec, true)
	errs, warns, err := o.check(m, rec, "save")
	if err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return nil, validation(errs, warns)
	}
	rec["created_by"] = o.actor.ID
	switch {
	case opts.KeepName && str(input["name"]) != "":
		rec["name"] = str(input["name"])
	case m.IsSubmittable:
		rec["name"] = "DRAFT-" + store.RandomName(5)
	default:
		n, err := store.NextName(o.ctx, o.tx, o.actor.Org, m.Name, o.e.naming(m), o.e.Now().In(o.e.Location()))
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
	return &Result{o.e.Output(m, rec), warns}, nil
}

// naming returns the naming pattern (config.numbering overrides the model).
func (e *Engine) naming(m *meta.Model) string {
	if p, ok := meta.AsMap(e.Reg().SiteConfig("numbering"))[m.Name].(string); ok && p != "" {
		return p
	}
	return m.Naming
}

// Get loads a record.
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
	return e.Output(m, rec), nil
}

func (o *op) load(m *meta.Model, name string) (store.Record, error) {
	rec, err := store.Get(o.ctx, o.tx, o.e.Reg(), o.actor.Org, m, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound(m.Name + " " + name)
	}
	return rec, err
}

// Update merges patch into a draft (or non-submittable) record, recomputes and runs save rules.
func (e *Engine) Update(ctx context.Context, a Actor, model, name string, patch map[string]any) (*Result, error) {
	var res *Result
	err := e.run(ctx, a, func(o *op) error {
		var err error
		res, err = o.update(model, name, patch)
		return err
	})
	return res, err
}

func (o *op) update(model, name string, patch map[string]any) (*Result, error) {
	m, err := o.e.model(model)
	if err != nil {
		return nil, err
	}
	cur, err := o.load(m, name)
	if err != nil {
		return nil, err
	}
	if m.IsSubmittable && toInt(cur["docstatus"]) != 0 {
		return nil, conflict("immutable", "Dokumen yang sudah diajukan tidak bisa diubah; batalkan lalu ubah (amend)")
	}
	rec, issues := o.e.normalize(m, patch, cur)
	if len(issues) > 0 {
		return nil, validation(issues, nil)
	}
	o.prepare(m, rec, false)
	errs, warns, err := o.check(m, rec, "save")
	if err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return nil, validation(errs, warns)
	}
	if err := store.Update(o.ctx, o.tx, o.e.Reg(), o.actor.Org, m, name, rec); err != nil {
		return nil, dbIssue(m, err)
	}
	if err := o.event(m, rec, "updated"); err != nil {
		return nil, err
	}
	if err := o.automations(m, cur, rec, "updated"); err != nil {
		return nil, err
	}
	return &Result{o.e.Output(m, rec), warns}, nil
}

// Delete removes a record (not a submitted one).
func (e *Engine) Delete(ctx context.Context, a Actor, model, name string) error {
	return e.run(ctx, a, func(o *op) error {
		m, err := o.e.model(model)
		if err != nil {
			return err
		}
		cur, err := o.load(m, name)
		if err != nil {
			return err
		}
		if m.IsSubmittable && toInt(cur["docstatus"]) == 1 {
			return conflict("immutable", "Dokumen yang sudah diajukan tidak bisa dihapus; batalkan dulu")
		}
		if err := store.Delete(o.ctx, o.tx, o.e.Reg(), o.actor.Org, m, name); err != nil {
			return err
		}
		return o.event(m, cur, "deleted")
	})
}

// Submit validates a draft with submit rules, names it with the naming pattern and makes it immutable.
// Warnings block unless force is set.
func (e *Engine) Submit(ctx context.Context, a Actor, model, name string, force bool) (*Result, error) {
	var res *Result
	err := e.run(ctx, a, func(o *op) error {
		m, err := o.e.model(model)
		if err != nil {
			return err
		}
		if !m.IsSubmittable {
			return badRequest(model + " is not submittable")
		}
		rec, err := o.load(m, name)
		if err != nil {
			return err
		}
		if toInt(rec["docstatus"]) != 0 {
			return conflict("not_draft", "Hanya draf yang bisa diajukan")
		}
		o.prepare(m, rec, false)
		errs, warns, err := o.check(m, rec, "submit")
		if err != nil {
			return err
		}
		if len(errs) > 0 {
			return validation(errs, warns)
		}
		if len(warns) > 0 && !force {
			v := validation(nil, warns)
			v.NeedsForce = true
			return v
		}
		newName := name
		if strings.HasPrefix(name, "DRAFT-") {
			if af := str(rec["amended_from"]); af != "" {
				newName, err = o.amendName(m, af)
			} else {
				newName, err = store.NextName(o.ctx, o.tx, a.Org, m.Name, e.naming(m), e.Now().In(e.Location()))
			}
			if err != nil {
				return err
			}
		}
		rec["name"], rec["docstatus"] = newName, int64(1)
		if err := store.Update(o.ctx, o.tx, e.Reg(), a.Org, m, name, rec); err != nil {
			return dbIssue(m, err)
		}
		if err := o.runHooks(m, rec, m.OnSubmit); err != nil {
			return err
		}
		if err := o.event(m, rec, "submitted"); err != nil {
			return err
		}
		if err := o.automations(m, rec, rec, "submitted"); err != nil {
			return err
		}
		res = &Result{e.Output(m, rec), warns}
		return nil
	})
	return res, err
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

// Cancel turns a submitted document into a cancelled one (docstatus 2).
func (e *Engine) Cancel(ctx context.Context, a Actor, model, name string) (*Result, error) {
	var res *Result
	err := e.run(ctx, a, func(o *op) error {
		m, err := o.e.model(model)
		if err != nil {
			return err
		}
		if !m.IsSubmittable {
			return badRequest(model + " is not submittable")
		}
		rec, err := o.load(m, name)
		if err != nil {
			return err
		}
		if toInt(rec["docstatus"]) != 1 {
			return conflict("not_submitted", "Hanya dokumen yang sudah diajukan yang bisa dibatalkan")
		}
		rec["docstatus"] = int64(2)
		if err := store.Update(o.ctx, o.tx, e.Reg(), a.Org, m, name, rec); err != nil {
			return err
		}
		if err := o.runHooks(m, rec, m.OnCancel); err != nil {
			return err
		}
		if err := o.event(m, rec, "cancelled"); err != nil {
			return err
		}
		if err := o.automations(m, rec, rec, "cancelled"); err != nil {
			return err
		}
		res = &Result{e.Output(m, rec), nil}
		return nil
	})
	return res, err
}

// Amend copies a cancelled document into a new draft with amended_from set; read-only stored values
// (like paid_amount) start again from zero.
func (e *Engine) Amend(ctx context.Context, a Actor, model, name string) (*Result, error) {
	var res *Result
	err := e.run(ctx, a, func(o *op) error {
		m, err := o.e.model(model)
		if err != nil {
			return err
		}
		if !m.IsSubmittable {
			return badRequest(model + " is not submittable")
		}
		old, err := o.load(m, name)
		if err != nil {
			return err
		}
		if toInt(old["docstatus"]) != 2 {
			return conflict("not_cancelled", "Hanya dokumen yang dibatalkan yang bisa di-amend")
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
			return err
		}
		if err := o.event(m, rec, "created"); err != nil {
			return err
		}
		res = &Result{e.Output(m, rec), nil}
		return nil
	})
	return res, err
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
		if _, err := fn(&HookCtx{op: o, Model: m, Record: rec}); err != nil {
			return fmt.Errorf("hook %s: %w", h, err)
		}
	}
	return nil
}
