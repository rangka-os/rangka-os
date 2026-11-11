package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/money"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

func formatInt(n int64) string { return strconv.FormatInt(n, 10) }

// ListOpts are list query options (GET /r/{model}).
type ListOpts struct {
	Filter   string // CEL
	Sort     string // field or -field
	Limit    int
	Offset   int
	Q        string // text search over text fields
	Children bool
}

// List returns matching records the actor may read and the total before paging. Records outside the actor's
// row scope, and rows whose read grant's if: is false, are left out before filtering and paging and are not
// counted. Without a read grant the list is denied. v0 filters in memory with CEL; a CEL→SQL translation for
// simple filters is the next step for large tables.
func (e *Engine) List(ctx context.Context, a Actor, model string, opts ListOpts) ([]store.Record, int, error) {
	m, err := e.model(model)
	if err != nil {
		return nil, 0, err
	}
	out, err := e.matching(ctx, a, m, opts)
	if err != nil {
		return nil, 0, err
	}
	hidden := e.hiddenFields(a, m)
	if opts.Sort != "" && !hidden[strings.TrimPrefix(opts.Sort, "-")] { // sorting by a hidden field would reveal its order
		desc := strings.HasPrefix(opts.Sort, "-")
		key := strings.TrimPrefix(opts.Sort, "-")
		sort.SliceStable(out, func(i, j int) bool {
			c := compareValues(out[i][key], out[j][key])
			if desc {
				return c > 0
			}
			return c < 0
		})
	}
	total := len(out)
	if opts.Offset > 0 {
		if opts.Offset >= len(out) {
			out = nil
		} else {
			out = out[opts.Offset:]
		}
	}
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	if opts.Limit < len(out) {
		out = out[:opts.Limit]
	}
	if out == nil {
		out = []store.Record{}
	}
	if hidden != nil || hasTable(m) {
		for _, r := range out {
			e.Redact(a, m, r)
		}
	}
	return out, total, nil
}

// matching returns every row List would return for opts before sorting and paging (Sort, Limit and Offset are
// ignored): the actor's read grant, row scope, filter and q all apply. A filter that does not compile is a 400; a
// filter that fails to evaluate on a row leaves that row out.
func (e *Engine) matching(ctx context.Context, a Actor, m *meta.Model, opts ListOpts) ([]store.Record, error) {
	if m.IsChild {
		return nil, badRequest(m.Name + " rows are read through their parent document")
	}
	readable := e.decide(a, m, "read")
	if !readable.maybe() {
		return nil, e.Deny(m.Name, "read")
	}
	if opts.Filter != "" {
		if _, err := cel.Compile(opts.Filter); err != nil {
			return nil, badRequest("filter: " + err.Error())
		}
	}
	children := opts.Children || strings.Contains(opts.Filter, ".")
	all, err := store.All(ctx, e.DB, e.Reg(), a.Org, m, children)
	if err != nil {
		return nil, err
	}
	links := map[string]map[string]any{}
	titles := map[string]string{}
	var out []store.Record
	q := strings.ToLower(strings.TrimSpace(opts.Q))
	rs := e.RowScope(a)
	hidden := e.hiddenFields(a, m) // q never matches the text of a field hidden from the actor
	for _, r := range all {
		if !rs.Visible(m, r) {
			continue
		}
		e.refreshClock(ctx, a, m, r, children, links)
		e.Output(m, r)
		if !e.recordAllowed(ctx, e.DB, a, m, readable, r, links) {
			continue
		}
		if opts.Filter != "" {
			v, err := e.newClientScope(ctx, a, m, r, rs, links).eval(opts.Filter)
			if err != nil {
				if _, ok := err.(*cel.SyntaxError); ok {
					return nil, badRequest(err.Error())
				}
				continue
			}
			if v != true {
				continue
			}
		}
		if q != "" && !matchText(m, r, q, hidden) && !e.matchLinkTitle(ctx, a, rs, m, r, q, titles, links) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// matchLinkTitle reports whether q occurs in the display title of any record a link field points at, so lists are
// searchable by what users see (a customer's name) and not only by the stored key. titles caches model/name → title.
// A target the actor could not read (grant, if: or row scope, on every hop of a linked title) never matches, so q
// cannot reveal its title.
func (e *Engine) matchLinkTitle(ctx context.Context, a Actor, rs *RowScope, m *meta.Model, r store.Record, q string, titles map[string]string, links map[string]map[string]any) bool {
	for _, f := range m.Fields {
		if f.Type != "link" || str(r[f.Name]) == "" {
			continue
		}
		if t := strings.ToLower(e.linkTitle(ctx, a, rs, f.To, str(r[f.Name]), titles, links)); t != "" && strings.Contains(t, q) {
			return true
		}
	}
	return false
}

// linkTitle returns the display title of record name of model to, or "" when it is missing or the actor could not
// read it with GET /r/{to}/{name} (read grant with its if:, row scope rs). titles caches model/name → title.
func (e *Engine) linkTitle(ctx context.Context, a Actor, rs *RowScope, to, name string, titles map[string]string, links map[string]map[string]any) string {
	key := to + "/" + name
	t, ok := titles[key]
	if !ok {
		if l, ok := e.readableLink(ctx, a, rs, to, name, links); ok {
			t = e.title(ctx, a, rs, to, l, 0, links)
		}
		titles[key] = t
	}
	return t
}

// readableLink loads record name of model to when the actor could read it with GET /r/{to}/{name}: it exists, it
// is inside row scope rs and it passes the read grant (its if: included). Otherwise ok is false and nothing of the
// record may be shown; callers show the stored code instead (audit 2026-11-06: titles leaked across outlets).
func (e *Engine) readableLink(ctx context.Context, a Actor, rs *RowScope, to, name string, links map[string]map[string]any) (store.Record, bool) {
	rec, ok, _ := e.readableLinkErr(ctx, a, rs, to, name, links)
	return rec, ok
}

// readableLinkErr is readableLink that also returns a database error (anything but not found): a client filter
// must fail on it instead of reading the record as missing (a predicate like grand_total == 0 would match).
func (e *Engine) readableLinkErr(ctx context.Context, a Actor, rs *RowScope, to, name string, links map[string]map[string]any) (store.Record, bool, error) {
	tm := e.Reg().Models[to]
	if tm == nil || name == "" {
		return nil, false, nil
	}
	rec, err := store.Get(ctx, e.DB, e.Reg(), a.Org, tm, name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !rs.Visible(tm, rec) {
		return nil, false, nil
	}
	if err := e.refreshClockErr(ctx, a, tm, rec, true, links); err != nil {
		return nil, false, err
	}
	rec = e.Output(tm, rec)
	pm := e.permModel(tm.Name)
	if pm == nil {
		return nil, false, nil
	}
	if ok, err := e.recordAllowedErr(ctx, e.DB, a, tm, e.decide(a, pm, "read"), rec, links); err != nil || !ok {
		return nil, false, err
	}
	return rec, true, nil
}

func matchText(m *meta.Model, r store.Record, q string, hidden map[string]bool) bool {
	if strings.Contains(strings.ToLower(str(r["name"])), q) {
		return true
	}
	for _, f := range m.Fields {
		if hidden[f.Name] {
			continue
		}
		switch f.Type {
		case "data", "text", "phone", "npwp", "select", "link":
			if strings.Contains(strings.ToLower(str(r[f.Name])), q) {
				return true
			}
		}
	}
	return false
}

// compareValues orders numbers numerically (money decimals exactly), everything else as text (missing
// values first).
func compareValues(a, b any) int {
	_, ad := a.(money.Decimal)
	_, bd := b.(money.Decimal)
	if ad || bd {
		ra, aok := exact(a)
		rb, bok := exact(b)
		if aok && bok {
			return ra.Cmp(rb)
		}
	}
	fa, aNum := numeric(a)
	fb, bNum := numeric(b)
	if aNum && bNum {
		switch {
		case fa < fb:
			return -1
		case fa > fb:
			return 1
		}
		return 0
	}
	return strings.Compare(textOf(a), textOf(b))
}

func numeric(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	}
	return 0, false
}

func textOf(v any) string {
	if v == nil {
		return ""
	}
	if b, ok := v.(bool); ok {
		if b {
			return "1"
		}
		return "0"
	}
	return fmt.Sprint(v)
}

// Title returns the display title of a record (title_field, following links), like the prototype title(). A title
// field that is a link is followed (up to 3 hops) only into a record the actor could read (readableLink); otherwise
// the stored code is the title, so a record outside the actor's row scope never lends its title.
func (e *Engine) Title(ctx context.Context, a Actor, model string, rec store.Record, depth int) string {
	return e.title(ctx, a, e.RowScope(a), model, rec, depth, nil)
}

func (e *Engine) title(ctx context.Context, a Actor, rs *RowScope, model string, rec store.Record, depth int, links map[string]map[string]any) string {
	m := e.Reg().Models[model]
	if m == nil || rec == nil {
		return ""
	}
	tf := m.TitleField
	if tf == "" || e.FieldHidden(a, m, m.Field(tf)) { // a hidden title field: the code is the title (contract 0027)
		return str(rec["name"])
	}
	if f := m.Field(tf); f != nil && f.Type == "link" && depth < 3 {
		code := str(rec[tf])
		if code == "" {
			return ""
		}
		l, ok := e.readableLink(ctx, a, rs, f.To, code, links)
		if !ok {
			return code
		}
		return e.title(ctx, a, rs, f.To, l, depth+1, links)
	}
	return str(rec[tf])
}
