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
// that is not finished (wait:, approve:), which holds the key until it is, with marker (the run's pending marker).
// A resumed run drops its own marker (and the bare one of a run that waited before markers had tokens).
func AddFlowKeyDocs(ctx context.Context, q pg.Querier, org, flow, key string, docs []string, replace, waiting bool, marker string) error {
	if docs == nil {
		docs = []string{}
	}
	if waiting {
		docs = append(docs, marker)
	}
	if replace {
		_, err := q.Exec(ctx, `update rk_flow_keys set docs = $4::text[], at = now() where org_id = $1 and flow = $2 and key = $3`,
			org, flow, key, pg.Array(docs))
		return err
	}
	_, err := q.Exec(ctx, `update rk_flow_keys set docs = array_remove(array_remove(docs, $5::text), '`+flowKeyPending+`') || $4::text[], at = now()
		where org_id = $1 and flow = $2 and key = $3`, org, flow, key, pg.Array(docs), marker)
	return err
}

// FlowKeyHeld reports whether key of flow still carries a waiting run's pending marker: false once the run was
// released (ReleaseFlowKey) or the key removed.
func FlowKeyHeld(ctx context.Context, q pg.Querier, org, flow, key, marker string) (bool, error) {
	res, err := q.Query(ctx, `select 1 from rk_flow_keys where org_id = $1 and flow = $2 and key = $3 and $4::text = any(docs) for update`, org, flow, key, marker)
	if err != nil {
		return false, err
	}
	return len(res.Rows) == 1, nil
}

// ReleaseFlowKey ends a waiting run that will never finish: its pending marker leaves the key's documents, and a key
// left with none is removed, so the same event is recorded again. A key that no longer carries marker (another run
// holds it now) is left alone.
func ReleaseFlowKey(ctx context.Context, q pg.Querier, org, flow, key, marker string) error {
	res, err := q.Query(ctx, `update rk_flow_keys set docs = array_remove(docs, $4::text), at = now()
		where org_id = $1 and flow = $2 and key = $3 and $4::text = any(docs) returning cardinality(docs)::bigint`, org, flow, key, marker)
	if err != nil || len(res.Rows) == 0 {
		return err
	}
	if n, _ := res.Rows[0][0].(int64); n == 0 {
		_, err = q.Exec(ctx, `delete from rk_flow_keys where org_id = $1 and flow = $2 and key = $3 and cardinality(docs) = 0`, org, flow, key)
	}
	return err
}

// RenameFlowKeyDocs follows a submitted draft's rename in the documents of flow keys.
func RenameFlowKeyDocs(ctx context.Context, q pg.Querier, org, model, from, to string) error {
	_, err := q.Exec(ctx, `update rk_flow_keys set docs = array_replace(docs, $2, $3) where org_id = $1 and $2 = any(docs)`,
		org, model+"/"+from, model+"/"+to)
	return err
}
