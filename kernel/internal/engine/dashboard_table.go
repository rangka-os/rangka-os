package engine

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Table and heatmap widgets (contract 0075). Both group the same way a chart does (groupKey) and read the same rows,
// so row scope, field hiding and the period window apply unchanged.

// groupRows groups rows by field g and returns the groups in display order: select options in their declared order,
// dates ascending, numbers ascending, anything else by label.
func (dc *dashCtx) groupRows(g *meta.Field, grain string, rows []store.Record) []*group {
	by := map[string]*group{}
	var order []*group
	for _, r := range rows {
		key, label, color, ok := dc.groupKey(g, r[g.Name], grain)
		if !ok {
			continue
		}
		gr := by[key]
		if gr == nil {
			gr = &group{key: key, label: label, color: color}
			by[key] = gr
			order = append(order, gr)
		}
		gr.rows = append(gr.rows, r)
	}
	sortGroups(g, order)
	return order
}

func sortGroups(g *meta.Field, gs []*group) {
	switch g.Type {
	case "select":
		idx := map[string]int{}
		for i, o := range g.Options {
			idx[o.Value] = i
		}
		pos := func(k string) int {
			if i, ok := idx[k]; ok {
				return i
			}
			return -1
		}
		sort.SliceStable(gs, func(i, j int) bool { return pos(gs[i].key) < pos(gs[j].key) })
	case "date":
		sort.SliceStable(gs, func(i, j int) bool { return gs[i].key < gs[j].key })
	case "int":
		sort.SliceStable(gs, func(i, j int) bool { return keyNum(gs[i].key) < keyNum(gs[j].key) })
	case "link":
		sort.SliceStable(gs, func(i, j int) bool { return gs[i].label < gs[j].label })
	}
}

func keyNum(k string) float64 {
	f, err := strconv.ParseFloat(k, 64)
	if err != nil {
		return math.MaxFloat64
	}
	return f
}

// colFormat is the format a table column is shown in: its own, else currency for a sum, average, minimum or maximum
// of a currency field, else a plain number.
func colFormat(c meta.TableColumn, m *meta.Model) string {
	if c.Format != "" {
		return c.Format
	}
	if c.Fn != "" && c.Fn != "count" {
		if f := m.Field(c.Field); f != nil && f.Type == "currency" {
			return "currency"
		}
	}
	return "number"
}

// tableValues computes every column of one row. An expr column sees the earlier columns by name; a division by zero
// (or any failure) leaves its cell empty (nil).
func tableValues(cols []meta.TableColumn, rows []store.Record) map[string]any {
	vals := map[string]any{}
	nums := map[string]any{}
	for _, c := range cols {
		if c.Fn != "" {
			v := num(aggregate(rows, c.Fn, c.Field))
			vals[c.Name], nums[c.Name] = v, v
			continue
		}
		v, err := cel.Eval(c.Expr, &cel.Env{Vars: func(n string) (any, bool) { x, ok := nums[n]; return x, ok }})
		f, ok := 0.0, false
		if err == nil {
			f, ok = cel.ToFloat(cel.Unwrap(v))
		}
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			vals[c.Name] = nil
			continue
		}
		vals[c.Name], nums[c.Name] = f, f
	}
	return vals
}

// tableDelta is the change of one cell against the previous window, in the shape of a number widget's delta/trend.
func tableDelta(v, prev any) (trend string, delta any, ok bool) {
	if v == nil || prev == nil {
		return "", nil, false
	}
	vf, pf := num(v), num(prev)
	switch {
	case vf > pf:
		trend = "up"
	case vf < pf:
		trend = "down"
	default:
		trend = "flat"
	}
	switch {
	case pf != 0:
		pct := int64(cel.JSRound(abs(vf-pf) / pf * 100))
		if pct == 0 {
			trend = "flat" // a change that rounds to 0% is no change, never a red or green arrow
		}
		delta = formatInt(pct) + "%"
	case vf != 0:
		delta = "baru"
	}
	return trend, delta, true
}

func (dc *dashCtx) table(w *meta.Widget, m *meta.Model, rows, prevRows []store.Record) (map[string]any, error) {
	g := m.Field(w.GroupBy)
	if g == nil {
		g = &meta.Field{Name: w.GroupBy, Type: "data"}
	}
	grain := w.Grain
	if grain == "" {
		grain = "day"
	}
	cur := dc.groupRows(g, grain, rows)
	compare := w.Compare == "previous_period" && w.Period != ""
	prevBy := map[string]*group{}
	if compare {
		for _, gr := range dc.groupRows(g, grain, prevRows) {
			prevBy[gr.key] = gr
			found := false
			for _, c := range cur {
				if c.key == gr.key {
					found = true
					break
				}
			}
			if !found { // active before, silent now: still a row, with zeros
				cur = append(cur, &group{key: gr.key, label: gr.label, color: gr.color})
			}
		}
	}
	type outRow struct {
		key, label string
		count      int
		vals, prev map[string]any
	}
	rowsOut := make([]*outRow, 0, len(cur))
	for _, gr := range cur {
		o := &outRow{key: gr.key, label: gr.label, count: len(gr.rows), vals: tableValues(w.TableCols, gr.rows)}
		if p := prevBy[gr.key]; p != nil {
			o.prev = tableValues(w.TableCols, p.rows)
		} else if compare {
			o.prev = tableValues(w.TableCols, nil)
		}
		rowsOut = append(rowsOut, o)
	}
	if w.Sort != "" {
		dir := 1
		if strings.HasPrefix(w.Sort, "-") {
			dir = -1
		}
		k := strings.TrimPrefix(w.Sort, "-")
		sort.SliceStable(rowsOut, func(i, j int) bool {
			if k == "label" {
				return strings.Compare(rowsOut[i].label, rowsOut[j].label)*dir < 0
			}
			return compareValues(rowsOut[i].vals[k], rowsOut[j].vals[k])*dir < 0
		})
	}
	if w.Limit > 0 && w.Limit < len(rowsOut) {
		rowsOut = rowsOut[:w.Limit]
	}
	shape := func(key, label string, count int, vals, prev map[string]any) map[string]any {
		r := map[string]any{"key": key, "label": label, "count": count, "values": vals}
		if compare {
			delta, trend := map[string]any{}, map[string]any{}
			for _, c := range w.TableCols {
				if t, d, ok := tableDelta(vals[c.Name], prev[c.Name]); ok {
					trend[c.Name], delta[c.Name] = t, d
				}
			}
			r["prev"], r["delta"], r["trend"] = prev, delta, trend
		}
		return r
	}
	out := make([]any, len(rowsOut))
	for i, o := range rowsOut {
		out[i] = shape(o.key, o.label, o.count, o.vals, o.prev)
	}
	cols := make([]any, len(w.TableCols))
	for i, c := range w.TableCols {
		cols[i] = map[string]any{"name": c.Name, "label": c.Label.Text(), "format": colFormat(c, m)}
	}
	// the totals row is computed over all rows, not summed from the rows shown, so an average or a ratio stays right
	var totalPrev map[string]any
	if compare {
		totalPrev = tableValues(w.TableCols, prevRows)
	}
	total := shape("", "", len(rows), tableValues(w.TableCols, rows), totalPrev)
	return map[string]any{"columns": cols, "rows": out, "total": total, "compare": compare}, nil
}

// heatmap is one aggregate per cell of group_by (rows) by series_by (columns). Cells without records are null, so
// "no sales" and "zero" never look the same. An int series_by with series_range has every integer of the range as a
// column (hours 0 to 23 even when the night is empty); the legend scale is max.
func (dc *dashCtx) heatmap(w *meta.Widget, m *meta.Model, rows []store.Record, money bool) (map[string]any, error) {
	rg := m.Field(w.GroupBy)
	if rg == nil {
		rg = &meta.Field{Name: w.GroupBy, Type: "data"}
	}
	cg := m.Field(w.SeriesBy)
	if cg == nil {
		cg = &meta.Field{Name: w.SeriesBy, Type: "data"}
	}
	rowGroups := dc.groupRows(rg, "day", rows)
	colGroups := dc.groupRows(cg, "day", rows)
	if cg.Type == "int" && len(w.SeriesRange) == 2 {
		have := map[string]*group{}
		for _, c := range colGroups {
			have[c.key] = c
		}
		colGroups = colGroups[:0:0]
		for i := w.SeriesRange[0]; i <= w.SeriesRange[1]; i++ {
			k := strconv.Itoa(i)
			if c := have[k]; c != nil {
				colGroups = append(colGroups, c)
			} else {
				colGroups = append(colGroups, &group{key: k, label: k})
			}
		}
	}
	if len(rowGroups) > 50 {
		rowGroups = rowGroups[:50]
	}
	if len(colGroups) > 24 {
		colGroups = colGroups[:24]
	}
	colIdx := map[string]int{}
	colsOut := make([]any, len(colGroups))
	for i, c := range colGroups {
		colIdx[c.key] = i
		colsOut[i] = map[string]any{"key": c.key, "label": c.label}
	}
	rowsOut := make([]any, len(rowGroups))
	values := make([]any, len(rowGroups))
	counts := make([]any, len(rowGroups))
	maxV := 0.0
	for i, r := range rowGroups {
		rowsOut[i] = map[string]any{"key": r.key, "label": r.label}
		cells := make([][]store.Record, len(colGroups))
		for _, rec := range r.rows {
			key, _, _, ok := dc.groupKey(cg, rec[cg.Name], "day")
			if !ok {
				continue
			}
			if j, in := colIdx[key]; in {
				cells[j] = append(cells[j], rec)
			}
		}
		vs := make([]any, len(colGroups))
		cs := make([]any, len(colGroups))
		for j, c := range cells {
			cs[j] = len(c)
			if len(c) == 0 {
				continue
			}
			v := num(aggregate(c, w.Aggregate, w.Field))
			vs[j] = v
			if v > maxV {
				maxV = v
			}
		}
		values[i], counts[i] = vs, cs
	}
	return map[string]any{"rows": rowsOut, "cols": colsOut, "values": values, "counts": counts, "max": maxV, "rupiah": money}, nil
}
