package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rangka-os/rangka-os/kernel/internal/docprint"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/ratelimit"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Order pages (contract 0073 §9): a guest scans the QR of a table (a portal link whose record is the page's anchor),
// reads the menu and sends an order. The guest may do this and nothing more: read the menu rows that pass the page's
// filter (title, price, group, sold out), create one order on the link's own anchor as the actor portal:<link id>,
// read the status of orders that link made in the last 12 hours, and cancel one of them while it is still open and
// at most 2 minutes old. Prices are never read from the request: the order model computes them from the menu row.

const (
	portalOrderMaxLines   = 30
	portalOrderMaxQty     = 50
	portalOrderMaxOpen    = 3   // open orders per anchor (a table)
	portalOrderMaxMenu    = 500 // menu rows on one page
	portalOrderNoteMax    = 140
	portalOrderGuestMax   = 40
	portalOrderMineMax    = 10
	portalOrderBodyMax    = 16 << 10
	portalOrderCancelSecs = 120
	portalOrderMineHours  = 12
)

// portalOrderRate bounds creates and cancels per link (one table) and per address, on top of the page bucket.
var (
	portalOrderLinkRate = ratelimit.Rate{RPS: 1.0 / 20, Burst: 3}
	portalOrderAddrRate = ratelimit.Rate{RPS: 0.5, Burst: 20} // wide: guests on one restaurant WiFi share an address
)

func (s *Server) portalOrderRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /p/{org}/{page}/{token}/order", s.portalOrderCreate)
	mux.HandleFunc("POST /p/{org}/{page}/{token}/order/{name}/cancel", s.portalOrderCancel)
	mux.HandleFunc("POST /api/v1/portal/{page}/qr", s.authed(s.portalQRSheet))
}

// portalOrderHit resolves a customer request on an order_page; it writes the miss and returns nil otherwise.
func (s *Server) portalOrderHit(w http.ResponseWriter, r *http.Request) *portalHit {
	portalHeaders(w)
	hit, status, err := s.portalResolve(r)
	if status != http.StatusOK {
		s.portalMiss(w, status, err, hit)
		return nil
	}
	if hit.page.Type != "order_page" {
		s.portalMiss(w, http.StatusNotFound, nil, nil)
		return nil
	}
	return hit
}

// portalOrderData is GET …/data of an order_page.
func (s *Server) portalOrderData(w http.ResponseWriter, r *http.Request, hit *portalHit) {
	ctx := pg.WithOrg(r.Context(), hit.org)
	reg := s.Engine.Reg()
	p := hit.page
	menu, err := s.portalMenu(ctx, hit)
	if err != nil {
		s.portalMiss(w, http.StatusInternalServerError, err, nil)
		return
	}
	title := fmt.Sprint(hit.rec["name"])
	if am := reg.Model(p.AnchorModel); am != nil {
		if f := portalShown(am, p.AnchorTitle); f != nil {
			if t, _ := hit.rec[f.Name].(string); t != "" {
				title = t
			}
		}
	}
	out := map[string]any{
		"org":    portalOrg(reg, hit.org),
		"page":   map[string]any{"name": p.Name, "label": p.Label.Text(), "type": p.Type},
		"anchor": map[string]any{"title": title, "outlet": s.portalAnchorOutlet(ctx, hit)},
		"menu":   menu,
		"limits": map[string]any{"lines": portalOrderMaxLines, "qty": portalOrderMaxQty, "note": portalOrderNoteMax,
			"guest": portalOrderGuestMax, "cancel_seconds": portalOrderCancelSecs},
		"fields":   map[string]any{"guest": p.Guest != "", "note": p.Note != "", "line_note": p.LinesNote != ""},
		"currency": portalCurrency(reg, hit.org),
		"orders":   s.portalMine(ctx, hit, r.URL.Query().Get("mine")),
	}
	s.portalViewed(ctx, r, hit, "page")
	writeJSON(w, 200, out)
}

// portalAnchorOutlet names the anchor's outlet when it links one (a table's outlet), by its title.
func (s *Server) portalAnchorOutlet(ctx context.Context, hit *portalHit) string {
	am := s.Engine.Reg().Model(hit.page.AnchorModel)
	f := portalShown(am, "outlet")
	if f == nil || f.Type != "link" {
		return ""
	}
	v := s.portalValue(ctx, hit.actor, f, hit.rec["outlet"])
	t, _ := v.(string)
	return t
}

// portalMenu lists the menu rows: name, title, price, group title, sold out. Nothing else of a row leaves the kernel.
func (s *Server) portalMenu(ctx context.Context, hit *portalHit) ([]any, error) {
	reg := s.Engine.Reg()
	p := hit.page
	mm := reg.Model(p.MenuModel)
	tf, pf := portalShown(mm, p.MenuTitle), portalShown(mm, p.MenuPrice)
	if tf == nil || pf == nil {
		return []any{}, nil
	}
	gf, sf := portalShown(mm, p.MenuGroup), portalShown(mm, p.MenuSoldOut)
	rows, _, err := s.Engine.List(ctx, hit.actor, mm.Name, engine.ListOpts{Filter: p.MenuFilter, Sort: tf.Name, Limit: portalOrderMaxMenu})
	if err != nil {
		return nil, err
	}
	groups := map[string]any{}
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		item := map[string]any{"name": row["name"], "title": row[tf.Name], "price": row[pf.Name], "group": "", "sold_out": false}
		if gf != nil {
			g := fmt.Sprint(row[gf.Name])
			if row[gf.Name] == nil {
				g = ""
			}
			if _, ok := groups[g]; !ok {
				groups[g] = s.portalValue(ctx, hit.actor, gf, row[gf.Name])
			}
			if g != "" {
				item["group"] = groups[g]
			}
		}
		if sf != nil {
			item["sold_out"] = row[sf.Name] == true
		}
		out = append(out, item)
	}
	return out, nil
}

// portalMenuIndex is the orderable rows by name: in the menu and not sold out.
func (s *Server) portalMenuIndex(ctx context.Context, hit *portalHit) (map[string]map[string]any, error) {
	menu, err := s.portalMenu(ctx, hit)
	if err != nil {
		return nil, err
	}
	idx := map[string]map[string]any{}
	for _, x := range menu {
		m := x.(map[string]any)
		idx[fmt.Sprint(m["name"])] = m
	}
	return idx, nil
}

// portalMine is the status of the orders the request names (?mine=a,b) that this link made in the last 12 hours on
// its own anchor. An order of another link, another table or an older one is left out without a word.
func (s *Server) portalMine(ctx context.Context, hit *portalHit, mine string) []any {
	out := []any{}
	if mine == "" {
		return out
	}
	reg := s.Engine.Reg()
	p := hit.page
	m := reg.Model(p.Model)
	sf := m.Field(p.StatusField)
	seen := map[string]bool{}
	for i, name := range strings.Split(mine, ",") {
		name = strings.TrimSpace(name)
		if i >= portalOrderMineMax || name == "" || len(name) > 80 || seen[name] {
			continue
		}
		seen[name] = true
		rec, err := s.Engine.Get(ctx, hit.actor, m.Name, name)
		if err != nil || !s.portalOwnOrder(hit, rec) {
			continue
		}
		out = append(out, s.portalOrderView(ctx, hit, m, sf, rec))
	}
	return out
}

// portalOwnOrder: rec was made by this link, on its anchor, in the last 12 hours.
func (s *Server) portalOwnOrder(hit *portalHit, rec store.Record) bool {
	if rec["created_by"] != hit.actor.ID || rec[hit.page.AnchorField] != hit.rec["name"] {
		return false
	}
	at, err := time.Parse(time.RFC3339, fmt.Sprint(rec["created_at"]))
	return err == nil && s.Engine.Now().Sub(at) < portalOrderMineHours*time.Hour
}

// portalOrderView is what a guest sees of one order: name, status (value and label), whether it can still be
// cancelled, the lines (menu title and qty) and when it was made.
func (s *Server) portalOrderView(ctx context.Context, hit *portalHit, m *meta.Model, sf *meta.Field, rec store.Record) map[string]any {
	p := hit.page
	st, _ := rec[p.StatusField].(string)
	label := st
	if sf != nil {
		if o := sf.Option(st); o != nil && o.Label.Text() != "" {
			label = o.Label.Text()
		}
	}
	lines := []any{}
	titles := map[string]string{}
	if menu, err := s.portalMenu(ctx, hit); err == nil {
		for _, x := range menu {
			mm := x.(map[string]any)
			titles[fmt.Sprint(mm["name"])] = fmt.Sprint(mm["title"])
		}
	}
	list, _ := rec[p.LinesField].([]any)
	for _, x := range list {
		row, ok := x.(map[string]any)
		if !ok {
			if sr, ok2 := x.(store.Record); ok2 {
				row = sr
			} else {
				continue
			}
		}
		item := fmt.Sprint(row[p.LinesItem])
		t := titles[item]
		if t == "" {
			t = item
		}
		line := map[string]any{"title": t, "qty": row[p.LinesQty]}
		if p.LinesNote != "" {
			line["note"] = row[p.LinesNote]
		}
		lines = append(lines, line)
	}
	cancel := false
	if p.StatusCancel != "" && contains(p.StatusOpen, st) {
		if at, err := time.Parse(time.RFC3339, fmt.Sprint(rec["created_at"])); err == nil {
			cancel = s.Engine.Now().Sub(at) < portalOrderCancelSecs*time.Second
		}
	}
	return map[string]any{"name": rec["name"], "status": st, "label": label, "open": contains(p.StatusOpen, st),
		"cancelled": p.StatusCancel != "" && st == p.StatusCancel, "can_cancel": cancel, "lines": lines, "created_at": rec["created_at"]}
}

// portalOrderBucket applies the order buckets (per link, per address); false means 429 was written.
func (s *Server) portalOrderBucket(w http.ResponseWriter, r *http.Request, hit *portalHit) bool {
	for _, b := range []struct {
		key  string
		rate ratelimit.Rate
	}{{"po:" + hit.id, portalOrderLinkRate}, {"pa:" + s.clientKey(r), portalOrderAddrRate}} {
		if _, err := s.portal.Wait(r.Context(), b.key, b.rate); err != nil {
			s.portalMiss(w, http.StatusTooManyRequests, err, nil)
			return false
		}
	}
	return true
}

// POST /p/{org}/{page}/{token}/order {guest?, note?, lines: [{item, qty, note?}]}: one new order on the anchor.
func (s *Server) portalOrderCreate(w http.ResponseWriter, r *http.Request) {
	hit := s.portalOrderHit(w, r)
	if hit == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, portalOrderBodyMax)
	body, ok := readObject(w, r)
	if !ok {
		return
	}
	if !s.portalOrderBucket(w, r, hit) {
		return
	}
	p := hit.page
	ctx := pg.WithOrg(r.Context(), hit.org)
	bad := func(code, msg string) { writeError(w, http.StatusBadRequest, code, msg) }
	raw, _ := body["lines"].([]any)
	if len(raw) == 0 {
		bad("order_empty", "Pilih minimal satu menu.")
		return
	}
	if len(raw) > portalOrderMaxLines {
		bad("order_too_long", fmt.Sprintf("Maksimal %d baris dalam satu pesanan.", portalOrderMaxLines))
		return
	}
	menu, err := s.portalMenuIndex(ctx, hit)
	if err != nil {
		s.portalMiss(w, http.StatusInternalServerError, err, nil)
		return
	}
	lines := make([]any, 0, len(raw))
	for _, x := range raw {
		l, _ := x.(map[string]any)
		item, _ := l["item"].(string)
		qty, isNum := l["qty"].(float64)
		row := menu[item]
		switch {
		case row == nil:
			writeError(w, http.StatusConflict, "order_item_unavailable", "Ada menu yang sudah tidak tersedia. Muat ulang menu lalu coba lagi.")
			return
		case row["sold_out"] == true:
			writeError(w, http.StatusConflict, "order_item_sold_out", fmt.Sprintf("%v sedang habis.", row["title"]))
			return
		case !isNum || qty != math.Trunc(qty) || qty < 1 || qty > portalOrderMaxQty:
			bad("order_qty", fmt.Sprintf("Jumlah per menu 1 sampai %d.", portalOrderMaxQty))
			return
		}
		line := map[string]any{p.LinesItem: item, p.LinesQty: qty}
		if p.LinesNote != "" {
			note, ok := portalText(l["note"], portalOrderNoteMax)
			if !ok {
				bad("order_note", fmt.Sprintf("Catatan maksimal %d huruf.", portalOrderNoteMax))
				return
			}
			line[p.LinesNote] = note
		}
		lines = append(lines, line)
	}
	input := map[string]any{p.AnchorField: hit.rec["name"], p.LinesField: lines}
	for _, slot := range [][3]any{{p.Guest, "guest", portalOrderGuestMax}, {p.Note, "note", portalOrderNoteMax}} {
		field, key, max := slot[0].(string), slot[1].(string), slot[2].(int)
		if field == "" {
			continue
		}
		v, ok := portalText(body[key], max)
		if !ok {
			bad("order_"+key, fmt.Sprintf("Maksimal %d huruf.", max))
			return
		}
		input[field] = v
	}
	open, err := s.portalOpenOrders(ctx, hit)
	if err != nil {
		s.portalMiss(w, http.StatusInternalServerError, err, nil)
		return
	}
	if open >= portalOrderMaxOpen {
		writeError(w, http.StatusConflict, "order_too_many_open", "Masih ada pesanan meja ini yang belum diterima kasir. Tunggu sebentar atau panggil pelayan.")
		return
	}
	res, err := s.Engine.Create(ctx, hit.actor, p.Model, input)
	if err != nil {
		status, _ := failBody(err)
		if status >= 500 {
			s.portalMiss(w, http.StatusInternalServerError, err, nil)
			return
		}
		writeError(w, http.StatusConflict, "order_refused", portalRefusal(err))
		return
	}
	rec := res.Record
	if err := store.Audit(ctx, s.DB, hit.org, hit.actor.ID, "portal.order", p.Model+"/"+fmt.Sprint(rec["name"]),
		fmt.Sprintf("%s (%d baris)", p.Name, len(lines)), map[string]any{"page": p.Name, "link": hit.id, "anchor": hit.rec["name"]}); err != nil {
		s.logf("portal: audit order %s: %v", hit.id, err)
	}
	m := s.Engine.Reg().Model(p.Model)
	full, err := s.Engine.Get(ctx, hit.actor, m.Name, fmt.Sprint(rec["name"]))
	if err != nil {
		full = rec
	}
	writeJSON(w, http.StatusCreated, map[string]any{"order": s.portalOrderView(ctx, hit, m, m.Field(p.StatusField), full)})
}

// portalRefusal is the message of a model rule that refused the order (written by the module for people), else a
// fixed sentence: a required-field or internal message never reaches the guest.
func portalRefusal(err error) string {
	var ve *engine.ValidationError
	if errors.As(err, &ve) {
		for _, i := range ve.Errors {
			if i.Rule != "" && i.Message != "" {
				return i.Message
			}
		}
	}
	return "Pesanan belum bisa dikirim. Panggil pelayan."
}

// portalText is a guest's free text: trimmed, at most max characters, no control characters.
func portalText(v any, max int) (string, bool) {
	if v == nil {
		return "", true
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s))
	return s, utf8.RuneCountInString(s) <= max
}

// portalOpenOrders counts the open orders on the link's anchor made in the last 12 hours.
func (s *Server) portalOpenOrders(ctx context.Context, hit *portalHit) (int, error) {
	reg := s.Engine.Reg()
	p := hit.page
	m := reg.Model(p.Model)
	af, sf := m.Field(p.AnchorField), m.Field(p.StatusField)
	if af == nil || sf == nil {
		return 0, fmt.Errorf("order page %s: anchor or status field missing", p.Name)
	}
	col := func(f *meta.Field) string {
		if store.IsColumn(f) {
			return pg.QuoteIdent(f.Name)
		}
		return "(x->>" + pg.QuoteLiteral(f.Name) + ")"
	}
	q := fmt.Sprintf("select count(*) from %s where org_id = $1 and %s = $2 and %s = any($3) and created_at > $4",
		pg.QuoteIdent(store.TableName(m.Name)), col(af), col(sf))
	since := s.Engine.Now().UTC().Add(-portalOrderMineHours * time.Hour)
	res, err := s.DB.Query(ctx, q, hit.org, fmt.Sprint(hit.rec["name"]), p.StatusOpen, since)
	if err != nil || len(res.Rows) != 1 {
		return 0, err
	}
	n, _ := portalNumber(res.Rows[0][0])
	return int(n), nil
}

// POST /p/{org}/{page}/{token}/order/{name}/cancel: the guest takes back an order of this link that is still open,
// within 2 minutes. The write is a plain update of the status field as the link's actor, so the model's rules and
// flows decide as for any write.
func (s *Server) portalOrderCancel(w http.ResponseWriter, r *http.Request) {
	hit := s.portalOrderHit(w, r)
	if hit == nil {
		return
	}
	if !s.portalOrderBucket(w, r, hit) {
		return
	}
	p := hit.page
	ctx := pg.WithOrg(r.Context(), hit.org)
	m := s.Engine.Reg().Model(p.Model)
	name := r.PathValue("name")
	rec, err := s.Engine.Get(ctx, hit.actor, m.Name, name)
	if err != nil || !s.portalOwnOrder(hit, rec) {
		writeError(w, http.StatusNotFound, "order_unknown", "Pesanan tidak ditemukan.")
		return
	}
	view := s.portalOrderView(ctx, hit, m, m.Field(p.StatusField), rec)
	if view["can_cancel"] != true {
		writeError(w, http.StatusConflict, "order_not_cancellable", "Pesanan sudah diproses kasir. Panggil pelayan untuk mengubahnya.")
		return
	}
	// conditional: a cashier's accept between the read and this write turns the cancel into a conflict
	if _, err := s.Engine.UpdateIf(ctx, hit.actor, m.Name, name, map[string]any{p.StatusField: p.StatusCancel},
		map[string]any{p.StatusField: rec[p.StatusField]}); err != nil {
		writeError(w, http.StatusConflict, "order_not_cancellable", "Pesanan sudah diproses kasir. Panggil pelayan untuk mengubahnya.")
		return
	}
	if err := store.Audit(ctx, s.DB, hit.org, hit.actor.ID, "portal.order.cancel", m.Name+"/"+name, p.Name,
		map[string]any{"page": p.Name, "link": hit.id}); err != nil {
		s.logf("portal: audit cancel %s: %v", hit.id, err)
	}
	rec, _ = s.Engine.Get(ctx, hit.actor, m.Name, name)
	writeJSON(w, 200, map[string]any{"order": s.portalOrderView(ctx, hit, m, m.Field(p.StatusField), rec)})
}

// POST /api/v1/portal/{page}/qr {records?: [...]}: new QR links for the anchors of an order_page (every active one
// when records is empty, at most 200). Live links of each anchor are revoked first, so a printed sheet replaces the
// old stickers. Owner and the page's share roles; each anchor must be readable by the caller.
func (s *Server) portalQRSheet(w http.ResponseWriter, r *http.Request, c *caller) {
	reg := s.Engine.Reg()
	p := reg.Portal(r.PathValue("page"))
	if p == nil || p.Type != "order_page" {
		writeError(w, http.StatusNotFound, "portal_page_unknown", "Halaman pesan "+r.PathValue("page")+" tidak ada")
		return
	}
	if !canShare(c.actor, p) {
		writeError(w, http.StatusForbidden, "portal_share_forbidden", "Peran Anda tidak boleh membuat QR untuk halaman ini.")
		return
	}
	body, ok := readObject(w, r)
	if !ok {
		return
	}
	var names []string
	if list, _ := body["records"].([]any); len(list) > 0 {
		for _, x := range list {
			if n, ok := x.(string); ok && n != "" {
				names = append(names, n)
			}
		}
	} else {
		filter := ""
		if am := reg.Model(p.AnchorModel); am != nil && am.Field("active") != nil {
			filter = "active != false"
		}
		rows, _, err := s.Engine.List(r.Context(), c.actor, p.AnchorModel, engine.ListOpts{Filter: filter, Limit: 200})
		if err != nil {
			fail(w, err)
			return
		}
		for _, row := range rows {
			names = append(names, fmt.Sprint(row["name"]))
		}
	}
	if len(names) > 200 {
		writeError(w, http.StatusBadRequest, "bad_request", "Maksimal 200 QR sekali cetak")
		return
	}
	sort.Strings(names)
	am := reg.Model(p.AnchorModel)
	out := []any{}
	for _, n := range names {
		rec, err := s.Engine.Get(r.Context(), c.actor, p.AnchorModel, n)
		if err != nil {
			fail(w, err)
			return
		}
		if _, err := s.DB.Exec(r.Context(), `update rk_portal_links set revoked_at = $4 where org_id = $1 and page = $2 and record = $3 and revoked_at is null`,
			c.actor.Org, p.Name, n, s.Engine.Now().UTC()); err != nil {
			fail(w, err)
			return
		}
		link, err := s.portalNewLink(r, c, p, p.AnchorModel, n, p.LifetimeDays)
		if err != nil {
			fail(w, err)
			return
		}
		title := n
		if f := portalShown(am, p.AnchorTitle); f != nil {
			if t, _ := rec[f.Name].(string); t != "" {
				title = t
			}
		}
		link["record"], link["title"] = n, title
		link["qr"] = string(docprint.QR(fmt.Sprint(link["url"])))
		out = append(out, link)
	}
	writeJSON(w, 200, map[string]any{"data": out, "page": p.Name, "label": p.Label.Text()})
}
