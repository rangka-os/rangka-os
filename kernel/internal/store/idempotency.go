package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// Idempotent writes (contract 0030 §6). A key is kept for IdempotencyTTL; a pending claim older than
// IdempotencyStale belongs to a request whose transaction never committed (the commit marks it committed), so it may
// be claimed again.
const (
	IdempotencyTTL   = 7 * 24 * time.Hour
	IdempotencyStale = 2 * time.Minute
)

// Idem is the stored state of one key.
type Idem struct {
	State, Hash, Target string
	Status              int
	Response            json.RawMessage
	Created             time.Time
}

// ErrIdemTaken reports that the key already has a row (see the returned Idem).
var ErrIdemTaken = errors.New("store: idempotency key taken")

// ClaimIdempotency inserts a pending row for the key. When the key is taken it returns the stored row and
// ErrIdemTaken, except that an expired row (older than IdempotencyTTL) or a stale pending claim is taken over.
func ClaimIdempotency(ctx context.Context, q pg.Querier, org, actor, key, hash string) (*Idem, error) {
	ttl := int64(IdempotencyTTL / time.Second)
	stale := int64(IdempotencyStale / time.Second)
	// take over an expired row or a stale pending claim, else insert; an existing live row is left alone
	res, err := q.Query(ctx, `insert into rk_idempotency (org_id, actor, key, request_hash, state) values ($1, $2, $3, $4, 'pending')
		on conflict (org_id, actor, key) do update set request_hash = excluded.request_hash, state = 'pending', target = '', status = 0,
		  response = null, created_at = now()
		where rk_idempotency.created_at < now() - make_interval(secs => $5)
		   or (rk_idempotency.state = 'pending' and rk_idempotency.created_at < now() - make_interval(secs => $6))
		returning state`, org, actor, key, hash, ttl, stale)
	if err != nil {
		return nil, err
	}
	if len(res.Rows) == 1 {
		return &Idem{State: "pending", Hash: hash}, nil
	}
	res, err = q.Query(ctx, `select state, request_hash, target, status, response, created_at from rk_idempotency where org_id = $1 and actor = $2 and key = $3`, org, actor, key)
	if err != nil {
		return nil, err
	}
	if len(res.Rows) == 0 { // deleted between the two statements (a release): try once more
		return ClaimIdempotency(ctx, q, org, actor, key, hash)
	}
	row := res.Rows[0]
	it := &Idem{State: str(row[0]), Hash: str(row[1]), Target: str(row[2]), Status: int(toInt(row[3]))}
	if row[4] != nil {
		b, _ := json.Marshal(row[4])
		it.Response = b
	}
	if t, ok := row[5].(time.Time); ok {
		it.Created = t
	}
	return it, ErrIdemTaken
}

// CommitIdempotency marks a pending key committed with the record it wrote; call it inside the writing transaction.
func CommitIdempotency(ctx context.Context, q pg.Querier, org, actor, key, target string) error {
	_, err := q.Query(ctx, `update rk_idempotency set state = 'committed', target = $4 where org_id = $1 and actor = $2 and key = $3 and state = 'pending'`,
		org, actor, key, target)
	return err
}

// FinishIdempotency stores the answer of a successful request.
func FinishIdempotency(ctx context.Context, q pg.Querier, org, actor, key string, status int, response json.RawMessage) error {
	_, err := q.Query(ctx, `update rk_idempotency set state = 'done', status = $4, response = $5 where org_id = $1 and actor = $2 and key = $3`,
		org, actor, key, status, pg.JSON{V: json.RawMessage(response)})
	return err
}

// ReleaseIdempotency drops a pending claim whose request failed before it wrote anything, so the key may run again.
func ReleaseIdempotency(ctx context.Context, q pg.Querier, org, actor, key string) error {
	_, err := q.Query(ctx, `delete from rk_idempotency where org_id = $1 and actor = $2 and key = $3 and state = 'pending'`, org, actor, key)
	return err
}

// PruneIdempotency deletes keys older than IdempotencyTTL.
func PruneIdempotency(ctx context.Context, q pg.Querier) error {
	_, err := q.Query(ctx, `delete from rk_idempotency where created_at < now() - make_interval(secs => $1)`, int64(IdempotencyTTL/time.Second))
	return err
}
