package jobs

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// orgSchedReg is a registry with one installed scheduled flow (07:00 daily). Org "jkt" sets its own time zone,
// which the scheduler does not follow: the flow's today() reads the site's zone (audit F2).
func orgSchedReg() *meta.Registry {
	return orgSchedRegAt("0 7 * * *")
}

func orgSchedRegAt(cron string) *meta.Registry {
	return &meta.Registry{
		App:       map[string]*meta.App{"users": {}},
		Installed: map[string]bool{"users": true},
		Flows:     map[string]*meta.Flow{"summary_daily": {Name: "summary_daily", App: "users", Schedule: cron}},
		Orgs:      map[string]*meta.OrgDoc{"jkt": {ID: "jkt", Config: map[string]any{"timezone": "Asia/Jayapura"}}},
	}
}

var jakarta, _ = time.LoadLocation("Asia/Jakarta")

func orgSchedDB(t *testing.T) *pg.Pool {
	t.Helper()
	ctx := context.Background()
	db, err := pg.NewPool(ctx, testdb.New(t), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Exec(ctx, store.SystemSQL()); err != nil {
		t.Fatal(err)
	}
	return db
}

// fixedOrgs is an Orgs lister whose answer the test can change.
type fixedOrgs struct {
	mu   sync.Mutex
	list []string
}

func (f *fixedOrgs) set(l ...string) { f.mu.Lock(); f.list = l; f.mu.Unlock() }
func (f *fixedOrgs) get(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.list...), nil
}

type flowJob struct {
	org, flow, slot string
	runAt           time.Time
}

func flowJobs(t *testing.T, db *pg.Pool) []flowJob {
	t.Helper()
	res, err := db.Query(context.Background(), `select org_id, args->>'flow', args->>'scheduled_at', scheduled_at from rk_jobs
		where kind = 'flow' order by org_id, id`)
	if err != nil {
		t.Fatal(err)
	}
	var out []flowJob
	for _, r := range res.Rows {
		out = append(out, flowJob{r[0].(string), r[1].(string), r[2].(string), r[3].(time.Time)})
	}
	return out
}

// Every org on the host gets its scheduled flows at 07:00 in the installation's time zone (an org's own timezone
// does not move it), and a restarted scheduler (catching up 10 minutes) does not enqueue a slot twice
// (rangka-os#263 slice a).
func TestScheduledFlowsRunForEveryOrg(t *testing.T) {
	ctx := context.Background()
	db := orgSchedDB(t)
	orgs := &fixedOrgs{}
	orgs.set("jkt", "other")
	clock := time.Date(2026, 11, 19, 6, 59, 0, 0, jakarta)
	now := func() time.Time { return clock }
	newSched := func() *Scheduler {
		s := NewScheduler(New(db, 1, 1), jakarta) // scheduler.timezone
		s.SetNow(now)
		s.Orgs = orgs.get
		s.SetStagger(5 * time.Minute)
		s.SetFlows(orgSchedReg(), "jkt")
		return s
	}
	s := newSched()
	s.Tick(ctx) // lists the orgs: "other" gets its schedule from now on
	if got := flowJobs(t, db); len(got) != 0 {
		t.Fatalf("jobs before 07:00: %v", got)
	}
	for _, org := range []string{"jkt", "other"} {
		if st := s.Status(ctx, org); len(st) != 1 || st[0]["next_run"] != "2026-11-19T07:00:00+07:00" {
			t.Fatalf("%s status = %v", org, st)
		}
	}
	clock = time.Date(2026, 11, 19, 7, 0, 30, 0, jakarta)
	s.Tick(ctx)
	s.Tick(ctx)
	got := flowJobs(t, db)
	if len(got) != 2 || got[0].org != "jkt" || got[1].org != "other" {
		t.Fatalf("after 07:00: %v", got)
	}
	// two orgs: each run starts at its org's fixed offset inside the 5-minute window
	for _, j := range got {
		if j.slot != "2026-11-19T07:00:00+07:00" {
			t.Fatalf("%s slot %s", j.org, j.slot)
		}
		slot, _ := time.Parse(time.RFC3339, j.slot)
		off := j.runAt.Sub(slot)
		if off < 0 || off >= 5*time.Minute {
			t.Fatalf("%s runs %v after its slot, want within [0, 5m)", j.org, off)
		}
		if want := s.offset(&Schedule{Org: j.org, Cron: s.items[0].Cron}, slot, 2, 5*time.Minute); off != want {
			t.Fatalf("%s offset %v, want the org's fixed %v", j.org, off, want)
		}
	}

	// a restart at the same minute, catching up 10 minutes, enqueues nothing new
	r := newSched()
	r.CatchUp = 10 * time.Minute
	r.SetFlows(orgSchedReg(), "jkt")
	r.Tick(ctx)
	if again := flowJobs(t, db); len(again) != 2 {
		t.Fatalf("restart enqueued a slot twice: %v", again)
	}
}

// A metadata reload between a slot and the next tick does not drop that slot (audit F5).
func TestReloadKeepsADueSlot(t *testing.T) {
	ctx := context.Background()
	db := orgSchedDB(t)
	orgs := &fixedOrgs{}
	orgs.set("a", "b")
	clock := time.Date(2026, 11, 19, 6, 59, 55, 0, time.UTC)
	s := NewScheduler(New(db, 1, 1), time.UTC)
	s.SetNow(func() time.Time { return clock })
	s.Orgs = orgs.get
	s.SetFlows(orgSchedReg(), "a")
	s.Tick(ctx)
	clock = time.Date(2026, 11, 19, 7, 0, 2, 0, time.UTC)
	s.SetFlows(orgSchedReg(), "a") // a change set applied at 07:00:02, before the tick
	s.Tick(ctx)
	if got := flowJobs(t, db); len(got) != 2 {
		t.Fatalf("the 07:00 slot was dropped by the reload: %v", got)
	}
}

// The default org keeps its schedules even when the org list does not name it (audit F9).
func TestDefaultOrgAlwaysScheduled(t *testing.T) {
	ctx := context.Background()
	db := orgSchedDB(t)
	orgs := &fixedOrgs{}
	orgs.set("b")
	s := NewScheduler(New(db, 1, 1), time.UTC)
	s.SetNow(func() time.Time { return time.Date(2026, 11, 19, 3, 0, 0, 0, time.UTC) })
	s.Orgs = orgs.get
	s.SetFlows(orgSchedReg(), "a")
	s.Tick(ctx)
	if len(s.Status(ctx, "a")) != 1 || len(s.Status(ctx, "b")) != 1 {
		t.Fatalf("schedules: %v", s.items)
	}
}

// An org whose enqueue fails does not stop the others, and its slot is enqueued on a later tick.
func TestOneOrgFailingDoesNotStopTheOthers(t *testing.T) {
	ctx := context.Background()
	db := orgSchedDB(t)
	if _, err := db.Exec(ctx, `create function rk_test_refuse() returns trigger language plpgsql as $$
		begin if new.org_id = 'aaa_bad' then raise exception 'refused'; end if; return new; end $$;
		create trigger rk_test_refuse before insert on rk_jobs for each row execute function rk_test_refuse();`); err != nil {
		t.Fatal(err)
	}
	orgs := &fixedOrgs{}
	orgs.set("aaa_bad", "zzz_good")
	clock := time.Date(2026, 11, 19, 6, 59, 0, 0, time.UTC)
	s := NewScheduler(New(db, 1, 1), time.UTC)
	s.SetNow(func() time.Time { return clock })
	s.Orgs = orgs.get
	var logged []string
	s.Log = func(f string, a ...any) { logged = append(logged, f) }
	s.SetFlows(orgSchedReg(), "zzz_good")
	s.Tick(ctx)
	clock = time.Date(2026, 11, 19, 7, 0, 10, 0, time.UTC)
	s.Tick(ctx)
	got := flowJobs(t, db)
	if len(got) != 1 || got[0].org != "zzz_good" {
		t.Fatalf("the good org must still be enqueued: %v", got)
	}
	if len(logged) == 0 {
		t.Fatal("the refused enqueue was not logged")
	}
	if _, err := db.Exec(ctx, "drop trigger rk_test_refuse on rk_jobs"); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(5 * time.Second)
	s.Tick(ctx)
	got = flowJobs(t, db)
	if len(got) != 2 || got[0].org != "aaa_bad" || got[0].slot != "2026-11-19T07:00:00Z" {
		t.Fatalf("the failed slot must be retried: %v", got)
	}
}

// A business created at run time gets its schedules within OrgRefresh, a removed one loses them, and the orgs that
// stay keep their next run.
func TestOrgRefreshAddsAndDropsOrgs(t *testing.T) {
	ctx := context.Background()
	db := orgSchedDB(t)
	orgs := &fixedOrgs{}
	orgs.set("a")
	clock := time.Date(2026, 11, 19, 3, 0, 0, 0, time.UTC)
	s := NewScheduler(New(db, 1, 1), time.UTC)
	s.SetNow(func() time.Time { return clock })
	s.Orgs = orgs.get
	s.SetFlows(orgSchedReg(), "a")
	s.Tick(ctx)
	keep := s.items[0]
	orgs.set("a", "b")
	clock = clock.Add(30 * time.Second)
	s.Tick(ctx)
	if len(s.Status(ctx, "b")) != 0 {
		t.Fatal("org list re-read before OrgRefresh")
	}
	clock = clock.Add(time.Minute)
	s.Tick(ctx)
	if len(s.Status(ctx, "b")) != 1 {
		t.Fatalf("new org b has no schedule: %v", s.items)
	}
	if s.items[0] != keep {
		t.Fatal("org a's schedule was rebuilt by a refresh")
	}
	orgs.set("b")
	clock = clock.Add(time.Minute)
	s.Tick(ctx)
	if len(s.Status(ctx, "a")) != 1 || len(s.Status(ctx, "b")) != 1 {
		t.Fatalf("after the list dropped the default org a: %v", s.items)
	}
	orgs.set("a")
	clock = clock.Add(time.Minute)
	s.Tick(ctx)
	if len(s.Status(ctx, "a")) != 1 || len(s.Status(ctx, "b")) != 0 {
		t.Fatalf("after removing b: %v", s.items)
	}
	// a lister that fails keeps the last list
	s.Orgs = func(context.Context) ([]string, error) { return nil, context.DeadlineExceeded }
	clock = clock.Add(time.Minute)
	s.Tick(ctx)
	if len(s.Status(ctx, "a")) != 1 {
		t.Fatal("a failed org list dropped the schedules")
	}
}

// The stagger offset is 0 with one org and never reaches the schedule's own interval.
func TestStaggerOffset(t *testing.T) {
	s := NewScheduler(nil, nil)
	const st = 5 * time.Minute
	daily, _ := ParseCron("0 7 * * *")
	minutely, _ := ParseCron("* * * * *")
	due := time.Date(2026, 11, 19, 7, 0, 0, 0, time.UTC)
	if off := s.offset(&Schedule{Org: "solo", Cron: daily}, due, 1, st); off != 0 {
		t.Fatalf("one org: offset %v", off)
	}
	seen := map[time.Duration]bool{}
	for _, org := range []string{"a", "b", "c", "d", "e", "f"} {
		off := s.offset(&Schedule{Org: org, Cron: daily}, due, 6, st)
		if off < 0 || off >= 5*time.Minute {
			t.Fatalf("%s: offset %v outside the window", org, off)
		}
		if off != s.offset(&Schedule{Org: org, Cron: daily}, due, 6, st) {
			t.Fatalf("%s: offset is not fixed", org)
		}
		seen[off] = true
		if m := s.offset(&Schedule{Org: org, Cron: minutely}, due, 6, st); m >= time.Minute {
			t.Fatalf("%s: every-minute offset %v reaches the next slot", org, m)
		}
	}
	if len(seen) < 2 {
		t.Fatal("orgs are not spread")
	}
	// a slot near midnight (or month end) never runs on the next day: the flow reads today() when it runs (audit F1)
	late, _ := ParseCron("59 23 * * *")
	end := time.Date(2026, 11, 30, 23, 59, 0, 0, time.UTC)
	for _, org := range []string{"a", "b", "c", "d", "e", "f"} {
		if off := s.offset(&Schedule{Org: org, Cron: late}, end, 6, st); end.Add(off).Day() != 30 {
			t.Fatalf("%s: offset %v moves the 23:59 run into the next day", org, off)
		}
	}
}
