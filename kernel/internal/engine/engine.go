// Package engine runs the document lifecycle on top of the store: defaults, computed fields, rules, naming
// at submit, immutability, cancel and amend, actions and hooks, list queries and dashboards. Its semantics
// follow the browser prototype (rangka-os/web/app/engine.js).
package engine

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/cache"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/plan"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Actor is who performs an operation.
type Actor struct {
	Org      string   // organisation id
	ID       string   // email of the person, or "system"/"seed"/"flow:<name>"
	UserName string   // the user record the person is (USR-001); user.name in CEL
	Roles    []string // member roles (owner, sales_manager, …)
	Human    bool     // signed in with a browser session (not a token)
	Outlets  []string // the member's outlets in Org (user.outlets in CEL; row scope, contract 0010 §5)
	// System marks kernel-internal actors (seed, flows and automations the kernel starts): they bypass
	// permission checks. Queue jobs replaying a person's action keep that person's roles and System=false.
	System bool
}

// Enqueuer puts background jobs on the queue (implemented by package jobs).
type Enqueuer interface {
	Enqueue(ctx context.Context, q pg.Querier, kind, org string, args map[string]any, opts EnqueueOpts) (int64, error)
}

// EnqueueOpts tune a job.
type EnqueueOpts struct {
	MaxAttempts int
	UniqueKey   string
	RunAt       time.Time
}

// Engine is the lifecycle runtime. The registry can be swapped while serving (change sets reload it).
type Engine struct {
	reg   atomic.Pointer[meta.Registry]
	DB    *pg.Pool
	Now   func() time.Time
	Hooks map[string]Hook
	Cache cache.Cache
	Queue Enqueuer
	// CallTarget resolves a flow's call: (connector.action or a port) to the connector, the action and the job
	// options (retry.max, idempotency); nil = calls are only recorded (no connector runtime).
	CallTarget func(reg *meta.Registry, call string, input map[string]any) (connector, action string, opts EnqueueOpts, err error)
	OnWrite    func(org, model string)
	Log        func(format string, args ...any)

	// TolerateMissingTables makes a dashboard widget over a table that does not exist yet carry an error
	// instead of failing the dashboard (live preview of a change set, contract 0015 §4).
	TolerateMissingTables bool

	// Plan caches the tenant's plan snapshot for the read-only check (contract 0028).
	Plan plan.Guard

	permCache   sync.Map // *meta.Model → *modelPerms
	parentCache sync.Map // child *meta.Model → parent *meta.Model
}

// New makes an engine with the built-in hooks.
func New(db *pg.Pool, reg *meta.Registry) *Engine {
	e := &Engine{DB: db, Now: time.Now, Hooks: map[string]Hook{}, Cache: cache.NewMemory(64 << 20), Log: func(string, ...any) {}}
	e.reg.Store(reg)
	registerBuiltinHooks(e)
	return e
}

// Reg returns the current registry.
func (e *Engine) Reg() *meta.Registry { return e.reg.Load() }

// SetRegistry swaps the registry (live metadata reload).
func (e *Engine) SetRegistry(r *meta.Registry) {
	e.reg.Store(r)
	e.permCache.Clear()
	e.parentCache.Clear()
	if e.Cache != nil {
		e.Cache.DeletePrefix("dash:")
	}
}

// Location is the organisation time zone (config.timezone, else scheduler.timezone, else UTC).
func (e *Engine) Location() *time.Location {
	r := e.Reg()
	for _, tz := range []any{r.SiteConfig("timezone"), r.SystemConfig("scheduler.timezone")} {
		if s, ok := tz.(string); ok && s != "" {
			if loc := loadLocation(s); loc != nil {
				return loc
			}
		}
	}
	return time.UTC
}

// locations caches time.LoadLocation by name: it reads the zoneinfo file on every call, and Location runs for
// every record a request creates or computes (card t_58a60f6b: 16% of a submit's CPU, all file syscalls).
var locations sync.Map

// loadLocation is time.LoadLocation through the cache; nil when the name is unknown (not cached, so a zoneinfo
// installed later is found).
func loadLocation(name string) *time.Location {
	if v, ok := locations.Load(name); ok {
		return v.(*time.Location)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil
	}
	locations.Store(name, loc)
	return loc
}

// Today is the ISO date in the organisation time zone.
func (e *Engine) Today() string { return e.Now().In(e.Location()).Format("2006-01-02") }

// ---------- errors

// Issue is one validation problem or warning.
type Issue struct {
	Field   string `json:"field,omitempty"`
	Rule    string `json:"rule,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ValidationError is returned with HTTP 422.
type ValidationError struct {
	Errors     []Issue `json:"errors"`
	Warnings   []Issue `json:"warnings"`
	NeedsForce bool    `json:"needs_force,omitempty"`
}

func (v *ValidationError) Error() string {
	var parts []string
	for _, i := range v.Errors {
		parts = append(parts, i.Message)
	}
	if len(parts) == 0 {
		for _, i := range v.Warnings {
			parts = append(parts, i.Message)
		}
		return "confirm warnings: " + strings.Join(parts, "; ")
	}
	return strings.Join(parts, "; ")
}

// StatusError carries an HTTP status (404, 409, 400, 501).
type StatusError struct {
	Status int
	Code   string
	Msg    string
}

func (s *StatusError) Error() string { return s.Msg }

func notFound(what string) error { return &StatusError{404, "not_found", what + " not found"} }
func conflict(code, msg string) error {
	return &StatusError{409, code, msg}
}
func badRequest(msg string) error { return &StatusError{400, "bad_request", msg} }

// ---------- transactions

// op is one unit of work: a transaction plus the actor and the models it touched.
type op struct {
	ctx     context.Context
	tx      *pg.Tx
	e       *Engine
	actor   Actor
	touched map[string]bool
	links   map[string]map[string]any
	// approving is set while Approve runs the approved verb: earlier approved requests then satisfy hold.
	approving bool
	// inFlow is set while a flow runs: its own writes do not start record-event flows again.
	inFlow bool
	// manualRun is set for a flow a caller started through REST with an event of their own (contract 0026).
	manualRun bool
	// first is the first record the unit of work wrote (<model>/<name>); primary the first document it submitted or
	// cancelled (the verb's own document, written after its hooks). The target of an idempotency key is primary, else
	// first.
	first, primary string
	// expect is UpdateIf's precondition, checked once by the update it guards
	expect map[string]any
	// counters are the v0 counters this attempt took a name from (nextName): if a write then finds that name
	// taken, run moves them past the existing names and starts again (staleCounter)
	counters []counterRef
}

// counterRef is one v0 counter (rk_naming) and the model whose names it numbers.
type counterRef struct{ model, prefix string }

// staleCounter is an attempt that failed because a name a v0 counter gave is already taken: rows written outside
// the counter after it started (SQL, a partial restore). err is what the attempt returned.
type staleCounter struct {
	refs []counterRef
	err  error
}

func (s *staleCounter) Error() string { return s.err.Error() }
func (s *staleCounter) Unwrap() error { return s.err }

// nameTaken: err is the unique violation of a record's name (the table's primary key), as store or dbIssue
// reports it.
func nameTaken(err error) bool {
	var pe *pg.Error
	if errors.As(err, &pe) && pe.Code == "23505" && strings.HasSuffix(pe.Constraint, "_pkey") {
		return true
	}
	var ve *ValidationError
	if errors.As(err, &ve) {
		for _, is := range ve.Errors {
			if is.Field == "name" && is.Code == "unique" {
				return true
			}
		}
	}
	return false
}

// writeAttempts bounds how often run starts a unit of work again after it lost a race (contract 0025).
const writeAttempts = 8

// run executes fn as one unit of work in one transaction (READ COMMITTED; flows that read a model hold its lock, see
// lockFlowRead). When the transaction fails because a concurrent one won (a deadlock, SQLSTATE 40P01, or a
// serialization failure, 40001), run rolls back and runs fn again from current data, up to writeAttempts times
// (contract 0025). fn must build its results inside the attempt.
//
// A v0 counter reads only its own row, so a name written outside it after it started (rows loaded by SQL, a
// partial restore) is not seen until the counter gives that name: the write then fails on the name, and run moves
// the counters the attempt used past the existing names (store.SyncCounter, the one read of the model's table)
// and runs fn again, once (contract 0009 §4, card t_32af87a2).
func (e *Engine) run(ctx context.Context, a Actor, fn func(o *op) error) error {
	var err error
	synced := false
	for attempt := 1; ; attempt++ {
		var retry bool
		retry, err = e.runOnce(ctx, a, fn)
		var sc *staleCounter
		if errors.As(err, &sc) {
			if synced || attempt >= writeAttempts || ctx.Err() != nil {
				err = sc.err
				break
			}
			if serr := e.syncCounters(ctx, a.Org, sc.refs); serr != nil {
				err = serr
				break
			}
			synced = true
			continue
		}
		if !retry || attempt >= writeAttempts || ctx.Err() != nil {
			break
		}
		// back off a little, longer each time, with jitter so the two losers do not collide again
		d := time.Duration(attempt*attempt)*5*time.Millisecond + time.Duration(rand.Int63n(int64(10*time.Millisecond)))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
	if err != nil && errors.Is(err, errConflict) {
		return conflict("write_conflict", "Data ini sedang diubah orang lain bersamaan. Coba simpan lagi.")
	}
	return err
}

// syncCounters moves each counter past the existing names of its model, in a transaction of its own (the failed
// attempt has rolled back and released the counter rows).
func (e *Engine) syncCounters(ctx context.Context, org string, refs []counterRef) error {
	tx, err := e.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, r := range refs {
		if err := store.SyncCounter(ctx, tx, org, r.model, r.prefix); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// errConflict marks a unit of work that still lost its race after writeAttempts.
var errConflict = errors.New("engine: concurrent write conflict")

// runOnce is one attempt of run; retry reports a lost race (the transaction is rolled back).
func (e *Engine) runOnce(ctx context.Context, a Actor, fn func(o *op) error) (retry bool, err error) {
	tx, err := e.DB.Begin(ctx)
	if err != nil {
		return false, err
	}
	o := &op{ctx: ctx, tx: tx, e: e, actor: a, touched: map[string]bool{}, links: map[string]map[string]any{}}
	defer tx.Rollback(ctx)
	if err := fn(o); err != nil {
		if tx.Conflicted() {
			return true, fmt.Errorf("%w: %v", errConflict, err)
		}
		if len(o.counters) > 0 && nameTaken(err) {
			return false, &staleCounter{refs: o.counters, err: err}
		}
		return false, err
	}
	if k, ok := idemFrom(ctx); ok { // contract 0030 §6: the key is committed with the write
		target := o.primary
		if target == "" {
			target = o.first
		}
		if err := store.CommitIdempotency(ctx, tx, k.Org, k.Actor, k.Key, k.Claim, target); err != nil {
			if errors.Is(err, store.ErrIdemLost) {
				return false, conflict("idempotency_in_progress", "A later request with this Idempotency-Key took it over; this one was not saved")
			}
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		if tx.Conflicted() {
			return true, fmt.Errorf("%w: %v", errConflict, err)
		}
		return false, err
	}
	for m := range o.touched {
		if e.Cache != nil {
			e.Cache.DeletePrefix("dash:" + a.Org + ":")
		}
		if e.OnWrite != nil {
			e.OnWrite(a.Org, m)
		}
	}
	return false, nil
}

func (o *op) event(m *meta.Model, rec store.Record, opName string) error {
	o.touched[m.Name] = true
	if o.first == "" {
		o.first = m.Name + "/" + fmt.Sprint(rec["name"])
	}
	if o.primary == "" && (opName == "submitted" || opName == "cancelled") {
		o.primary = m.Name + "/" + fmt.Sprint(rec["name"])
	}
	ev := store.Event{Type: "record", Org: o.actor.Org, Model: m.Name, Name: fmt.Sprint(rec["name"]), Op: opName}
	if m.IsSubmittable {
		d := toInt(rec["docstatus"])
		ev.Docstatus = &d
	}
	ev.Fields = o.e.eventFields(m, rec)
	if err := store.Notify(o.ctx, o.tx, ev); err != nil {
		return err
	}
	summary := fmt.Sprintf("%s %s %s", m.Name, rec["name"], opName)
	var data any
	if t, ok := occurredFrom(o.ctx); ok { // contract 0030 §7: when the user pressed the button (a queued write)
		data = map[string]any{"occurred_at": t.UTC().Format(time.RFC3339)}
	}
	return store.Audit(o.ctx, o.tx, o.actor.Org, o.actor.ID, "record."+opName, m.Name+"/"+fmt.Sprint(rec["name"]), summary, data)
}

func (e *Engine) model(name string) (*meta.Model, error) {
	m := e.Reg().Model(name)
	if m == nil {
		return nil, notFound("model " + name)
	}
	return m, nil
}

// IsNotFound reports a 404 error.
func IsNotFound(err error) bool {
	var se *StatusError
	return errors.Is(err, store.ErrNotFound) || (errors.As(err, &se) && se.Status == 404)
}

func toInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
