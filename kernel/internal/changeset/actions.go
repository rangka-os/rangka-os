package changeset

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/plan"
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
	return s.ProposeFrom(ctx, a, body, "")
}

// ProposeFrom is Propose with the kernel's own source mark: "catalogue" when the catalogue built the change set
// (contract 0036 §4). The assistant's "ai" mark wins: a body never sets source itself.
func (s *Service) ProposeFrom(ctx context.Context, a Actor, body map[string]any, source string) (*ChangeSet, error) {
	if err := s.notInSandbox(ctx, a); err != nil {
		return nil, err
	}
	if body == nil {
		body = map[string]any{}
	}
	// a person the plan made read-only changes nothing (contract 0028 §4), a proposal included: with
	// config.ai.auto_apply: low_risk the kernel would apply it for them (audit cloud-package-plan F-6)
	if err := s.planReadOnly(a); err != nil {
		return nil, err
	}
	doc := map[string]any{}
	for k, v := range body {
		switch k {
		case "id", "status", "layers", "risk", "validation", "preview", "history", "source":
			continue
		}
		doc[k] = v
	}
	// a change set belongs to the proposer's organisation; rk_changesets has no row-level security
	if v := doc["org"]; v != nil {
		if org, ok := v.(string); !ok || (org != "" && org != a.Org) {
			return nil, &Error{403, "org_mismatch", "Change set hanya bisa diusulkan untuk organisasi kamu sendiri.", nil}
		}
		delete(doc, "org")
	}
	if a.Assistant {
		delete(doc, "author") // the assistant proposes as the person it acts for; it cannot name another author
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
	cs.Source = source
	if a.Assistant {
		cs.Source = "ai"
	}
	cs.Org = a.Org
	for i, op := range cs.Ops {
		if (op.Op == "create" || op.Op == "replace") && op.Content == "" {
			return nil, &Error{422, "invalid_changeset", fmt.Sprintf("ops/%d: %s needs content (the full .rk text)", i, op.Op), nil}
		}
		if op.Op == "patch" && len(op.Patch) == 0 {
			return nil, &Error{422, "invalid_changeset", fmt.Sprintf("ops/%d: patch needs a JSON Patch list", i), nil}
		}
	}
	if err := s.operatorPlan(ctx, cs.Ops); err != nil {
		return nil, err
	}
	live := s.live()
	pinPlans(live, cs.Ops)
	files, err := live.result(cs.Ops)
	if e, ok := err.(*Error); ok {
		return nil, e
	}
	if err != nil {
		return nil, &Error{422, "bad_ops", err.Error(), nil}
	}
	if a.Assistant { // the platform's hard limits (contract 0045 §2): no risk level makes these proposable
		if paths := AssistantForbidden(cs.Ops, parsed(live, nil), parsed(live, files)); len(paths) > 0 {
			return nil, &Error{403, "ai_forbidden_path", "Raka tidak bisa mengusulkan perubahan ini: " + strings.Join(paths, ", ") +
				". Bagian ini diubah lewat Console oleh admin atau pemilik.", map[string]any{"paths": paths}}
		}
	}
	as := AssessWith(cs.Ops, parsed(live, nil), parsed(live, files), Env{Model: s.appModel, Installed: s.installedFor, ModuleOf: s.moduleOf})
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

// installedFor returns the units a config.rk would install with the live units (contract 0032 §3).
func (s *Service) installedFor(cfg map[string]any) map[string]bool {
	if s.Reg == nil || s.Reg() == nil {
		return nil
	}
	return s.Reg().InstalledFor(cfg)
}

// modulesDiff compares the live installed units with those of the change set's registry (contract 0032 §3): it
// counts the rows of every removed model in all organisations, raises the change set to high when any is above
// zero, and names the units config no longer lists that another unit keeps installed.
func (s *Service) modulesDiff(ctx context.Context, cs *ChangeSet, live *store.Live, after *meta.Registry, p *Preview) error {
	touches := false
	for _, op := range cs.Ops {
		touches = touches || op.Path == "config.rk"
	}
	if !touches || s.Reg == nil || s.Reg() == nil || after == nil {
		return nil
	}
	before := s.Reg()
	d := &ModulesDiff{Added: []string{}, Removed: []string{}, Rows: map[string]int64{}, Kept: []KeptUnit{}}
	for _, n := range after.AppOrder() {
		if !before.IsInstalled(n) {
			d.Added = append(d.Added, n)
		}
	}
	for _, n := range before.AppOrder() {
		if !after.IsInstalled(n) {
			d.Removed = append(d.Removed, n)
		}
	}
	sort.Strings(d.Added)
	sort.Strings(d.Removed)
	var withRows, submitted []string
	for _, n := range d.Removed {
		u := before.App[n]
		if u == nil {
			continue
		}
		for _, mn := range u.Models {
			if _, ok := live.Columns[store.TableName(mn)]; !ok {
				d.Rows[mn] = 0
				continue
			}
			c, err := store.CountAllOrgs(ctx, s.DB, mn)
			if err != nil {
				return err
			}
			d.Rows[mn] = c
			p.Affected[mn] = c
			if c > 0 {
				withRows = append(withRows, fmt.Sprintf("%s (%d)", mn, c))
				sub, err := store.CountSubmittedTable(ctx, s.DB, store.TableName(mn), live.Columns[store.TableName(mn)])
				if err != nil {
					return err
				}
				if sub > 0 {
					if d.Submitted == nil {
						d.Submitted = map[string]int64{}
					}
					d.Submitted[mn] = sub
					submitted = append(submitted, fmt.Sprintf("%s (%d)", mn, sub))
				}
			}
		}
	}
	// units the old config named that the new one does not, still installed: another unit requires them
	oldNamed := map[string]bool{}
	for _, x := range meta.AsList(before.SiteConfig("apps")) {
		if n, ok := x.(string); ok {
			oldNamed[n] = true
		}
	}
	for _, n := range before.ConfigModules() {
		oldNamed[n] = true
	}
	newNamed := map[string]bool{}
	for _, x := range meta.AsList(after.SiteConfig("apps")) {
		if n, ok := x.(string); ok {
			newNamed[n] = true
		}
	}
	for _, n := range after.ConfigModules() {
		newNamed[n] = true
	}
	for _, n := range sortedSet(oldNamed) {
		if newNamed[n] || !after.IsInstalled(n) {
			continue
		}
		var by []string
		for _, u := range after.Apps {
			if u.Name == n || !after.IsInstalled(u.Name) {
				continue
			}
			_, m := u.Modules[n]
			_, r := u.Requires[n]
			if m || r {
				by = append(by, u.Name)
			}
		}
		sort.Strings(by)
		if by == nil {
			by = []string{}
		}
		d.Kept = append(d.Kept, KeptUnit{Name: n, RequiredBy: by})
		p.Notes = append(p.Notes, fmt.Sprintf("%s tetap terpasang: dibutuhkan oleh %s", n, strings.Join(by, ", ")))
	}
	if len(d.Added) > 0 {
		p.Notes = append(p.Notes, "dipasang: "+strings.Join(d.Added, ", "))
	}
	if len(d.Removed) > 0 {
		p.Notes = append(p.Notes, "dilepas: "+strings.Join(d.Removed, ", ")+" (tabel dan datanya disimpan, kembali saat dipasang lagi)")
	}
	if len(submitted) > 0 {
		// e.g. daily sales forms of today: once their app is removed, the cashier's "one sales source per outlet and
		// day" guard no longer sees them, and the same day can be recorded twice
		p.Notes = append(p.Notes, "dokumen yang sudah diajukan di app yang dilepas: "+strings.Join(submitted, ", ")+
			"; app lain yang masih terpasang tidak melihatnya lagi, jadi pemeriksaan yang memakai dokumen itu (mis. satu sumber penjualan per outlet per hari) berhenti berlaku")
	}
	if len(withRows) > 0 && cs.Risk != "high" {
		cs.Risk = "high"
		p.Notes = append(p.Notes, "risiko naik ke tinggi: model yang dilepas masih berisi data, "+strings.Join(withRows, ", ")+"; hanya pemilik yang bisa menyetujui")
	}
	p.Modules = d
	return nil
}

// removedRows raises cs.Risk to high when a unit the change set removes from config.rk still holds rows, counted now
// against the live database (modulesDiff without a preview to fill).
func (s *Service) removedRows(ctx context.Context, cs *ChangeSet, after *meta.Registry) error {
	touches := false
	for _, op := range cs.Ops {
		touches = touches || op.Path == "config.rk"
	}
	if !touches || s.DB == nil {
		return nil
	}
	live, err := store.Introspect(ctx, s.DB)
	if err != nil {
		return err
	}
	return s.modulesDiff(ctx, cs, live, after, &Preview{Affected: map[string]int64{}})
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// moduleOf returns the unit (app or module) a model belongs to, "" when unknown (contract 0045 §2b).
func (s *Service) moduleOf(name string) string {
	if s.Reg == nil {
		return ""
	}
	if m := s.Reg().Models[name]; m != nil {
		return m.App
	}
	return ""
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
		case strings.HasPrefix(f, "../modules/"):
			f = strings.TrimPrefix(f, "../")
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
		if e, ok := err.(*Error); ok && e.Code == "stale_base" {
			return nil, nil, roots{}, nil, e
		}
		if e, ok := err.(*Error); ok {
			v := &Validation{Errors: []meta.Item{{Level: "error", File: "", Pos: "1:1", Where: "ops", Code: e.Code, Message: e.Msg}}, Warnings: []meta.Item{}}
			return v, nil, roots{}, func() {}, nil
		}
		return nil, nil, roots{}, nil, err
	}
	reg, rep := meta.ValidateOpts(meta.Options{AppsDir: r.apps, SiteDir: r.site, ModulesDir: r.modules}, nil, s.Schemas)
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
	s.mu.Lock() // not while Apply or Revert of this kernel writes files (audit changeset followup 1)
	defer s.mu.Unlock()
	cs, err := s.Get(ctx, a.Org, id)
	if err != nil {
		return nil, err
	}
	if !in(cs.Status, "proposed", "validated", "invalid", "previewed") {
		return nil, &Error{409, "bad_status", "a change set that is " + cs.Status + " cannot be validated again", nil}
	}
	v, reg, _, cleanup, err := s.check(cs)
	if err != nil {
		return nil, err
	}
	err = s.blockDestructive(ctx, v, reg)
	if err == nil {
		err = s.planApps(ctx, v, reg)
	}
	cleanup()
	if err != nil {
		return nil, err
	}
	cs.Validation, cs.Preview = v, nil
	status, note := "validated", fmt.Sprintf("%d warning(s)", len(v.Warnings))
	if len(v.Errors) > 0 {
		status, note = "invalid", fmt.Sprintf("%d error(s): %s", len(v.Errors), v.Errors[0].Code+" "+v.Errors[0].Message)
	}
	return cs, s.transition(ctx, cs, a, status, note, false)
}

// blockDestructive adds a blocking error for each destructive migration step the change would need (a retype
// that may lose data). Change sets never apply destructive DDL: that is an operator step with
// rangka migrate --allow-destructive.
func (s *Service) blockDestructive(ctx context.Context, v *Validation, reg *meta.Registry) error {
	if s.DB == nil || reg == nil || len(v.Errors) > 0 {
		return nil
	}
	live, err := store.Introspect(ctx, s.DB)
	if err != nil {
		return err
	}
	steps, _ := store.PlanSteps(store.Tables(reg), live, time.Now(), false)
	for _, st := range steps {
		if st.Destructive {
			v.Errors = append(v.Errors, meta.Item{Level: "error", Pos: "1:1", Where: "(migration)", Code: "destructive_migration",
				Message: "this change needs a destructive migration that change sets never apply (" + st.SQL + "); an operator reviews it with " +
					"`rangka migrate --dry-run --allow-destructive` and runs `rangka migrate --allow-destructive`"})
		}
	}
	return nil
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
	cs, auto, err := s.preview(ctx, a, id)
	if err != nil || !auto {
		return cs, err
	}
	// A low-risk proposal from Raka is applied once previewed when the company opted in with
	// config.ai.auto_apply: low_risk. The kernel applies it as the system on the company's own setting:
	// Raka's token still cannot apply (Apply refuses an assistant actor). Apply takes s.mu itself.
	return s.Apply(ctx, Actor{ID: "system", Org: a.Org, Kind: "system"}, cs.ID)
}

// preview is Preview under s.mu (not while Apply or Revert of this kernel writes files; audit changeset followup 1);
// auto reports that the change set is to be applied right after.
func (s *Service) preview(ctx context.Context, a Actor, id string) (cs *ChangeSet, auto bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cs, err = s.Get(ctx, a.Org, id)
	if err != nil {
		return nil, false, err
	}
	if !in(cs.Status, "proposed", "validated", "previewed") {
		return nil, false, &Error{409, "bad_status", "a change set that is " + cs.Status + " cannot be previewed", nil}
	}
	v, reg, _, cleanup, err := s.check(cs)
	if err != nil {
		return nil, false, err
	}
	defer cleanup()
	if err := s.blockDestructive(ctx, v, reg); err != nil {
		return nil, false, err
	}
	cs.Validation = v
	live0 := s.live()
	after, _ := live0.result(cs.Ops)
	if len(v.Errors) > 0 {
		cs.Preview = nil
		if err := s.transition(ctx, cs, a, "invalid", fmt.Sprintf("%d error(s)", len(v.Errors)), false); err != nil {
			return nil, false, err
		}
		return cs, false, &Error{409, "invalid", "the change set has validation errors", cs}
	}
	live, err := store.Introspect(ctx, s.DB)
	if err != nil {
		return nil, false, err
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
		if !strings.Contains(op.Path, "/models/") && !strings.HasPrefix(op.Path, "models/") && !strings.HasPrefix(op.Path, "custom/") || isAppOverlay(op.Path) {
			continue
		}
		model := strings.TrimSuffix(filepath.Base(op.Path), ".rk")
		if _, ok := live.Columns[store.TableName(model)]; !ok {
			p.Affected[model] = 0
			continue
		}
		n, err := store.Count(ctx, s.DB, cs.Org, model)
		if err != nil {
			return nil, false, err
		}
		p.Affected[model] = n
		if op.Op == "delete" && n > 0 {
			p.Reversible = false
			p.Notes = append(p.Notes, fmt.Sprintf("deleting %s hides %d record(s); the table is kept", model, n))
		}
	}
	if err := s.modulesDiff(ctx, cs, live, reg, p); err != nil {
		return nil, false, err
	}
	// an upgrade that locks a field an installed company flow writes: the flow was approved before the lock and goes on
	// writing it for every role (audit flow-locked F4); the approver sees it, and the change set needs an approver
	if s.Reg != nil {
		if ws := newlyLockedFlowWrites(s.Reg(), reg); len(ws) > 0 {
			p.LockedFlows = ws
			p.Notes = append(p.Notes, lockedFlowNote(ws))
			if riskRank[cs.Risk] < riskRank["medium"] {
				cs.Risk = "medium"
			}
		}
	}
	if s.Requires != nil && s.Reg != nil {
		p.Requires = s.Requires(ctx, cs.Org, s.Reg(), reg)
	}
	cs.Preview = p
	if err := s.transition(ctx, cs, a, "previewed", fmt.Sprintf("%d statement(s)", len(stmts)), false); err != nil {
		return cs, false, err
	}
	return cs, a.Assistant && cs.Source == "ai" && cs.Risk == "low" && s.autoApplyLowRisk() && s.planReadOnly(a) == nil, nil
}

// autoApplyLowRisk reports config.ai.auto_apply: low_risk.
func (s *Service) autoApplyLowRisk() bool {
	if s.Reg == nil {
		return false
	}
	v, _ := s.Reg().SiteConfig("ai.auto_apply").(string)
	return v == "low_risk"
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

// ApprovableBy lists the roles that may approve cs: owner alone for high risk, else every approver, sorted.
func (s *Service) ApprovableBy(cs *ChangeSet) []string {
	if cs.Risk == "high" {
		return []string{"owner"}
	}
	out := []string{}
	for r := range s.Approvers() {
		out = append(out, r)
	}
	sort.Strings(out)
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
	if err := s.planReadOnly(a); err != nil {
		return err
	}
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

// planReadOnly refuses a person the plan made read-only (contract 0028 §4): approving, rejecting or applying a
// change set is a decision, and one could raise the very limit that holds them (audit cloud-package-plan F-2).
func (s *Service) planReadOnly(a Actor) error {
	if s.ReadOnly == nil || a.ID == "" {
		return nil
	}
	if msg := s.ReadOnly(a.ID); msg != "" {
		return &Error{403, "plan_read_only", msg, nil}
	}
	return nil
}

// planApps adds a blocking error when the change installs apps past the plan's apps limit (contract 0057 §1).
// Removing apps, or keeping as many, is always allowed.
func (s *Service) planApps(ctx context.Context, v *Validation, reg *meta.Registry) error {
	if s.DB == nil || reg == nil || s.Reg == nil || len(v.Errors) > 0 {
		return nil
	}
	cur, err := plan.Resolve(ctx, s.DB, reg)
	if err != nil {
		return err
	}
	if pe := plan.CheckAppsChange(reg, cur, len(plan.InstalledApps(s.Reg())), len(plan.InstalledApps(reg))); pe != nil {
		v.Errors = append(v.Errors, meta.Item{Level: "error", Pos: "1:1", Where: "config.rk apps", Code: pe.Code, Message: pe.Msg})
	}
	return nil
}

// operatorPlan refuses a change to features.rk while an operator set the tenant's plan (`rangka plan set`,
// contract 0028): the plans are the operator's (a hosting provider's packages), so the business may not raise its
// own limits through a change set (audit cloud-package-plan F-1). The operator changes them: the file, then
// rangka plan set.
func (s *Service) operatorPlan(ctx context.Context, ops []Op) error {
	touches := false
	for _, op := range ops {
		if op.Path == "features.rk" {
			touches = true
		}
	}
	if !touches || s.DB == nil {
		return nil
	}
	st, err := plan.LoadStored(ctx, s.DB)
	if err != nil {
		return err
	}
	if st != nil && st.Source == "operator" {
		return &Error{403, "plan_managed", "Paket usaha ini diatur oleh penyedia layanan, jadi features.rk tidak bisa diubah lewat change set. Hubungi penyedia untuk naik atau ganti paket.", nil}
	}
	return nil
}

// pinPlans pins each op on features.rk to the text it was proposed against (Op.Base): the operator may write its
// plans and run rangka plan set between the proposal and the apply, and the apply would otherwise write over them
// (audit cloud-package-plan 9). result then answers stale_base for a features.rk changed since.
func pinPlans(r roots, ops []Op) {
	for i, op := range ops {
		if op.Path != "features.rk" || op.Base != "" {
			continue
		}
		f, err := r.file(op.Path)
		if err != nil {
			continue
		}
		if c, err := readFile(f); err == nil && c != nil {
			ops[i].Base = store.FileHash(*c)
		}
	}
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
	if err := s.notInSandbox(ctx, a); err != nil {
		return nil, err
	}
	if err := s.mayApprove(a); err != nil {
		return nil, err
	}
	s.mu.Lock() // not while Apply or Revert of this kernel writes files (audit changeset followup 1)
	defer s.mu.Unlock()
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
	s.mu.Lock() // not while Apply or Revert of this kernel writes files (audit changeset followup 1)
	defer s.mu.Unlock()
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
	if err := s.notInSandbox(ctx, a); err != nil {
		return nil, err
	}
	if a.Assistant { // never, not even through config.ai.auto_apply
		return nil, &Error{403, "ai_forbidden", auth.AIForbiddenMessage, nil}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cs, err := s.Get(ctx, a.Org, id)
	if err != nil {
		return nil, err
	}
	// config.ai.auto_apply: low_risk lets the kernel apply a low-risk change set as the system right after its
	// preview (contract 0020 §4), and an approver apply one without a separate approval; anyone else needs an
	// approved change set (audit C3-1 F4)
	autoApply := s.autoApplyLowRisk() && cs.Risk == "low" && in(cs.Status, "validated", "previewed") &&
		(a.Kind == "system" || s.isApprover(a))
	if cs.Status != "approved" && !autoApply {
		return nil, &Error{409, "not_approved", "apply needs an approved change set (it is " + cs.Status + ")", nil}
	}
	if cs.Status == "approved" && !s.isApprover(a) {
		return nil, &Error{403, "not_approver", "only an approver applies an approved change set", nil}
	}
	if err := s.planReadOnly(a); err != nil {
		return nil, err
	}
	// the operator may have taken over the plan since the proposal
	if err := s.operatorPlan(ctx, cs.Ops); err != nil {
		return nil, err
	}
	v, after, _, cleanup, err := s.check(cs)
	if err != nil {
		return nil, err
	}
	// the rows of the units the change set removes are counted again now: rows written since the preview, or a
	// preview from before the kernel kept that risk, would otherwise not raise it (audit changeset-followups 2)
	var rows *ChangeSet
	if len(v.Errors) == 0 {
		rows = &ChangeSet{Ops: cs.Ops, Org: cs.Org, Risk: "low"}
		if err := s.removedRows(ctx, rows, after); err != nil {
			cleanup()
			return nil, err
		}
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
	// a features.rk op proposed before plans were pinned (pinPlans) carries no base: if the file exists it may hold
	// the operator's writes since, so the change set is proposed again (audit changeset-followups 4)
	for _, op := range cs.Ops {
		if op.Path != "features.rk" || op.Base != "" {
			continue
		}
		if f, err := live.file(op.Path); err == nil {
			if c, _ := readFile(f); c != nil {
				return nil, staleBase(op.Path)
			}
		}
	}
	files, err := live.result(cs.Ops)
	if e, ok := err.(*Error); ok {
		return nil, e
	}
	if err != nil {
		return nil, &Error{409, "bad_ops", err.Error(), nil}
	}
	// the files may have changed since the proposal (another change set, the operator): assess the change set again
	// against them, and send it back for approval when its risk rose (audit changeset-set-once, note 2)
	as := AssessWith(cs.Ops, parsed(live, nil), parsed(live, files), Env{Model: s.appModel, Installed: s.installedFor, ModuleOf: s.moduleOf})
	if rows != nil && riskRank[rows.Risk] > riskRank[as.Risk] {
		as.Risk = rows.Risk
		as.Reasons = append(as.Reasons, "high: a unit the change set removes still holds rows")
	}
	if riskRank[as.Risk] > riskRank[cs.Risk] {
		was := cs.Risk
		cs.Risk, cs.Layers = as.Risk, as.Layers
		if cs.Layers == nil {
			cs.Layers = []int{}
		}
		note := "risk rose from " + was + " to " + as.Risk + " since the proposal (the files changed); preview and approve again"
		if len(as.Reasons) > 0 {
			note += ": " + strings.Join(as.Reasons, "; ")
		}
		if err := s.transition(ctx, cs, a, "validated", note, false); err != nil {
			return nil, err
		}
		return cs, &Error{409, "risk_changed", "Risiko perubahan ini naik dari " + was + " ke " + as.Risk + " karena berkasnya berubah sejak diusulkan. Lihat pratinjau dan setujui lagi.", cs}
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
	cs.applied = map[string]string{}
	for p, c := range files {
		cs.applied[p] = ""
		if c != nil {
			cs.applied[p] = store.FileHash(*c)
		}
	}
	note := "applied"
	if autoApply && cs.Status != "approved" {
		note = "applied automatically (config.ai.auto_apply: low_risk)"
	}
	if err := s.transition(ctx, cs, a, "applied", note, false); err != nil {
		// another kernel process moved the change set on (rejected it) while its files were written: the files go
		// back, so the live metadata matches the row, which does not say applied (audit changeset followup 1; within
		// one process s.mu keeps every step out while Apply runs)
		if e, ok := err.(*Error); ok && e.Code == "stale_status" {
			s.write(live, prev)
			msg := "Change set ini diubah langkah lain saat sedang diterapkan; berkasnya dikembalikan. Tabel atau kolom yang sudah ditambahkan tetap ada."
			if rerr := s.Reload(ctx); rerr != nil {
				msg += " Memuat ulang metadata gagal: " + rerr.Error()
			}
			return cs, &Error{409, "stale_status", msg, nil}
		}
		return cs, err
	}
	return cs, nil
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
	if err := s.notInSandbox(ctx, a); err != nil {
		return nil, err
	}
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
	// restoring an older features.rk would undo the operator's plans as surely as a new change set (audit
	// cloud-package-plan F-5)
	restores := make([]Op, 0, len(cs.previous))
	for p := range cs.previous {
		restores = append(restores, Op{Op: "replace", Path: p})
	}
	if err := s.operatorPlan(ctx, restores); err != nil {
		return nil, err
	}
	live := s.live()
	current := map[string]*string{}
	for p := range cs.previous {
		f, _ := live.file(p)
		c, _ := readFile(f)
		current[p] = c
	}
	// a file changed since the apply (another change set, the operator's plans): restoring the older text would undo
	// that change unseen (a lost update), so revert refuses; a change set applied before the hashes were kept is not checked
	if cs.applied != nil {
		paths := make([]string, 0, len(current))
		for p := range current {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			h, ok := cs.applied[p]
			if !ok {
				continue
			}
			now := ""
			if current[p] != nil {
				now = store.FileHash(*current[p])
			}
			if now != h {
				return nil, &Error{409, "stale_base", "Berkas " + p + " sudah berubah sejak perubahan ini diterapkan; mengembalikannya akan menimpa perubahan itu. Buat change set baru.", map[string]any{"path": p}}
			}
		}
	}
	// undoing a high-risk change (tax, roles, money fields) is as much the owner's decision as making it (audit
	// orgs-risk A1). A change set applied before the hashes were kept skipped the check above, so the files may hold
	// later changes the restore would undo: the restore itself is assessed against the current files (audit D1).
	risk := cs.Risk
	if cs.applied == nil {
		ops := make([]Op, 0, len(cs.previous))
		for p, c := range cs.previous {
			op := "replace"
			if c == nil {
				op = "delete"
			}
			ops = append(ops, Op{Op: op, Path: p})
		}
		sort.Slice(ops, func(i, j int) bool { return ops[i].Path < ops[j].Path })
		as := AssessWith(ops, parsed(live, nil), parsed(live, cs.previous), Env{Model: s.appModel, Installed: s.installedFor, ModuleOf: s.moduleOf})
		if riskRank[as.Risk] > riskRank[risk] {
			risk = as.Risk
		}
	}
	// a revert that restores an older config.rk removes the units the change set added: their rows are counted now, as
	// apply does, so a unit that holds rows is the owner's to remove (apply-rows audit note 3)
	if prev, ok := cs.previous["config.rk"]; ok && prev != nil && riskRank[risk] < riskRank["high"] {
		back := &ChangeSet{Ops: []Op{{Op: "replace", Path: "config.rk", Content: *prev}}, Org: cs.Org, Risk: "low"}
		v, after, _, cleanup, err := s.check(back)
		if err != nil {
			return nil, err
		}
		if len(v.Errors) == 0 {
			err = s.removedRows(ctx, back, after)
		}
		cleanup()
		if err != nil {
			return nil, err
		}
		if back.Risk == "high" {
			risk = "high"
		}
	}
	if risk == "high" && !hasRole(a, "owner") {
		return nil, &Error{403, "owner_required", "reverting a high-risk change set needs the owner", nil}
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

// notInSandbox refuses a change set decided from the ruang coba (contract 0052 §2): metadata belongs to the site
// and is shared with the real org, so a change made "in the sandbox" would change the real business.
func (s *Service) notInSandbox(ctx context.Context, a Actor) error {
	sb, err := store.IsSandbox(ctx, s.DB, a.Org)
	if err != nil {
		return err
	}
	if sb {
		return &Error{409, "sandbox_readonly_meta", "Ruang coba tidak bisa mengubah sistem. Pindah ke usaha asli dulu untuk mengusulkan atau menerapkan perubahan.", nil}
	}
	return nil
}
