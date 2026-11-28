package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/docprint"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/ratelimit"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Portal pages (contract 0073): a member makes a customer link to one document; the customer opens it without
// signing in. Only sha256(token) is stored (rk_portal_links), and a page shows only what its portal/<name>.rk lists.

const (
	msgPortalUnknown = "Tautan tidak ditemukan. Periksa lagi tautannya, atau minta tautan baru ke penjual."
	msgPortalExpired = "Tautan sudah kedaluwarsa, minta tautan baru."
)

// portalRate is the kernel's own bucket per client address on /p/ (contract 0073 §6), whatever the plan says.
var portalRate = ratelimit.Rate{RPS: 1, Burst: 30}

// portalTokenRe is the shape of a token: 32 random bytes in base64url without padding.
var portalTokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// portalRoutes registers the member and customer routes of contract 0073 §4.
func (s *Server) portalRoutes(mux *http.ServeMux) {
	a := s.authed
	mux.HandleFunc("GET /api/v1/portal", a(s.listPortalPages))
	mux.HandleFunc("POST /api/v1/portal/{page}/links", a(s.createPortalLink))
	mux.HandleFunc("GET /api/v1/portal/{page}/links", a(s.listPortalLinks))
	mux.HandleFunc("POST /api/v1/portal/{page}/links/revoke", a(s.revokePortalLinks))
	mux.HandleFunc("GET /p/{org}/{page}/{token}", s.portalShell)
	mux.HandleFunc("GET /p/{org}/{page}/{token}/data", s.portalData)
	mux.HandleFunc("GET /p/{org}/{page}/{token}/print", s.portalPrint)
	s.portalOrderRoutes(mux)
}

// ---- members ----

// canShare: the owner, or a role the page lists in share (contract 0073 §1).
func canShare(a engine.Actor, p *meta.Portal) bool {
	for _, r := range a.Roles {
		if r == "owner" || contains(p.Share, r) {
			return true
		}
	}
	return false
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// GET /api/v1/portal: the installed pages the caller may share.
func (s *Server) listPortalPages(w http.ResponseWriter, r *http.Request, c *caller) {
	reg := s.Engine.Reg()
	out := []any{}
	for _, n := range reg.PortalNames() {
		p := reg.Portal(n)
		if canShare(c.actor, p) {
			out = append(out, map[string]any{"name": p.Name, "label": p.Label.Text(), "model": p.Model, "lifetime_days": p.LifetimeDays})
		}
	}
	writeJSON(w, 200, map[string]any{"data": out})
}

// sharePage resolves {page} and the record named in the request for a member who may share it: the page exists,
// the caller is in its share roles and can read the record. It writes the error and returns ok=false otherwise.
func (s *Server) sharePage(w http.ResponseWriter, r *http.Request, c *caller, record string) (*meta.Portal, store.Record, bool) {
	p := s.Engine.Reg().Portal(r.PathValue("page"))
	if p == nil {
		writeError(w, http.StatusNotFound, "portal_page_unknown", "Halaman pelanggan "+r.PathValue("page")+" tidak ada")
		return nil, nil, false
	}
	if !canShare(c.actor, p) {
		writeError(w, http.StatusForbidden, "portal_share_forbidden", "Peran Anda tidak boleh membagikan tautan pelanggan untuk halaman ini.")
		return nil, nil, false
	}
	if record == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "record wajib diisi")
		return nil, nil, false
	}
	rec, err := s.Engine.Get(r.Context(), c.actor, p.LinkModel(), record)
	if err != nil {
		fail(w, err)
		return nil, nil, false
	}
	return p, rec, true
}

// POST /api/v1/portal/{page}/links {record, days?}: a new link; the only answer that carries a token.
func (s *Server) createPortalLink(w http.ResponseWriter, r *http.Request, c *caller) {
	body, ok := readObject(w, r)
	if !ok {
		return
	}
	record, _ := body["record"].(string)
	p, rec, ok := s.sharePage(w, r, c, record)
	if !ok {
		return
	}
	days := p.LifetimeDays
	if v, has := body["days"]; has {
		f, isNum := v.(float64)
		if !isNum || f != math.Trunc(f) || f < 1 || int(f) > p.LifetimeDays {
			writeError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("days harus bilangan bulat 1 sampai %d", p.LifetimeDays))
			return
		}
		days = int(f)
	}
	if p.Type != "order_page" {
		m := s.Engine.Reg().Model(p.Model)
		switch ds := docstatus(rec); {
		case m.IsSubmittable && ds == 0:
			writeError(w, http.StatusConflict, "portal_not_submitted", "Ajukan dokumen ini dulu sebelum membagikan tautan ke pelanggan.")
			return
		case ds == 2:
			writeError(w, http.StatusConflict, "portal_cancelled", "Dokumen ini sudah dibatalkan; bagikan tautan dokumen penggantinya.")
			return
		}
		// v1 shows amounts in the organisation's currency only: a foreign-currency document keeps its prices in _fx
		// twins the page does not list (contract 0073 §5)
		if org := portalCurrency(s.Engine.Reg(), c.actor.Org); portalDocCurrency(s.Engine.Reg(), c.actor.Org, m, rec) != org {
			writeError(w, http.StatusConflict, "portal_currency_unsupported", "Tautan pelanggan belum bisa untuk dokumen mata uang asing; kirim versi cetaknya.")
			return
		}
	}
	link, err := s.portalNewLink(r, c, p, p.LinkModel(), record, days)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, link)
}

// portalNewLink stores a new link (only its token's hash) and audits it; the answer is the only place the token is.
func (s *Server) portalNewLink(r *http.Request, c *caller, p *meta.Portal, model, record string, days int) (map[string]any, error) {
	tok := make([]byte, 32)
	idb := make([]byte, 9)
	if _, err := rand.Read(tok); err != nil {
		return nil, err
	}
	if _, err := rand.Read(idb); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(tok)
	sum := sha256.Sum256([]byte(token))
	id := "pl_" + hex.EncodeToString(idb)
	now := s.Engine.Now().UTC()
	expires := now.Add(time.Duration(days) * 24 * time.Hour)
	ctx := r.Context()
	if _, err := s.DB.Exec(ctx, `insert into rk_portal_links (id, org_id, page, model, record, token_hash, expires_at, created_by, created_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, c.actor.Org, p.Name, model, record, sum[:], expires, c.actor.ID, now); err != nil {
		return nil, err
	}
	if err := store.Audit(ctx, s.DB, c.actor.Org, c.actor.ID, "portal.link.create", model+"/"+record, p.Name+" ("+strconv.Itoa(days)+" hari)",
		map[string]any{"page": p.Name, "link": id, "expires_at": expires.Format(time.RFC3339)}); err != nil {
		s.logf("portal: audit create %s: %v", id, err)
	}
	path := "/p/" + url.PathEscape(c.actor.Org) + "/" + url.PathEscape(p.Name) + "/" + token
	return map[string]any{"id": id, "url": s.publicBase(r) + path, "path": path, "expires_at": expires.Format(time.RFC3339)}, nil
}

// publicBase is the kernel's public address: system.rk auth.issuer when set, else the request's scheme and host.
func (s *Server) publicBase(r *http.Request) string {
	if s.Engine != nil && s.Engine.Notices.BaseURL != "" {
		return strings.TrimRight(s.Engine.Notices.BaseURL, "/")
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// GET /api/v1/portal/{page}/links?record=: the links of one record, without tokens.
func (s *Server) listPortalLinks(w http.ResponseWriter, r *http.Request, c *caller) {
	record := r.URL.Query().Get("record")
	p, _, ok := s.sharePage(w, r, c, record)
	if !ok {
		return
	}
	res, err := s.DB.Query(r.Context(), `select id, created_by, created_at, expires_at, revoked_at, last_view_at, views
		from rk_portal_links where org_id = $1 and page = $2 and record = $3 order by created_at desc, id limit 100`, c.actor.Org, p.Name, record)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": res.Maps()})
}

// POST /api/v1/portal/{page}/links/revoke {record, id?}: one link, or every live link of the record.
func (s *Server) revokePortalLinks(w http.ResponseWriter, r *http.Request, c *caller) {
	body, ok := readObject(w, r)
	if !ok {
		return
	}
	record, _ := body["record"].(string)
	id, _ := body["id"].(string)
	p, _, ok := s.sharePage(w, r, c, record)
	if !ok {
		return
	}
	ctx := r.Context()
	q := `update rk_portal_links set revoked_at = $4 where org_id = $1 and page = $2 and record = $3 and revoked_at is null`
	args := []any{c.actor.Org, p.Name, record, s.Engine.Now().UTC()}
	if id != "" {
		q += ` and id = $5`
		args = append(args, id)
	}
	res, err := s.DB.Exec(ctx, q+` returning id`, args...)
	if err != nil {
		fail(w, err)
		return
	}
	n := len(res.Rows)
	if n > 0 {
		if err := store.Audit(ctx, s.DB, c.actor.Org, c.actor.ID, "portal.link.revoke", p.Model+"/"+record,
			fmt.Sprintf("%s (%d tautan)", p.Name, n), map[string]any{"page": p.Name, "link": id, "revoked": n}); err != nil {
			s.logf("portal: audit revoke %s/%s: %v", p.Model, record, err)
		}
	}
	writeJSON(w, 200, map[string]any{"revoked": n})
}

// ---- customers ----

// portalHit is one resolved customer request.
type portalHit struct {
	id, org string
	page    *meta.Portal
	model   *meta.Model
	rec     store.Record
	expires time.Time
	actor   engine.Actor
}

// portalHeaders: nothing of a portal answer is cached, indexed or sent on as a referrer (the token is in the path).
func portalHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
}

// portalResolve applies the portal bucket, then finds the link and its record. It returns the HTTP status for a
// miss: 429, 404 portal_link_unknown (every kind of miss looks the same), 410 portal_link_expired, or 500.
func (s *Server) portalResolve(r *http.Request) (*portalHit, int, error) {
	if _, err := s.portal.Wait(r.Context(), "p:"+s.clientKey(r), portalRate); err != nil {
		var refused *ratelimit.Refused
		if errors.As(err, &refused) {
			return nil, http.StatusTooManyRequests, err
		}
		return nil, http.StatusTooManyRequests, err
	}
	if s.Engine == nil || s.DB == nil {
		return nil, http.StatusNotFound, nil
	}
	org, page, token := r.PathValue("org"), r.PathValue("page"), r.PathValue("token")
	reg := s.Engine.Reg()
	p := reg.Portal(page)
	if p == nil || org == "" || !portalTokenRe.MatchString(token) {
		return nil, http.StatusNotFound, nil
	}
	sum := sha256.Sum256([]byte(token))
	ctx := pg.WithOrg(r.Context(), org)
	res, err := s.DB.Query(ctx, `select id, model, record, expires_at, revoked_at from rk_portal_links
		where org_id = $1 and page = $2 and token_hash = $3`, org, page, sum[:])
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if len(res.Rows) != 1 {
		return nil, http.StatusNotFound, nil
	}
	row := res.Maps()[0]
	id, model, record := fmt.Sprint(row["id"]), fmt.Sprint(row["model"]), fmt.Sprint(row["record"])
	if model != p.LinkModel() {
		return nil, http.StatusNotFound, nil
	}
	expires, _ := row["expires_at"].(time.Time)
	if row["revoked_at"] != nil || !s.Engine.Now().Before(expires) {
		// the token matched a real link, so its organisation is known: the page names the seller to ask
		return &portalHit{id: id, org: org, page: p}, http.StatusGone, nil
	}
	m := reg.Model(model)
	actor := engine.Actor{Org: org, ID: "portal:" + id, System: true}
	rec, err := s.Engine.Get(ctx, actor, model, record)
	if err != nil {
		return nil, http.StatusNotFound, nil
	}
	if m.IsSubmittable && docstatus(rec) == 0 {
		return nil, http.StatusNotFound, nil
	}
	return &portalHit{id: id, org: org, page: p, model: m, rec: rec, expires: expires, actor: actor}, http.StatusOK, nil
}

// portalMiss writes a miss as JSON. A 410 names the seller (hit.org is the link's own organisation); a 404 names
// nobody, since its path is whatever the caller typed.
func (s *Server) portalMiss(w http.ResponseWriter, status int, err error, hit *portalHit) {
	switch status {
	case http.StatusTooManyRequests:
		var refused *ratelimit.Refused
		if errors.As(err, &refused) {
			w.Header().Set("Retry-After", strconv.Itoa(refused.RetrySeconds()))
		}
		writeError(w, status, "rate_limited", msgRateLimited)
	case http.StatusGone:
		body := map[string]any{"error": map[string]any{"code": "portal_link_expired", "message": msgPortalExpired}}
		if hit != nil && s.Engine != nil {
			body["org"] = portalOrg(s.Engine.Reg(), hit.org)
		}
		writeJSON(w, status, body)
	case http.StatusInternalServerError:
		s.logf("portal: %v", err)
		writeError(w, status, "internal", "Halaman belum bisa dibuka. Coba lagi sebentar.")
	default:
		writeError(w, http.StatusNotFound, "portal_link_unknown", msgPortalUnknown)
	}
}

// portalShellCSP: the shell runs only /portal/ scripts and styles and shows data: images (the QRIS).
const portalShellCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// GET /p/{org}/{page}/{token}: the page shell, with the link's status (200, 404, 410). The shell reads …/data.
func (s *Server) portalShell(w http.ResponseWriter, r *http.Request) {
	portalHeaders(w)
	_, status, err := s.portalResolve(r)
	if status == http.StatusTooManyRequests || status == http.StatusInternalServerError {
		s.portalMiss(w, status, err, nil)
		return
	}
	b, rerr := os.ReadFile(filepath.Join(s.WebDir, "portal", "index.html"))
	if s.WebDir == "" || rerr != nil {
		s.portalMiss(w, status, err, nil)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", portalShellCSP)
	w.WriteHeader(status)
	w.Write(b)
}

// GET /p/{org}/{page}/{token}/data: the page as JSON (contract 0073 §5).
func (s *Server) portalData(w http.ResponseWriter, r *http.Request) {
	portalHeaders(w)
	hit, status, err := s.portalResolve(r)
	if status != http.StatusOK {
		s.portalMiss(w, status, err, hit)
		return
	}
	if hit.page.Type == "order_page" {
		s.portalOrderData(w, r, hit)
		return
	}
	ctx := pg.WithOrg(r.Context(), hit.org)
	reg := s.Engine.Reg()
	p, m, rec := hit.page, hit.model, hit.rec
	ds := docstatus(rec)
	amount, hasAmount := portalNumber(rec[p.Amount])
	state := "open"
	switch {
	case ds == 2:
		state = "cancelled"
	case p.Amount != "" && hasAmount && amount <= 0:
		state = "paid"
	}
	doc := map[string]any{"name": rec["name"], "state": state, "customer": "", "replaced_by": ""}
	if f := portalShown(m, p.Customer); f != nil {
		doc["customer"] = s.portalValue(ctx, hit.actor, f, rec[f.Name])
	}
	if f := portalShown(m, p.Amount); f != nil {
		doc["amount"] = map[string]any{"label": fieldLabel(f), "value": rec[f.Name], "currency": portalDocCurrency(reg, hit.org, m, rec)}
	}
	if f := portalShown(m, p.Due); f != nil {
		doc["due"] = map[string]any{"label": fieldLabel(f), "value": rec[f.Name]}
	}
	if ds == 2 {
		doc["replaced_by"] = s.portalSuccessor(ctx, hit.org, m, fmt.Sprint(rec["name"]))
	}
	out := map[string]any{
		"org":        portalOrg(reg, hit.org),
		"page":       map[string]any{"name": p.Name, "label": p.Label.Text(), "type": p.Type},
		"doc":        doc,
		"fields":     s.portalFields(ctx, hit.actor, m, p.Fields, rec),
		"lines":      nil,
		"payment":    nil,
		"print":      p.Print != "" && state != "cancelled",
		"expires_at": hit.expires.UTC().Format(time.RFC3339),
	}
	if f := m.Field(p.LinesField); f != nil && f.Type == "table" {
		if cm := reg.Model(f.Of); cm != nil {
			out["lines"] = portalLines(cm, f, p.LinesColumns, rec[f.Name])
		}
	}
	if state == "open" && p.PayModel != "" {
		out["payment"] = s.portalPayment(ctx, hit, fmt.Sprint(rec["name"]))
	}
	s.portalViewed(ctx, r, hit, "page")
	writeJSON(w, 200, out)
}

// portalViewAuditEvery: a link audits portal.view at most once in this window; views counts every one.
const portalViewAuditEvery = time.Hour

// portalViewed counts the view and audits it (portal.view), never with the token. HEAD is no view. The audit is
// throttled per link (contract 0073 §4), so a link fetched in a loop does not grow rk_audit by a row per request.
func (s *Server) portalViewed(ctx context.Context, r *http.Request, hit *portalHit, kind string) {
	if r.Method == http.MethodHead {
		return
	}
	now := s.Engine.Now().UTC()
	res, err := s.DB.Query(ctx, `with old as (select last_view_at from rk_portal_links where id = $1)
		update rk_portal_links set views = views + 1, last_view_at = $2 where id = $1 returning (select last_view_at from old)`, hit.id, now)
	if err != nil {
		s.logf("portal: count view %s: %v", hit.id, err)
		return
	}
	if len(res.Rows) == 1 {
		if prev, ok := res.Rows[0][0].(time.Time); ok && now.Sub(prev) < portalViewAuditEvery {
			return
		}
	}
	if err := store.Audit(ctx, s.DB, hit.org, hit.actor.ID, "portal.view", hit.model.Name+"/"+fmt.Sprint(hit.rec["name"]), hit.page.Name+" ("+kind+")",
		map[string]any{"page": hit.page.Name, "kind": kind}); err != nil {
		s.logf("portal: audit view %s: %v", hit.id, err)
	}
}

// portalHiddenField: a field no page shows, whatever the page lists (contract 0073 §2, defence in depth behind the
// validator): any hidden_for, hidden: true, or a type pages never show.
func portalHiddenField(m *meta.Model, f *meta.Field) bool {
	if f == nil || len(meta.StrList(f.Raw["hidden_for"])) > 0 {
		return true
	}
	if base := f.MoneyOf(); base != "" && m != nil { // an IDR twin is hidden when its money field is
		if bf := m.Field(base); bf != nil && bf != f && portalHiddenField(m, bf) {
			return true
		}
	}
	if h, _ := f.Raw["hidden"].(bool); h {
		return true
	}
	switch f.Type {
	case "attach", "image", "json", "signature", "geo", "dynamic_link", "password":
		return true
	}
	return false
}

// portalShown is field name of m when a page may show it, else nil.
func portalShown(m *meta.Model, name string) *meta.Field {
	if name == "" || m == nil {
		return nil
	}
	f := m.Field(name)
	if portalHiddenField(m, f) || f.Type == "table" {
		return nil
	}
	return f
}

func fieldLabel(f *meta.Field) string {
	if t := f.Label.Text(); t != "" {
		return t
	}
	return f.Name
}

// portalValue is what a page shows for one value: a link's target title, a select's option label, else the value.
func (s *Server) portalValue(ctx context.Context, a engine.Actor, f *meta.Field, v any) any {
	sv, isStr := v.(string)
	switch {
	case f.Type == "link" && isStr && sv != "":
		tm := s.Engine.Reg().Model(f.To)
		if tm == nil || tm.TitleField == "" {
			return sv
		}
		tf := tm.Field(tm.TitleField)
		if tf == nil || portalHiddenField(tm, tf) || tf.Type == "link" {
			return sv
		}
		if lr, err := s.Engine.Get(ctx, a, f.To, sv); err == nil {
			if t, ok := lr[tm.TitleField].(string); ok && t != "" {
				return t
			}
		}
		return sv
	case f.Type == "select" && isStr:
		if o := f.Option(sv); o != nil {
			if t := o.Label.Text(); t != "" {
				return t
			}
		}
	}
	return v
}

// portalFields lists the page's fields with their labels, types and values.
func (s *Server) portalFields(ctx context.Context, a engine.Actor, m *meta.Model, names []string, rec store.Record) []any {
	out := []any{}
	for _, n := range names {
		f := portalShown(m, n)
		if f == nil {
			continue
		}
		out = append(out, map[string]any{"name": f.Name, "label": fieldLabel(f), "type": f.Type, "value": s.portalValue(ctx, a, f, rec[f.Name])})
	}
	return out
}

// portalLines is the table of one table field, columns as listed (a hidden one is dropped).
func portalLines(cm *meta.Model, f *meta.Field, columns []string, v any) map[string]any {
	cols := []any{}
	var keep []*meta.Field
	for _, c := range columns {
		cf := portalShown(cm, c)
		if cf == nil || cf.Type == "link" {
			if cf != nil { // a link column shows its stored value only (titles would cost a read per row)
				keep = append(keep, cf)
				cols = append(cols, map[string]any{"name": cf.Name, "label": fieldLabel(cf), "type": cf.Type})
			}
			continue
		}
		keep = append(keep, cf)
		cols = append(cols, map[string]any{"name": cf.Name, "label": fieldLabel(cf), "type": cf.Type})
	}
	rows := []any{}
	list, _ := v.([]any)
	if list == nil {
		if rl, ok := v.([]map[string]any); ok {
			for _, x := range rl {
				list = append(list, x)
			}
		}
	}
	for _, x := range list {
		row, ok := x.(map[string]any)
		if !ok {
			if sr, ok2 := x.(store.Record); ok2 {
				row = sr
			} else {
				continue
			}
		}
		o := map[string]any{}
		for _, cf := range keep {
			val := row[cf.Name]
			if cf.Type == "select" {
				if sv, ok := val.(string); ok {
					if opt := cf.Option(sv); opt != nil && opt.Label.Text() != "" {
						val = opt.Label.Text()
					}
				}
			}
			o[cf.Name] = val
		}
		rows = append(rows, o)
	}
	return map[string]any{"label": fieldLabel(f), "columns": cols, "rows": rows}
}

// portalPayment reads the first payment row that names the document (contract 0073 §2); nil without one.
func (s *Server) portalPayment(ctx context.Context, hit *portalHit, doc string) any {
	reg := s.Engine.Reg()
	p := hit.page
	pm := reg.Model(p.PayModel)
	if pm == nil || portalShown(pm, p.PayLink) == nil {
		return nil
	}
	names, err := store.FindBy(ctx, s.DB, reg, hit.org, pm, p.PayLink, doc)
	if err != nil || len(names) == 0 {
		if err != nil {
			s.logf("portal: payment of %s: %v", doc, err)
		}
		return nil
	}
	row, err := s.Engine.Get(ctx, hit.actor, pm.Name, names[0])
	if err != nil || row[p.PayLink] != doc {
		return nil
	}
	out := map[string]any{"fields": s.portalFields(ctx, hit.actor, pm, p.PayFields, row), "qris": nil}
	if f := portalShown(pm, p.PayQRIS); f != nil {
		if payload, _ := row[f.Name].(string); strings.TrimSpace(payload) != "" {
			var label []string
			for _, n := range p.PayQRISLabel {
				if lf := portalShown(pm, n); lf != nil {
					if t := strings.TrimSpace(fmt.Sprint(s.portalValue(ctx, hit.actor, lf, row[lf.Name]))); t != "" && row[lf.Name] != nil {
						label = append(label, t)
					}
				}
			}
			out["qris"] = map[string]any{"image": string(docprint.QR(payload)), "label": strings.Join(label, " · ")}
		}
	}
	return out
}

// portalSuccessor follows the amendment chain of a cancelled document (amended_from) and names the submitted
// document that replaces it, or "": a draft amendment has only a temporary name, and a chain that ends cancelled
// replaces nothing. At most 20 hops.
func (s *Server) portalSuccessor(ctx context.Context, org string, m *meta.Model, name string) string {
	q := fmt.Sprintf("select name, docstatus from %s where org_id = $1 and amended_from = $2 order by docstatus = 2, name desc limit 1",
		pg.QuoteIdent(store.TableName(m.Name)))
	best, cur := "", name
	for i := 0; i < 20; i++ {
		res, err := s.DB.Query(ctx, q, org, cur)
		if err != nil {
			s.logf("portal: successor of %s: %v", name, err)
			return ""
		}
		if len(res.Rows) == 0 {
			break
		}
		next := fmt.Sprint(res.Rows[0][0])
		ds, _ := portalNumber(res.Rows[0][1])
		switch int(ds) {
		case 1:
			best = next
		case 2:
			best = ""
		default: // a draft: nothing to name yet; it has no amendment of its own
			return best
		}
		cur = next
	}
	return best
}

// portalOrg is the seller as the page names it.
func portalOrg(reg *meta.Registry, org string) map[string]any {
	id := reg.OrgIdentity(org)
	name, _ := id["name"].(string)
	legal, _ := id["legal_name"].(string)
	phone := ""
	if lh := reg.Letterhead(org); lh != nil {
		if l, _ := lh["legal_name"].(string); l != "" {
			legal = l
		}
		phone, _ = lh["phone"].(string)
	}
	if name == "" {
		name = legal
	}
	return map[string]any{"name": name, "legal_name": legal, "phone": phone}
}

// portalCurrency is the organisation's currency (config currency), IDR by default.
func portalCurrency(reg *meta.Registry, org string) string {
	if c, ok := reg.OrgConfig(org, "currency").(string); ok && c != "" {
		return c
	}
	return "IDR"
}

// portalDocCurrency is the document's own currency (its currency field) when it has one, else the organisation's.
func portalDocCurrency(reg *meta.Registry, org string, m *meta.Model, rec map[string]any) string {
	if f := m.Field("currency"); f != nil {
		if c, _ := rec["currency"].(string); c != "" {
			return c
		}
	}
	return portalCurrency(reg, org)
}

func portalNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}

// portalForbidden: a hidden_for field, or the IDR twin of one. A customer holds none of the roles it names.
func portalForbidden(m *meta.Model, f *meta.Field) bool {
	if len(meta.StrList(f.Raw["hidden_for"])) > 0 {
		return true
	}
	if base := f.MoneyOf(); base != "" {
		if bf := m.Field(base); bf != nil && bf != f {
			return portalForbidden(m, bf)
		}
	}
	return false
}

// portalStrip is a copy of rec without its hidden_for fields, children included (the print of contract 0073 §2).
func portalStrip(reg *meta.Registry, m *meta.Model, rec map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range rec {
		f := m.Field(k)
		if f == nil {
			out[k] = v // kernel fields: name, docstatus, owner, timestamps
			continue
		}
		// the print shows what the format templates, as for a member, minus hidden_for fields (contract 0073 §2);
		// hidden: true only keeps a field off the form (a unit name kept for the print), so it stays
		if portalForbidden(m, f) {
			continue
		}
		if f.Type == "table" {
			cm := reg.Model(f.Of)
			list, _ := v.([]any)
			rows := []any{}
			for _, x := range list {
				if row, ok := x.(map[string]any); ok && cm != nil {
					rows = append(rows, portalStrip(reg, cm, row))
				}
			}
			out[k] = rows
			continue
		}
		out[k] = v
	}
	return out
}

// GET /p/{org}/{page}/{token}/print: the page's print format as HTML, every hidden field removed first.
func (s *Server) portalPrint(w http.ResponseWriter, r *http.Request) {
	portalHeaders(w)
	hit, status, err := s.portalResolve(r)
	if status != http.StatusOK {
		s.portalMiss(w, status, err, hit)
		return
	}
	reg := s.Engine.Reg()
	f := printFormat(reg, hit.model.Name, hit.page.Print)
	if hit.page.Print == "" || f == nil || f.Output == "xml" || docstatus(hit.rec) == 2 {
		writeError(w, http.StatusNotFound, "portal_print_unavailable", "Dokumen ini tidak punya versi cetak untuk pelanggan.")
		return
	}
	if !meta.PortalPrintAnchored(f) {
		// a related list not matched on the document's name would read other records of the org as the kernel
		// (contract 0073 §2); the validator refuses it (portal_print_related), a site override is refused here
		writeError(w, http.StatusNotFound, "portal_print_unavailable", "Dokumen ini tidak punya versi cetak untuk pelanggan.")
		return
	}
	ctx := pg.WithOrg(r.Context(), hit.org)
	m := hit.model
	rec := store.Record(portalStrip(reg, m, hit.rec))
	related, truncated, _, rerr := s.printRelated(ctx, hit.actor, f, hit.rec, false)
	if rerr != nil {
		s.portalMiss(w, http.StatusInternalServerError, rerr, nil)
		return
	}
	// the format's require checks, as for a member (contract 0029 §4); a refusal names nothing to the customer
	vars := map[string]any{"doc": map[string]any(hit.rec)}
	for key, rows := range related {
		rel := f.Related[key]
		for _, row := range rows {
			for _, q := range rel.Require {
				if ok, _ := s.Engine.Holds(ctx, hit.actor, rel.Model, store.Record(row), q.Check, vars); !ok {
					writeError(w, http.StatusNotFound, "portal_print_unavailable", "Dokumen ini tidak punya versi cetak untuk pelanggan.")
					return
				}
			}
		}
		if rm := reg.Model(rel.Model); rm != nil {
			for i, row := range rows {
				rows[i] = portalStrip(reg, rm, row)
			}
		}
	}
	// link records: the page's customer is the customer's own record (minus hidden_for fields); any other link
	// gives its name and title only
	links := s.printLinks(ctx, hit.actor, m, hit.rec)
	for fn, lr := range links {
		lf := m.Field(fn)
		lm := (*meta.Model)(nil)
		if lf != nil {
			lm = reg.Model(lf.To)
		}
		switch {
		case lm == nil:
			delete(links, fn)
		case fn == hit.page.Customer:
			links[fn] = portalStrip(reg, lm, lr)
		default:
			short := map[string]any{"name": lr["name"]}
			if tf := lm.Field(lm.TitleField); tf != nil && !portalHiddenField(lm, tf) {
				short[lm.TitleField] = lr[lm.TitleField]
			}
			links[fn] = short
		}
	}
	children := map[string]*meta.Model{}
	for _, fd := range m.Fields {
		if fd.Type == "table" {
			children[fd.Of] = reg.Model(fd.Of)
		}
	}
	page, err := docprint.Render(docprint.Input{
		Format: f, Model: m, Doc: rec, Links: links, Related: related, RelatedTruncated: truncated,
		Letterhead: reg.Letterhead(hit.org), Org: printOrg(reg, hit.org),
		Tax: reg.TaxSettings(hit.org), Printed: s.Engine.Now().In(s.Engine.Location()),
		Children: children,
	})
	if err != nil {
		s.portalMiss(w, http.StatusInternalServerError, err, nil)
		return
	}
	s.portalViewed(ctx, r, hit, "print")
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", docprint.CSP)
	w.WriteHeader(http.StatusOK)
	w.Write(page)
}
