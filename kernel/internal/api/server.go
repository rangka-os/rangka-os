package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/changeset"
	"github.com/rangka-os/rangka-os/kernel/internal/connectors"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/files"
	"github.com/rangka-os/rangka-os/kernel/internal/imports"
	"github.com/rangka-os/rangka-os/kernel/internal/jobs"
	"github.com/rangka-os/rangka-os/kernel/internal/mcp"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/realtime"
	"github.com/rangka-os/rangka-os/kernel/internal/secrets"
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
	Imports      *imports.Service
	WebDir       string              // rangka-os/web, served at /
	TemplatesDir string              // rangka-os/templates (theme and layout templates)
	Org          string              // default organisation
	Site         string              // site name
	DeployTarget string              // dev, docker, helm, desktop, cloud, partner
	Files        files.Store         // attachments blob store; nil = files capability disabled (501 files_disabled)
	MaxUploadMB  int                 // files.max_upload_mb; 0 = 20
	AI           *AIConfig           // rangka-ai sidecar; nil = assistant off (contract 0020)
	Connectors   *connectors.Runtime // connector webhooks (/hooks/…); nil = 404
	Secrets      *secrets.Service    // connector secrets (contract 0017); nil = every auth field reports unset
	Internal     *Internal           // /_rangka/ endpoints for the hosting side; nil = they do not exist (contract 0040 §1)
	Log          func(format string, args ...any)

	previewMu sync.Mutex // guards previews (preview.go)
	previews  []*previewEntry

	blobMu sync.Mutex // orders blob removal against uploads of the same bytes (one process)

	ai aiState // assistant limits and health probe (ai.go)
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
	return engine.Actor{Org: u.Org, ID: u.Email, UserName: name, Roles: u.Roles, Human: p.Human(), Outlets: u.Outlets}
}

// ChangeActor converts a principal into a change-set actor.
func ChangeActor(p *auth.Principal) changeset.Actor {
	kind := "user"
	switch p.Via {
	case "token":
		kind = "cli"
	case "oidc", "ai":
		kind = "ai"
	}
	return changeset.Actor{ID: p.User.Email, Org: p.User.Org, Roles: p.User.Roles, Human: p.Human(), Kind: kind, Assistant: p.Via == "ai"}
}

func (s *Server) principal(r *http.Request) (*auth.Principal, error) {
	p, err := s.Auth.Resolve(r)
	if err != nil {
		return nil, err
	}
	if p != nil && p.User.Org == "" && p.Via == "dev" {
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
		// the embedded assistant only reads over REST; it proposes change sets over MCP (contract 0020 §4)
		if p.Via == "ai" && r.Method != http.MethodGet && r.Method != http.MethodHead && strings.HasPrefix(r.URL.Path, "/api/v1/") {
			writeError(w, http.StatusForbidden, "ai_forbidden", auth.AIForbiddenMessage)
			return
		}
		c := &caller{p: p, actor: Actor(p)}
		// queries made while serving this request run with rangka.org set (row-level security backstop)
		h(w, r.WithContext(pg.WithOrg(r.Context(), c.actor.Org)), c)
	}
}

// Handler builds the router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	a := s.authed
	// records
	mux.HandleFunc("GET /api/v1/meta", a(s.getMeta))
	mux.HandleFunc("GET /api/v1/r/{model}", a(s.listRecords))
	mux.HandleFunc("GET /api/v1/aggregate/{model}", a(s.aggregateRecords))
	mux.HandleFunc("POST /api/v1/r/{model}", a(s.idempotent(s.createRecord)))
	mux.HandleFunc("POST /api/v1/compute/{model}", a(s.computeRecord))
	mux.HandleFunc("GET /api/v1/r/{model}/{name}", a(s.getRecord))
	mux.HandleFunc("PATCH /api/v1/r/{model}/{name}", a(s.patchRecord))
	mux.HandleFunc("DELETE /api/v1/r/{model}/{name}", a(s.deleteRecord))
	mux.HandleFunc("POST /api/v1/r/{model}/{name}/{verb}", a(s.idempotent(s.lifecycle)))
	mux.HandleFunc("GET /api/v1/attachments/{id}", a(s.getAttachment))
	mux.HandleFunc("DELETE /api/v1/attachments/{id}", a(s.deleteAttachment))
	mux.HandleFunc("POST /api/v1/r/{model}/{name}/actions/{action}", a(s.idempotent(s.runAction)))
	// guarded record routes: the guard checks the permission, then the stream's handler runs
	mux.HandleFunc("GET /api/v1/r/{model}/{name}/print", s.recordGuard("print", s.printRecord))
	mux.HandleFunc("GET /api/v1/r/{model}/{name}/attachments", s.recordGuard("read", s.listAttachments))
	mux.HandleFunc("POST /api/v1/r/{model}/{name}/attachments", s.recordGuard("write", s.uploadAttachment))
	mux.HandleFunc("GET /api/v1/dashboards/{name}", a(s.getDashboard))
	mux.HandleFunc("GET /api/v1/screens/{name}", a(s.getScreen))
	mux.HandleFunc("GET /api/v1/export/r/{model}", a(s.exportRecords))
	mux.HandleFunc("GET /api/v1/export/dashboards/{name}", a(s.exportDashboard))
	mux.HandleFunc("POST /api/v1/flows/{name}/run", a(s.runFlow))
	// imports
	mux.HandleFunc("POST /api/v1/imports/{import}", a(s.previewImport))
	mux.HandleFunc("POST /api/v1/imports/{import}/{id}/commit", a(s.commitImport))
	mux.HandleFunc("GET /api/v1/imports/{import}/{id}", a(s.getImport))
	mux.HandleFunc("GET /api/v1/imports/{import}", a(s.listImports))
	// realtime
	mux.HandleFunc("GET /api/v1/events", a(func(w http.ResponseWriter, r *http.Request, c *caller) {
		s.Hub.ServeSSE(w, r, c.actor.Org, s.eventAllow(r.Context(), c.actor))
	}))
	// approvals
	mux.HandleFunc("GET /api/v1/approvals", a(s.listApprovals))
	mux.HandleFunc("GET /api/v1/approvals/{id}", a(s.getApproval))
	mux.HandleFunc("POST /api/v1/approvals/{id}/approve", a(s.decideApproval(true)))
	mux.HandleFunc("POST /api/v1/approvals/{id}/reject", a(s.decideApproval(false)))
	// change sets
	mux.HandleFunc("GET /api/v1/changesets", a(s.listChangesets))
	mux.HandleFunc("POST /api/v1/changesets", a(s.proposeChangeset))
	mux.HandleFunc("GET /api/v1/changesets/{id}", a(s.getChangeset))
	mux.HandleFunc("POST /api/v1/changesets/{id}/{verb}", a(s.changesetVerb))
	// live preview of a change set (contract 0015 §4): read-only
	mux.HandleFunc("GET /api/v1/preview", a(s.previewRoot))
	mux.HandleFunc("GET /api/v1/preview/{id}/meta", a(s.previewGuard(s.previewMeta)))
	mux.HandleFunc("GET /api/v1/preview/{id}/r/{model}", a(s.previewGuard(s.previewList)))
	mux.HandleFunc("GET /api/v1/preview/{id}/r/{model}/{name}", a(s.previewGuard(s.previewGet)))
	mux.HandleFunc("GET /api/v1/preview/{id}/dashboards/{name}", a(s.previewGuard(s.previewDashboard)))
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		mux.HandleFunc(m+" /api/v1/preview/{path...}", a(s.previewReadOnly))
		mux.HandleFunc(m+" /api/v1/preview", a(s.previewReadOnly))
	}
	// process canvas (contract 0014)
	mux.HandleFunc("GET /api/v1/processes", a(s.listProcesses))
	mux.HandleFunc("GET /api/v1/processes/{name}", a(s.getProcess))
	mux.HandleFunc("POST /api/v1/processes/{name}/ops", a(s.processOps))
	mux.HandleFunc("PUT /api/v1/processes/{name}/layout", a(s.putProcessLayout))
	// system, orgs, files
	mux.HandleFunc("GET /api/v1/system/status", a(func(w http.ResponseWriter, r *http.Request, c *caller) {
		writeJSON(w, 200, s.Status(r.Context(), c.actor.Org))
	}))
	mux.HandleFunc("GET /api/v1/system/jobs", a(s.listJobs))
	mux.HandleFunc("GET /api/v1/system/jobs/{id}", a(s.getJob))
	mux.HandleFunc("POST /api/v1/system/jobs/{id}/replay", a(s.replayJob))
	mux.HandleFunc("GET /api/v1/system/audit", a(s.listAudit))
	mux.HandleFunc("GET /api/v1/catalogue", a(s.getCatalogue))              // modules, apps and connectors to install (contract 0036)
	mux.HandleFunc("POST /api/v1/catalogue/propose", a(s.proposeCatalogue)) // Pasang / Lepas (contract 0036 §4)
	// connector secrets, write-only (contract 0017 §3): no answer ever carries a value
	mux.HandleFunc("GET /api/v1/connectors/{connector}/auth", a(s.getConnectorAuth))
	mux.HandleFunc("PUT /api/v1/connectors/{connector}/auth/{field}", a(s.putConnectorAuth))
	mux.HandleFunc("DELETE /api/v1/connectors/{connector}/auth/{field}", a(s.deleteConnectorAuth))
	// member API (contract 0043): API tokens with members:write only
	mux.HandleFunc("PUT /api/v1/members/{upstream}/{subject}", a(s.putMember))
	mux.HandleFunc("DELETE /api/v1/members/{upstream}/{subject}", a(s.deleteMember))
	mux.HandleFunc("GET /api/v1/plan", a(s.getPlan)) // plan limits and usage (contract 0028)
	mux.HandleFunc("GET /api/v1/orgs", a(s.listOrgs))
	mux.HandleFunc("GET /api/v1/orgs/{id}/members", a(s.listMembers))
	mux.HandleFunc("GET /api/v1/files", a(s.listFiles))
	mux.HandleFunc("GET /api/v1/files/{path...}", a(s.getFile))
	mux.HandleFunc("GET /api/v1/files/meta", a(s.listFiles)) // v0 aliases
	mux.HandleFunc("GET /api/v1/files/meta/{path...}", a(s.getFile))
	mux.HandleFunc("POST /api/v1/tokens", a(s.createToken))
	// embedded assistant (contract 0020): relayed to the rangka-ai sidecar; a() refuses assistant tokens
	mux.HandleFunc("POST /api/v1/ai/chat", a(s.aiChat))
	// connector webhooks (contract 0002 §A.1, 0017 §7): not signed in; the org's secrets verify each request
	mux.HandleFunc("POST /hooks/{org}/{connector}/{trigger}", s.hook)
	mux.HandleFunc("GET /hooks/{org}/{connector}/{trigger}", s.hook)
	mux.HandleFunc("POST /hooks/{connector}/{trigger}", s.hook)
	mux.HandleFunc("GET /hooks/{connector}/{trigger}", s.hook)
	// auth and Rangka ID
	mux.HandleFunc("POST /auth/login", s.login)
	mux.HandleFunc("POST /auth/logout", s.logout)
	mux.HandleFunc("POST /auth/org", a(s.switchOrg))
	mux.HandleFunc("GET /auth/me", a(s.me))
	// upstream sign-in (contract 0042): the kernel as relying party of an external OpenID Provider
	mux.HandleFunc("GET /auth/providers", s.Auth.Providers)
	mux.HandleFunc("GET /auth/upstream/{id}/start", s.Auth.UpstreamStart)
	mux.HandleFunc("GET /auth/upstream/{id}/callback", s.Auth.UpstreamCallback)
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
		Engine:    func() *engine.Engine { return s.Engine },
		Changes:   s.Changes,
		Meta:      func(ctx context.Context, a engine.Actor) (any, error) { return s.Meta(ctx, a) },
		File:      func(ctx context.Context, p string) (any, error) { return s.File(p) },
		Status:    func(ctx context.Context, org string) any { return s.Status(ctx, org) },
		Catalogue: func() any { return s.Engine.Reg().Catalogue() },
		Skills:    s.skillLibrary,
	}
	for _, m := range []string{"POST", "GET", "DELETE"} {
		mux.Handle(m+" /mcp", mcpHandler)
	}
	mux.HandleFunc("GET /api/v1/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, 404, "not_found", "no such endpoint "+r.URL.Path)
	})
	if s.Internal != nil && s.Internal.Token != "" {
		mux.HandleFunc("POST /_rangka/tick", s.internalOnly(s.tick))
	}
	// static web (app, console, design system)
	if s.WebDir != "" {
		mux.Handle("GET /", s.static())
	}
	return s.logRequests(cors(mux))
}

func (s *Server) static() http.Handler {
	fsrv := http.FileServer(http.Dir(s.WebDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// the end-user app moved from /prototype/ to /app/; old links keep working
		if r.URL.Path == "/prototype" || strings.HasPrefix(r.URL.Path, "/prototype/") {
			http.Redirect(w, r, "/app/", http.StatusMovedPermanently)
			return
		}
		if r.URL.Path == "/" {
			if _, err := os.Stat(filepath.Join(s.WebDir, "app", "index.html")); err == nil {
				http.Redirect(w, r, "/app/", http.StatusFound)
				return
			}
			if _, err := os.Stat(filepath.Join(s.WebDir, "index.html")); err != nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Write([]byte(indexPage(s.WebDir)))
				return
			}
		}
		staticHeaders(w.Header(), r.URL.Path)
		fsrv.ServeHTTP(w, r)
	})
}

// staticHeaders sets what the installable web app needs from the file server. The service worker and the pages it
// serves must be revalidated on every load, or a kernel upgrade keeps running yesterday's worker (browsers cap the
// worker's HTTP cache at 24 hours, but "no-cache" makes it immediate); the manifest needs its own media type, which
// Go's mime table does not know.
func staticHeaders(h http.Header, p string) {
	switch {
	case strings.HasSuffix(p, "/sw.js"):
		h.Set("Cache-Control", "no-cache")
		h.Set("Content-Type", "text/javascript; charset=utf-8")
	case strings.HasSuffix(p, ".webmanifest"):
		h.Set("Cache-Control", "no-cache")
		h.Set("Content-Type", "application/manifest+json")
	case strings.HasSuffix(p, "/") || strings.HasSuffix(p, ".html"):
		h.Set("Cache-Control", "no-cache")
	}
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
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, Mcp-Session-Id, Mcp-Protocol-Version")
			w.Header().Set("Access-Control-Expose-Headers", "Idempotency-Replayed, Location, Retry-After")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
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
			target := r.URL.RequestURI()
			if strings.HasPrefix(r.URL.Path, "/auth/upstream/") {
				target = r.URL.EscapedPath() // the callback's query carries the provider's code and our state
			}
			s.Log("%s %s %d %s", r.Method, target, sw.status, time.Since(start).Round(time.Millisecond))
		}
	})
}
