package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// maxAggGroups is how many groups Aggregate returns; the rest are summed into Other (contract 0018 §3).
const maxAggGroups = 50

// emptyGroupLabel labels the group of records whose group field is empty.
const emptyGroupLabel = "(kosong)"

// AggFn is one aggregate of GET /aggregate/{model}: Kind is count|sum|avg|min|max, Field is empty for count.
type AggFn struct{ Kind, Field string }

// key is the name of the fn's value in AggResult.Values: "count", or "<kind>:<field>".
func (f AggFn) key() string {
	if f.Kind == "count" {
		return "count"
	}
	return f.Kind + ":" + f.Field
}

// AggGroup is the aggregate of the records sharing one value of the group field.
type AggGroup struct {
	Key    any            `json:"key"`
	Label  string         `json:"label"`
	Count  int64          `json:"count"`
	Values map[string]any `json:"values"`
}

// AggResult is the answer of Aggregate (contract 0018 §2). Other is nil when every group fits.
type AggResult struct {
	Count  int64          `json:"count"`
	Values map[string]any `json:"values"`
	Groups []AggGroup     `json:"groups"`
	Other  *AggGroup      `json:"other,omitempty"`
}

// ParseAggFns parses the fn parameter, for example "count,sum:grand_total".
func ParseAggFns(s string) ([]AggFn, error) {
	var out []AggFn
	for _, part := range strings.Split(s, ",") {
		kind, field, hasField := strings.Cut(strings.TrimSpace(part), ":")
		switch {
		case kind == "count" && !hasField:
		case kind == "sum" || kind == "avg" || kind == "min" || kind == "max":
			if field == "" {
				return nil, badRequest("fn: " + kind + " needs a field (" + kind + ":<field>)")
			}
		default:
			return nil, badRequest(fmt.Sprintf("fn: %q is not count, sum:<field>, avg:<field>, min:<field> or max:<field>", part))
		}
		out = append(out, AggFn{Kind: kind, Field: field})
	}
	return out, nil
}

// Aggregate counts and sums the rows List matches for opts (same read grant, row scope, filter and q; no paging),
// overall and per value of the group field when group is set (contract 0018).
func (e *Engine) Aggregate(ctx context.Context, a Actor, model string, opts ListOpts, group string, fns []AggFn) (AggResult, error) {
	m, err := e.model(model)
	if err != nil {
		return AggResult{}, err
	}
	if err := checkAggregate(m, group, fns); err != nil {
		return AggResult{}, err
	}
	rows, err := e.matching(ctx, a, m, opts)
	if err != nil {
		return AggResult{}, err
	}
	res := AggResult{Count: int64(len(rows)), Values: aggValues(rows, fns), Groups: []AggGroup{}}
	if group == "" {
		return res, nil
	}
	type bucket struct {
		key  any
		rows []store.Record
	}
	byKey := map[string]*bucket{}
	var buckets []*bucket
	for _, r := range rows {
		v := r[group]
		if v == "" {
			v = nil
		}
		k := fmt.Sprintf("%T:%v", v, v)
		b := byKey[k]
		if b == nil {
			b = &bucket{key: v}
			byKey[k] = b
			buckets = append(buckets, b)
		}
		b.rows = append(b.rows, r)
	}
	sort.SliceStable(buckets, func(i, j int) bool {
		bi, bj := buckets[i], buckets[j]
		if len(bi.rows) != len(bj.rows) {
			return len(bi.rows) > len(bj.rows)
		}
		if (bi.key == nil) != (bj.key == nil) {
			return bj.key == nil
		}
		return compareValues(bi.key, bj.key) < 0
	})
	f := m.Field(group)
	titles := map[string]string{}
	links := map[string]map[string]any{}
	rs := e.RowScope(a)
	var rest []store.Record
	for i, b := range buckets {
		if i >= maxAggGroups {
			rest = append(rest, b.rows...)
			continue
		}
		label := emptyGroupLabel
		if b.key != nil {
			label = fmt.Sprint(b.key)
			if f != nil && f.Type == "link" {
				if t := e.linkTitle(ctx, a, rs, f.To, str(b.key), titles, links); t != "" {
					label = t
				}
			}
		}
		res.Groups = append(res.Groups, AggGroup{Key: b.key, Label: label, Count: int64(len(b.rows)), Values: aggValues(b.rows, fns)})
	}
	if len(rest) > 0 {
		res.Other = &AggGroup{Count: int64(len(rest)), Values: aggValues(rest, fns)}
	}
	return res, nil
}

// checkAggregate refuses a group that is neither a field nor a system column of m (or is a table), and a
// sum|avg|min|max over a field that is not a number (contract 0018 §4).
func checkAggregate(m *meta.Model, group string, fns []AggFn) error {
	if group != "" {
		f := m.Field(group)
		if f == nil && !meta.SystemFields[group] {
			return badRequest("group: " + group + " is not a field of " + m.Name)
		}
		if f != nil && f.Type == "table" {
			return badRequest("group: " + group + " is a table and cannot be grouped")
		}
	}
	for _, fn := range fns {
		if fn.Kind == "count" {
			continue
		}
		f := m.Field(fn.Field)
		if f == nil {
			return badRequest("fn: " + fn.Field + " is not a field of " + m.Name)
		}
		switch f.Type {
		case "currency", "int", "float", "percent":
		default:
			return badRequest("fn: " + fn.key() + " needs a number field; " + fn.Field + " is " + f.Type)
		}
	}
	return nil
}

// aggValues computes every fn over rows with the dashboard engine's aggregate.
func aggValues(rows []store.Record, fns []AggFn) map[string]any {
	out := make(map[string]any, len(fns))
	for _, fn := range fns {
		out[fn.key()] = aggregate(rows, fn.Kind, fn.Field)
	}
	return out
}
