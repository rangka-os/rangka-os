// Package engine runs the document lifecycle on top of the store: defaults, computed fields, rules, naming
// at submit, immutability, cancel and amend, actions and hooks, list queries and dashboards. Its semantics
// follow the browser prototype (rangka-os/web/prototype/engine.js).
package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/cache"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Actor is who performs an operation.
type Actor struct {
	Org      string   // organisation id
	ID       string   // email of the person, or "system"/"seed"/"flow:<name>"
	UserName string   // the user record the person is (USR-001); user.name in CEL
	Roles    []string // member roles (owner, sales_manager, …)
	Human    bool     // signed in with a browser session (not a token)
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
	reg     atomic.Pointer[meta.Registry]
	DB      *pg.Pool
	Now     func() time.Time
	Hooks   map[string]Hook
	Cache   cache.Cache
	Queue   Enqueuer
	OnWrite func(org, model string)
	Log     func(format string, args ...any)
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
	if e.Cache != nil {
		e.Cache.DeletePrefix("dash:")
	}
}

// Location is the organisation time zone (config.timezone, else scheduler.timezone, else UTC).
func (e *Engine) Location() *time.Location {
	r := e.Reg()
	for _, tz := range []any{r.SiteConfig("timezone"), r.SystemConfig("scheduler.timezone")} {
		if s, ok := tz.(string); ok && s != "" {
			if loc, err := time.LoadLocation(s); err == nil {
				return loc
			}
		}
	}
	return time.UTC
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
}

func (e *Engine) run(ctx context.Context, a Actor, fn func(o *op) error) error {
	tx, err := e.DB.Begin(ctx)
	if err != nil {
		return err
	}
	o := &op{ctx: ctx, tx: tx, e: e, actor: a, touched: map[string]bool{}, links: map[string]map[string]any{}}
	defer tx.Rollback(ctx)
	if err := fn(o); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for m := range o.touched {
		if e.Cache != nil {
			e.Cache.DeletePrefix("dash:" + a.Org + ":")
		}
		if e.OnWrite != nil {
			e.OnWrite(a.Org, m)
		}
	}
	return nil
}

func (o *op) event(m *meta.Model, rec store.Record, opName string) error {
	o.touched[m.Name] = true
	ev := store.Event{Type: "record", Org: o.actor.Org, Model: m.Name, Name: fmt.Sprint(rec["name"]), Op: opName}
	if m.IsSubmittable {
		d := toInt(rec["docstatus"])
		ev.Docstatus = &d
	}
	if err := store.Notify(o.ctx, o.tx, ev); err != nil {
		return err
	}
	summary := fmt.Sprintf("%s %s %s", m.Name, rec["name"], opName)
	return store.Audit(o.ctx, o.tx, o.actor.Org, o.actor.ID, "record."+opName, m.Name+"/"+fmt.Sprint(rec["name"]), summary, nil)
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
