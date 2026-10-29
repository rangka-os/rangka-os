package engine

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/cache"
	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

var months = []string{"Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"}

var toneColor = map[string]string{"ok": "var(--chart-5)", "bad": "var(--bad-solid)", "warn": "var(--chart-4)"}

// Dashboard computes every widget of a dashboard over the last period days (default_period when 0).
// The shapes follow the prototype's computeWidget(). cached reports a cache hit (dashboards with cache:).
func (e *Engine) Dashboard(ctx context.Context, a Actor, name string, period int) (out map[string]any, cached bool, err error) {
	d := e.Reg().Dashboard(name)
	if d == nil {
		return nil, false, notFound("dashboard " + name)
	}
	if period <= 0 {
		period = d.DefaultPeriod
	}
	key := "dash:" + a.Org + ":" + name + ":" + formatInt(int64(period)) + ":" + e.Today()
	ttl := cache.ParseTTL(d.Cache)
	if ttl > 0 && e.Cache != nil {
		if v, ok := e.Cache.Get(key); ok {
			return v.(map[string]any), true, nil
		}
	}
	dc := &dashCtx{e: e, ctx: ctx, a: a, rows: map[string][]store.Record{}, links: map[string]map[string]any{}, titles: map[string]titleInfo{}}
	widgets := []any{}
	for _, w := range d.Widgets {
		wo := map[string]any{}
		for k, v := range w.Raw {
			if k != "filter" && k != "if" {
				wo[k] = v
			}
		}
		res, err := dc.widget(w, period)
		if err != nil {
			return nil, false, err
		}
		for k, v := range res {
			wo[k] = v
		}
		widgets = append(widgets, wo)
	}
	out = map[string]any{"name": d.Name, "label": d.Label, "period": period, "widgets": widgets}
	if ttl > 0 && e.Cache != nil {
		e.Cache.Set(key, out, ttl)
	}
	return out, false, nil
}

type dashCtx struct {
	e      *Engine
	ctx    context.Context
	a      Actor
	rows   map[string][]store.Record
	links  map[string]map[string]any
	titles map[string]titleInfo
}

func (dc *dashCtx) all(m *meta.Model) ([]store.Record, error) {
	if r, ok := dc.rows[m.Name]; ok {
		return r, nil
	}
	r, err := store.All(dc.ctx, dc.e.DB, dc.e.Reg(), dc.a.Org, m, false)
	if err != nil {
		return nil, err
	}
	for _, rec := range r {
		dc.e.Output(m, rec)
	}
	dc.rows[m.Name] = r
	return r, nil
}

func (dc *dashCtx) widgetRows(w *meta.Widget, m *meta.Model, from, to string) ([]store.Record, error) {
	all, err := dc.all(m)
	if err != nil {
		return nil, err
	}
	var out []store.Record
	for _, r := range all {
		if w.Filter != "" {
			v, err := dc.e.newScope(dc.ctx, dc.e.DB, dc.a, m, r, dc.links).eval(w.Filter)
			if err != nil || !cel.Truthy(v) {
				continue
			}
		}
		if w.Period != "" && from != "" {
			d := str(r[w.Period])
			if !cel.IsDate(d) || d < from || (to != "" && d >= to) {
				continue
			}
		}
		out = append(out, r)
	}
	return out, nil
}

func aggregate(rows []store.Record, kind, field string) any {
	if kind == "count" || kind == "" {
		return int64(len(rows))
	}
	if len(rows) == 0 {
		return float64(0)
	}
	xs := make([]float64, len(rows))
	for i, r := range rows {
		xs[i] = num(r[field])
	}
	switch kind {
	case "sum", "avg":
		s := 0.0
		for _, x := range xs {
			s += x
		}
		if kind == "avg" {
			return s / float64(len(xs))
		}
		return s
	case "min":
		m := xs[0]
		for _, x := range xs {
			if x < m {
				m = x
			}
		}
		return m
	}
	m := xs[0]
	for _, x := range xs {
		if x > m {
			m = x
		}
	}
	return m
}

func (dc *dashCtx) widget(w *meta.Widget, days int) (map[string]any, error) {
	m := dc.e.Reg().Model(w.Model)
	if m == nil {
		return map[string]any{"error": "Model " + w.Model + " tidak terpasang"}, nil
	}
	if w.If != "" {
		v, err := cel.Eval(w.If, &cel.Env{Today: dc.e.Today, Apps: dc.e.Reg().IsInstalled, Vars: func(n string) (any, bool) {
			if n == "user" {
				return UserVar(dc.a), true
			}
			return nil, false
		}})
		if err != nil || !cel.Truthy(v) {
			return map[string]any{"hidden": true}, nil
		}
	}
	today := dc.e.Today()
	from := ""
	if w.Period != "" {
		from = cel.AddDays(today, -days)
	}
	rows, err := dc.widgetRows(w, m, from, "")
	if err != nil {
		return nil, err
	}
	var f *meta.Field
	if w.Field != "" {
		f = m.Field(w.Field)
	}
	money := f != nil && f.Type == "currency"
	switch w.Type {
	case "number":
		v := aggregate(rows, w.Aggregate, w.Field)
		out := map[string]any{"value": v, "rupiah": money, "count": len(rows)}
		if w.Period != "" {
			prevRows, err := dc.widgetRows(w, m, cel.AddDays(today, -2*days), from)
			if err != nil {
				return nil, err
			}
			prev := aggregate(prevRows, w.Aggregate, w.Field)
			vf, pf := num(v), num(prev)
			out["prev"] = prev
			switch {
			case vf > pf:
				out["trend"] = "up"
			case vf < pf:
				out["trend"] = "down"
			default:
				out["trend"] = "flat"
			}
			switch {
			case pf != 0:
				out["delta"] = formatInt(int64(cel.JSRound(abs(vf-pf)/pf*100))) + "%"
			case vf != 0:
				out["delta"] = "baru"
			default:
				out["delta"] = nil
			}
		}
		return out, nil
	case "chart":
		return dc.chart(w, m, rows, money)
	case "list":
		list := append([]store.Record{}, rows...)
		if w.Sort != "" {
			dir := 1
			if strings.HasPrefix(w.Sort, "-") {
				dir = -1
			}
			k := strings.Replace(w.Sort, "-", "", 1)
			sort.SliceStable(list, func(i, j int) bool { return compareValues(list[i][k], list[j][k])*dir < 0 })
		}
		limit := w.Limit
		if limit == 0 {
			limit = 10
		}
		if limit < len(list) {
			list = list[:limit]
		}
		outRows := make([]any, len(list))
		for i, r := range list {
			outRows[i] = r
		}
		return map[string]any{"rows": outRows, "total": len(rows)}, nil
	}
	return map[string]any{"error": "Jenis widget tidak dikenal: " + w.Type}, nil
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func bucket(iso, grain string) (string, string, bool) {
	if !cel.IsDate(iso) {
		return "", "", false
	}
	d, _ := time.Parse("2006-01-02", iso)
	switch grain {
	case "month":
		return iso[:7], months[d.Month()-1] + " " + d.Format("2006"), true
	case "week":
		wd := (int(d.Weekday()) + 6) % 7
		d = d.AddDate(0, 0, -wd)
		return d.Format("2006-01-02"), formatInt(int64(d.Day())) + " " + months[d.Month()-1], true
	}
	return iso, formatInt(int64(d.Day())) + " " + months[d.Month()-1], true
}

type group struct {
	key, label, color string
	rows              []store.Record
}

type titleInfo struct {
	title  string
	exists bool
}

// title returns the display title of a linked record and whether it exists.
func (dc *dashCtx) title(model, name string) (string, bool) {
	if name == "" {
		return "", false
	}
	k := model + "/" + name
	if t, ok := dc.titles[k]; ok {
		return t.title, t.exists
	}
	var t titleInfo
	if tm := dc.e.Reg().Models[model]; tm != nil {
		if rec, err := store.Get(dc.ctx, dc.e.DB, dc.e.Reg(), dc.a.Org, tm, name); err == nil {
			t = titleInfo{dc.e.Title(dc.ctx, dc.a, model, rec, 0), true}
		}
	}
	dc.titles[k] = t
	return t.title, t.exists
}

func (dc *dashCtx) chart(w *meta.Widget, m *meta.Model, rows []store.Record, money bool) (map[string]any, error) {
	g := m.Field(w.GroupBy)
	if g == nil {
		g = &meta.Field{Name: w.GroupBy, Type: "data"}
	}
	groups := map[string]*group{}
	var order []string
	grain := w.Grain
	if grain == "" {
		grain = "day"
	}
	for _, r := range rows {
		raw := r[w.GroupBy]
		rs := str(raw)
		var key, label, color string
		switch g.Type {
		case "date":
			k, l, ok := bucket(rs, grain)
			if !ok {
				continue
			}
			key, label = k, l
		case "select":
			o := g.Option(rs)
			key = or(rs, "—").(string)
			switch {
			case o != nil:
				label = o.Label.Text()
				color = toneColor[o.Tone]
			case rs != "":
				label = rs
			default:
				label = "Tanpa nilai"
			}
		case "link":
			key = or(rs, "—").(string)
			if t, ok := dc.title(g.To, rs); ok {
				label = t
			} else {
				label = "Tanpa " + strings.ToLower(g.Label.Text())
			}
		case "check":
			if b, _ := raw.(bool); b {
				key, label = "1", "Ya"
			} else {
				key, label = "0", "Tidak"
			}
		default:
			key = or(rs, "—").(string)
			label = or(rs, "Tanpa nilai").(string)
		}
		gr := groups[key]
		if gr == nil {
			gr = &group{key: key, label: label, color: color}
			groups[key] = gr
			order = append(order, key)
		}
		gr.rows = append(gr.rows, r)
	}
	type point struct {
		Key   string `json:"key"`
		Label string `json:"label"`
		Color string `json:"color,omitempty"`
		Value any    `json:"value"`
		Count int    `json:"count"`
	}
	data := make([]point, 0, len(order))
	for _, k := range order {
		gr := groups[k]
		data = append(data, point{gr.key, gr.label, gr.color, aggregate(gr.rows, w.Aggregate, w.Field), len(gr.rows)})
	}
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
		sort.SliceStable(data, func(i, j int) bool { return pos(data[i].Key) < pos(data[j].Key) })
	case "date":
		sort.SliceStable(data, func(i, j int) bool { return data[i].Key < data[j].Key })
	}
	if w.Sort != "" {
		dir := 1
		if strings.HasPrefix(w.Sort, "-") {
			dir = -1
		}
		k := strings.Replace(w.Sort, "-", "", 1)
		sort.SliceStable(data, func(i, j int) bool {
			if k == "label" {
				return strings.Compare(data[i].Label, data[j].Label)*dir < 0
			}
			return compareValues(data[i].Value, data[j].Value)*dir < 0
		})
	}
	if w.Limit > 0 && w.Limit < len(data) {
		data = data[:w.Limit]
	}
	out := make([]any, len(data))
	for i, p := range data {
		pm := map[string]any{"key": p.Key, "label": p.Label, "value": p.Value, "count": p.Count}
		if p.Color != "" {
			pm["color"] = p.Color
		}
		out[i] = pm
	}
	return map[string]any{"data": out, "rupiah": money}, nil
}
