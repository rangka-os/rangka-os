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
