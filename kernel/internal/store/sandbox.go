package store

import (
	"context"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// IsSandbox reports whether org is a sandbox org (ruang coba, contract 0052). An unknown org is not one.
func IsSandbox(ctx context.Context, q pg.Querier, org string) (bool, error) {
	if org == "" {
		return false, nil
	}
	res, err := q.Query(ctx, "select sandbox from rk_orgs where id = $1", org)
	if err != nil || len(res.Rows) == 0 {
		return false, err
	}
	b, _ := res.Rows[0][0].(bool)
	return b, nil
}
