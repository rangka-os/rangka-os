package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

func sysStr(reg *meta.Registry, path, def string) string {
	if v, ok := reg.SystemConfig(path).(string); ok && v != "" {
		return v
	}
	return def
}

// Status is GET /system/status (the Console's system page).
func (s *Server) Status(ctx context.Context) map[string]any {
	reg := s.Engine.Reg()
	caps := map[string]any{}
	lat, err := s.DB.Ping(ctx)
	open, idle := s.DB.Stats()
	caps["db"] = map[string]any{"driver": sysStr(reg, "db.driver", "postgres"), "ok": err == nil, "latency_ms": float64(lat.Microseconds()) / 1000, "pool": map[string]any{"open": open, "idle": idle}}
	cs := s.Engine.Cache.Stats()
	caps["cache"] = map[string]any{"driver": cs.Driver, "ok": cs.OK, "hits": cs.Hits, "misses": cs.Misses, "keys": cs.Keys}
	if s.Queue != nil {
		counts, qerr := s.Queue.Counts(ctx)
		caps["queue"] = map[string]any{"driver": sysStr(reg, "queue.driver", "postgres"), "ok": qerr == nil && s.Queue.Running(), "workers": s.Queue.Workers, "counts": counts}
	}
	if s.Sched != nil {
		sc := map[string]any{"driver": sysStr(reg, "scheduler.driver", "internal"), "ok": len(s.Sched.Bad) == 0, "timezone": s.Sched.Loc.String(), "schedules": s.Sched.Status(ctx)}
		if len(s.Sched.Bad) > 0 {
			sc["errors"] = s.Sched.Bad
		}
		caps["scheduler"] = sc
	}
	transports := meta.StrList(reg.SystemConfig("realtime.transports"))
	if transports == nil {
		transports = []string{"sse"}
	}
	caps["realtime"] = map[string]any{"driver": sysStr(reg, "realtime.driver", "postgres"), "ok": s.Hub.Listening(), "transports": transports, "connections": s.Hub.Connections()}
	caps["files"] = map[string]any{"driver": sysStr(reg, "files.driver", "local"), "ok": true, "path": sysStr(reg, "files.path", "")}
	caps["search"] = map[string]any{"driver": sysStr(reg, "search.driver", "postgres"), "ok": err == nil}
	notify := map[string]any{}
	for _, ch := range []string{"email", "push", "whatsapp"} {
		cfg := meta.AsMap(reg.SystemConfig("notify." + ch))
		n := map[string]any{"driver": cfg["driver"]}
		if n["driver"] == nil {
			if c, ok := cfg["connector"]; ok {
				n["driver"] = "connector:" + meta.Str(cfg, "connector")
				_ = c
			} else {
				n["driver"] = "log"
			}
		}
		if en, ok := cfg["enabled"].(bool); ok {
			n["enabled"] = en
		} else {
			n["enabled"] = cfg != nil
		}
		notify[ch] = n
	}
	caps["notify"] = notify
	clients := []any{}
	if s.Auth.Clients != nil {
		for _, c := range s.Auth.Clients() {
			clients = append(clients, map[string]any{"id": c.ID, "label": c.Label, "redirect_uris": c.RedirectURIs, "scopes": c.Scopes, "public": c.Public})
		}
	}
	caps["auth"] = map[string]any{"issuer": s.Auth.Issuer, "clients": clients, "dev_open": s.Auth.DevOpen}
	apps := []any{}
	for _, name := range reg.AppOrder() {
		a := reg.App[name]
		apps = append(apps, map[string]any{"name": a.Name, "version": a.Version, "models": len(a.Models), "dashboards": len(a.Dashboards)})
	}
	var orgs, members int64
	if res, err := s.DB.Query(ctx, "select (select count(*) from rk_orgs), (select count(*) from rk_members)"); err == nil {
		orgs, members = res.Rows[0][0].(int64), res.Rows[0][1].(int64)
	}
	target := s.DeployTarget
	if target == "" {
		target = "dev"
	}
	return map[string]any{"version": s.Version, "uptime_s": int64(time.Since(s.Started).Seconds()), "site": s.Site, "capabilities": caps,
		"apps": apps, "orgs": orgs, "members": members, "schema_version": "0.4",
		"deploy":     map[string]any{"target": target, "started_at": s.Started.UTC().Format(time.RFC3339)},
		"connectors": s.connectorHealth(ctx)}
}

// connectorHealth counts connector activity in the last 24 hours from the audit log
// (connector.event, connector.call, connector.error entries with target connector/<name>).
func (s *Server) connectorHealth(ctx context.Context) []any {
	out := []any{}
	for _, name := range InstalledConnectors(s.Engine.Reg()) {
		c := map[string]any{"name": name, "ok": true, "events_24h": int64(0), "calls_24h": int64(0), "errors_24h": int64(0), "last_event_at": nil}
		res, err := s.DB.Query(ctx, `select count(*) filter (where kind = 'connector.event'), count(*) filter (where kind = 'connector.call'),
			count(*) filter (where kind = 'connector.error'), max(at) filter (where kind = 'connector.event')
			from rk_audit where target = $1 and at > now() - interval '24 hours'`, "connector/"+name)
		if err == nil && len(res.Rows) > 0 {
			r := res.Rows[0]
			c["events_24h"], c["calls_24h"], c["errors_24h"] = r[0], r[1], r[2]
			if t, ok := r[3].(time.Time); ok {
				c["last_event_at"] = t.UTC().Format(time.RFC3339)
			}
			c["ok"] = r[2] == int64(0)
		} else if err != nil {
			c["ok"] = false
		}
		out = append(out, c)
	}
	return out
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request, c *caller) {
	if s.Queue == nil {
		writeJSON(w, 200, map[string]any{"data": []any{}})
		return
	}
	list, err := s.Queue.List(r.Context(), c.actor.Org, r.URL.Query().Get("state"), intParam(r, "limit", 50))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": list})
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request, c *caller) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	j, err := s.Queue.Get(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	if j == nil {
		writeError(w, 404, "not_found", "job not found")
		return
	}
	writeJSON(w, 200, j)
}

func (s *Server) memberOrgs(ctx context.Context, email string) []string {
	res, err := s.DB.Query(ctx, "select m.org_id from rk_members m join rk_users u on u.id = m.user_id where u.email = $1", email)
	out := []string{}
	if err == nil {
		for _, r := range res.Rows {
			out = append(out, r[0].(string))
		}
	}
	return out
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request, c *caller) {
	q := r.URL.Query()
	orgs := s.memberOrgs(r.Context(), c.actor.ID)
	if c.actor.Org != "" && !containsStr(orgs, c.actor.Org) {
		orgs = append(orgs, c.actor.Org)
	}
	if o := q.Get("org"); o != "" && !containsStr(orgs, o) {
		writeError(w, 403, "forbidden", "you are not a member of "+o)
		return
	}
	list, err := store.ListAudit(r.Context(), s.DB, store.AuditFilter{Orgs: orgs, Org: q.Get("org"), Kind: q.Get("kind"), Actor: q.Get("actor"), Limit: intParam(r, "limit", 50)})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": list})
}

func containsStr(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// orgUsage counts what an organisation uses (CONTRACTS v0.1 decision 8).
func (s *Server) orgUsage(ctx context.Context, org string) map[string]any {
	reg := s.Engine.Reg()
	var users, records, outlets, ai int64
	if res, err := s.DB.Query(ctx, "select count(*) from rk_members where org_id = $1", org); err == nil {
		users = res.Rows[0][0].(int64)
	}
	for _, m := range reg.InstalledModels() {
		if n, err := store.Count(ctx, s.DB, org, m.Name); err == nil {
			records += n
			if m.Name == "outlet" {
				outlets = n
			}
		}
	}
	if res, err := s.DB.Query(ctx, `select count(*) from rk_changesets where org_id = $1 and author->>'kind' = 'ai' and created_at >= date_trunc('month', now())`, org); err == nil {
		ai = res.Rows[0][0].(int64)
	}
	return map[string]any{"users": users, "outlets": outlets, "records": records, "storage_mb": 0, "ai_credits_month": ai}
}

func (s *Server) listOrgs(w http.ResponseWriter, r *http.Request, c *caller) {
	res, err := s.DB.Query(r.Context(), `select o.id, o.name, coalesce((select role from rk_members m join rk_users u on u.id = m.user_id where m.org_id = o.id and u.email = $1), '')
		from rk_orgs o order by o.id`, c.actor.ID)
	if err != nil {
		fail(w, err)
		return
	}
	plan, _ := s.Engine.Reg().SiteConfig("plan").(string)
	out := []any{}
	for _, row := range res.Rows {
		id := row[0].(string)
		out = append(out, map[string]any{"id": id, "name": row[1], "plan": plan, "usage": s.orgUsage(r.Context(), id), "role": row[2], "current": id == c.actor.Org})
	}
	writeJSON(w, 200, map[string]any{"data": out})
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request, c *caller) {
	res, err := s.DB.Query(r.Context(), `select u.id, u.email, u.full_name, m.role, coalesce(m.record, ''),
		greatest((select max(created_at) from rk_sessions x where x.user_id = u.id), (select max(last_used_at) from rk_tokens t where t.user_id = u.id))
		from rk_members m join rk_users u on u.id = m.user_id where m.org_id = $1 order by u.email`, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	out := []any{}
	for _, row := range res.Rows {
		var seen any
		if t, ok := row[5].(time.Time); ok {
			seen = t.UTC().Format(time.RFC3339)
		}
		out = append(out, map[string]any{"id": row[0], "email": row[1], "name": row[2], "roles": []any{row[3]}, "two_factor": false,
			"status": "active", "last_seen_at": seen, "record": row[4]})
	}
	writeJSON(w, 200, map[string]any{"data": out})
}
