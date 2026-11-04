package engine

import (
	"context"
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
	if opts.Sort != "" {
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
			v, err := e.newScope(ctx, e.DB, a, m, r, links).eval(opts.Filter)
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
		if q != "" && !matchText(m, r, q) && !e.matchLinkTitle(ctx, a, m, r, q, titles, links) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// matchLinkTitle reports whether q occurs in the display title of any record a link field points at, so lists are
// searchable by what users see (a customer's name) and not only by the stored key. titles caches model/name → title.
// A target the actor may not read (model-level or record-level) never matches, so q cannot reveal its title.
func (e *Engine) matchLinkTitle(ctx context.Context, a Actor, m *meta.Model, r store.Record, q string, titles map[string]string, links map[string]map[string]any) bool {
	for _, f := range m.Fields {
		if f.Type != "link" || str(r[f.Name]) == "" {
			continue
		}
		if t := strings.ToLower(e.linkTitle(ctx, a, f.To, str(r[f.Name]), titles, links)); t != "" && strings.Contains(t, q) {
			return true
		}
	}
	return false
}

// linkTitle returns the display title of record name of model to, or "" when it is missing or the actor may not read
// it (model-level or record-level). titles caches model/name → title.
func (e *Engine) linkTitle(ctx context.Context, a Actor, to, name string, titles map[string]string, links map[string]map[string]any) string {
	key := to + "/" + name
	t, ok := titles[key]
	if !ok {
		if tm := e.Reg().Models[to]; tm != nil {
			if l, err := store.Get(ctx, e.DB, e.Reg(), a.Org, tm, name); err == nil && e.recordAllowed(ctx, e.DB, a, tm, e.decide(a, e.permModel(tm.Name), "read"), l, links) {
				t = e.Title(ctx, a, to, l, 0)
			}
		}
		titles[key] = t
	}
	return t
}

func matchText(m *meta.Model, r store.Record, q string) bool {
	if strings.Contains(strings.ToLower(str(r["name"])), q) {
		return true
	}
	for _, f := range m.Fields {
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

// Title returns the display title of a record (title_field, following links), like the prototype title().
func (e *Engine) Title(ctx context.Context, a Actor, model string, rec store.Record, depth int) string {
	m := e.Reg().Models[model]
	if m == nil || rec == nil {
		return ""
	}
	tf := m.TitleField
	if tf == "" {
		return str(rec["name"])
	}
	if f := m.Field(tf); f != nil && f.Type == "link" && depth < 3 {
		tm := e.Reg().Models[f.To]
		if tm == nil || str(rec[tf]) == "" {
			return ""
		}
		l, err := store.Get(ctx, e.DB, e.Reg(), a.Org, tm, str(rec[tf]))
		if err != nil {
			return ""
		}
		return e.Title(ctx, a, f.To, l, depth+1)
	}
	return str(rec[tf])
}
