package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Contract 0053 §2: Mulai di sini counts the org's data, not the caller's row scope, and only a boolean (plus the
// time of the oldest matching record) leaves the kernel.

// HasRecords reports whether the org has at least one record of model: one EXISTS query.
func (e *Engine) HasRecords(ctx context.Context, org, model string) (bool, error) {
	res, err := e.DB.Query(pg.WithOrg(ctx, org), fmt.Sprintf("select exists (select 1 from %s where org_id = $1)",
		pg.QuoteIdent(store.TableName(model))), org)
	if err != nil {
		return false, err
	}
	ok, _ := res.Rows[0][0].(bool)
	return ok, nil
}

// StageDone reports whether the org has a record of m matching when (every record when when is ""), and the
// created time of the oldest one. Without when it is one query. With when, the conditions a flow's find can push to
// SQL (status == 'paid', dates, docstatus) filter the rows in the query, and the rest is evaluated per record as
// metadata (no actor grants, no row scope), oldest first, a chunk at a time, stopping at the first match. A record
// whose when fails to evaluate does not match; a database error while it loads a linked record is returned.
func (e *Engine) StageDone(ctx context.Context, org string, m *meta.Model, when string) (bool, time.Time, error) {
	ctx = pg.WithOrg(ctx, org)
	if strings.TrimSpace(when) == "" {
		res, err := e.DB.Query(ctx, fmt.Sprintf("select min(created_at) from %s where org_id = $1", pg.QuoteIdent(store.TableName(m.Name))), org)
		if err != nil {
			return false, time.Time{}, err
		}
		t, ok := res.Rows[0][0].(time.Time)
		return ok, t, nil
	}
	sys := Actor{Org: org, ID: "system", System: true}
	o := &op{ctx: ctx, e: e, actor: sys, reg: e.Reg(), links: map[string]map[string]any{}}
	conds, args := flowFindSQL(o, m, when, nil)
	children := whenReadsTable(m, when)
	var found bool
	var at time.Time
	err := store.ScanOldest(ctx, e.DB, m, org, conds, args, 200, func(r store.Record) (bool, error) {
		if children {
			if err := store.LoadChildren(ctx, e.DB, e.Reg(), org, m, []store.Record{r}); err != nil {
				return true, err
			}
		}
		e.Output(m, r)
		v, err := e.newScope(ctx, e.DB, sys, m, r, o.links).eval(when)
		if err != nil {
			if isLoadError(err) {
				return true, err
			}
			return false, nil
		}
		if v != true {
			return false, nil
		}
		found = true
		at, _ = time.Parse(time.RFC3339, fmt.Sprint(r["created_at"]))
		return true, nil
	})
	if err != nil {
		return false, time.Time{}, err
	}
	return found, at, nil
}

// whenReadsTable reports whether when names a table (child rows) field of m: size(items) as well as items.map(…).
func whenReadsTable(m *meta.Model, when string) bool {
	tables := map[string]bool{}
	for _, f := range m.Fields {
		if f.Type == "table" {
			tables[f.Name] = true
		}
	}
	for _, r := range cel.Idents(when) {
		if r.Kind == "ref" && tables[r.Head] {
			return true
		}
	}
	return false
}

// LinkNeeds is the models named by a required link field of m, other than m itself, without duplicates, in
// field order (contract 0053 §2 needs before filtering by data).
func LinkNeeds(reg *meta.Registry, m *meta.Model) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range m.Fields {
		if f.Type != "link" || !f.Required || f.To == "" || f.To == m.Name || seen[f.To] {
			continue
		}
		if reg.Model(f.To) == nil {
			continue
		}
		seen[f.To] = true
		out = append(out, f.To)
	}
	return out
}

// OnboardingHidden is the org's Mulai di sini flag (contract 0053 §3); false when never set.
func (e *Engine) OnboardingHidden(ctx context.Context, org string) (bool, error) {
	res, err := e.DB.Query(ctx, "select hidden from rk_onboarding where org_id = $1", org)
	if err != nil {
		return false, err
	}
	if len(res.Rows) == 0 {
		return false, nil
	}
	h, _ := res.Rows[0][0].(bool)
	return h, nil
}

// SetOnboardingHidden stores the org's flag.
func (e *Engine) SetOnboardingHidden(ctx context.Context, org string, hidden bool, by string) error {
	_, err := e.DB.Exec(ctx, `insert into rk_onboarding (org_id, hidden, by_user, at) values ($1, $2, $3, now())
		on conflict (org_id) do update set hidden = excluded.hidden, by_user = excluded.by_user, at = now()`, org, hidden, by)
	return err
}
