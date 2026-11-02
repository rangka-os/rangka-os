package store

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// FindOpts tune Find.
type FindOpts struct {
	ForUpdate    bool // lock the matching rows until the transaction ends
	WithChildren bool
}

// Find loads the records of a model whose fields equal the given values (text compare; "docstatus" is allowed
// for submittable models), ordered by name. With ForUpdate the rows are locked (select … for update) so a
// hook can decide on stored values that no concurrent transaction changes.
func Find(ctx context.Context, q pg.Querier, reg *meta.Registry, org string, m *meta.Model, eq map[string]any, opts FindOpts) ([]Record, error) {
	keys := make([]string, 0, len(eq))
	for k := range eq {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	where := []string{"org_id = $1"}
	args := []any{org}
	for _, k := range keys {
		switch {
		case k == "docstatus" && m.IsSubmittable:
			args = append(args, toInt(eq[k]))
			where = append(where, fmt.Sprintf("docstatus = $%d", len(args)))
			continue
		case k == "name":
		default:
			f := m.Field(k)
			if f == nil || !IsColumn(f) {
				return nil, fmt.Errorf("find %s: %s is not a column", m.Name, k)
			}
		}
		v := str(eq[k])
		if v == "" {
			where = append(where, fmt.Sprintf("(%s is null or %s::text = '')", pg.QuoteIdent(k), pg.QuoteIdent(k)))
			continue
		}
		args = append(args, v)
		where = append(where, fmt.Sprintf("%s::text = $%d", pg.QuoteIdent(k), len(args)))
	}
	sql := fmt.Sprintf("select * from %s where %s order by name", pg.QuoteIdent(TableName(m.Name)), strings.Join(where, " and "))
	if opts.ForUpdate {
		sql += " for update"
	}
	res, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(res.Rows))
	for _, row := range res.Maps() {
		out = append(out, fromRow(m, row))
	}
	if opts.WithChildren {
		if err := loadChildren(ctx, q, reg, org, m, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// GetForUpdate locks one record (select … for update) and loads it with its child rows.
func GetForUpdate(ctx context.Context, q pg.Querier, reg *meta.Registry, org string, m *meta.Model, name string) (Record, error) {
	res, err := q.Query(ctx, fmt.Sprintf("select 1 from %s where org_id = $1 and name = $2 for update", pg.QuoteIdent(TableName(m.Name))), org, name)
	if err != nil {
		return nil, err
	}
	if len(res.Rows) == 0 {
		return nil, ErrNotFound
	}
	return Get(ctx, q, reg, org, m, name)
}

// LockKey takes a transaction-scoped advisory lock on a key, for rows that may not exist yet (the first
// stock balance of an outlet and item).
func LockKey(ctx context.Context, q pg.Querier, key string) error {
	_, err := q.Query(ctx, "select pg_advisory_xact_lock(hashtextextended($1, 0))", key)
	return err
}
