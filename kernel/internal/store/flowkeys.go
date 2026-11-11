package store

import (
	"context"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// flowKeyPending marks a key whose run waits (wait:, approve:); see engine.flowKeyPending.
const flowKeyPending = "flow:pending"

// ClaimFlowKey takes key of flow in org for the run in q's transaction (contract 0002 §A.2, idempotency:). A new
// key is inserted; a key a committed run holds is locked (a concurrent run with the same key waits for this
// transaction) and its documents returned, for the caller to decide whether the key is free again. fresh is true
// for a new key.
func ClaimFlowKey(ctx context.Context, q pg.Querier, org, flow, key string) (fresh bool, docs []string, err error) {
	for attempt := 0; attempt < 3; attempt++ {
		res, err := q.Query(ctx, `insert into rk_flow_keys (org_id, flow, key) values ($1, $2, $3) on conflict do nothing returning key`, org, flow, key)
		if err != nil {
			return false, nil, err
		}
		if len(res.Rows) == 1 {
			return true, nil, nil
		}
		res, err = q.Query(ctx, `select docs from rk_flow_keys where org_id = $1 and flow = $2 and key = $3 for update`, org, flow, key)
		if err != nil {
			return false, nil, err
		}
		if len(res.Rows) == 0 {
			continue // removed meanwhile: insert again
		}
		if xs, ok := res.Rows[0][0].([]any); ok {
			for _, x := range xs {
				if s, ok := x.(string); ok {
					docs = append(docs, s)
				}
			}
		}
		return false, docs, nil
	}
	return false, nil, ErrNotFound
}

// AddFlowKeyDocs records documents (<model>/<name>) the run holding key created: replace for a run that claimed
// the key (an earlier run's documents are gone), else added to those of the run it resumes. waiting marks a run
// that is not finished (wait:, approve:), which holds the key until it is.
func AddFlowKeyDocs(ctx context.Context, q pg.Querier, org, flow, key string, docs []string, replace, waiting bool) error {
	if docs == nil {
		docs = []string{}
	}
	if waiting {
		docs = append(docs, flowKeyPending)
	}
	base := `array_remove(docs, '` + flowKeyPending + `')`
	if replace {
		base = `'{}'::text[]`
	}
	_, err := q.Exec(ctx, `update rk_flow_keys set docs = `+base+` || $4::text[], at = now() where org_id = $1 and flow = $2 and key = $3`,
		org, flow, key, pg.Array(docs))
	return err
}

// RenameFlowKeyDocs follows a submitted draft's rename in the documents of flow keys.
func RenameFlowKeyDocs(ctx context.Context, q pg.Querier, org, model, from, to string) error {
	_, err := q.Exec(ctx, `update rk_flow_keys set docs = array_replace(docs, $2, $3) where org_id = $1 and $2 = any(docs)`,
		org, model+"/"+from, model+"/"+to)
	return err
}
