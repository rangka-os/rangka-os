package changeset

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/rk"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

func remarshal(in, out any) {
	b, _ := json.Marshal(in)
	json.Unmarshal(b, out)
}

// parsed returns a function reading a change-set path as data from a set of files (or the roots).
func parsed(r roots, files map[string]*string) func(string) (map[string]any, bool) {
	return func(p string) (map[string]any, bool) {
		var text *string
		if files != nil {
			if c, ok := files[p]; ok {
				text = c
			} else {
				return parsed(r, nil)(p)
			}
		} else {
			f, err := r.file(p)
			if err != nil {
				return nil, false
			}
			text, _ = readFile(f)
		}
		if text == nil {
			return nil, false
		}
		d, err := rk.LoadBytes(p, []byte(*text))
		if err != nil {
			return map[string]any{}, true
		}
		return meta.AsMap(d.Data), true
	}
}

// Propose stores a new change set (body = changeset.json without computed fields) with layers and risk.
func (s *Service) Propose(ctx context.Context, a Actor, body map[string]any) (*ChangeSet, error) {
	if body == nil {
		body = map[string]any{}
	}
	doc := map[string]any{}
	for k, v := range body {
		switch k {
		case "id", "status", "layers", "risk", "validation", "preview", "history":
			continue
		}
		doc[k] = v
	}
	if _, ok := doc["author"]; !ok {
		kind := a.Kind
		if kind == "" {
			kind = "user"
		}
		doc["author"] = map[string]any{"kind": kind, "id": a.ID}
	}
	doc["id"], doc["status"] = newID(), "proposed"
	if errs := s.Schemas.Validate("changeset", schema.FromData(normalize(doc))); len(errs) > 0 {
		var items []map[string]any
		for _, e := range errs {
			items = append(items, map[string]any{"field": e.PathString(), "code": "schema", "message": e.Message})
		}
		return nil, &Error{422, "invalid_changeset", "the change set does not match changeset.json", map[string]any{"errors": items, "warnings": []any{}}}
	}
	cs := &ChangeSet{}
	remarshal(doc, cs)
	if cs.Org == "" {
		cs.Org = a.Org
	}
	for i, op := range cs.Ops {
		if (op.Op == "create" || op.Op == "replace") && op.Content == "" {
			return nil, &Error{422, "invalid_changeset", fmt.Sprintf("ops/%d: %s needs content (the full .rk text)", i, op.Op), nil}
		}
		if op.Op == "patch" && len(op.Patch) == 0 {
			return nil, &Error{422, "invalid_changeset", fmt.Sprintf("ops/%d: patch needs a JSON Patch list", i), nil}
		}
	}
	live := s.live()
	files, err := live.result(cs.Ops)
	if err != nil {
		return nil, &Error{422, "bad_ops", err.Error(), nil}
	}
	as := Assess(cs.Ops, parsed(live, nil), parsed(live, files), s.appModel)
	cs.Layers, cs.Risk = as.Layers, as.Risk
	if cs.Layers == nil {
		cs.Layers = []int{}
	}
	note := "risk " + as.Risk
	if len(as.Reasons) > 0 {
		note += ": " + strings.Join(as.Reasons, "; ")
	}
	if err := s.transition(ctx, cs, a, "proposed", note, true); err != nil {
		return nil, err
	}
	return cs, nil
}

// appModel returns the app's own model file (without overlays) by name.
func (s *Service) appModel(name string) map[string]any {
	if s.Reg == nil {
		return nil
	}
	if m := s.Reg().Models[name]; m != nil && m.Doc != nil {
		return meta.AsMap(m.Doc.Data)
	}
	return nil
}

func relItems(items []meta.Item, r roots) []meta.Item {
	out := []meta.Item{}
	for _, it := range items {
		// sandbox paths → change-set paths
		f := filepath.ToSlash(it.File)
		switch {
		case strings.HasPrefix(f, "../site/"):
			parts := strings.SplitN(strings.TrimPrefix(f, "../site/"), "/", 2)
			if len(parts) == 2 {
				f = parts[1]
			}
		case !strings.HasPrefix(f, "../"):
			f = "apps/" + f
		}
		it.File = f
		out = append(out, it)
	}
	return out
}

// check validates the change set's result on a sandbox copy.
func (s *Service) check(cs *ChangeSet) (*Validation, *meta.Registry, roots, func(), error) {
	r, cleanup, _, err := s.sandbox(cs.Ops)
	if err != nil {
		if e, ok := err.(*Error); ok {
			v := &Validation{Errors: []meta.Item{{Level: "error", File: "", Pos: "1:1", Where: "ops", Code: e.Code, Message: e.Msg}}, Warnings: []meta.Item{}}
			return v, nil, roots{}, func() {}, nil
		}
		return nil, nil, roots{}, nil, err
	}
	reg, rep := meta.Validate(r.apps, r.site, nil, s.Schemas)
	v := &Validation{Errors: []meta.Item{}, Warnings: []meta.Item{}}
	for _, it := range relItems(rep.All(), r) {
		if it.Level == "error" {
			v.Errors = append(v.Errors, it)
		} else {
			v.Warnings = append(v.Warnings, it)
		}
	}
	return v, reg, r, cleanup, nil
}

// Validate runs the validator on a copy with the change applied → validated or invalid.
func (s *Service) Validate(ctx context.Context, a Actor, id string) (*ChangeSet, error) {
	cs, err := s.Get(ctx, a.Org, id)
	if err != nil {
		return nil, err
	}
	if !in(cs.Status, "proposed", "validated", "invalid", "previewed") {
		return nil, &Error{409, "bad_status", "a change set that is " + cs.Status + " cannot be validated again", nil}
	}
	v, _, _, cleanup, err := s.check(cs)
	if err != nil {
		return nil, err
	}
	cleanup()
	cs.Validation, cs.Preview = v, nil
	status, note := "validated", fmt.Sprintf("%d warning(s)", len(v.Warnings))
	if len(v.Errors) > 0 {
		status, note = "invalid", fmt.Sprintf("%d error(s): %s", len(v.Errors), v.Errors[0].Code+" "+v.Errors[0].Message)
	}
	return cs, s.transition(ctx, cs, a, status, note, false)
}

func in(s string, set ...string) bool {
	for _, x := range set {
		if s == x {
			return true
		}
	}
	return false
}

// Preview dry-runs the change on a copy: the DDL that would run against the live database and the number
// of records in the models it touches.
func (s *Service) Preview(ctx context.Context, a Actor, id string) (*ChangeSet, error) {
	cs, err := s.Get(ctx, a.Org, id)
	if err != nil {
		return nil, err
	}
	if !in(cs.Status, "proposed", "validated", "previewed") {
		return nil, &Error{409, "bad_status", "a change set that is " + cs.Status + " cannot be previewed", nil}
	}
	v, reg, _, cleanup, err := s.check(cs)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	cs.Validation = v
	live0 := s.live()
	after, _ := live0.result(cs.Ops)
	if len(v.Errors) > 0 {
		cs.Preview = nil
		if err := s.transition(ctx, cs, a, "invalid", fmt.Sprintf("%d error(s)", len(v.Errors)), false); err != nil {
			return nil, err
		}
		return cs, &Error{409, "invalid", "the change set has validation errors", cs}
	}
	live, err := store.Introspect(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	stmts, notes := store.Plan(store.Tables(reg), live)
	p := &Preview{Migrations: stmts, Affected: map[string]int64{}, Reversible: true, Notes: notes, Files: []FileDiff{}}
	var paths []string
	for path := range after {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		f, _ := live0.file(path)
		before, _ := readFile(f)
		p.Files = append(p.Files, FileDiff{Path: path, Before: before, After: after[path]})
	}
	if p.Migrations == nil {
		p.Migrations = []string{}
	}
	for _, op := range cs.Ops {
		if !strings.Contains(op.Path, "/models/") && !strings.HasPrefix(op.Path, "custom/") {
			continue
		}
		model := strings.TrimSuffix(filepath.Base(op.Path), ".rk")
		if _, ok := live.Columns[store.TableName(model)]; !ok {
			p.Affected[model] = 0
			continue
		}
		n, err := store.Count(ctx, s.DB, cs.Org, model)
		if err != nil {
			return nil, err
		}
		p.Affected[model] = n
		if op.Op == "delete" && n > 0 {
			p.Reversible = false
			p.Notes = append(p.Notes, fmt.Sprintf("deleting %s hides %d record(s); the table is kept", model, n))
		}
	}
	cs.Preview = p
	return cs, s.transition(ctx, cs, a, "previewed", fmt.Sprintf("%d statement(s)", len(stmts)), false)
}

// Approvers returns the roles that may approve (config.ai.approvers, always including owner).
func (s *Service) Approvers() map[string]bool {
	out := map[string]bool{"owner": true}
	if s.Reg != nil {
		for _, r := range meta.StrList(s.Reg().SiteConfig("ai.approvers")) {
			out[r] = true
		}
	}
	return out
}

func hasRole(a Actor, role string) bool {
	for _, r := range a.Roles {
		if r == role {
			return true
		}
	}
	return false
}

func (s *Service) isApprover(a Actor) bool {
	ap := s.Approvers()
	for _, r := range a.Roles {
		if ap[r] {
			return true
		}
	}
	return false
}

func (s *Service) mayApprove(a Actor) error {
	if !a.Human {
		return &Error{403, "human_required", "a person signed in to the Console or the app must approve change sets (API and MCP tokens cannot)", nil}
	}
	ap := s.Approvers()
	for _, r := range a.Roles {
		if ap[r] {
			return nil
		}
	}
	return &Error{403, "not_approver", "only " + strings.Join(meta.StrList(keysOf(ap)), ", ") + " may approve change sets", nil}
}

func keysOf(m map[string]bool) any {
	var out []any
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Approve marks a validated (or previewed) change set approved.
func (s *Service) Approve(ctx context.Context, a Actor, id, note string) (*ChangeSet, error) {
	if err := s.mayApprove(a); err != nil {
		return nil, err
	}
	cs, err := s.Get(ctx, a.Org, id)
	if err != nil {
		return nil, err
	}
	if cs.Status != "previewed" {
		return nil, &Error{409, "bad_status", "approve a change set after its preview (it is " + cs.Status + ")", nil}
	}
	if cs.Risk == "high" && !hasRole(a, "owner") {
		return nil, &Error{403, "owner_required", "a high-risk change set needs the owner's approval", nil}
	}
	return cs, s.transition(ctx, cs, a, "approved", note, false)
}

// Reject closes a change set without applying it.
func (s *Service) Reject(ctx context.Context, a Actor, id, note string) (*ChangeSet, error) {
	if err := s.mayApprove(a); err != nil {
		return nil, err
	}
	cs, err := s.Get(ctx, a.Org, id)
	if err != nil {
		return nil, err
	}
	if !in(cs.Status, "proposed", "validated", "invalid", "previewed", "approved") {
		return nil, &Error{409, "bad_status", "a change set that is " + cs.Status + " cannot be rejected", nil}
	}
	return cs, s.transition(ctx, cs, a, "rejected", note, false)
}

// Apply writes the files, migrates and reloads metadata. It needs approval, or config.ai.auto_apply:
// low_risk for a low-risk validated change set.
func (s *Service) Apply(ctx context.Context, a Actor, id string) (*ChangeSet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cs, err := s.Get(ctx, a.Org, id)
	if err != nil {
		return nil, err
	}
	autoApply := false
	if s.Reg != nil {
		if v, _ := s.Reg().SiteConfig("ai.auto_apply").(string); v == "low_risk" && cs.Risk == "low" && in(cs.Status, "validated", "previewed") {
			autoApply = true
		}
	}
	if cs.Status != "approved" && !autoApply {
		return nil, &Error{409, "not_approved", "apply needs an approved change set (it is " + cs.Status + ")", nil}
	}
	if cs.Status == "approved" && !s.isApprover(a) {
		return nil, &Error{403, "not_approver", "only an approver applies an approved change set", nil}
	}
	v, _, _, cleanup, err := s.check(cs)
	if err != nil {
		return nil, err
	}
	cleanup()
	if len(v.Errors) > 0 {
		cs.Validation = v
		if err := s.transition(ctx, cs, a, "invalid", "metadata changed since approval; validate again", false); err != nil {
			return nil, err
		}
		return cs, &Error{409, "invalid", "the change no longer validates against the current metadata", cs}
	}
	live := s.live()
	files, err := live.result(cs.Ops)
	if err != nil {
		return nil, &Error{409, "bad_ops", err.Error(), nil}
	}
	prev := map[string]*string{}
	for p := range files {
		f, _ := live.file(p)
		c, err := readFile(f)
		if err != nil {
			return nil, err
		}
		prev[p] = c
	}
	if err := s.write(live, files); err != nil {
		s.write(live, prev)
		return nil, err
	}
	if err := s.Reload(ctx); err != nil {
		s.write(live, prev)
		rerr := s.Reload(ctx)
		cs.previous = prev
		note := "apply failed and was rolled back: " + err.Error()
		if rerr != nil {
			note += "; reload after rollback: " + rerr.Error()
		}
		s.transition(ctx, cs, a, "failed", note, false)
		return cs, &Error{500, "apply_failed", note, cs}
	}
	cs.previous = prev
	note := "applied"
	if autoApply && cs.Status != "approved" {
		note = "applied automatically (config.ai.auto_apply: low_risk)"
	}
	return cs, s.transition(ctx, cs, a, "applied", note, false)
}

func (s *Service) write(r roots, files map[string]*string) error {
	for p, c := range files {
		f, err := r.file(p)
		if err != nil {
			return err
		}
		if err := writeFile(f, c); err != nil {
			return err
		}
	}
	return nil
}

// Revert restores the file versions from before an applied change set and reloads.
func (s *Service) Revert(ctx context.Context, a Actor, id string) (*ChangeSet, error) {
	if err := s.mayApprove(a); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cs, err := s.Get(ctx, a.Org, id)
	if err != nil {
		return nil, err
	}
	if cs.Status != "applied" || cs.previous == nil {
		return nil, &Error{409, "bad_status", "only an applied change set can be reverted (it is " + cs.Status + ")", nil}
	}
	live := s.live()
	current := map[string]*string{}
	for p := range cs.previous {
		f, _ := live.file(p)
		c, _ := readFile(f)
		current[p] = c
	}
	if err := s.write(live, cs.previous); err != nil {
		return nil, err
	}
	if err := s.Reload(ctx); err != nil {
		s.write(live, current)
		s.Reload(ctx)
		return nil, &Error{500, "revert_failed", "revert failed and was undone: " + err.Error(), nil}
	}
	return cs, s.transition(ctx, cs, a, "reverted", "previous file versions restored (added columns stay; their data is kept)", false)
}
