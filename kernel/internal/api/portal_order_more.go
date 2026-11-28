package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Order pages, second part (contract 0073 §9): options per line (the 0048 §1 shape), "bring later" lines, the estimate
// before sending (the model's own computes, contract 0034), requests to the staff, and the table session: the anchor's
// open document whose lines and totals every phone at the table sees.

const (
	portalOrderMaxOptions  = 12 // options on one line
	portalOrderMaxSession  = 50 // rounds shown on the session view
	portalOrderSessionScan = 20 // open documents looked at per anchor
)

// portalErr is a refusal of a guest's body, written as JSON with its status.
type portalErr struct {
	status    int
	code, msg string
}

func (e *portalErr) write(w http.ResponseWriter, s *Server) {
	if e.status >= 500 {
		s.portalMiss(w, http.StatusInternalServerError, fmt.Errorf("%s", e.msg), nil)
		return
	}
	writeError(w, e.status, e.code, e.msg)
}

func badBody(code, msg string) *portalErr  { return &portalErr{http.StatusBadRequest, code, msg} }
func conflict(code, msg string) *portalErr { return &portalErr{http.StatusConflict, code, msg} }

// portalRowLinks lists the link values of a table field's rows (a menu row's option groups, in display order).
func portalRowLinks(v any, link string) []any {
	out := []any{}
	list, _ := v.([]any)
	for _, x := range list {
		row, ok := x.(map[string]any)
		if !ok {
			if sr, ok2 := x.(store.Record); ok2 {
				row = sr
			} else {
				continue
			}
		}
		if s, _ := row[link].(string); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// portalOptGroup is one option group as a guest sees it.
type portalOptGroup struct {
	Title    string           `json:"title"`
	Required bool             `json:"required"`
	Multi    bool             `json:"multi"`
	Max      int              `json:"max"`
	Options  []map[string]any `json:"options"`
}

// portalOptionCatalog is every live option group with its live options: title, required, multi, max, and per option
// name, title and price. Nothing else of either model leaves the kernel.
func (s *Server) portalOptionCatalog(ctx context.Context, hit *portalHit) map[string]*portalOptGroup {
	reg := s.Engine.Reg()
	o := hit.page.Opt
	out := map[string]*portalOptGroup{}
	gm, om := reg.Model(o.GroupModel), reg.Model(o.Model)
	if gm == nil || om == nil {
		return out
	}
	tf := portalShown(gm, o.GroupTitle)
	if tf == nil {
		return out
	}
	groups, _, err := s.Engine.List(ctx, hit.actor, gm.Name, engine.ListOpts{Filter: o.GroupFilter, Limit: 500})
	if err != nil {
		s.logf("portal: option groups: %v", err)
		return out
	}
	for _, g := range groups {
		pg := &portalOptGroup{Title: fmt.Sprint(g[tf.Name]), Options: []map[string]any{}}
		if f := portalShown(gm, o.GroupRequired); f != nil {
			pg.Required = g[f.Name] == true
		}
		if f := portalShown(gm, o.GroupMulti); f != nil {
			pg.Multi = g[f.Name] == true
		}
		if f := portalShown(gm, o.GroupMax); f != nil {
			n, _ := portalNumber(g[f.Name])
			pg.Max = int(n)
		}
		out[fmt.Sprint(g["name"])] = pg
	}
	ot, op, ol := portalShown(om, o.Title), portalShown(om, o.Price), om.Field(o.Link)
	if ot == nil || op == nil || ol == nil {
		return out
	}
	opts, _, err := s.Engine.List(ctx, hit.actor, om.Name, engine.ListOpts{Filter: o.Filter, Sort: "name", Limit: 2000}) // in the order they were made
	if err != nil {
		s.logf("portal: options: %v", err)
		return out
	}
	for _, x := range opts {
		if g := out[fmt.Sprint(x[ol.Name])]; g != nil {
			g.Options = append(g.Options, map[string]any{"name": x["name"], "title": x[ot.Name], "price": x[op.Name]})
		}
	}
	return out
}

// portalLineOptions are the titles of the options rec's options table records for one line key.
func (s *Server) portalLineOptions(ctx context.Context, hit *portalHit, rec store.Record, key string) []any {
	o := hit.page.Opt
	out := []any{}
	om := s.Engine.Reg().Model(o.Model)
	ot := portalShown(om, o.Title)
	list, _ := rec[o.Table].([]any)
	for _, x := range list {
		row, ok := x.(map[string]any)
		if !ok {
			if sr, ok2 := x.(store.Record); ok2 {
				row = sr
			} else {
				continue
			}
		}
		if fmt.Sprint(row[o.Line]) != key || ot == nil {
			continue
		}
		if opt, err := s.Engine.Get(ctx, hit.actor, om.Name, fmt.Sprint(row[o.Option])); err == nil {
			out = append(out, opt[ot.Name])
		}
	}
	return out
}

// portalOrderInput builds the order record from a guest's body: the anchor from the link, only the declared fields,
// line keys L1, L2… from the kernel, and options checked against the item's own groups (the group's required, multi
// and max). It never reads a price, an anchor or any other field from the body.
func (s *Server) portalOrderInput(ctx context.Context, hit *portalHit, body map[string]any) (map[string]any, *portalErr) {
	p := hit.page
	raw, _ := body["lines"].([]any)
	if len(raw) == 0 {
		return nil, badBody("order_empty", "Pilih minimal satu menu.")
	}
	if len(raw) > portalOrderMaxLines {
		return nil, badBody("order_too_long", fmt.Sprintf("Maksimal %d baris dalam satu pesanan.", portalOrderMaxLines))
	}
	menu, err := s.portalMenuIndex(ctx, hit)
	if err != nil {
		return nil, &portalErr{status: 500, msg: err.Error()}
	}
	var catalog map[string]*portalOptGroup
	optGroup := map[string]string{}
	if p.Opt.Model != "" {
		catalog = s.portalOptionCatalog(ctx, hit)
		for gname, g := range catalog {
			for _, o := range g.Options {
				optGroup[fmt.Sprint(o["name"])] = gname
			}
		}
	}
	lines := make([]any, 0, len(raw))
	options := []any{}
	for i, x := range raw {
		l, _ := x.(map[string]any)
		item, _ := l["item"].(string)
		qty, isNum := l["qty"].(float64)
		row := menu[item]
		switch {
		case row == nil:
			return nil, conflict("order_item_unavailable", "Ada menu yang sudah tidak tersedia. Muat ulang menu lalu coba lagi.")
		case row["sold_out"] == true:
			return nil, conflict("order_item_sold_out", fmt.Sprintf("%v sedang habis.", row["title"]))
		case !isNum || qty != math.Trunc(qty) || qty < 1 || qty > portalOrderMaxQty:
			return nil, badBody("order_qty", fmt.Sprintf("Jumlah per menu 1 sampai %d.", portalOrderMaxQty))
		}
		line := map[string]any{p.LinesItem: item, p.LinesQty: qty}
		key := fmt.Sprintf("L%d", i+1)
		if p.LinesKey != "" {
			line[p.LinesKey] = key
		}
		if p.LinesNote != "" {
			note, ok := portalText(l["note"], portalOrderNoteMax)
			if !ok {
				return nil, badBody("order_note", fmt.Sprintf("Catatan maksimal %d huruf.", portalOrderNoteMax))
			}
			line[p.LinesNote] = note
		}
		if p.LinesLater != "" {
			later, isBool := l["later"].(bool)
			if l["later"] != nil && !isBool {
				return nil, badBody("order_later", "later harus true atau false")
			}
			line[p.LinesLater] = later
		}
		chosen, _ := l["options"].([]any)
		if l["options"] != nil && chosen == nil {
			return nil, badBody("order_option", "options harus daftar")
		}
		if len(chosen) > portalOrderMaxOptions {
			return nil, badBody("order_option", fmt.Sprintf("Maksimal %d pilihan per menu.", portalOrderMaxOptions))
		}
		if catalog != nil {
			rowGroups := map[string]bool{}
			if gs, _ := row["groups"].([]any); gs != nil {
				for _, g := range gs {
					rowGroups[fmt.Sprint(g)] = true
				}
			}
			count := map[string]int{}
			seen := map[string]bool{}
			for _, c := range chosen {
				id, _ := c.(string)
				g := optGroup[id]
				if id == "" || g == "" || !rowGroups[g] || seen[id] {
					return nil, conflict("order_option_unavailable", fmt.Sprintf("Pilihan untuk %v sudah berubah. Pilih lagi ya.", row["title"]))
				}
				seen[id] = true
				count[g]++
				options = append(options, map[string]any{p.Opt.Line: key, p.Opt.Option: id})
			}
			gnames := make([]string, 0, len(rowGroups))
			for g := range rowGroups {
				gnames = append(gnames, g)
			}
			sort.Strings(gnames)
			for _, g := range gnames {
				cg := catalog[g]
				if cg == nil {
					continue // a group switched off: nothing to choose
				}
				n := count[g]
				switch {
				case cg.Required && n == 0 && len(cg.Options) > 0:
					return nil, conflict("order_option_required", fmt.Sprintf("Pilih %s untuk %v.", cg.Title, row["title"]))
				case !cg.Multi && n > 1:
					return nil, conflict("order_option_one", fmt.Sprintf("%s untuk %v hanya boleh satu.", cg.Title, row["title"]))
				case cg.Max > 0 && n > cg.Max:
					return nil, conflict("order_option_max", fmt.Sprintf("%s untuk %v maksimal %d.", cg.Title, row["title"], cg.Max))
				}
			}
		} else if len(chosen) > 0 {
			return nil, badBody("order_option", "Halaman ini tidak memakai pilihan menu.")
		}
		lines = append(lines, line)
	}
	input := map[string]any{p.AnchorField: hit.rec["name"], p.LinesField: lines}
	if p.Opt.Table != "" {
		input[p.Opt.Table] = options
	}
	if p.ReqField != "" {
		input[p.ReqField] = p.ReqOrder
	}
	if perr := portalGuestFields(p, body, input); perr != nil {
		return nil, perr
	}
	return input, nil
}

// portalGuestFields copies the guest's name and note into input when the page declares them.
func portalGuestFields(p *meta.Portal, body, input map[string]any) *portalErr {
	for _, slot := range [][3]any{{p.Guest, "guest", portalOrderGuestMax}, {p.Note, "note", portalOrderNoteMax}} {
		field, key, max := slot[0].(string), slot[1].(string), slot[2].(int)
		if field == "" {
			continue
		}
		v, ok := portalText(body[key], max)
		if !ok {
			return badBody("order_"+key, fmt.Sprintf("Maksimal %d huruf.", max))
		}
		input[field] = v
	}
	return nil
}

// POST /p/{org}/{page}/{token}/estimate {lines, …}: the order as the model would compute it, nothing written (contract
// 0034): the page shows the model's subtotal, service and tax, never its own arithmetic.
func (s *Server) portalOrderEstimate(w http.ResponseWriter, r *http.Request) {
	hit := s.portalOrderHit(w, r)
	if hit == nil {
		return
	}
	p := hit.page
	if len(p.Estimate) == 0 {
		s.portalMiss(w, http.StatusNotFound, nil, nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, portalOrderBodyMax)
	body, ok := readObject(w, r)
	if !ok {
		return
	}
	if _, err := s.portal.Wait(r.Context(), "pe:"+s.clientKey(r), portalEstimateRate); err != nil {
		s.portalMiss(w, http.StatusTooManyRequests, err, nil)
		return
	}
	ctx := pg.WithOrg(r.Context(), hit.org)
	input, perr := s.portalOrderInput(ctx, hit, body)
	if perr != nil {
		perr.write(w, s)
		return
	}
	res, err := s.Engine.Compute(ctx, hit.actor, p.Model, input)
	if err != nil {
		if status, _ := failBody(err); status >= 500 {
			s.portalMiss(w, http.StatusInternalServerError, err, nil)
			return
		}
		writeError(w, http.StatusConflict, "order_refused", portalRefusal(s.Engine.Reg(), hit.page, err))
		return
	}
	m := s.Engine.Reg().Model(p.Model)
	out := []any{}
	for _, n := range p.Estimate {
		if f := portalShown(m, n); f != nil {
			out = append(out, map[string]any{"name": f.Name, "label": fieldLabel(f), "value": res.Record[f.Name]})
		}
	}
	writeJSON(w, 200, map[string]any{"estimate": out})
}

// POST /p/{org}/{page}/{token}/request {kind, about?, note?, guest?}: a request to the staff (call a waiter, ask for
// the bill, ask for held dishes). about names one of this link's own orders.
func (s *Server) portalOrderRequest(w http.ResponseWriter, r *http.Request) {
	hit := s.portalOrderHit(w, r)
	if hit == nil {
		return
	}
	p := hit.page
	if p.ReqField == "" {
		s.portalMiss(w, http.StatusNotFound, nil, nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, portalOrderBodyMax)
	body, ok := readObject(w, r)
	if !ok {
		return
	}
	kind, _ := body["kind"].(string)
	if !contains(p.ReqValues, kind) { // refused before the order buckets: it writes nothing
		writeError(w, http.StatusBadRequest, "request_kind", "Permintaan tidak dikenal.")
		return
	}
	if !s.portalOrderBucket(w, r, hit) {
		return
	}
	ctx := pg.WithOrg(r.Context(), hit.org)
	m := s.Engine.Reg().Model(p.Model)
	input := map[string]any{p.AnchorField: hit.rec["name"], p.ReqField: kind}
	if about, _ := body["about"].(string); about != "" {
		rec, err := s.Engine.Get(ctx, hit.actor, m.Name, about)
		if p.ReqAbout == "" || err != nil || !s.portalOwnOrder(hit, rec) {
			writeError(w, http.StatusNotFound, "order_unknown", "Pesanan tidak ditemukan.")
			return
		}
		input[p.ReqAbout] = about
	}
	if perr := portalGuestFields(p, body, input); perr != nil {
		perr.write(w, s)
		return
	}
	open, err := s.portalOpenOrders(ctx, hit)
	if err != nil {
		s.portalMiss(w, http.StatusInternalServerError, err, nil)
		return
	}
	if open >= portalOrderMaxOpen {
		writeError(w, http.StatusConflict, "order_too_many_open", "Permintaan meja ini sudah sampai ke kasir. Tunggu sebentar ya.")
		return
	}
	if recent, err := s.portalRecentOrders(ctx, hit); err != nil || recent >= portalOrderRecentMax {
		if err != nil {
			s.portalMiss(w, http.StatusInternalServerError, err, nil)
			return
		}
		writeError(w, http.StatusConflict, "order_too_many_recent", "Sudah banyak permintaan dari meja ini dalam 10 menit. Panggil pelayan langsung ya.")
		return
	}
	res, err := s.Engine.Create(ctx, hit.actor, p.Model, input)
	if err != nil {
		if status, _ := failBody(err); status >= 500 {
			s.portalMiss(w, http.StatusInternalServerError, err, nil)
			return
		}
		writeError(w, http.StatusConflict, "order_refused", portalRefusal(s.Engine.Reg(), hit.page, err))
		return
	}
	name := fmt.Sprint(res.Record["name"])
	if err := store.Audit(ctx, s.DB, hit.org, hit.actor.ID, "portal.request", m.Name+"/"+name, p.Name+" ("+kind+")",
		map[string]any{"page": p.Name, "link": hit.id, "anchor": hit.rec["name"], "kind": kind}); err != nil {
		s.logf("portal: audit request %s: %v", hit.id, err)
	}
	full, err := s.Engine.Get(ctx, hit.actor, m.Name, name)
	if err != nil {
		full = res.Record
	}
	writeJSON(w, http.StatusCreated, map[string]any{"order": s.portalOrderView(ctx, hit, m, m.Field(p.StatusField), full)})
}

// portalSession is the anchor's open document (the table's open bill) as every phone at the table sees it: its lines
// (title, qty, amount), its totals, and the rounds the table sent into it (all phones). nil when the table has none
// open: the next round waits for the staff.
func (s *Server) portalSession(ctx context.Context, hit *portalHit) any {
	reg := s.Engine.Reg()
	p := hit.page
	se := p.Sess
	sm := reg.Model(se.Model)
	if sm == nil {
		return nil
	}
	names, err := s.portalNamesBy(ctx, hit.org, sm, se.Link, fmt.Sprint(hit.rec["name"]), portalOrderSessionScan)
	if err != nil {
		s.logf("portal: session of %v: %v", hit.rec["name"], err)
		return nil
	}
	var doc store.Record
	for _, n := range names {
		rec, err := s.Engine.Get(ctx, hit.actor, sm.Name, n)
		if err != nil || rec[se.Link] != hit.rec["name"] {
			continue
		}
		if se.Filter != "" {
			if ok, err := s.Engine.Holds(ctx, hit.actor, sm.Name, rec, se.Filter, nil); err != nil || !ok {
				continue
			}
		}
		doc = rec
		break
	}
	if doc == nil {
		return nil
	}
	out := map[string]any{"lines": []any{}, "totals": []any{}, "rounds": []any{}}
	if f := sm.Field(se.LinesField); f != nil && f.Type == "table" {
		if cm := reg.Model(f.Of); cm != nil {
			tf, qf, af := portalShown(cm, se.LinesTitle), portalShown(cm, se.LinesQty), portalShown(cm, se.LinesAmount)
			list, _ := doc[f.Name].([]any)
			lines := []any{}
			for _, x := range list {
				row, ok := x.(map[string]any)
				if !ok {
					if sr, ok2 := x.(store.Record); ok2 {
						row = sr
					} else {
						continue
					}
				}
				l := map[string]any{}
				if tf != nil {
					l["title"] = row[tf.Name]
				}
				if qf != nil {
					l["qty"] = row[qf.Name]
				}
				if af != nil {
					l["amount"] = row[af.Name]
				}
				lines = append(lines, l)
			}
			out["lines"] = lines
		}
	}
	totals := []any{}
	for _, n := range se.Totals {
		if f := portalShown(sm, n); f != nil {
			totals = append(totals, map[string]any{"name": f.Name, "label": fieldLabel(f), "value": doc[f.Name]})
		}
	}
	out["totals"] = totals
	m := reg.Model(p.Model)
	if lf := m.Field(se.OrderLink); lf != nil {
		rounds, err := s.portalNamesBy(ctx, hit.org, m, lf.Name, fmt.Sprint(doc["name"]), portalOrderMaxSession)
		if err == nil {
			sort.Strings(rounds) // oldest first; the page shows the newest on top
			sf := m.Field(p.StatusField)
			views := []any{}
			for _, n := range rounds {
				if rec, err := s.Engine.Get(ctx, hit.actor, m.Name, n); err == nil && rec[p.AnchorField] == hit.rec["name"] {
					v := s.portalOrderView(ctx, hit, m, sf, rec)
					v["mine"] = rec["created_by"] == hit.actor.ID
					views = append(views, v)
				}
			}
			out["rounds"] = views
		}
	}
	out["opened"] = strings.TrimSpace(fmt.Sprint(doc["created_at"]))
	return out
}

// portalIdempotent runs a guest's write once per Idempotency-Key (contract 0030 §6 keys, scoped to the link's actor
// portal:<link id>): a resend of the same body answers the first result, another body with the key is refused. Without
// a key the write simply runs. The order buckets are spent only by the run that claims the key.
func (s *Server) portalIdempotent(w http.ResponseWriter, r *http.Request, hit *portalHit, run func(http.ResponseWriter, *http.Request)) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		run(w, r)
		return
	}
	if !validIdemKey(key) {
		writeError(w, http.StatusBadRequest, "idempotency_key", "Idempotency-Key must be 1 to 128 printable ASCII characters")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, portalOrderBodyMax+1))
	if err != nil || len(body) > portalOrderBodyMax {
		writeError(w, http.StatusBadRequest, "bad_body", "Pesanan terlalu besar.")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	s.pruneIdempotency()
	sum := sha256.Sum256([]byte(r.Method + " " + r.URL.Path + "\n" + string(canonicalBody(body, body))))
	hash := hex.EncodeToString(sum[:])
	ctx := pg.WithOrg(r.Context(), hit.org)
	it, err := store.ClaimIdempotency(ctx, s.DB, hit.org, hit.actor.ID, key, hash)
	if err != nil && !errors.Is(err, store.ErrIdemTaken) {
		s.portalMiss(w, http.StatusInternalServerError, err, nil)
		return
	}
	if err != nil {
		s.idemReplay(w, r.WithContext(ctx), &caller{actor: hit.actor}, it, hash)
		return
	}
	cw := &captureWriter{h: http.Header{}}
	k := engine.IdemKey{Org: hit.org, Actor: hit.actor.ID, Key: key, Claim: it.Claim}
	run(cw, r.WithContext(engine.WithIdempotency(r.Context(), k)))
	bg := context.WithoutCancel(ctx)
	if cw.status >= 200 && cw.status < 300 {
		if err := store.FinishIdempotency(bg, s.DB, hit.org, hit.actor.ID, key, it.Claim, cw.status, json.RawMessage(bytes.TrimSpace(cw.buf.Bytes()))); err != nil {
			s.logf("portal: idempotency answer %s: %v", hit.id, err)
		}
	} else if err := store.ReleaseIdempotency(bg, s.DB, hit.org, hit.actor.ID, key, it.Claim); err != nil {
		s.logf("portal: idempotency release %s: %v", hit.id, err)
	}
	cw.flush(w)
}

// portalNamesBy are the names of the newest records of m whose field equals value, at most limit (store.FindBy reads
// only the first two rows, which a table with past bills outgrows).
func (s *Server) portalNamesBy(ctx context.Context, org string, m *meta.Model, field, value string, limit int) ([]string, error) {
	f := m.Field(field)
	if f == nil {
		return nil, fmt.Errorf("model %s has no field %s", m.Name, field)
	}
	col := "(x->>" + pg.QuoteLiteral(f.Name) + ")"
	if store.IsColumn(f) {
		col = pg.QuoteIdent(f.Name)
	}
	q := fmt.Sprintf("select name from %s where org_id = $1 and %s = $2 order by created_at desc, name desc limit %d",
		pg.QuoteIdent(store.TableName(m.Name)), col, limit)
	res, err := s.DB.Query(ctx, q, org, value)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(res.Rows))
	for _, row := range res.Rows {
		out = append(out, fmt.Sprint(row[0]))
	}
	return out, nil
}
