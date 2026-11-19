package jobs

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

func TestCheckPause(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		until  time.Time
		reason string
		want   string
	}{
		{time.Time{}, "", "needs --until"},
		{now, "", "not in the future"},
		{now.Add(-time.Hour), "", "not in the future"},
		{now.Add(MaxPause + time.Second), "", "more than 30 days"},
		{now.Add(time.Hour), strings.Repeat("x", MaxPauseReason+1), "longer than 500"},
	} {
		if err := CheckPause(c.until, c.reason, now); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("CheckPause(%v, %d chars) = %v, want %q", c.until, len(c.reason), err, c.want)
		}
	}
	if err := CheckPause(now.Add(MaxPause), strings.Repeat("é", MaxPauseReason), now); err != nil {
		t.Fatalf("30 days and 500 characters: %v", err)
	}
}

func TestPauseSkips(t *testing.T) {
	from := time.Date(2026, 11, 19, 7, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Hour)
	if !(PauseState{Paused: true}).skips(from.Add(-time.Hour)) {
		t.Fatal("a paused scheduler skips every due slot")
	}
	ended := PauseState{From: from, To: to}
	for due, want := range map[time.Time]bool{from.Add(-time.Minute): false, from: true, to.Add(-time.Minute): true, to: false} {
		if got := ended.skips(due); got != want {
			t.Errorf("after a pause [%v, %v): skips(%v) = %v, want %v", from, to, due, got, want)
		}
	}
	if (PauseState{}).skips(from) {
		t.Fatal("never paused: nothing is skipped")
	}
}

func auditKinds(t *testing.T, ctx context.Context, s *Scheduler) []store.AuditEntry {
	t.Helper()
	l, err := store.ListAudit(ctx, s.Q.DB, store.AuditFilter{Kind: "scheduler."})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// Pause, status and resume round trip, each audited (contract 0070 §2, §4).
func TestPauseAndResume(t *testing.T) {
	ctx := context.Background()
	db := orgSchedDB(t)
	s := NewScheduler(New(db, 1, 1), time.UTC)
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	st, err := ReadPause(ctx, db, now)
	if err != nil || st.Paused || !st.Since.IsZero() {
		t.Fatalf("fresh: %+v %v", st, err)
	}
	if j := st.JSON(); j["paused"] != false || j["until"] != nil || j["since"] != nil || j["reason"] != "" {
		t.Fatalf("fresh JSON: %v", j)
	}
	if _, err := Pause(ctx, db, now.Add(-time.Minute), "x", "ops", now); err == nil {
		t.Fatal("a pause into the past was accepted")
	}
	until := now.Add(24 * time.Hour)
	if _, err := Pause(ctx, db, until, "  menunggak  ", "staf@rangka.cloud", now); err != nil {
		t.Fatal(err)
	}
	st, err = ReadPause(ctx, db, now.Add(time.Hour))
	if err != nil || !st.Paused || !st.Until.Equal(until) || st.Reason != "menunggak" || !st.Since.Equal(now) || st.By != "staf@rangka.cloud" {
		t.Fatalf("paused: %+v %v", st, err)
	}
	j := st.JSON()
	if j["paused"] != true || j["until"] != "2026-11-20T10:00:00Z" || j["since"] != "2026-11-19T10:00:00Z" || j["reason"] != "menunggak" {
		t.Fatalf("paused JSON: %v", j)
	}
	// pausing again moves until and keeps the start
	later := now.Add(48 * time.Hour)
	if _, err := Pause(ctx, db, later, "diperpanjang", "staf@rangka.cloud", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if st, _ = ReadPause(ctx, db, now.Add(2*time.Hour)); !st.Until.Equal(later) || !st.Since.Equal(now) || st.Reason != "diperpanjang" {
		t.Fatalf("extended: %+v", st)
	}
	at := now.Add(3 * time.Hour)
	st, changed, err := Resume(ctx, db, "staf2@rangka.cloud", at)
	if err != nil || !changed || st.Paused || !st.Since.Equal(at) || st.By != "staf2@rangka.cloud" || !st.From.Equal(now) || !st.To.Equal(at) {
		t.Fatalf("resume: %+v %v %v", st, changed, err)
	}
	if _, changed, err = Resume(ctx, db, "ops", at.Add(time.Minute)); err != nil || changed {
		t.Fatalf("second resume: changed=%v %v", changed, err)
	}
	l := auditKinds(t, ctx, s)
	if len(l) != 3 || l[0].Kind != "scheduler.resumed" || l[0].Actor != "staf2@rangka.cloud" || l[1].Kind != "scheduler.paused" || l[2].Kind != "scheduler.paused" ||
		l[2].Actor != "staf@rangka.cloud" || l[0].Org != "" {
		t.Fatalf("audit: %+v", l)
	}
}

// While paused, due slots of flows and polls are skipped and logged, queued jobs are untouched, and a slot that came
// due during the pause is never caught up: not after resume, not by an instance that starts afterwards and catches
// up 10 minutes. The pause ends by itself at until, recorded once as resumed by system (contract 0070 §3).
func TestSchedulerSkipsWhilePaused(t *testing.T) {
	ctx := context.Background()
	db := orgSchedDB(t)
	clock := time.Date(2026, 11, 19, 6, 50, 0, 0, time.UTC)
	var mu sync.Mutex
	var logged []string
	newSched := func() *Scheduler {
		s := NewScheduler(New(db, 1, 1), time.UTC)
		s.SetNow(func() time.Time { return clock })
		s.Log = func(f string, a ...any) {
			mu.Lock()
			logged = append(logged, f)
			mu.Unlock()
		}
		s.CatchUp = 10 * time.Minute
		s.SetFlows(orgSchedRegAt("*/5 * * * *"), "kopi")
		s.SetPolls("kopi", []PollSpec{{Name: "vendor.sales", Every: 5 * time.Minute}})
		return s
	}
	s := newSched()
	s.Tick(ctx) // 06:50: the 06:50 slot (and catch-up 06:40, 06:45) run
	before := len(flowJobs(t, db))
	if before == 0 {
		t.Fatal("no flow enqueued before the pause")
	}
	if _, err := Pause(ctx, db, time.Date(2026, 11, 19, 7, 10, 0, 0, time.UTC), "uji", "ops", clock.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, c := range []time.Time{time.Date(2026, 11, 19, 6, 55, 1, 0, time.UTC), time.Date(2026, 11, 19, 7, 5, 1, 0, time.UTC)} {
		clock = c
		s.Tick(ctx)
		if !s.Paused() {
			t.Fatalf("%v: Paused() = false", c)
		}
	}
	if got := len(flowJobs(t, db)); got != before {
		t.Fatalf("flows enqueued while paused: %d -> %d", before, got)
	}
	res, _ := db.Query(ctx, "select count(*) from rk_jobs where kind = 'poll'")
	polls := res.Rows[0][0].(int64)
	mu.Lock()
	skipped := 0
	for _, l := range logged {
		if strings.HasPrefix(l, "scheduler: paused: skipped") {
			skipped++
		}
	}
	mu.Unlock()
	if skipped < 4 { // flows 06:55, 07:00, 07:05 and polls
		t.Fatalf("skipped fires logged: %d (%v)", skipped, logged)
	}

	// 07:10: the pause has ended; only the 07:10 slot runs, 06:55 to 07:05 are not caught up
	clock = time.Date(2026, 11, 19, 7, 10, 1, 0, time.UTC)
	s.Tick(ctx)
	if s.Paused() {
		t.Fatal("still paused after until")
	}
	got := flowJobs(t, db)
	if len(got) != before+1 || got[len(got)-1].slot != "2026-11-19T07:10:00Z" {
		t.Fatalf("after the pause: %v", got[before:])
	}
	res, _ = db.Query(ctx, "select count(*) from rk_jobs where kind = 'poll'")
	if n := res.Rows[0][0].(int64); n != polls+1 {
		t.Fatalf("polls after the pause: %d -> %d", polls, n)
	}
	// a fresh instance catching up 10 minutes skips the slots inside the pause too
	r := newSched()
	r.Tick(ctx)
	if again := flowJobs(t, db); len(again) != len(got) {
		t.Fatalf("a new instance caught up paused slots: %v", again[len(got):])
	}
	l := auditKinds(t, ctx, s)
	if len(l) != 2 || l[0].Kind != "scheduler.resumed" || l[0].Actor != "system" {
		t.Fatalf("the lapse must be audited once as resumed by system: %+v", l)
	}
	if st, _ := ReadPause(ctx, db, clock); st.Paused || st.By != "system" || !st.Since.Equal(time.Date(2026, 11, 19, 7, 10, 0, 0, time.UTC)) {
		t.Fatalf("status after the lapse: %+v", st)
	}
}

// A database without rk_scheduler_state (not yet migrated by this version) reads as never paused.
func TestNoPauseTableRunsUnpaused(t *testing.T) {
	ctx := context.Background()
	db := orgSchedDB(t)
	if _, err := db.Exec(ctx, "drop table rk_scheduler_state"); err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 11, 19, 7, 0, 1, 0, time.UTC)
	s := NewScheduler(New(db, 1, 1), time.UTC)
	s.SetNow(func() time.Time { return clock })
	s.CatchUp = time.Minute
	s.SetFlows(orgSchedReg(), "kopi")
	s.Tick(ctx)
	if got := flowJobs(t, db); len(got) != 1 {
		t.Fatalf("without the table the scheduler must still run: %v", got)
	}
	if _, err := ReadPause(ctx, db, clock); err != ErrNoPauseTable {
		t.Fatalf("ReadPause = %v, want ErrNoPauseTable", err)
	}
}
