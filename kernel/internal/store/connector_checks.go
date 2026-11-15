package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// ConnectorCheck is the test state of one company connector in one organisation (contract 0033 §3). Hash is the
// content hash of the connector file the state belongs to; a caller treats a different current hash as untested.
type ConnectorCheck struct {
	Org, Connector, Hash string
	State                string // needs_auth | untested | tested; the table holds untested and tested
	TestedAt             *time.Time
	TestedBy             string
	LastResult           json.RawMessage // scrubbed, cut answer or {"error": code, "message": ...}
	WritesConfirmedAt    *time.Time
	WritesConfirmedBy    string
}

// ErrConnectorNotTested is returned by ConfirmConnectorWrites when there is no successful test of that hash in the
// last 24 hours.
var ErrConnectorNotTested = errors.New("connector has no successful test of this hash in the last 24 hours")

// GetConnectorCheck returns the check row, or nil, nil when the connector was never tested.
func GetConnectorCheck(ctx context.Context, q pg.Querier, org, connector string) (*ConnectorCheck, error) {
	res, err := q.Query(ctx, `select hash, state, tested_at, tested_by, last_result::text, writes_confirmed_at, writes_confirmed_by
		from rk_connector_checks where org_id = $1 and connector = $2`, org, connector)
	if err != nil {
		return nil, err
	}
	if len(res.Rows) == 0 {
		return nil, nil
	}
	r := res.Rows[0]
	c := &ConnectorCheck{Org: org, Connector: connector, Hash: str(r[0]), State: str(r[1]), TestedBy: str(r[3]), WritesConfirmedBy: str(r[6])}
	c.TestedAt = timePtr(r[2])
	c.WritesConfirmedAt = timePtr(r[5])
	if s, ok := r[4].(string); ok {
		c.LastResult = json.RawMessage(s)
	}
	return c, nil
}

func timePtr(v any) *time.Time {
	if t, ok := v.(time.Time); ok {
		return &t
	}
	return nil
}

// SaveConnectorTest records a test run of the connector file with the given hash: state tested when ok, else
// untested. The result is kept either way. A new test replaces the row and clears any write confirmation, which is
// only valid for the hash and test it was given on.
func SaveConnectorTest(ctx context.Context, q pg.Querier, org, connector, hash, by string, ok bool, result json.RawMessage) error {
	state := "untested"
	if ok {
		state = "tested"
	}
	var r any
	if len(result) > 0 {
		r = string(result)
	}
	_, err := q.Query(ctx, `insert into rk_connector_checks (org_id, connector, hash, state, tested_at, tested_by, last_result)
		values ($1, $2, $3, $4, now(), $5, $6::jsonb)
		on conflict (org_id, connector) do update set hash = excluded.hash, state = excluded.state, tested_at = excluded.tested_at,
			tested_by = excluded.tested_by, last_result = excluded.last_result, writes_confirmed_at = null, writes_confirmed_by = ''`,
		org, connector, hash, state, by, r)
	return err
}

// ConfirmConnectorWrites marks the writes of the connector confirmed by an owner. It needs a row in state tested for
// exactly this hash, tested within the last 24 hours; otherwise ErrConnectorNotTested.
func ConfirmConnectorWrites(ctx context.Context, q pg.Querier, org, connector, hash, by string) error {
	res, err := q.Query(ctx, `update rk_connector_checks set writes_confirmed_at = now(), writes_confirmed_by = $4
		where org_id = $1 and connector = $2 and hash = $3 and state = 'tested' and tested_at > now() - interval '24 hours'
		returning connector`, org, connector, hash, by)
	if err != nil {
		return err
	}
	if len(res.Rows) == 0 {
		return ErrConnectorNotTested
	}
	return nil
}
