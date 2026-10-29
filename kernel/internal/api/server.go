package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/changeset"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/jobs"
	"github.com/rangka-os/rangka-os/kernel/internal/mcp"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/realtime"
)

// Server holds what the handlers need. The server package fills it.
type Server struct {
	Version      string
	Started      time.Time
	Engine       *engine.Engine
	DB           *pg.Pool
	Auth         *auth.Service
	Hub          *realtime.Hub
	Queue        *jobs.Queue
	Sched        *jobs.Scheduler
	Changes      *changeset.Service
	WebDir       string // rangka-os/web, served at /
	TemplatesDir string // rangka-os/templates (theme and layout templates)
	Org          string // default organisation
	Site         string // site name
	DeployTarget string // dev, docker, helm, desktop, cloud, partner
	Log          func(format string, args ...any)
}

type ctxKey struct{}

type caller struct {
	p     *auth.Principal
	actor engine.Actor
}

// Actor converts a principal into an engine actor.
func Actor(p *auth.Principal) engine.Actor {
	u := p.User
	name := u.Record
	if name == "" {
		name = u.Email
	}
	return engine.Actor{Org: u.Org, ID: u.Email, UserName: name, Roles: u.Roles, Human: p.Human()}
}

// ChangeActor converts a principal into a change-set actor.
func ChangeActor(p *auth.Principal) changeset.Actor {
	kind := "user"
	switch p.Via {
	case "token":
		kind = "cli"
	case "oidc":
		kind = "ai"
	}
	return changeset.Actor{ID: p.User.Email, Org: p.User.Org, Roles: p.User.Roles, Human: p.Human(), Kind: kind}
}

func (s *Server) principal(r *http.Request) (*auth.Principal, error) {
	p, err := s.Auth.Resolve(r)
	if err != nil {
		return nil, err
	}
	if p != nil && p.User.Org == "" {
		p.User.Org = s.Org
	}
	return p, nil
}

// authed wraps handlers that need a signed-in caller.
func (s *Server) authed(h func(w http.ResponseWriter, r *http.Request, c *caller)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := s.principal(r)
		if err != nil && !errors.Is(err, auth.ErrUnauthenticated) {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		if p == nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="rangka"`)
			writeError(w, http.StatusUnauthorized, "unauthenticated", auth.ErrUnauthenticated.Error())
			return
		}
		h(w, r, &caller{p: p, actor: Actor(p)})
	}
}

// Handler builds the router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	a := s.authed
	// records
	mux.HandleFunc("GET /api/v1/meta", a(s.getMeta))
	mux.HandleFunc("GET /api/v1/r/{model}", a(s.listRecords))
	mux.HandleFunc("POST /api/v1/r/{model}", a(s.createRecord))
	mux.HandleFunc("GET /api/v1/r/{model}/{name}", a(s.getRecord))
	mux.HandleFunc("PATCH /api/v1/r/{model}/{name}", a(s.patchRecord))
	mux.HandleFunc("DELETE /api/v1/r/{model}/{name}", a(s.deleteRecord))
	mux.HandleFunc("POST /api/v1/r/{model}/{name}/{verb}", a(s.lifecycle))
	mux.HandleFunc("POST /api/v1/r/{model}/{name}/actions/{action}", a(s.runAction))
	mux.HandleFunc("GET /api/v1/dashboards/{name}", a(s.getDashboard))
	mux.HandleFunc("POST /api/v1/flows/{name}/run", a(s.runFlow))
	// realtime
	mux.HandleFunc("GET /api/v1/events", a(func(w http.ResponseWriter, r *http.Request, c *caller) { s.Hub.ServeSSE(w, r, c.actor.Org) }))
	// change sets
	mux.HandleFunc("GET /api/v1/changesets", a(s.listChangesets))
	mux.HandleFunc("POST /api/v1/changesets", a(s.proposeChangeset))
	mux.HandleFunc("GET /api/v1/changesets/{id}", a(s.getChangeset))
	mux.HandleFunc("POST /api/v1/changesets/{id}/{verb}", a(s.changesetVerb))
	// system, orgs, files
	mux.HandleFunc("GET /api/v1/system/status", a(func(w http.ResponseWriter, r *http.Request, c *caller) { writeJSON(w, 200, s.Status(r.Context())) }))
	mux.HandleFunc("GET /api/v1/system/jobs", a(s.listJobs))
	mux.HandleFunc("GET /api/v1/system/jobs/{id}", a(s.getJob))
	mux.HandleFunc("GET /api/v1/system/audit", a(s.listAudit))
	mux.HandleFunc("GET /api/v1/orgs", a(s.listOrgs))
	mux.HandleFunc("GET /api/v1/orgs/{id}/members", a(s.listMembers))
	mux.HandleFunc("GET /api/v1/files", a(s.listFiles))
	mux.HandleFunc("GET /api/v1/files/{path...}", a(s.getFile))
	mux.HandleFunc("GET /api/v1/files/meta", a(s.listFiles)) // v0 aliases
	mux.HandleFunc("GET /api/v1/files/meta/{path...}", a(s.getFile))
	mux.HandleFunc("POST /api/v1/tokens", a(s.createToken))
	// auth and Rangka ID
	mux.HandleFunc("POST /auth/login", s.login)
	mux.HandleFunc("POST /auth/logout", s.logout)
	mux.HandleFunc("GET /auth/me", a(s.me))
	mux.HandleFunc("GET /.well-known/openid-configuration", s.Auth.Discovery)
	mux.HandleFunc("GET /oauth/authorize", s.Auth.Authorize)
	mux.HandleFunc("POST /oauth/authorize", s.Auth.Authorize)
	mux.HandleFunc("POST /oauth/token", s.Auth.Token)
	mux.HandleFunc("GET /oauth/userinfo", s.Auth.UserInfo)
	mux.HandleFunc("GET /oauth/jwks", s.Auth.JWKS)
	// MCP
	mcpHandler := &mcp.Handler{
		Version: s.Version,
		Resolve: func(r *http.Request) (*mcp.Caller, error) {
			p, err := s.principal(r)
			if err != nil || p == nil || !mcp.RequireScope(p) {
				return nil, err
			}
			return &mcp.Caller{Actor: Actor(p), Change: ChangeActor(p)}, nil
		},
		Engine:  func() *engine.Engine { return s.Engine },
		Changes: s.Changes,
		Meta:    func(ctx context.Context, a engine.Actor) (any, error) { return s.Meta(ctx, a) },
		File:    func(ctx context.Context, p string) (any, error) { return s.File(p) },
		Status:  func(ctx context.Context) any { return s.Status(ctx) },
	}
	for _, m := range []string{"POST", "GET", "DELETE"} {
		mux.Handle(m+" /mcp", mcpHandler)
	}
	mux.HandleFunc("GET /api/v1/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, 404, "not_found", "no such endpoint "+r.URL.Path)
	})
	// static web (prototype, console, design system)
	if s.WebDir != "" {
		mux.Handle("GET /", s.static())
	}
	return s.logRequests(cors(mux))
}

func (s *Server) static() http.Handler {
	fsrv := http.FileServer(http.Dir(s.WebDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			if _, err := os.Stat(filepath.Join(s.WebDir, "index.html")); err != nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Write([]byte(indexPage(s.WebDir)))
				return
			}
		}
		fsrv.ServeHTTP(w, r)
	})
}

func indexPage(dir string) string {
	var links []string
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if e.IsDir() {
			links = append(links, `<li><a href="/`+e.Name()+`/">`+e.Name()+`</a></li>`)
		}
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Rangka OS</title>
<style>body{font:15px/1.5 system-ui,sans-serif;max-width:640px;margin:48px auto;padding:0 16px;color:#172b4d}code{background:#f1f2f4;padding:0 4px;border-radius:3px}</style></head>
<body><h1>Rangka OS kernel</h1><p>Web apps served from <code>rangka-os/web</code>:</p><ul>` + strings.Join(links, "") + `</ul>
<p>API: <a href="/api/v1/meta">/api/v1/meta</a>, <a href="/api/v1/system/status">/api/v1/system/status</a>, <a href="/.well-known/openid-configuration">OIDC discovery</a>, MCP at <code>POST /mcp</code>.</p></body></html>`
}

// cors allows the Console and local tools on other origins to call the API with credentials.
func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && (strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/mcp" || strings.HasPrefix(r.URL.Path, "/auth/") || strings.HasPrefix(r.URL.Path, "/oauth/")) {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Mcp-Session-Id, Mcp-Protocol-Version")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(c int) { w.status = c; w.ResponseWriter.WriteHeader(c) }
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		h.ServeHTTP(sw, r)
		if s.Log != nil && (strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/mcp" || strings.HasPrefix(r.URL.Path, "/auth/") || strings.HasPrefix(r.URL.Path, "/oauth/")) {
			s.Log("%s %s %d %s", r.Method, r.URL.RequestURI(), sw.status, time.Since(start).Round(time.Millisecond))
		}
	})
}
