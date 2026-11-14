package store

import (
	"context"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// Head-office exceptions (contract 0058 §10).

// Exception is one thing an outlet's work left for head office: a kind (edge_price_drift, negative_stock,
// device_number_gap, rule_overridden), the record it is about and free detail.
type Exception struct {
	Org, Outlet, Device, Kind, Ref string
	Detail                         map[string]any
}

// ExceptionRow is an exception as listed.
type ExceptionRow struct {
	ID         int64          `json:"id"`
	Outlet     string         `json:"outlet"`
	Device     string         `json:"device"`
	Kind       string         `json:"kind"`
	Ref        string         `json:"ref"`
	Detail     map[string]any `json:"detail"`
	CreatedAt  time.Time      `json:"created_at"`
	ResolvedBy *string        `json:"resolved_by"`
	ResolvedAt *time.Time     `json:"resolved_at"`
}

// RecordException stores x; it takes a Querier so a caller can write it in its own transaction.
func RecordException(ctx context.Context, q pg.Querier, x Exception) error {
	d := x.Detail
	if d == nil {
		d = map[string]any{}
	}
	_, err := q.Query(ctx, `insert into rk_exceptions (org_id, outlet, device, kind, ref, detail) values ($1, $2, $3, $4, $5, $6)`,
		x.Org, x.Outlet, x.Device, x.Kind, x.Ref, pg.JSON{V: d})
	return err
}

// ListExceptions returns the org's exceptions, newest first; outlet and kind filter when not empty, open keeps
// the unresolved.
func ListExceptions(ctx context.Context, q pg.Querier, org, outlet, kind string, open bool) ([]ExceptionRow, error) {
	res, err := q.Query(ctx, `select id, outlet, device, kind, ref, detail, created_at, resolved_by, resolved_at from rk_exceptions
		where org_id = $1 and ($2 = '' or outlet = $2) and ($3 = '' or kind = $3) and (not $4 or resolved_at is null)
		order by created_at desc, id desc`, org, outlet, kind, open)
	if err != nil {
		return nil, err
	}
	out := []ExceptionRow{}
	for _, r := range res.Rows {
		x := ExceptionRow{ID: r[0].(int64), Outlet: r[1].(string), Device: r[2].(string), Kind: r[3].(string), Ref: r[4].(string)}
		x.Detail, _ = r[5].(map[string]any)
		if x.Detail == nil {
			x.Detail = map[string]any{}
		}
		x.CreatedAt, _ = r[6].(time.Time)
		if s, ok := r[7].(string); ok {
			x.ResolvedBy = &s
		}
		if t, ok := r[8].(time.Time); ok {
			x.ResolvedAt = &t
		}
		out = append(out, x)
	}
	return out, nil
}

// ResolveException marks an open exception of org resolved by who; an already resolved one keeps its first
// resolver. It reports false when org has no such exception.
func ResolveException(ctx context.Context, q pg.Querier, org string, id int64, who string) (bool, error) {
	if _, err := q.Query(ctx, `update rk_exceptions set resolved_by = $3, resolved_at = now() where org_id = $1 and id = $2 and resolved_at is null`,
		org, id, who); err != nil {
		return false, err
	}
	res, err := q.Query(ctx, `select 1 from rk_exceptions where org_id = $1 and id = $2`, org, id)
	if err != nil {
		return false, err
	}
	return len(res.Rows) > 0, nil
}
