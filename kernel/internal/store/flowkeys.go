package store

import (
	"context"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// ClaimFlowKey takes key of flow in org for the run in q's transaction (contract 0002 §A.2, idempotency:). A new
// key is inserted; a key a committed run holds is locked (a concurrent run with the same key waits for this
// transaction) and its documents returned, for the caller to decide whether the key is free again. fresh is true
// for a new key.
func ClaimFlowKey(ctx context.Context, q pg.Querier, org, flow, key string) (fresh bool, docs []string, err error) {
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
	if len(res.Rows) == 0 { // removed meanwhile: take it as new
		_, err := q.Exec(ctx, `insert into rk_flow_keys (org_id, flow, key) values ($1, $2, $3) on conflict do nothing`, org, flow, key)
		return err == nil, nil, err
	}
	for _, x := range asList(res.Rows[0][0]) {
		if s, ok := x.(string); ok {
			docs = append(docs, s)
		}
	}
	return false, docs, nil
}

// SetFlowKeyDocs records the documents the run holding key created, replacing those of an earlier run.
func SetFlowKeyDocs(ctx context.Context, q pg.Querier, org, flow, key string, docs []string) error {
	if docs == nil {
		docs = []string{}
	}
	_, err := q.Exec(ctx, `update rk_flow_keys set docs = $4, at = now() where org_id = $1 and flow = $2 and key = $3`, org, flow, key, pg.Array(docs))
	return err
}

// RenameFlowKeyDocs follows a submitted draft's rename in the documents of flow keys.
func RenameFlowKeyDocs(ctx context.Context, q pg.Querier, org, model, from, to string) error {
	_, err := q.Exec(ctx, `update rk_flow_keys set docs = array_replace(docs, $2, $3) where org_id = $1 and $2 = any(docs)`,
		org, model+"/"+from, model+"/"+to)
	return err
}

func asList(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out
	case string: // a text[] read as its literal
		x = strings.Trim(x, "{}")
		if x == "" {
			return nil
		}
		var out []any
		for _, s := range strings.Split(x, ",") {
			out = append(out, strings.Trim(s, `"`))
		}
		return out
	}
	return nil
}
