package store

import (
	"context"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// ClaimFlowKey records that flow ran with key in org (contract 0002 §A.2, idempotency:). It reports false when a
// committed run already holds the key; a concurrent run with the same key waits for the other transaction.
func ClaimFlowKey(ctx context.Context, q pg.Querier, org, flow, key string) (bool, error) {
	res, err := q.Query(ctx, `insert into rk_flow_keys (org_id, flow, key) values ($1, $2, $3) on conflict do nothing returning key`, org, flow, key)
	if err != nil {
		return false, err
	}
	return len(res.Rows) == 1, nil
}
