package engine

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
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

// List returns matching records and the total before paging. v0 filters in memory with CEL; a CEL→SQL
// translation for simple filters is the next step for large tables.
func (e *Engine) List(ctx context.Context, a Actor, model string, opts ListOpts) ([]store.Record, int, error) {
	m, err := e.model(model)
	if err != nil {
		return nil, 0, err
	}
	if m.IsChild {
		return nil, 0, badRequest(model + " rows are read through their parent document")
	}
	if opts.Filter != "" {
		if _, err := cel.Compile(opts.Filter); err != nil {
			return nil, 0, badRequest("filter: " + err.Error())
		}
	}
	all, err := store.All(ctx, e.DB, e.Reg(), a.Org, m, opts.Children || strings.Contains(opts.Filter, "."))
	if err != nil {
		return nil, 0, err
	}
	links := map[string]map[string]any{}
	var out []store.Record
	q := strings.ToLower(strings.TrimSpace(opts.Q))
	for _, r := range all {
		e.Output(m, r)
		if opts.Filter != "" {
			v, err := e.newScope(ctx, e.DB, a, m, r, links).eval(opts.Filter)
			if err != nil {
				if _, ok := err.(*cel.SyntaxError); ok {
					return nil, 0, badRequest(err.Error())
				}
				continue
			}
			if v != true {
				continue
			}
		}
		if q != "" && !matchText(m, r, q) {
			continue
		}
		out = append(out, r)
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

// compareValues orders numbers numerically, everything else as text (missing values first).
func compareValues(a, b any) int {
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
