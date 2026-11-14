package store

import (
	"context"
	"errors"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// Edge events (contract 0057 §4, 0006 §3): every event a paired device sends is stored once per org and key, with
// what the kernel answered, so a replay of the key answers the same.

// EdgeEventTTL is how long a key is kept (0006 §3: at least 400 days).
const EdgeEventTTL = 400 * 24 * time.Hour

// EdgeEvent is one stored event.
type EdgeEvent struct {
	Org, Key, Device string
	Seq              int64
	Kind, Hash       string
	Status, Code     string
	Ref              string
	OccurredAt       time.Time
}

// ErrEdgeKeyTaken reports that the key already has a row (InsertEdgeEvent).
var ErrEdgeKeyTaken = errors.New("store: edge event key taken")

// InsertEdgeEvent stores x; ErrEdgeKeyTaken when (org, key) is already stored. Inside a write's transaction, a
// concurrent insert of the same key waits for that transaction and then reports the key taken.
func InsertEdgeEvent(ctx context.Context, q pg.Querier, x EdgeEvent) error {
	var at any
	if !x.OccurredAt.IsZero() {
		at = x.OccurredAt
	}
	res, err := q.Query(ctx, `insert into rk_edge_events (org_id, key, device, seq, kind, payload_sha256, status, code, ref, occurred_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) on conflict (org_id, key) do nothing returning key`,
		x.Org, x.Key, x.Device, x.Seq, x.Kind, x.Hash, x.Status, x.Code, x.Ref, at)
	if err != nil {
		return err
	}
	if len(res.Rows) == 0 {
		return ErrEdgeKeyTaken
	}
	return nil
}

// EdgeEventOf is the stored event of (org, key), or nil.
func EdgeEventOf(ctx context.Context, q pg.Querier, org, key string) (*EdgeEvent, error) {
	res, err := q.Query(ctx, `select device, seq, kind, payload_sha256, status, code, ref, occurred_at from rk_edge_events where org_id = $1 and key = $2`, org, key)
	if err != nil || len(res.Rows) == 0 {
		return nil, err
	}
	r := res.Rows[0]
	x := &EdgeEvent{Org: org, Key: key, Device: str(r[0]), Seq: toInt(r[1]), Kind: str(r[2]), Hash: str(r[3]), Status: str(r[4]), Code: str(r[5]), Ref: str(r[6])}
	x.OccurredAt, _ = r[7].(time.Time)
	return x, nil
}

// SetEdgeEventStatus changes a stored event's status and code (a write the engine held for approval: Task 6 rejects
// it before it is held).
func SetEdgeEventStatus(ctx context.Context, q pg.Querier, org, key, status, code string) error {
	_, err := q.Query(ctx, `update rk_edge_events set status = $3, code = $4 where org_id = $1 and key = $2`, org, key, status, code)
	return err
}

// PruneEdgeEvents deletes events received more than EdgeEventTTL ago.
func PruneEdgeEvents(ctx context.Context, q pg.Querier) error {
	_, err := q.Query(ctx, `delete from rk_edge_events where received_at < now() - make_interval(secs => $1)`, int64(EdgeEventTTL/time.Second))
	return err
}
