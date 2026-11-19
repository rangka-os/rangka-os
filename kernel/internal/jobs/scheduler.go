package jobs

import (
	"context"
	"hash/fnv"
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
	Loc    *time.Location // the org's time zone: its orgs: config.timezone, else scheduler.timezone
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
	// CatchUp makes SetFlows start each schedule this far in the past, so a freshly started instance (request-only
	// mode, contract 0040 §3) still enqueues runs that came due before it existed. Repeats are dropped by the
	// unique key per flow and minute. 0 = start from now (rangka serve, where the loop never stops).
	CatchUp time.Duration
	polls   []*pollItem
	// Orgs lists the organisations scheduled flows run for (every row of rk_orgs, sandboxes included: contract 0052
	// runs the scheduler there as usual). nil = only the org SetFlows names (tests, single-org tools).
	Orgs func(ctx context.Context) ([]string, error)
	// OrgRefresh is how often Tick re-reads Orgs, so a business created at run time gets its schedules without a
	// metadata reload. Default 1 minute.
	OrgRefresh time.Duration
	// Stagger spreads the runs of one slot over this window on a host with several orgs: each org runs at a fixed
	// offset (a hash of its id, below the schedule's own interval), so a 07:00 summary for 500 businesses does not
	// start 500 flows at once. The unique key keeps the slot, not the offset. 0 = no spread.
	Stagger    time.Duration
	reg        *meta.Registry
	defaultOrg string
	orgs       []string // the last list Orgs answered; nil until it answered once
	orgsAt     time.Time
	// pollMu is held for a whole tickPolls and by SetPolls, so SetPolls returns only after a tick that copied the old
	// list has finished enqueueing: once it returns, no poll of a removed spec is queued any more.
	pollMu sync.Mutex
}

// PollSpec is a periodic "poll" job (a connector trigger with via: poll, contract 0002 §A.1).
type PollSpec struct {
	Name  string // whatsapp_business.message_status
	Every time.Duration
	Args  map[string]any
}

type pollItem struct {
	PollSpec
	org  string
	last time.Time // the last slot enqueued
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

// SetFlows registers the flows that have schedule: for every org (called again after a metadata reload). org is the
// installation's default org: the only one until Orgs has answered, and the fallback when it lists none. A reload
// starts every schedule afresh, as before per-org schedules.
func (s *Scheduler) SetFlows(reg *meta.Registry, org string) {
	now := s.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reg, s.defaultOrg = reg, org
	from := now.Add(-s.CatchUp)
	var items []*Schedule
	var bad []string
	for _, o := range s.orgList() {
		its, b := s.flowsFor(reg, o, from)
		items = append(items, its...)
		if bad == nil {
			bad = b // the same parse errors for every org: listed once
		}
	}
	s.items, s.Bad = items, bad
}

// orgList is the orgs schedules run for: the last answer of Orgs, else the default org. s.mu is held.
func (s *Scheduler) orgList() []string {
	if len(s.orgs) > 0 {
		return s.orgs
	}
	if s.defaultOrg == "" {
		return nil
	}
	return []string{s.defaultOrg}
}

// flowsFor builds one org's schedules, each starting at from in the org's time zone. s.mu is held.
func (s *Scheduler) flowsFor(reg *meta.Registry, org string, from time.Time) ([]*Schedule, []string) {
	var items []*Schedule
	bad := []string{}
	loc := s.orgLoc(reg, org)
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
		items = append(items, &Schedule{Name: name, Source: reg.App[f.App].DocKey("flows/" + name + ".rk"), Cron: c, Org: org, Loc: loc,
			next: c.Next(from.In(loc))})
	}
	if len(bad) == 0 {
		bad = nil
	}
	return items, bad
}

// orgLoc is the time zone an org's schedules follow: the timezone its own orgs: entry in config.rk sets, else the
// installation's scheduler.timezone (s.Loc). The site-wide config.timezone is not read here, so a site that never set
// scheduler.timezone keeps the times it had before per-org schedules.
func (s *Scheduler) orgLoc(reg *meta.Registry, org string) *time.Location {
	if reg != nil {
		if o := reg.Orgs[org]; o != nil {
			if tz, ok := o.Config["timezone"].(string); ok && tz != "" {
				if l, err := time.LoadLocation(tz); err == nil {
					return l
				}
				s.Log("scheduler: org %s: unknown timezone %q, using %s", org, tz, s.Loc)
			}
		}
	}
	return s.Loc
}

// Location is the time zone an org's schedules follow (the system status page).
func (s *Scheduler) Location(org string) *time.Location {
	s.mu.Lock()
	reg := s.reg
	s.mu.Unlock()
	return s.orgLoc(reg, org)
}

// refreshOrgs re-reads Orgs when the last answer is older than OrgRefresh: a new org gets its schedules, a removed
// org loses them, and the others keep their next run untouched. A failed read keeps the last list.
func (s *Scheduler) refreshOrgs(ctx context.Context, now time.Time) {
	if s.Orgs == nil {
		return
	}
	every := s.OrgRefresh
	if every <= 0 {
		every = time.Minute
	}
	s.mu.Lock()
	at := s.orgsAt
	s.mu.Unlock()
	if !at.IsZero() && now.Sub(at) < every && !now.Before(at) {
		return
	}
	list, err := s.Orgs(ctx)
	if err != nil {
		s.Log("scheduler: list organisations: %v", err)
		s.mu.Lock()
		s.orgsAt = now // try again after OrgRefresh, not on every tick
		s.mu.Unlock()
		return
	}
	list = append([]string{}, list...)
	sort.Strings(list)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orgsAt = now
	// the first answer of a fresh instance catches up like SetFlows; an org that appears later is new on the host
	// and starts now, so it never runs a slot from before it existed
	from := now
	if s.orgs == nil {
		from = now.Add(-s.CatchUp)
	}
	before := map[string]bool{}
	for _, o := range s.orgList() {
		before[o] = true
	}
	s.orgs = list
	after := map[string]bool{}
	for _, o := range s.orgList() {
		after[o] = true
	}
	items := []*Schedule{}
	for _, it := range s.items {
		if after[it.Org] {
			items = append(items, it)
		}
	}
	if s.reg != nil {
		for _, o := range s.orgList() {
			if !before[o] {
				its, _ := s.flowsFor(s.reg, o, from)
				items = append(items, its...)
			}
		}
	}
	s.items = items
}

// offset is where in the Stagger window an org's run of the slot due starts: a hash of the org, below the
// schedule's interval so a run never slides past the next slot. 0 on a host with one org.
func (s *Scheduler) offset(it *Schedule, due time.Time, orgs int) time.Duration {
	if s.Stagger <= 0 || orgs < 2 {
		return 0
	}
	win := s.Stagger
	if gap := it.Cron.Next(due).Sub(due); gap < win {
		win = gap
	}
	if win <= time.Second {
		return 0
	}
	h := fnv.New32a()
	h.Write([]byte(it.Org)) //nolint:errcheck // hash writes do not fail
	return time.Duration(h.Sum32()%uint32(win/time.Second)) * time.Second
}

// SetPolls registers the poll triggers of an org (called again after a metadata reload). Slots are aligned to
// multiples of Every, so several kernel instances enqueue the same slot under one unique key.
func (s *Scheduler) SetPolls(org string, specs []PollSpec) {
	s.pollMu.Lock()
	defer s.pollMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := map[string]time.Time{}
	for _, p := range s.polls {
		if p.org == org {
			prev[p.Name] = p.last
		}
	}
	items := []*pollItem{}
	for _, p := range s.polls {
		if p.org != org {
			items = append(items, p)
		}
	}
	for _, sp := range specs {
		if sp.Every <= 0 {
			continue
		}
		items = append(items, &pollItem{PollSpec: sp, org: org, last: prev[sp.Name]})
	}
	s.polls = items
}

func sortedFlowNames(reg *meta.Registry) []string {
	var out []string
	for n := range reg.Flows {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Tick enqueues every schedule whose time has come, for every org; the unique key (org + flow + slot) makes it safe to
// run on several instances and across restarts. An org whose enqueue fails is logged and tried again next tick; the
// other orgs go on.
func (s *Scheduler) Tick(ctx context.Context) {
	now := s.Now().In(s.Loc)
	s.tickPolls(ctx, now)
	s.refreshOrgs(ctx, now)
	s.mu.Lock()
	items := append([]*Schedule{}, s.items...)
	orgs := len(s.orgList())
	s.mu.Unlock()
	for _, it := range items {
		for {
			s.mu.Lock()
			due := it.next
			s.mu.Unlock()
			if due.IsZero() || due.After(now) {
				break
			}
			opts := engine.EnqueueOpts{UniqueKey: "flow:" + it.Org + ":" + it.Name + ":" + strconv.FormatInt(due.Unix(), 10)}
			if off := s.offset(it, due, orgs); off > 0 {
				opts.RunAt = due.Add(off)
			}
			_, err := s.Q.Enqueue(ctx, nil, "flow", it.Org, map[string]any{"flow": it.Name, "scheduled_at": due.Format(time.RFC3339)}, opts)
			if err != nil {
				s.Log("scheduler: %s: %s: %v", it.Org, it.Name, err)
				break
			}
			s.mu.Lock()
			it.next = it.Cron.Next(due)
			s.mu.Unlock()
		}
	}
}

// pollEnqueueTimeout bounds one tick's poll enqueues. pollMu is held meanwhile, so a database that does not answer
// would otherwise hold a metadata reload (SetPolls) as long; a poll not enqueued in time keeps its slot open and is
// tried again on the next tick (t_3c967f78).
var pollEnqueueTimeout = 10 * time.Second

// tickPolls enqueues each poll whose current slot has not been enqueued yet.
func (s *Scheduler) tickPolls(ctx context.Context, now time.Time) {
	s.pollMu.Lock()
	defer s.pollMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, pollEnqueueTimeout)
	defer cancel()
	s.mu.Lock()
	polls := append([]*pollItem{}, s.polls...)
	s.mu.Unlock()
	for _, p := range polls {
		slot := now.Truncate(p.Every)
		s.mu.Lock()
		due := slot.After(p.last)
		s.mu.Unlock()
		if !due {
			continue
		}
		args := map[string]any{"scheduled_at": slot.UTC().Format(time.RFC3339)}
		for k, v := range p.Args {
			args[k] = v
		}
		// one attempt: the next slot is the retry, so a vendor outage leaves one dead-lettered poll per slot
		if _, err := s.Q.Enqueue(ctx, nil, "poll", p.org, args, engine.EnqueueOpts{MaxAttempts: 1,
			UniqueKey: "poll:" + p.org + ":" + p.Name + ":" + strconv.FormatInt(slot.Unix(), 10)}); err != nil {
			s.Log("scheduler: poll %s: %v", p.Name, err)
			continue
		}
		s.mu.Lock()
		p.last = slot
		s.mu.Unlock()
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

// Status lists one org's schedules with their next run and the last run's state.
func (s *Scheduler) Status(ctx context.Context, org string) []map[string]any {
	s.mu.Lock()
	items := append([]*Schedule{}, s.items...)
	s.mu.Unlock()
	out := []map[string]any{}
	for _, it := range items {
		if it.Org != org {
			continue
		}
		s.mu.Lock()
		next := it.next
		s.mu.Unlock()
		m := map[string]any{"name": it.Name, "source": it.Source, "cron": it.Cron.Spec, "next_run": next.In(it.Loc).Format(time.RFC3339), "last_run": nil, "last_status": nil}
		if res, err := s.Q.DB.Query(ctx, `select coalesce(attempted_at, scheduled_at), state from rk_jobs where kind = 'flow' and args->>'flow' = $1 and org_id = $2
			and state <> 'available' order by id desc limit 1`, it.Name, it.Org); err == nil && len(res.Rows) > 0 {
			if t, ok := res.Rows[0][0].(time.Time); ok {
				m["last_run"] = t.In(it.Loc).Format(time.RFC3339)
			}
			m["last_status"] = res.Rows[0][1]
		}
		out = append(out, m)
	}
	return out
}
