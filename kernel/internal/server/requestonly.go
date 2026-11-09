package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Request-only mode (contract 0040 §1 to §3): the same kernel for platforms that freeze or discard the process
// between requests. Nothing is written at start, nothing runs in the background, and the scheduler and queue run when
// the hosting side calls POST /_rangka/tick.

// poolSize is system.rk db.pool (default 10). Request-only defaults to 3, or RANGKA_DB_POOL: one connection for a
// tick's lock and two for work, so many small instances do not exhaust the database.
func (cfg Config) poolSize(reg *meta.Registry) int {
	if !cfg.RequestOnly {
		return intCfg(reg, "db.pool", 10)
	}
	if n, err := strconv.Atoi(os.Getenv("RANGKA_DB_POOL")); err == nil && n > 0 {
		return n
	}
	return 3
}

// migratedState remembers whether the database matches the metadata; while it does not, request-only answers 503
// not_migrated and looks again at most every recheckMigrated.
type migratedState struct {
	mu      sync.Mutex
	ok      bool
	checked time.Time
}

const recheckMigrated = 10 * time.Second

// checkMigrated plans the migration without running it (no writes) and records whether anything is pending.
func (s *Server) checkMigrated(ctx context.Context, reg *meta.Registry) error {
	ctx = pg.WithOrg(ctx, "")
	res, err := store.MigrateWith(ctx, s.DB, reg, store.MigrateOpts{DryRun: true, Source: "serve"})
	if err != nil {
		return fmt.Errorf("migrate check: %w", err)
	}
	ok := len(res.Statements) == 0
	s.migrated.mu.Lock()
	s.migrated.ok, s.migrated.checked = ok, time.Now()
	s.migrated.mu.Unlock()
	if !ok {
		s.Cfg.Logf("request-only: the database is behind the metadata (%d statement(s)); answering 503 until `rangka migrate` runs", len(res.Statements))
	}
	return nil
}

// isMigrated reports the last check, checking again when it said no and is older than recheckMigrated.
func (s *Server) isMigrated(ctx context.Context) bool {
	s.migrated.mu.Lock()
	ok, stale := s.migrated.ok, time.Since(s.migrated.checked) > recheckMigrated
	s.migrated.mu.Unlock()
	if ok || !stale {
		return ok
	}
	if err := s.checkMigrated(ctx, s.Engine.Reg()); err != nil {
		s.Cfg.Logf("request-only: %v", err)
		return false
	}
	s.migrated.mu.Lock()
	defer s.migrated.mu.Unlock()
	return s.migrated.ok
}

// requestOnly wraps the API handler: while the database is behind the metadata every request except /_rangka/
// answers 503 not_migrated. In rangka serve it returns h unchanged.
func (s *Server) requestOnly(h http.Handler) http.Handler {
	if !s.Cfg.RequestOnly {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/_rangka/") && !s.isMigrated(r.Context()) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Retry-After", "10")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"error":{"code":"not_migrated","message":"Sistem sedang diperbarui, sebentar lagi."}}`)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// TickReport is the answer of POST /_rangka/tick.
type TickReport struct {
	// Skipped: another instance holds the tick lock, so this one did nothing.
	Skipped bool `json:"skipped,omitempty"`
	// Ran is how many queued jobs this tick ran (succeeded or failed); Left is how many are due and still waiting.
	Ran  int   `json:"ran"`
	Left int64 `json:"left"`
	MS   int64 `json:"ms"`
}

// Tick runs the scheduler once and then due jobs until none is left or budget is spent (contract 0040 §3). One
// instance at a time: the others answer {"skipped": true} at once.
func (s *Server) Tick(ctx context.Context, budget time.Duration) (TickReport, error) {
	start := time.Now()
	ctx = pg.WithOrg(ctx, "") // the scheduler and the queue are installation-wide; each job sets its own org
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return TickReport{}, fmt.Errorf("tick: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // releases the lock; nothing was written in this transaction
	res, err := tx.Query(ctx, "select pg_try_advisory_xact_lock(hashtext('rk_tick'))")
	if err != nil {
		return TickReport{}, fmt.Errorf("tick lock: %w", err)
	}
	if len(res.Rows) == 1 && res.Rows[0][0] == false {
		return TickReport{Skipped: true, MS: time.Since(start).Milliseconds()}, nil
	}
	rep := TickReport{}
	s.Sched.Tick(ctx)
	deadline := start.Add(budget)
	for time.Now().Before(deadline) {
		did, err := s.Queue.RunOne(ctx)
		if err != nil {
			s.Cfg.Logf("tick: %v", err)
			break
		}
		if !did {
			break
		}
		rep.Ran++
	}
	if left, err := s.DB.Query(ctx, "select count(*) from rk_jobs where state in ('available', 'retryable') and scheduled_at <= now()"); err == nil && len(left.Rows) == 1 {
		rep.Left, _ = left.Rows[0][0].(int64)
	}
	rep.MS = time.Since(start).Milliseconds()
	return rep, nil
}
