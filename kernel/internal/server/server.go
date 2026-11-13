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
	"regexp"
	"sync"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/api"
	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/backup"
	"github.com/rangka-os/rangka-os/kernel/internal/cache"
	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/changeset"
	"github.com/rangka-os/rangka-os/kernel/internal/connectors"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/files"
	"github.com/rangka-os/rangka-os/kernel/internal/imports"
	"github.com/rangka-os/rangka-os/kernel/internal/jobs"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/plan"
	"github.com/rangka-os/rangka-os/kernel/internal/realtime"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/secrets"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Config of a kernel instance.
type Config struct {
	AppsDir, SiteDir, ConnectorsDir string
	ModulesDir                      string // default: modules next to AppsDir (contract 0001)
	WebDir, TemplatesDir            string
	DatabaseURL                     string
	Addr                            string
	Org                             string
	DevOpen                         bool
	Schemas                         *schema.Set
	Version                         string
	DeployTarget                    string           // dev, docker, helm, desktop, cloud, partner
	FilesDir                        string           // attachments root (--files-dir); "" = system.rk files.path, else data/files
	AI                              *api.AIConfig    // rangka-ai sidecar (--ai-url); nil = assistant off
	Now                             func() time.Time // fixed clocks in tests
	Logf                            func(format string, args ...any)
	// RequestOnly runs the kernel for platforms that freeze the process between requests (contract 0040 §1): no
	// migrate or writes at start, no background loops; the scheduler and queue run on POST /_rangka/tick.
	RequestOnly bool
	// InternalToken guards /_rangka/ (contract 0040 §1); "" = those endpoints do not exist.
	InternalToken string
}

// Server is a running kernel.
type Server struct {
	migrated migratedState // request-only: does the database match the metadata (requestonly.go)
	Cfg      Config
	DB       *pg.Pool
	Engine   *engine.Engine
	Hub      *realtime.Hub
	Queue    *jobs.Queue
	Sched    *jobs.Scheduler
	Auth     *auth.Service
	Changes  *changeset.Service
	API      *api.Server
	// Connectors runs connector actions and webhooks (contract 0002 §A).
	Connectors *connectors.Runtime
	reload     sync.Mutex
	wg         sync.WaitGroup
}

// LoadRegistry validates the metadata directories and refuses to continue on errors.
func LoadRegistry(cfg Config) (*meta.Registry, error) {
	reg, rep := meta.ValidateOpts(meta.Options{AppsDir: cfg.AppsDir, SiteDir: cfg.SiteDir, ConnectorsDir: cfg.ConnectorsDir, ModulesDir: cfg.ModulesDir}, nil, cfg.Schemas)
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
	ups, err := upstreams(reg.SystemConfig("auth.upstream"), os.LookupEnv)
	if err != nil {
		return nil, err
	}
	// the platform's egress policy (contract 0017 §5) covers connectors and the OIDC upstream issuers system.rk names
	egress, err := connectors.EgressFromEnv(os.Getenv)
	if err != nil {
		return nil, err
	}
	var upstreamHTTP *http.Client
	if t := egress.Transport(); t != nil {
		upstreamHTTP = &http.Client{Transport: t}
	}
	fstore, err := openFiles(cfg, reg)
	if err != nil {
		return nil, err
	}
	if cfg.Org == "" {
		cfg.Org = "default"
		if cfg.SiteDir != "" {
			cfg.Org = filepath.Base(filepath.Clean(cfg.SiteDir))
		}
	}
	db, err := pg.NewPool(ctx, cfg.DatabaseURL, cfg.poolSize(reg))
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	s := &Server{Cfg: cfg, DB: db}
	if cfg.RequestOnly {
		// No writes at start: the hosting side runs `rangka migrate` and `rangka seed` (contract 0040 §2).
		if err := s.checkMigrated(ctx, reg); err != nil {
			db.Close()
			return nil, err
		}
	} else {
		warnIfBypassRLS(ctx, db, cfg.Logf)
		if _, err := store.MigrateWith(ctx, db, reg, store.MigrateOpts{Source: "serve", Before: cfg.preBackup(db, reg)}); err != nil {
			db.Close()
			return nil, fmt.Errorf("migrate: %w", err)
		}
		if err := SyncOrgs(ctx, db, reg); err != nil {
			db.Close()
			return nil, err
		}
		if err := plan.Sync(ctx, db, reg); err != nil { // contract 0028: rangka user add reads the limits from here
			db.Close()
			return nil, fmt.Errorf("plan: %w", err)
		}
		if err := auth.EnsureOrg(ctx, db, cfg.Org, titleCase(cfg.Org)); err != nil {
			db.Close()
			return nil, err
		}
	}
	e := engine.New(db, reg)
	e.Now = cfg.Now
	e.Log = cfg.Logf
	e.Cache = cache.New(strCfg(reg, "cache.driver", "memory"), intCfg(reg, "cache.max_mb", 64), strCfg(reg, "cache.url", ""))
	s.Engine = e
	if err := e.Plan.Refresh(ctx, db, reg, e.Now()); err != nil { // the read-only check reads this snapshot (contract 0028)
		cfg.Logf("plan: %v", err)
	}
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
	if cfg.RequestOnly {
		// A cold instance must still enqueue what came due since the last tick; the unique key per flow and minute
		// drops repeats (contract 0040 §3).
		s.Sched.CatchUp = time.Duration(intCfg(reg, "scheduler.catch_up_s", 600)) * time.Second
	}
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
		Clients: func() []auth.Client { return clients(s.Engine.Reg()) }, Upstreams: ups, UpstreamHTTP: upstreamHTTP, Log: cfg.Logf,
		Password: func() any { return s.Engine.Reg().SystemConfig("auth.password") }}
	s.Changes = &changeset.Service{DB: db, AppsDir: cfg.AppsDir, SiteDir: cfg.SiteDir, ConnectorsDir: cfg.ConnectorsDir, ModulesDir: cfg.ModulesDir,
		Schemas: cfg.Schemas, Reg: e.Reg, Reload: s.Reload, Now: cfg.Now,
		ReadOnly: func(id string) string { // contract 0028 §4: a person past the people limit decides nothing
			if snap := e.Plan.Peek(db, e.Reg(), e.Now(), cfg.Logf); snap != nil {
				if pe := snap.ReadOnlyPerson(id); pe != nil {
					return pe.Msg
				}
			}
			return ""
		}}
	site := ""
	if cfg.SiteDir != "" {
		site = filepath.Base(filepath.Clean(cfg.SiteDir))
	}
	s.API = &api.Server{Version: cfg.Version, Started: time.Now(), Engine: e, DB: db, Auth: s.Auth, Hub: s.Hub, Queue: s.Queue,
		Sched: s.Sched, Changes: s.Changes, Imports: &imports.Service{DB: db, Engine: e}, WebDir: cfg.WebDir, TemplatesDir: cfg.TemplatesDir, Org: cfg.Org, Site: site, Log: cfg.Logf, DeployTarget: cfg.DeployTarget,
		MaxUploadMB: intCfg(reg, "files.max_upload_mb", 20), AI: cfg.AI}
	if cfg.InternalToken != "" {
		budget := time.Duration(intCfg(reg, "queue.tick_budget_s", 40)) * time.Second
		s.API.Internal = &api.Internal{Token: cfg.InternalToken, Tick: func(ctx context.Context) (any, error) { return s.Tick(ctx, budget) }}
	}
	if fstore != nil {
		s.API.Files = fstore // keep a nil *Local out of the interface
	}
	// connector secrets (contract 0017): without a KEK the kernel starts and db-driver writes answer secrets_key_missing
	kek, err := secrets.LoadKEK(nil)
	if err != nil {
		db.Close()
		return nil, err
	}
	platformSecrets, err := secrets.PlatformFromEnv(os.Getenv)
	if err != nil {
		db.Close()
		return nil, err
	}
	s.API.Secrets = &secrets.Service{DB: db, Reg: e.Reg, KEK: kek, Now: cfg.Now, Vault: secrets.VaultFromEnv(nil), Log: cfg.Logf, Platform: platformSecrets}
	if d := platformSecrets.Driver; (d == "file" || d == "vault") && platformSecrets.Path == "" {
		cfg.Logf("secrets: RANGKA_SECRETS_DRIVER=%s without RANGKA_SECRETS_PATH; connector secrets read as unset", d)
	}
	if kek == nil && s.API.Secrets.Driver() == "db" {
		cfg.Logf("secrets: driver db without RANGKA_MASTER_KEY; connector secrets cannot be set (run rangka keygen)")
	}
	if s.API.Secrets.Driver() == "vault" && s.API.Secrets.Vault == nil {
		cfg.Logf("secrets: driver vault without VAULT_ADDR; connector secrets read as unset")
	}
	// the connector runtime (contract 0002 §A): calls run as "call" jobs, webhooks start flows
	s.Connectors = &connectors.Runtime{Resolver: connectors.Resolver{Reg: e.Reg, Secrets: s.API.Secrets}, DB: db, Now: cfg.Now, Log: cfg.Logf, Egress: egress,
		StartFlow: func(ctx context.Context, qr pg.Querier, org, flow string, event map[string]any, key string) error {
			_, err := s.Queue.Enqueue(ctx, qr, "flow", org, map[string]any{"flow": flow, "event": event}, engine.EnqueueOpts{UniqueKey: key})
			return err
		}}
	s.API.Connectors = s.Connectors
	s.Queue.Handlers["call"] = s.runCallJob
	s.Queue.Handlers["poll"] = s.runPollJob
	s.Sched.SetPolls(cfg.Org, pollSpecs(reg))
	e.CallTarget = callTarget
	s.Changes.Requires = s.API.Secrets.Requires(api.InstalledConnectors)
	return s, nil
}

// openFiles opens the attachments store (contract 0011 §1). Root: cfg.FilesDir (--files-dir, $RANGKA_FILES_DIR),
// then system.rk files.path, then data/files; relative to the working directory. nil when files.enabled is false.
func openFiles(cfg Config, reg *meta.Registry) (*files.Local, error) {
	if reg.SystemConfig("files.enabled") == false {
		return nil, nil
	}
	if d := strCfg(reg, "files.driver", "local"); d != "local" {
		return nil, fmt.Errorf("files: driver %s is not available yet; use local", d)
	}
	l, err := files.NewLocal(cfg.filesRoot(reg))
	if err != nil {
		return nil, fmt.Errorf("files: %w", err)
	}
	return l, nil
}

// filesRoot is the attachments directory (backup.FilesDir: cfg.FilesDir, then system.rk files.path, then
// data/files, relative to the working directory). The pre-migrate backup archives the same directory.
func (cfg Config) filesRoot(reg *meta.Registry) string {
	return backup.FilesDir(reg, cfg.FilesDir)
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

var (
	envRef     = regexp.MustCompile(`^\$\{([A-Z][A-Z0-9_]*)\}$`)
	upstreamID = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// upstreams reads system.rk auth.upstream (contract 0042 §1) and checks it: id required, [a-z][a-z0-9_]* and
// unique; issuer required and https (http only on a loopback host); client_id required; client_secret only as
// ${ENV}. ${ENV} values are resolved now; a missing variable stops the start with an error that names the
// variable, never a value. The list is fixed until the next start.
func upstreams(cfg any, lookup func(string) (string, bool)) ([]auth.Upstream, error) {
	var out []auth.Upstream
	seen := map[string]bool{}
	for i, u := range meta.AsList(cfg) {
		um := meta.AsMap(u)
		up := auth.Upstream{ID: meta.Str(um, "id"), Label: meta.Str(um, "label"), Issuer: meta.Str(um, "issuer")}
		where := fmt.Sprintf("auth.upstream[%d]", i)
		if !upstreamID.MatchString(up.ID) {
			return nil, fmt.Errorf("%s: id %q must match [a-z][a-z0-9_]*", where, up.ID)
		}
		where = "auth.upstream " + up.ID
		if seen[up.ID] {
			return nil, fmt.Errorf("%s: id is used twice", where)
		}
		seen[up.ID] = true
		if up.Issuer == "" {
			return nil, fmt.Errorf("%s: issuer is required", where)
		}
		if !auth.SecureURL(up.Issuer) {
			return nil, fmt.Errorf("%s: issuer must be an https URL (http only for localhost)", where)
		}
		if up.Label == "" {
			up.Label = up.ID
		}
		for _, f := range []struct {
			key    string
			dst    *string
			secret bool
		}{{"client_id", &up.ClientID, false}, {"client_secret", &up.ClientSecret, true}} {
			v := meta.Str(um, f.key)
			m := envRef.FindStringSubmatch(v)
			switch {
			case m != nil && meta.PlatformEnv(m[1]):
				return nil, fmt.Errorf("%s: %s reads ${%s}, a variable of the platform; bind a variable of your own", where, f.key, m[1])
			case m != nil:
				val, ok := lookup(m[1])
				if !ok || val == "" {
					return nil, fmt.Errorf("%s: %s reads ${%s}, which is not set", where, f.key, m[1])
				}
				v = val
			case f.secret && v != "":
				return nil, fmt.Errorf("%s: client_secret must be a ${ENV} reference, not a literal", where)
			}
			*f.dst = v
		}
		if up.ClientID == "" {
			return nil, fmt.Errorf("%s: client_id is required", where)
		}
		out = append(out, up)
	}
	return out, nil
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
	// migrate and its pre-migrate backup are installation-wide: not limited by the caller's org (RLS)
	ctx = pg.WithOrg(ctx, "")
	reg, err := LoadRegistry(s.Cfg)
	if err != nil {
		return err
	}
	if _, err := store.MigrateWith(ctx, s.DB, reg, store.MigrateOpts{Source: "changeset", Before: s.Cfg.preBackup(s.DB, reg)}); err != nil {
		return err
	}
	if err := SyncOrgs(ctx, s.DB, reg); err != nil {
		return err
	}
	if err := plan.Sync(ctx, s.DB, reg); err != nil {
		return err
	}
	s.Engine.SetRegistry(reg)
	s.Sched.SetFlows(reg, s.Cfg.Org)
	s.Sched.SetPolls(s.Cfg.Org, pollSpecs(reg))
	s.Cfg.Logf("metadata reloaded: %d models, %d dashboards", len(reg.Models), len(reg.Dashboards))
	return nil
}

// warnIfBypassRLS logs when the database role skips row-level security (superuser or BYPASSRLS).
func warnIfBypassRLS(ctx context.Context, db *pg.Pool, logf func(string, ...any)) {
	res, err := db.Query(ctx, "select rolsuper or rolbypassrls from pg_roles where rolname = current_user")
	if err == nil && len(res.Rows) == 1 && res.Rows[0][0] == true {
		logf("warning: connected as a superuser or BYPASSRLS role; row-level security is not enforced")
	}
}

func (s *Server) runActionJob(ctx context.Context, j *jobs.Job) (any, error) {
	ctx = pg.WithOrg(ctx, j.Org)
	act := meta.AsMap(j.Args["actor"])
	// a queued action replays a person's request: same roles, same permission checks (not System). The roles
	// are the ones captured at enqueue time; a member demoted before the job runs keeps the old grants.
	a := engine.Actor{Org: j.Org, ID: meta.Str(act, "id"), UserName: meta.Str(act, "user"), Roles: meta.StrList(act["roles"]), Outlets: meta.StrList(act["outlets"])}
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
	ctx = pg.WithOrg(ctx, j.Org)
	name := meta.Str(j.Args, "flow")
	// a resumed wait: runs as whoever started the flow; schedules and kernel-started flows run as System
	a := engine.JobActor(j.Org, j.Args["actor"])
	if a.System {
		a.Roles = append([]string{"system"}, a.Roles...)
		if a.ID == "" || a.ID == "system" {
			a.ID = "flow:" + name
		}
		if a.UserName == "" {
			a.UserName = "system"
		}
	}
	start := 0
	if r, ok := j.Args["resume"].(int64); ok {
		start = int(r)
	}
	out, err := s.Engine.RunFlowResume(ctx, a, name, meta.AsMap(j.Args["event"]), start, meta.AsMap(j.Args["vars"]), j.Args["manual"] == true, meta.AsMap(j.Args["flow_key"]))
	var perm *jobs.Permanent
	if err != nil && (j.Attempt >= j.MaxAttempts || errors.As(err, &perm)) {
		// the run will never finish: its idempotency key is free again for the same event (audit flowkey-release N1);
		// a replay of this dead-lettered job is then skipped
		if rerr := s.Engine.ReleaseFlowJob(context.WithoutCancel(ctx), j.Org, j.Args); rerr != nil {
			s.Engine.Log("flow %s: release idempotency key: %v", name, rerr)
		}
	}
	return out, err
}

// runCallJob sends one connector action (a flow's call: step). Its errors are scrubbed; a call that cannot succeed
// (keys missing, a refused host, a status the action does not retry) is discarded at once.
func (s *Server) runCallJob(ctx context.Context, j *jobs.Job) (any, error) {
	ctx = pg.WithOrg(ctx, j.Org)
	return s.Connectors.Call(ctx, j.Org, meta.Str(j.Args, "connector"), meta.Str(j.Args, "action"), meta.AsMap(j.Args["input"]))
}

// runPollJob runs one poll of a connector trigger (contract 0002 §A.1).
func (s *Server) runPollJob(ctx context.Context, j *jobs.Job) (any, error) {
	ctx = pg.WithOrg(ctx, j.Org)
	return s.Connectors.Poll(ctx, j.Org, meta.Str(j.Args, "connector"), meta.Str(j.Args, "trigger"))
}

// pollSpecs lists the scheduler's poll jobs: the via: poll triggers installed flows listen to.
func pollSpecs(reg *meta.Registry) []jobs.PollSpec {
	var out []jobs.PollSpec
	for _, p := range connectors.Polls(reg) {
		out = append(out, jobs.PollSpec{Name: p.Connector + "." + p.Trigger, Every: p.Every,
			Args: map[string]any{"connector": p.Connector, "trigger": p.Trigger}})
	}
	return out
}

// callTarget resolves a call: target and its job options: retry.max + 1 attempts, and the action's idempotency
// expression (CEL over input) as the unique key, so a retried flow never sends twice.
func callTarget(reg *meta.Registry, call string, input map[string]any) (string, string, engine.EnqueueOpts, error) {
	conn, act, err := connectors.ResolvePort(reg, call)
	if err != nil {
		return "", "", engine.EnqueueOpts{}, err
	}
	r := connectors.Resolver{Reg: func() *meta.Registry { return reg }}
	_, a, err := r.Action(conn, act)
	if err != nil {
		return "", "", engine.EnqueueOpts{}, err
	}
	opts := engine.EnqueueOpts{MaxAttempts: connectors.MaxAttempts(a)}
	if src, ok := a["idempotency"].(string); ok && src != "" {
		v, err := cel.Eval(src, &cel.Env{Vars: func(n string) (any, bool) {
			if n == "input" {
				return input, true
			}
			return nil, false
		}})
		if err == nil {
			if k := fmt.Sprint(cel.Unwrap(v)); k != "" && k != "<nil>" {
				opts.UniqueKey = k
			}
		}
	}
	return conn, act, opts, nil
}

// Handler is the HTTP handler.
func (s *Server) Handler() http.Handler { return s.API.Handler() }

// ListenAndServe serves HTTP until ctx ends, then shuts down gracefully.
func (s *Server) ListenAndServe(ctx context.Context) error {
	hs := &http.Server{Addr: s.Cfg.Addr, Handler: s.requestOnly(s.Handler()), ReadHeaderTimeout: 10 * time.Second}
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
