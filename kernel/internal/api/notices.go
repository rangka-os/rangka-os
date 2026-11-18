package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// The person's own inbox of kernel notices (contract 0066 §4.1). Only the caller's own rows: there is no way to read
// or create someone else's notice.

// noticeScopeLimited refuses tokens minted for the member API or the host's secrets: they are not a person reading
// their inbox.
func noticeScopeLimited(w http.ResponseWriter, c *caller) bool {
	if c.p.Via == "token" && (c.p.HasScope(auth.ScopeMembersWrite) || c.p.HasScope("secrets:host")) {
		writeError(w, http.StatusForbidden, "scope_limited", "Token ini tidak bisa membaca kabar.")
		return true
	}
	return false
}

// listNotices is GET /api/v1/notices?limit=50&before=<cursor>&unread=1 → {data, unread, next}.
func (s *Server) listNotices(w http.ResponseWriter, r *http.Request, c *caller) {
	if noticeScopeLimited(w, c) {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if c.p.User.ID == 0 { // dev-open: nobody to hold an inbox for
		writeJSON(w, 200, map[string]any{"data": []any{}, "unread": 0, "next": nil})
		return
	}
	items, unread, next, err := store.ListNotices(r.Context(), s.DB, store.NoticeFilter{Org: c.actor.Org, User: c.p.User.ID, Limit: limit,
		Before: q.Get("before"), Unread: q.Get("unread") == "1" || q.Get("unread") == "true"})
	if err != nil {
		fail(w, err)
		return
	}
	var nx any
	if next != "" {
		nx = next
	}
	writeJSON(w, 200, map[string]any{"data": items, "unread": unread, "next": nx})
}

// readNotices is POST /api/v1/notices/read {ids}: marks those of the caller's notices read; no ids marks all.
func (s *Server) readNotices(w http.ResponseWriter, r *http.Request, c *caller) {
	if noticeScopeLimited(w, c) {
		return
	}
	var in struct {
		IDs []string `json:"ids"`
	}
	if r.ContentLength != 0 {
		if err := readJSON(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, "bad_json", "body must be {ids}")
			return
		}
	}
	if c.p.User.ID == 0 {
		writeJSON(w, 200, map[string]any{"marked": 0, "unread": 0})
		return
	}
	n, err := store.MarkNoticesRead(r.Context(), s.DB, c.actor.Org, c.p.User.ID, in.IDs, s.Engine.Now())
	if err != nil {
		fail(w, err)
		return
	}
	unread, err := store.CountUnreadNotices(r.Context(), s.DB, c.actor.Org, c.p.User.ID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"marked": n, "unread": unread})
}

// noticeStatus is /system/status capabilities.notify.notices (contract 0066 §4.2): whether a host is told, the host
// posts waiting or retrying, those that failed for good in the last 24 hours, and who mails notices.
func (s *Server) noticeStatus(ctx context.Context, reg *meta.Registry, org string) map[string]any {
	host := s.Engine.Notices.Host
	out := map[string]any{"host": host, "pending": 0, "failed_24h": 0, "mail": "log"}
	if host {
		out["mail"] = "host"
	}
	if s.DB == nil {
		return out
	}
	res, err := s.DB.Query(ctx, `select count(*) filter (where state in ('available', 'retryable', 'running')),
		count(*) filter (where state = 'discarded' and finalized_at > now() - interval '24 hours')
		from rk_jobs where kind = 'notice_host' and ($1 = '' or org_id = $1)`, org)
	if err == nil && len(res.Rows) == 1 {
		out["pending"], out["failed_24h"] = res.Rows[0][0], res.Rows[0][1]
	}
	return out
}
