package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Scheduler pause (contract 0070): an operator stops the installation's schedules (cron flows and connector polls)
// until a time, with `rangka scheduler pause`. The state is one row of rk_scheduler_state; every tick reads it.
// While paused, slots that come due are skipped and logged, never caught up later. Jobs already in the queue run.

// MaxPause is the longest pause: until may be at most this far ahead.
const MaxPause = 30 * 24 * time.Hour

// MaxPauseReason is the longest reason, in characters.
const MaxPauseReason = 500

// PauseState is the scheduler's pause as of a moment.
type PauseState struct {
	// Paused: schedules are skipped now (a pause is set and its until has not passed).
	Paused bool
	// Until is when the current pause ends; zero when not paused.
	Until time.Time
	// Reason is the current pause's reason; empty when not paused.
	Reason string
	// By is who set the current pause, or who ended the last one.
	By string
	// Since is when the current pause started, or when the last one ended (resumed or lapsed); zero when the
	// scheduler was never paused.
	Since time.Time
	// From and To bound the last pause that has ended: a slot due in [From, To) was skipped and is never caught
	// up, even by an instance that starts afterwards. Zero when there is none.
	From, To time.Time
	// lapsed: until has passed but the row still says paused (the next tick records the end)
	lapsed bool
}

// JSON is the shape `rangka scheduler status --json` and GET /system/status print.
func (p PauseState) JSON() map[string]any {
	m := map[string]any{"paused": p.Paused, "until": nil, "reason": p.Reason, "since": nil}
	if p.Paused {
		m["until"] = p.Until.UTC().Format(time.RFC3339)
	}
	if !p.Since.IsZero() {
		m["since"] = p.Since.UTC().Format(time.RFC3339)
	}
	if p.By != "" {
		m["by"] = p.By
	}
	return m
}

// skips reports whether a slot due at due is skipped: every slot while paused, and after a pause the slots that
// came due during it.
func (p PauseState) skips(due time.Time) bool {
	if p.Paused {
		return true
	}
	return !p.To.IsZero() && !due.Before(p.From) && due.Before(p.To)
}

// ErrNoPauseTable means rk_scheduler_state does not exist yet: the database has not been migrated by this version.
var ErrNoPauseTable = errors.New("rk_scheduler_state does not exist: run `rangka migrate` first")

// ReadPause reads the pause as of now. A pause whose until has passed reads as not paused (it ended at until).
func ReadPause(ctx context.Context, q pg.Querier, now time.Time) (PauseState, error) {
	res, err := q.Query(ctx, `select paused, paused_until, reason, paused_at, paused_by, resumed_at, resumed_by
		from rk_scheduler_state where id = 1`)
	if err != nil {
		if pg.IsCode(err, "42P01") {
			return PauseState{}, ErrNoPauseTable
		}
		return PauseState{}, err
	}
	if len(res.Rows) == 0 {
		return PauseState{}, nil
	}
	r := res.Rows[0]
	paused, _ := r[0].(bool)
	until, _ := r[1].(time.Time)
	reason, _ := r[2].(string)
	pausedAt, _ := r[3].(time.Time)
	pausedBy, _ := r[4].(string)
	resumedAt, _ := r[5].(time.Time)
	resumedBy, _ := r[6].(string)
	switch {
	case paused && until.After(now):
		return PauseState{Paused: true, Until: until, Reason: reason, By: pausedBy, Since: pausedAt}, nil
	case paused: // lapsed, not yet recorded as resumed
		return PauseState{By: "system", Since: until, From: pausedAt, To: until, lapsed: true}, nil
	case !resumedAt.IsZero():
		return PauseState{By: resumedBy, Since: resumedAt, From: pausedAt, To: resumedAt}, nil
	}
	return PauseState{}, nil
}

// CheckPause validates a pause request: until in the future and at most MaxPause ahead, reason not too long.
func CheckPause(until time.Time, reason string, now time.Time) error {
	if until.IsZero() {
		return fmt.Errorf("a pause needs --until (at most %d days ahead)", int(MaxPause.Hours()/24))
	}
	if !until.After(now) {
		return fmt.Errorf("--until %s is not in the future", until.UTC().Format(time.RFC3339))
	}
	if until.Sub(now) > MaxPause {
		return fmt.Errorf("--until %s is more than %d days ahead", until.UTC().Format(time.RFC3339), int(MaxPause.Hours()/24))
	}
	if utf8.RuneCountInString(reason) > MaxPauseReason {
		return fmt.Errorf("--reason is longer than %d characters", MaxPauseReason)
	}
	return nil
}

// Pause sets (or replaces) the pause until until, audited as scheduler.paused. Pausing again while paused moves
// until and the reason; the pause keeps its start.
func Pause(ctx context.Context, db *pg.Pool, until time.Time, reason, by string, now time.Time) (PauseState, error) {
	reason = strings.TrimSpace(reason)
	if err := CheckPause(until, reason, now); err != nil {
		return PauseState{}, err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return PauseState{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // a no-op after Commit
	cur, err := ReadPause(ctx, tx, now)
	if err != nil {
		return PauseState{}, err
	}
	from := now
	if cur.Paused {
		from = cur.Since
	}
	if _, err := tx.Exec(ctx, `insert into rk_scheduler_state (id, paused, paused_until, reason, paused_at, paused_by, resumed_at, resumed_by)
		values (1, true, $1, $2, $3, $4, null, '')
		on conflict (id) do update set paused = true, paused_until = $1, reason = $2, paused_at = $3, paused_by = $4,
		resumed_at = null, resumed_by = ''`, until, reason, from, by); err != nil {
		return PauseState{}, err
	}
	data := map[string]any{"until": until.UTC().Format(time.RFC3339), "reason": reason}
	if cur.Paused {
		data["previous_until"] = cur.Until.UTC().Format(time.RFC3339)
	}
	if err := store.Audit(ctx, tx, "", by, "scheduler.paused", "scheduler",
		"scheduled jobs paused until "+until.UTC().Format(time.RFC3339), data); err != nil {
		return PauseState{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PauseState{}, err
	}
	return PauseState{Paused: true, Until: until, Reason: reason, By: by, Since: from}, nil
}

// Resume ends the pause now, audited as scheduler.resumed. Resuming a scheduler that is not paused changes nothing
// and is not an error (changed is false); a pause that lapsed is then recorded as ended at its until.
func Resume(ctx context.Context, db *pg.Pool, by string, now time.Time) (st PauseState, changed bool, err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return PauseState{}, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // a no-op after Commit
	res, err := tx.Query(ctx, `update rk_scheduler_state set paused = false, resumed_at = $1, resumed_by = $2
		where id = 1 and paused and paused_until > $1 returning paused_at, paused_until, reason`, now, by)
	if err != nil {
		if pg.IsCode(err, "42P01") {
			return PauseState{}, false, ErrNoPauseTable
		}
		return PauseState{}, false, err
	}
	if len(res.Rows) == 1 {
		from, _ := res.Rows[0][0].(time.Time)
		until, _ := res.Rows[0][1].(time.Time)
		reason, _ := res.Rows[0][2].(string)
		if err := store.Audit(ctx, tx, "", by, "scheduler.resumed", "scheduler", "scheduled jobs resumed",
			map[string]any{"paused_at": from.UTC().Format(time.RFC3339), "until": until.UTC().Format(time.RFC3339), "reason": reason, "lapsed": false}); err != nil {
			return PauseState{}, false, err
		}
		changed = true
	} else if _, err := endLapsed(ctx, tx, now); err != nil {
		return PauseState{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PauseState{}, false, err
	}
	st, err = ReadPause(ctx, db, now)
	return st, changed, err
}

// endLapsed records a pause whose until has passed as resumed at until by "system", with an audit entry. Several
// instances may try at once: only the one whose update matched the row writes the entry.
func endLapsed(ctx context.Context, q pg.Querier, now time.Time) (bool, error) {
	res, err := q.Query(ctx, `update rk_scheduler_state set paused = false, resumed_at = paused_until, resumed_by = 'system'
		where id = 1 and paused and paused_until <= $1 returning paused_at, paused_until, reason`, now)
	if err != nil || len(res.Rows) == 0 {
		return false, err
	}
	from, _ := res.Rows[0][0].(time.Time)
	until, _ := res.Rows[0][1].(time.Time)
	reason, _ := res.Rows[0][2].(string)
	if err := store.Audit(ctx, q, "", "system", "scheduler.resumed", "scheduler", "scheduled jobs resumed: the pause ended",
		map[string]any{"paused_at": from.UTC().Format(time.RFC3339), "until": until.UTC().Format(time.RFC3339), "reason": reason, "lapsed": true}); err != nil {
		return false, err
	}
	return true, nil
}

// pauseState reads the pause for a tick and records a lapsed pause as ended. A database without the table (not
// yet migrated by this version) reads as never paused, logged once.
func (s *Scheduler) pauseState(ctx context.Context, now time.Time) (PauseState, error) {
	if s.Q == nil || s.Q.DB == nil {
		return PauseState{}, nil
	}
	st, err := ReadPause(ctx, s.Q.DB, now)
	if errors.Is(err, ErrNoPauseTable) {
		s.mu.Lock()
		warned := s.noPauseTableWarned
		s.noPauseTableWarned = true
		s.mu.Unlock()
		if !warned {
			s.Log("scheduler: %v; schedules run unpaused until then", err)
		}
		return PauseState{}, nil
	}
	if err != nil {
		return PauseState{}, err
	}
	if st.lapsed {
		if ended, err := s.endLapsedTx(ctx, now); err != nil {
			s.Log("scheduler: record the end of the pause: %v", err)
		} else if ended {
			s.Log("scheduler: the pause ended at %s; schedules run again", st.To.UTC().Format(time.RFC3339))
		}
	}
	s.mu.Lock()
	wasPaused := s.paused
	s.paused = st.Paused
	s.mu.Unlock()
	switch {
	case st.Paused && !wasPaused:
		s.Log("scheduler: paused until %s (%s): scheduled flows and polls are skipped", st.Until.UTC().Format(time.RFC3339), st.Reason)
	case !st.Paused && wasPaused && !st.lapsed:
		s.Log("scheduler: resumed by %s; schedules run again", st.By)
	}
	return st, nil
}

// Paused reports whether the last tick found the scheduler paused.
func (s *Scheduler) Paused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.paused
}

// endLapsedTx runs endLapsed in its own transaction, so the row and its audit entry are written together.
func (s *Scheduler) endLapsedTx(ctx context.Context, now time.Time) (bool, error) {
	tx, err := s.Q.DB.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // a no-op after Commit
	ended, err := endLapsed(ctx, tx, now)
	if err != nil {
		return false, err
	}
	return ended, tx.Commit(ctx)
}
