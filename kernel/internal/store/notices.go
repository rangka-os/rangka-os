package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// NoticeKeepDays is how long the inbox keeps a notice (contract 0066 §4.1).
const NoticeKeepDays = 90

// NoticeRatePerMinute is the kernel's own cap per organisation and minute, inbox and host together (§3).
const NoticeRatePerMinute = 60

// Notice is one inbox row as the API shows it (contract 0066 §4.1).
type Notice struct {
	ID       string         `json:"id"`
	Kind     string         `json:"kind"`
	Severity string         `json:"severity"`
	Title    string         `json:"title"`
	Text     string         `json:"text"`
	Link     string         `json:"link"`
	Data     map[string]any `json:"-"`
	At       time.Time      `json:"at"`
	Read     bool           `json:"read"`
}

// NewNoticeID is ntc_<random>.
func NewNoticeID() string {
	b := make([]byte, 10)
	rand.Read(b)
	return "ntc_" + hex.EncodeToString(b)
}

// InsertNotice writes one recipient's row; a row for the same event and person already there is kept (the event's
// id is the dedupe key). It returns the new row's id, "" when it was a duplicate.
func InsertNotice(ctx context.Context, q pg.Querier, org, eventID string, userID int64, n Notice) (string, error) {
	if n.ID == "" {
		n.ID = NewNoticeID()
	}
	data := n.Data
	if data == nil {
		data = map[string]any{}
	}
	res, err := q.Query(ctx, `insert into rk_notices (id, org_id, event_id, user_id, kind, severity, title, text, link, data, at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) on conflict (org_id, event_id, user_id) do nothing returning id`,
		n.ID, org, eventID, userID, n.Kind, n.Severity, n.Title, n.Text, n.Link, pg.JSON{V: data}, n.At)
	if err != nil || len(res.Rows) == 0 {
		return "", err
	}
	return n.ID, nil
}

// NoticeFilter narrows ListNotices.
type NoticeFilter struct {
	Org    string
	User   int64
	Limit  int
	Before string // a notice id: rows older than it
	Unread bool
}

// ListNotices returns a person's newest notices, the number of their unread ones and the cursor of the next
// page ("" when there is none).
func ListNotices(ctx context.Context, q pg.Querier, f NoticeFilter) ([]Notice, int64, string, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	res, err := q.Query(ctx, `select n.id, n.kind, n.severity, n.title, n.text, n.link, n.data, n.at, n.read_at is not null
		from rk_notices n where n.org_id = $1 and n.user_id = $2 and (not $3 or n.read_at is null)
		and ($4 = '' or n.seq < (select b.seq from rk_notices b where b.org_id = $1 and b.user_id = $2 and b.id = $4))
		order by n.seq desc limit $5`, f.Org, f.User, f.Unread, f.Before, f.Limit+1)
	if err != nil {
		return nil, 0, "", err
	}
	out := []Notice{}
	for _, r := range res.Rows {
		n := Notice{ID: str(r[0]), Kind: str(r[1]), Severity: str(r[2]), Title: str(r[3]), Text: str(r[4]), Link: str(r[5])}
		n.Data, _ = r[6].(map[string]any)
		n.At, _ = r[7].(time.Time)
		n.Read, _ = r[8].(bool)
		out = append(out, n)
	}
	next := ""
	if len(out) > f.Limit {
		out = out[:f.Limit]
		next = out[len(out)-1].ID
	}
	unread, err := CountUnreadNotices(ctx, q, f.Org, f.User)
	return out, unread, next, err
}

// CountUnreadNotices counts a person's unread notices.
func CountUnreadNotices(ctx context.Context, q pg.Querier, org string, user int64) (int64, error) {
	res, err := q.Query(ctx, "select count(*) from rk_notices where org_id = $1 and user_id = $2 and read_at is null", org, user)
	if err != nil || len(res.Rows) == 0 {
		return 0, err
	}
	n, _ := res.Rows[0][0].(int64)
	return n, nil
}

// MarkNoticesRead marks a person's notices read: ids, or every unread one when ids is empty. It returns how many
// changed.
func MarkNoticesRead(ctx context.Context, q pg.Querier, org string, user int64, ids []string, at time.Time) (int64, error) {
	all := len(ids) == 0
	if ids == nil {
		ids = []string{}
	}
	res, err := q.Query(ctx, `with u as (update rk_notices set read_at = $5 where org_id = $1 and user_id = $2 and read_at is null
		and ($3 or id = any($4)) returning 1) select count(*) from u`, org, user, all, pg.Array(ids), at)
	if err != nil || len(res.Rows) == 0 {
		return 0, err
	}
	n, _ := res.Rows[0][0].(int64)
	return n, nil
}

// DeleteMemberNotices removes a person's notices in an org (their membership ended, contract 0066 §4.1).
func DeleteMemberNotices(ctx context.Context, q pg.Querier, org string, user int64) error {
	if _, err := q.Exec(ctx, "delete from rk_notices where org_id = $1 and user_id = $2", org, user); err != nil {
		return err
	}
	_, err := q.Exec(ctx, "delete from rk_notice_prefs where org_id = $1 and user_id = $2", org, user)
	return err
}

// PruneNotices removes notices older than NoticeKeepDays and rate rows older than a day.
func PruneNotices(ctx context.Context, q pg.Querier, now time.Time) error {
	if _, err := q.Exec(ctx, "delete from rk_notices where at < $1", now.Add(-NoticeKeepDays*24*time.Hour)); err != nil {
		return err
	}
	_, err := q.Exec(ctx, "delete from rk_notice_rate where minute < $1 and (audited or dropped = '{}'::jsonb)", now.Add(-24*time.Hour))
	return err
}

// TakeNoticeSlot counts one notice of org against the minute of now; ok is false past NoticeRatePerMinute, and
// the drop is counted under kind for the notice.dropped audit entry. It runs on its own connection (not the
// emitting transaction), so a busy org never waits on another transaction's counter row.
func TakeNoticeSlot(ctx context.Context, q pg.Querier, org, kind string, now time.Time) (bool, error) {
	minute := now.UTC().Truncate(time.Minute)
	res, err := q.Query(ctx, `insert into rk_notice_rate (org_id, minute, n) values ($1, $2, 1)
		on conflict (org_id, minute) do update set n = rk_notice_rate.n + 1 returning n`, org, minute)
	if err != nil || len(res.Rows) == 0 {
		return true, err
	}
	if n, _ := res.Rows[0][0].(int64); n <= NoticeRatePerMinute {
		return true, nil
	}
	_, err = q.Exec(ctx, `update rk_notice_rate set dropped = jsonb_set(dropped, array[$3::text], to_jsonb(coalesce((dropped ->> $3)::int, 0) + 1))
		where org_id = $1 and minute = $2`, org, minute, kind)
	return false, err
}

// FlushDroppedNotices writes one notice.dropped audit entry per past minute that dropped notices (§3): the kinds
// and their counts.
func FlushDroppedNotices(ctx context.Context, q pg.Querier, now time.Time) error {
	res, err := q.Query(ctx, `update rk_notice_rate set audited = true where not audited and dropped <> '{}'::jsonb and minute < $1
		returning org_id, minute, dropped`, now.UTC().Truncate(time.Minute))
	if err != nil {
		return err
	}
	for _, r := range res.Rows {
		kinds, _ := r[2].(map[string]any)
		total := 0
		for _, v := range kinds {
			total += int(toFloat(v))
		}
		minute, _ := r[1].(time.Time)
		b, _ := json.Marshal(kinds)
		if err := Audit(ctx, q, str(r[0]), "system", "notice.dropped", "notices", "lebih dari 60 kabar per menit: "+string(b),
			map[string]any{"minute": minute.UTC().Format(time.RFC3339), "count": total, "kinds": kinds}); err != nil {
			return err
		}
	}
	return nil
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	case json.Number:
		f, _ := x.Float64()
		return f
	}
	return 0
}
