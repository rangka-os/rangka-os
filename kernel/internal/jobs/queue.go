// Package jobs is the queue and scheduler capability (system.rk queue: and scheduler:): jobs live in the
// rk_jobs table, workers claim them with FOR UPDATE SKIP LOCKED, failures retry with backoff, and cron
// schedules of flows enqueue jobs with a unique key so several kernel instances never double-run.
//
// The specification named River (riverqueue); the module proxy was unreachable at build time, so this is a
// small Postgres queue with River's job states (available, running, retryable, completed, discarded,
// cancelled) so the Console and status shapes do not change when River is swapped in.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Job is a claimed job.
type Job struct {
	ID          int64          `json:"id"`
	Kind        string         `json:"kind"`
	Org         string         `json:"org"`
	Args        map[string]any `json:"args"`
	Attempt     int            `json:"attempt"`
	MaxAttempts int            `json:"max_attempts"`
}

// Handler runs a job; its result is stored.
type Handler func(ctx context.Context, j *Job) (any, error)

// Queue runs workers over rk_jobs.
type Queue struct {
	DB          *pg.Pool
	Workers     int
	MaxAttempts int
	Backoff     func(attempt int) time.Duration
	Handlers    map[string]Handler
	Log         func(format string, args ...any)
	wake        chan struct{}
	wg          sync.WaitGroup
	running     bool
	mu          sync.Mutex
}

// New makes a queue (workers from system.rk queue.workers, retries from queue.retries).
func New(db *pg.Pool, workers, retries int) *Queue {
	if workers <= 0 {
		workers = 2
	}
	if retries < 0 {
		retries = 3
	}
	return &Queue{DB: db, Workers: workers, MaxAttempts: retries + 1, Handlers: map[string]Handler{},
		Backoff: func(a int) time.Duration { return time.Duration(a*a) * time.Second }, Log: func(string, ...any) {}, wake: make(chan struct{}, 1)}
}

// Running reports whether workers were started.
func (q *Queue) Running() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.running
}

// Enqueue inserts a job (in the caller's transaction when qr is a Tx). With a unique key an existing job
// with that key is returned instead.
func (q *Queue) Enqueue(ctx context.Context, qr pg.Querier, kind, org string, args map[string]any, opts engine.EnqueueOpts) (int64, error) {
	if qr == nil {
		qr = q.DB
	}
	max := opts.MaxAttempts
	if max <= 0 {
		max = q.MaxAttempts
	}
	runAt := opts.RunAt
	if runAt.IsZero() {
		runAt = time.Now()
	}
	var uk any
	if opts.UniqueKey != "" {
		uk = opts.UniqueKey
	}
	if args == nil {
		args = map[string]any{}
	}
	res, err := qr.Query(ctx, `insert into rk_jobs (kind, org_id, args, max_attempts, unique_key, scheduled_at) values ($1, $2, $3, $4, $5, $6)
		on conflict (unique_key) where unique_key is not null do nothing returning id`, kind, org, pg.JSON{V: args}, max, uk, runAt)
	if err != nil {
		return 0, err
	}
	if len(res.Rows) == 0 {
		res, err = qr.Query(ctx, "select id from rk_jobs where unique_key = $1", uk)
		if err != nil || len(res.Rows) == 0 {
			return 0, err
		}
		return res.Rows[0][0].(int64), nil
	}
	id := res.Rows[0][0].(int64)
	store.Notify(ctx, qr, store.Event{Type: "job", Org: org, ID: strconv.FormatInt(id, 10), Kind: kind, State: "available"})
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return id, nil
}

// Start launches the workers; they stop when ctx ends.
func (q *Queue) Start(ctx context.Context) {
	q.mu.Lock()
	q.running = true
	q.mu.Unlock()
	for i := 0; i < q.Workers; i++ {
		q.wg.Add(1)
		go func() {
			defer q.wg.Done()
			q.work(ctx)
		}()
	}
}

// Wait blocks until workers exit.
func (q *Queue) Wait() { q.wg.Wait() }

func (q *Queue) work(ctx context.Context) {
	for ctx.Err() == nil {
		did, err := q.RunOne(ctx)
		if err != nil && ctx.Err() == nil {
			q.Log("jobs: %v", err)
		}
		if did {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-q.wake:
			time.Sleep(50 * time.Millisecond) // let the enqueuing transaction commit
		case <-time.After(time.Second):
		}
	}
}

// RunOne claims and runs one due job; it reports whether there was one.
func (q *Queue) RunOne(ctx context.Context) (bool, error) {
	res, err := q.DB.Query(ctx, `update rk_jobs set state = 'running', attempt = attempt + 1, attempted_at = now()
		where id = (select id from rk_jobs where state in ('available', 'retryable') and scheduled_at <= now()
			order by scheduled_at, id for update skip locked limit 1)
		returning id, kind, org_id, args, attempt, max_attempts`)
	if err != nil {
		return false, err
	}
	if len(res.Rows) == 0 {
		return false, nil
	}
	r := res.Rows[0]
	j := &Job{ID: r[0].(int64), Kind: r[1].(string), Org: r[2].(string), Attempt: int(r[4].(int64)), MaxAttempts: int(r[5].(int64))}
	j.Args, _ = r[3].(map[string]any)
	q.event(ctx, j, "running")
	h := q.Handlers[j.Kind]
	var out any
	if h == nil {
		err = fmt.Errorf("no handler for job kind %q", j.Kind)
	} else {
		out, err = safeRun(ctx, h, j)
	}
	if err == nil {
		_, uerr := q.DB.Query(ctx, "update rk_jobs set state = 'completed', finalized_at = now(), result = $2 where id = $1", j.ID, pg.JSON{V: out})
		q.event(ctx, j, "completed")
		return true, uerr
	}
	entry := map[string]any{"at": time.Now().UTC().Format(time.RFC3339), "attempt": j.Attempt, "error": err.Error()}
	state := "retryable"
	if j.Attempt >= j.MaxAttempts {
		state = "discarded"
	}
	_, uerr := q.DB.Query(ctx, `update rk_jobs set state = $2, errors = errors || $3::jsonb,
		scheduled_at = case when $2 = 'retryable' then now() + make_interval(secs => $4) else scheduled_at end,
		finalized_at = case when $2 = 'discarded' then now() else null end where id = $1`,
		j.ID, state, pg.JSON{V: []any{entry}}, q.Backoff(j.Attempt).Seconds())
	q.event(ctx, j, state)
	q.Log("jobs: job %d (%s) attempt %d failed: %v", j.ID, j.Kind, j.Attempt, err)
	return true, uerr
}

func safeRun(ctx context.Context, h Handler, j *Job) (out any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return h(ctx, j)
}

func (q *Queue) event(ctx context.Context, j *Job, state string) {
	store.Notify(ctx, q.DB, store.Event{Type: "job", Org: j.Org, ID: strconv.FormatInt(j.ID, 10), Kind: j.Kind, State: state})
}

// Counts returns jobs per state (River's states).
func (q *Queue) Counts(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{"available": 0, "running": 0, "retryable": 0, "completed": 0, "discarded": 0, "cancelled": 0}
	res, err := q.DB.Query(ctx, "select state, count(*) from rk_jobs group by state")
	if err != nil {
		return out, err
	}
	for _, r := range res.Rows {
		out[r[0].(string)] = r[1].(int64)
	}
	return out, nil
}

const jobCols = `id, kind, queue, state, attempt, max_attempts, created_at, scheduled_at, finalized_at, errors, args, org_id, result`

// jobOut is the job shape of CONTRACTS v0.1 decision 8.
func jobOut(m map[string]any) map[string]any {
	ts := func(v any) any {
		if t, ok := v.(time.Time); ok {
			return t.UTC().Format(time.RFC3339)
		}
		return nil
	}
	var lastErr any
	if l, ok := m["errors"].([]any); ok && len(l) > 0 {
		if e, ok := l[len(l)-1].(map[string]any); ok {
			lastErr = e["error"]
		}
	}
	return map[string]any{"id": m["id"], "kind": m["kind"], "queue": m["queue"], "state": m["state"], "attempt": m["attempt"],
		"max_attempts": m["max_attempts"], "created_at": ts(m["created_at"]), "scheduled_at": ts(m["scheduled_at"]),
		"finished_at": ts(m["finalized_at"]), "error": lastErr, "args": m["args"], "org": m["org_id"], "result": m["result"]}
}

// List returns recent jobs, optionally by state.
func (q *Queue) List(ctx context.Context, org, state string, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	res, err := q.DB.Query(ctx, `select `+jobCols+` from rk_jobs where ($1 = '' or state = $1) and (org_id = $2 or org_id = '') order by id desc limit $3`, state, org, limit)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, m := range res.Maps() {
		out = append(out, jobOut(m))
	}
	return out, nil
}

// Get returns one job.
func (q *Queue) Get(ctx context.Context, id int64) (map[string]any, error) {
	res, err := q.DB.Query(ctx, "select "+jobCols+" from rk_jobs where id = $1", id)
	if err != nil || len(res.Rows) == 0 {
		return nil, err
	}
	return jobOut(res.Maps()[0]), nil
}

// MarshalArgs is a helper for tests.
func MarshalArgs(v any) string { b, _ := json.Marshal(v); return string(b) }
