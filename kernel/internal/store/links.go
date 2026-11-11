package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// RenameRecordLinks points every link to record from of model at to: link fields of installed models (child
// models included) and company custom link fields (x_*, kept in the x column). Submit uses it when a draft
// (DRAFT-xxx) gets its final name, so a record that linked the draft keeps pointing at the same document.
// Only the link value changes; the linking records are not saved again (no hooks, flows or modified_at).
func RenameRecordLinks(ctx context.Context, q pg.Querier, reg *meta.Registry, org, model, from, to string) error {
	if from == to {
		return nil
	}
	for _, rm := range reg.InstalledModels() {
		tn := pg.QuoteIdent(TableName(rm.Name))
		var sets []string
		var custom []string
		for _, f := range rm.Fields {
			if f.Type != "link" || f.To != model {
				continue
			}
			if strings.HasPrefix(f.Name, "x_") {
				custom = append(custom, f.Name)
				continue
			}
			if !IsColumn(f) {
				continue
			}
			c := pg.QuoteIdent(f.Name)
			sets = append(sets, fmt.Sprintf("%s = case when %s = $2 then $3 else %s end", c, c, c))
		}
		if len(sets) > 0 {
			var where []string
			for _, f := range rm.Fields {
				if f.Type == "link" && f.To == model && IsColumn(f) {
					where = append(where, pg.QuoteIdent(f.Name)+" = $2")
				}
			}
			sql := fmt.Sprintf("update %s set %s where org_id = $1 and (%s)", tn, strings.Join(sets, ", "), strings.Join(where, " or "))
			if _, err := q.Exec(ctx, sql, org, from, to); err != nil {
				return fmt.Errorf("rename links in %s: %w", rm.Name, err)
			}
		}
		for _, f := range custom {
			sql := fmt.Sprintf("update %s set x = jsonb_set(x, $4::text[], to_jsonb($3::text)) where org_id = $1 and x->>$5 = $2", tn)
			if _, err := q.Exec(ctx, sql, org, from, to, []string{f}, f); err != nil {
				return fmt.Errorf("rename links in %s.%s: %w", rm.Name, f, err)
			}
		}
	}
	return nil
}

// RecordsLinking lists the names of rm's records in org that link model's record name from: a link field of rm
// (x_ custom links included) or a link field of one of rm's child rows. Submit uses it to look only at the
// approvals of records the draft rename touches (card t_6e1fa9ae).
func RecordsLinking(ctx context.Context, q pg.Querier, reg *meta.Registry, org string, rm *meta.Model, model, from string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	collect := func(sql string, args ...any) error {
		res, err := q.Query(ctx, sql, args...)
		if err != nil {
			return err
		}
		for _, r := range res.Rows {
			if n, _ := r[0].(string); n != "" && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
		return nil
	}
	where := func(m *meta.Model) []string {
		var w []string
		for _, f := range m.Fields {
			if f.Type != "link" || f.To != model {
				continue
			}
			switch {
			case strings.HasPrefix(f.Name, "x_"):
				w = append(w, "x->>"+pg.QuoteLiteral(f.Name)+" = $2")
			case IsColumn(f):
				w = append(w, pg.QuoteIdent(f.Name)+" = $2")
			}
		}
		return w
	}
	if w := where(rm); len(w) > 0 {
		sql := fmt.Sprintf("select name from %s where org_id = $1 and (%s)", pg.QuoteIdent(TableName(rm.Name)), strings.Join(w, " or "))
		if err := collect(sql, org, from); err != nil {
			return nil, fmt.Errorf("links to %s in %s: %w", model, rm.Name, err)
		}
	}
	for _, f := range rm.Fields {
		if f.Type != "table" {
			continue
		}
		cm := reg.Model(f.Of)
		if cm == nil {
			continue
		}
		if w := where(cm); len(w) > 0 {
			sql := fmt.Sprintf("select distinct parent as name from %s where org_id = $1 and parent_field = $3 and (%s)", pg.QuoteIdent(TableName(cm.Name)), strings.Join(w, " or "))
			if err := collect(sql, org, from, f.Name); err != nil {
				return nil, fmt.Errorf("links to %s in %s.%s: %w", model, rm.Name, f.Name, err)
			}
		}
	}
	return out, nil
}

// LiveReferrer finds a live record that links model's record name: a record of an installed (non-child) model whose
// link field, x_ custom link or child row holds name, and that is not cancelled (docstatus 2). The record itself
// (a self link, its own child rows) does not count. ok is false when nothing live links it. Delete refuses while
// one does, so a draft or a document never points at a name that no longer exists (card t_5a50f661).
func LiveReferrer(ctx context.Context, q pg.Querier, reg *meta.Registry, org, model, name string) (refModel, refName string, ok bool, err error) {
	for _, rm := range reg.InstalledModels() {
		if rm.IsChild {
			continue
		}
		names, err := RecordsLinking(ctx, q, reg, org, rm, model, name)
		if err != nil {
			return "", "", false, err
		}
		var cand []string
		for _, n := range names {
			if !(rm.Name == model && n == name) {
				cand = append(cand, n)
			}
		}
		if len(cand) == 0 {
			continue
		}
		// a child table shared by two parent models gives names of both: keep only rm's own, live records
		live := ""
		if rm.IsSubmittable {
			live = " and docstatus <> 2"
		}
		res, err := q.Query(ctx, fmt.Sprintf("select name from %s where org_id = $1 and name = any($2)%s order by name limit 1",
			pg.QuoteIdent(TableName(rm.Name)), live), org, pg.Array(cand))
		if err != nil {
			return "", "", false, fmt.Errorf("live links to %s in %s: %w", model, rm.Name, err)
		}
		if len(res.Rows) == 1 {
			n, _ := res.Rows[0][0].(string)
			return rm.Name, n, true, nil
		}
	}
	return "", "", false, nil
}
