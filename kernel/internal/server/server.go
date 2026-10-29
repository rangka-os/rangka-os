// Package server wires the kernel together: metadata, database, engine, cache, queue and scheduler,
// realtime, Rangka ID, change sets, MCP and the HTTP API. It also reloads metadata live after a change set.
package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/api"
	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/cache"
	"github.com/rangka-os/rangka-os/kernel/internal/changeset"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/jobs"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/realtime"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Config of a kernel instance.
type Config struct {
	AppsDir, SiteDir, ConnectorsDir string
	WebDir, TemplatesDir            string
	DatabaseURL                     string
	Addr                            string
	Org                             string
	DevOpen                         bool
	Schemas                         *schema.Set
	Version                         string
	DeployTarget                    string           // dev, docker, helm, desktop, cloud, partner
	Now                             func() time.Time // fixed clocks in tests
	Logf                            func(format string, args ...any)
}

// Server is a running kernel.
type Server struct {
	Cfg     Config
	DB      *pg.Pool
	Engine  *engine.Engine
	Hub     *realtime.Hub
	Queue   *jobs.Queue
	Sched   *jobs.Scheduler
	Auth    *auth.Service
	Changes *changeset.Service
	API     *api.Server
	reload  sync.Mutex
	wg      sync.WaitGroup
}

// LoadRegistry validates the metadata directories and refuses to continue on errors.
func LoadRegistry(cfg Config) (*meta.Registry, error) {
	reg, rep := meta.ValidateOpts(meta.Options{AppsDir: cfg.AppsDir, SiteDir: cfg.SiteDir, ConnectorsDir: cfg.ConnectorsDir}, nil, cfg.Schemas)
	if rep.Errors() > 0 {
		var b []byte
		for _, it := range rep.All() {
			if it.Level == "error" {
				b = append(b, fmt.Sprintf("\n  %s:%s %s %s %s", it.File, it.Pos, it.Code, it.Where, it.Message)...)
			}
		}
		return nil, fmt.Errorf("metadata has %d error(s):%s", rep.Errors(), b)
	}
	return reg, nil
}

func intCfg(reg *meta.Registry, path string, def int) int {
	switch v := reg.SystemConfig(path).(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return def
}

func strCfg(reg *meta.Registry, path, def string) string {
	if v, ok := reg.SystemConfig(path).(string); ok && v != "" {
		return v
	}
	return def
}

// New connects, migrates and wires everything (nothing runs until Start).
func New(ctx context.Context, cfg Config) (*Server, error) {
	if cfg.Schemas == nil {
		cfg.Schemas = schema.Embedded()
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	reg, err := LoadRegistry(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Org == "" {
		cfg.Org = "default"
		if cfg.SiteDir != "" {
			cfg.Org = filepath.Base(filepath.Clean(cfg.SiteDir))
		}
	}
	db, err := pg.NewPool(ctx, cfg.DatabaseURL, intCfg(reg, "db.pool", 10))
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	if _, err := store.Migrate(ctx, db, reg, false, "serve"); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := auth.EnsureOrg(ctx, db, cfg.Org, titleCase(cfg.Org)); err != nil {
		db.Close()
		return nil, err
	}
	s := &Server{Cfg: cfg, DB: db}
	e := engine.New(db, reg)
	e.Now = cfg.Now
	e.Log = cfg.Logf
	e.Cache = cache.New(strCfg(reg, "cache.driver", "memory"), intCfg(reg, "cache.max_mb", 64), strCfg(reg, "cache.url", ""))
	s.Engine = e
	s.Queue = jobs.New(db, intCfg(reg, "queue.workers", 2), intCfg(reg, "queue.retries", 3))
	s.Queue.Log = cfg.Logf
	e.Queue = s.Queue
	s.Queue.Handlers["action"] = s.runActionJob
	s.Queue.Handlers["flow"] = s.runFlowJob
	loc := time.UTC
	if l, err := time.LoadLocation(strCfg(reg, "scheduler.timezone", "UTC")); err == nil {
		loc = l
	}
	s.Sched = jobs.NewScheduler(s.Queue, loc)
	s.Sched.SetNow(cfg.Now)
	s.Sched.Log = cfg.Logf
	s.Sched.SetFlows(reg, cfg.Org)
	s.Hub = realtime.NewHub()
	s.Hub.Log = cfg.Logf
	issuer := strCfg(reg, "auth.issuer", "")
	if issuer == "" {
		issuer = "http://localhost" + cfg.Addr
	}
	ttl, err := time.ParseDuration(strCfg(reg, "auth.session_ttl", "24h"))
	if err != nil {
		ttl = 24 * time.Hour
	}
	s.Auth = &auth.Service{DB: db, Issuer: issuer, SessionTTL: ttl, DevOpen: cfg.DevOpen, DefaultOrg: cfg.Org, Now: cfg.Now,
		Clients: func() []auth.Client { return clients(s.Engine.Reg()) }}
	s.Changes = &changeset.Service{DB: db, AppsDir: cfg.AppsDir, SiteDir: cfg.SiteDir, ConnectorsDir: cfg.ConnectorsDir,
		Schemas: cfg.Schemas, Reg: e.Reg, Reload: s.Reload, Now: cfg.Now}
	site := ""
	if cfg.SiteDir != "" {
		site = filepath.Base(filepath.Clean(cfg.SiteDir))
	}
	s.API = &api.Server{Version: cfg.Version, Started: time.Now(), Engine: e, DB: db, Auth: s.Auth, Hub: s.Hub, Queue: s.Queue,
		Sched: s.Sched, Changes: s.Changes, WebDir: cfg.WebDir, TemplatesDir: cfg.TemplatesDir, Org: cfg.Org, Site: site, Log: cfg.Logf, DeployTarget: cfg.DeployTarget}
	return s, nil
}

func titleCase(id string) string {
	out := []rune(id)
	up := true
	for i, r := range out {
		if r == '-' || r == '_' {
			out[i] = ' '
			up = true
			continue
		}
		if up && r >= 'a' && r <= 'z' {
			out[i] = r - 32
		}
		up = false
	}
	return string(out)
}

func clients(reg *meta.Registry) []auth.Client {
	var out []auth.Client
	for _, c := range meta.AsList(reg.SystemConfig("auth.clients")) {
		cm := meta.AsMap(c)
		out = append(out, auth.Client{ID: meta.Str(cm, "id"), Label: meta.Str(cm, "label"), RedirectURIs: meta.StrList(cm["redirect_uris"]),
			Public: cm["public"] == true, Scopes: meta.StrList(cm["scopes"])})
	}
	return out
}

// Start runs the realtime listener, queue workers and scheduler until ctx ends.
func (s *Server) Start(ctx context.Context) {
	s.wg.Add(2)
	go func() { defer s.wg.Done(); s.Hub.Run(ctx, s.DB) }()
	go func() { defer s.wg.Done(); s.Sched.Run(ctx) }()
	s.Queue.Start(ctx)
}

// Wait waits for background loops after ctx ended.
func (s *Server) Wait() {
	s.wg.Wait()
	s.Queue.Wait()
}

// Reload re-reads the metadata directories, migrates the database and swaps the registry live.
func (s *Server) Reload(ctx context.Context) error {
	s.reload.Lock()
	defer s.reload.Unlock()
	reg, err := LoadRegistry(s.Cfg)
	if err != nil {
		return err
	}
	if _, err := store.Migrate(ctx, s.DB, reg, false, "changeset"); err != nil {
		return err
	}
	s.Engine.SetRegistry(reg)
	s.Sched.SetFlows(reg, s.Cfg.Org)
	s.Cfg.Logf("metadata reloaded: %d models, %d dashboards", len(reg.Models), len(reg.Dashboards))
	return nil
}

func (s *Server) runActionJob(ctx context.Context, j *jobs.Job) (any, error) {
	act := meta.AsMap(j.Args["actor"])
	a := engine.Actor{Org: j.Org, ID: meta.Str(act, "id"), UserName: meta.Str(act, "user"), Roles: meta.StrList(act["roles"])}
	if a.ID == "" {
		a.ID = "queue"
	}
	args := meta.AsMap(j.Args["args"])
	res, err := s.Engine.RunAction(ctx, a, meta.Str(j.Args, "model"), meta.Str(j.Args, "name"), meta.Str(j.Args, "action"), args, engine.ActionOpts{FromQueue: true})
	if err != nil {
		var se *engine.StatusError
		if errors.As(err, &se) && se.Status < 500 && se.Status != 409 {
			return nil, err
		}
		return nil, err
	}
	return res.Result, nil
}

func (s *Server) runFlowJob(ctx context.Context, j *jobs.Job) (any, error) {
	name := meta.Str(j.Args, "flow")
	a := engine.Actor{Org: j.Org, ID: "flow:" + name, UserName: "system", Roles: []string{"system"}}
	start := 0
	if r, ok := j.Args["resume"].(int64); ok {
		start = int(r)
	}
	return s.Engine.RunFlowFrom(ctx, a, name, meta.AsMap(j.Args["event"]), start, meta.AsMap(j.Args["vars"]))
}

// Handler is the HTTP handler.
func (s *Server) Handler() http.Handler { return s.API.Handler() }

// ListenAndServe serves HTTP until ctx ends, then shuts down gracefully.
func (s *Server) ListenAndServe(ctx context.Context) error {
	hs := &http.Server{Addr: s.Cfg.Addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return hs.Shutdown(sctx)
	}
}

// Close releases the database.
func (s *Server) Close() { s.DB.Close() }

// FindDir returns the first existing directory among candidates ("" when none).
func FindDir(candidates ...string) string {
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	return ""
}
