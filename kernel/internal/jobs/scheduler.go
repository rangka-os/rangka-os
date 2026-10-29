package jobs

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// Schedule is one periodic job (a flow with schedule:).
type Schedule struct {
	Name   string
	Source string // apps/crm/flows/morning_follow_up.rk
	Cron   *Cron
	Org    string
	next   time.Time
}

// Scheduler enqueues "flow" jobs when schedules come due.
type Scheduler struct {
	Q     *Queue
	Loc   *time.Location
	Log   func(format string, args ...any)
	clock func() time.Time
	mu    sync.Mutex
	items []*Schedule
	Bad   []string // schedules that did not parse
}

// NewScheduler makes a scheduler in a time zone (system.rk scheduler.timezone).
func NewScheduler(q *Queue, loc *time.Location) *Scheduler {
	if loc == nil {
		loc = time.UTC
	}
	return &Scheduler{Q: q, Loc: loc, clock: time.Now, Log: func(string, ...any) {}}
}

// SetNow replaces the clock (tests).
func (s *Scheduler) SetNow(f func() time.Time) {
	s.mu.Lock()
	s.clock = f
	s.mu.Unlock()
}

// Now is the scheduler's clock.
func (s *Scheduler) Now() time.Time {
	s.mu.Lock()
	f := s.clock
	s.mu.Unlock()
	return f()
}

// SetFlows registers the flows that have schedule: for an org (called again after a metadata reload).
func (s *Scheduler) SetFlows(reg *meta.Registry, org string) {
	var items []*Schedule
	var bad []string
	now := s.Now().In(s.Loc)
	for _, name := range sortedFlowNames(reg) {
		f := reg.Flows[name]
		if f.Schedule == "" || !reg.IsInstalled(f.App) {
			continue
		}
		c, err := ParseCron(f.Schedule)
		if err != nil {
			bad = append(bad, name+": "+err.Error())
			continue
		}
		items = append(items, &Schedule{Name: name, Source: "apps/" + f.App + "/flows/" + name + ".rk", Cron: c, Org: org, next: c.Next(now)})
	}
	s.mu.Lock()
	s.items, s.Bad = items, bad
	s.mu.Unlock()
}

func sortedFlowNames(reg *meta.Registry) []string {
	var out []string
	for n := range reg.Flows {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Tick enqueues every schedule whose time has come; the unique key (flow + minute) makes it safe to run
// on several instances.
func (s *Scheduler) Tick(ctx context.Context) {
	now := s.Now().In(s.Loc)
	s.mu.Lock()
	items := append([]*Schedule{}, s.items...)
	s.mu.Unlock()
	for _, it := range items {
		for {
			s.mu.Lock()
			due := it.next
			s.mu.Unlock()
			if due.IsZero() || due.After(now) {
				break
			}
			_, err := s.Q.Enqueue(ctx, nil, "flow", it.Org, map[string]any{"flow": it.Name, "scheduled_at": due.Format(time.RFC3339)},
				engine.EnqueueOpts{UniqueKey: "flow:" + it.Org + ":" + it.Name + ":" + strconv.FormatInt(due.Unix(), 10)})
			if err != nil {
				s.Log("scheduler: %s: %v", it.Name, err)
				break
			}
			s.mu.Lock()
			it.next = it.Cron.Next(due)
			s.mu.Unlock()
		}
	}
}

// Run ticks every few seconds until ctx ends.
func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Status lists schedules with their next run and the last run's state.
func (s *Scheduler) Status(ctx context.Context) []map[string]any {
	s.mu.Lock()
	items := append([]*Schedule{}, s.items...)
	s.mu.Unlock()
	out := []map[string]any{}
	for _, it := range items {
		s.mu.Lock()
		next := it.next
		s.mu.Unlock()
		m := map[string]any{"name": it.Name, "source": it.Source, "cron": it.Cron.Spec, "next_run": next.Format(time.RFC3339), "last_run": nil, "last_status": nil}
		if res, err := s.Q.DB.Query(ctx, `select coalesce(attempted_at, scheduled_at), state from rk_jobs where kind = 'flow' and args->>'flow' = $1 and org_id = $2
			and state <> 'available' order by id desc limit 1`, it.Name, it.Org); err == nil && len(res.Rows) > 0 {
			if t, ok := res.Rows[0][0].(time.Time); ok {
				m["last_run"] = t.In(s.Loc).Format(time.RFC3339)
			}
			m["last_status"] = res.Rows[0][1]
		}
		out = append(out, m)
	}
	return out
}
