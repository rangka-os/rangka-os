package engine

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/money"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

func init() { store.FieldBackfill = backfillFields }

// backfillFields is store.FieldBackfill. It works per document: for every document model that has new fields
// itself or in one of its child tables, it loads each org's records with their child rows and, per record,
//   - fills the new default fields of rows whose column is NULL,
//   - computes the new compute fields of the child rows, then of the document (so a document total over a new
//     child compute sees the child values), each in dependency order, leaving every stored value as it is,
//   - on a submitted or cancelled document leaves empty every new compute that reads another record (a link
//     member, parent., status), or reads a new field left empty: the other record may have changed since.
//
// Values that would break a unique field are reported together, record by record, and nothing is written.
// Only the new fields are written (store.WriteColumns).
func backfillFields(ctx context.Context, q pg.Querier, reg *meta.Registry, added map[string][]*meta.Field) error {
	docs := map[string]bool{}
	for name := range added {
		m := reg.Models[name]
		if m == nil {
			continue
		}
		if !m.IsChild {
			docs[name] = true
			continue
		}
		for _, pm := range reg.InstalledModels() {
			for _, f := range pm.Fields {
				if f.Type == "table" && f.Of == m.Name && !pm.IsChild {
					docs[pm.Name] = true
				}
			}
		}
	}
	nulls := map[string]map[string]map[string]bool{} // model → org|name → fields
	for name, fields := range added {
		n, err := nullRows(ctx, q, reg.Models[name], fields)
		if err != nil {
			return err
		}
		nulls[name] = n
	}
	type write struct {
		m         *meta.Model
		org, name string
		rec       store.Record
		set       []*meta.Field
	}
	var writes []write
	seen := map[string]map[string][]string{} // model.field → org|value → record names
	note := func(m *meta.Model, org string, rec store.Record, set []*meta.Field) {
		writes = append(writes, write{m, org, str(rec["name"]), rec, set})
		for _, f := range set {
			if !f.Unique {
				continue
			}
			v := rec[f.Name]
			if v == nil || v == "" {
				continue
			}
			k := m.Name + "." + f.Name
			if seen[k] == nil {
				seen[k] = map[string][]string{}
			}
			seen[k][org+"|"+fmt.Sprint(v)] = append(seen[k][org+"|"+fmt.Sprint(v)], str(rec["name"]))
		}
	}
	e := New(nil, reg)
	names := make([]string, 0, len(docs))
	for n := range docs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, dn := range names {
		dm := reg.Models[dn]
		res, err := q.Query(ctx, fmt.Sprintf("select distinct org_id from %s order by 1", pg.QuoteIdent(store.TableName(dm.Name))))
		if err != nil {
			return err
		}
		for _, row := range res.Rows {
			org, _ := row[0].(string)
			a := Actor{Org: org, ID: "system"}
			recs, err := store.All(ctx, q, reg, org, dm, true)
			if err != nil {
				return err
			}
			for _, rec := range recs {
				frozen := dm.IsSubmittable && toInt(rec["docstatus"]) != 0
				s := e.newScope(ctx, q, a, dm, rec, nil)
				if fields := added[dm.Name]; len(fields) > 0 && !frozen {
					// first pass, so a child compute over parent.<new field> sees it; the second pass below
					// sees the child values
					s.backfillRow(fields, nulls[dm.Name][org+"|"+str(rec["name"])], nil)
				}
				var childEmpty []string // new child computes left empty (frozen), which the document may read
				for _, tf := range dm.Fields {
					cm := reg.Models[tf.Of]
					if tf.Type != "table" || cm == nil || len(added[cm.Name]) == 0 {
						continue
					}
					empty := emptyComputes(cm, added[cm.Name], frozen, nil)
					childEmpty = append(childEmpty, keysOf(empty)...)
					for _, r := range rows(rec[tf.Name]) {
						cs := e.newScope(ctx, q, a, cm, r, s.links)
						cs.parent = s
						set := cs.backfillRow(added[cm.Name], nulls[cm.Name][org+"|"+str(r["name"])], empty)
						note(cm, org, r, set)
					}
				}
				if fields := added[dm.Name]; len(fields) > 0 {
					empty := emptyComputes(dm, fields, frozen, childEmpty)
					set := s.backfillRow(fields, nulls[dm.Name][org+"|"+str(rec["name"])], empty)
					note(dm, org, rec, set)
				}
			}
		}
	}
	var clashes []string
	for k, byVal := range seen {
		for kv, recs := range byVal {
			if len(recs) > 1 {
				sort.Strings(recs)
				org, v, _ := strings.Cut(kv, "|")
				clashes = append(clashes, fmt.Sprintf("%s = %q in org %s: %s", k, v, org, strings.Join(recs, ", ")))
			}
		}
	}
	if len(clashes) > 0 {
		sort.Strings(clashes)
		return fmt.Errorf("the new unique field would hold the same value twice; fix these records with the old metadata, then migrate again: %s", strings.Join(clashes, "; "))
	}
	for _, w := range writes {
		if err := store.WriteColumns(ctx, q, w.org, w.m, w.name, w.set, w.rec); err != nil {
			return err
		}
	}
	return nil
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

var wordRe = regexp.MustCompile(`[a-z_][a-z0-9_]*`)

// emptyComputes are the new compute fields of m left empty on a frozen (submitted or cancelled) document: those
// that read another record, and, transitively, those that read a new field left empty (also one of a child
// table, given as also). Nil when the document is not frozen.
func emptyComputes(m *meta.Model, fields []*meta.Field, frozen bool, also []string) map[string]bool {
	if !frozen {
		return nil
	}
	empty := map[string]bool{}
	for _, n := range also {
		empty[n] = true
	}
	isNew := map[string]bool{}
	for _, f := range fields {
		isNew[f.Name] = true
	}
	for _, f := range computeOrder(m) {
		if !isNew[f.Name] {
			continue
		}
		if readsRecord(m, f.Compute) {
			empty[f.Name] = true
			continue
		}
		for _, w := range wordRe.FindAllString(f.Compute, -1) {
			if empty[w] {
				empty[f.Name] = true
				break
			}
		}
	}
	for _, n := range also {
		if !isNew[n] {
			delete(empty, n)
		}
	}
	return empty
}

// backfillRow fills one record or child row: defaults of the NULL default fields, then the new computes not in
// empty, in dependency order; it returns the fields to write.
func (s *scope) backfillRow(fields []*meta.Field, null map[string]bool, empty map[string]bool) []*meta.Field {
	var set []*meta.Field
	for _, f := range fields {
		if f.Compute != "" || f.MoneyOf() != "" || !null[f.Name] {
			continue
		}
		set = append(set, f)
		v, err := s.eval(f.Default)
		if err != nil || v == nil {
			continue
		}
		switch v.(type) {
		case map[string]any, []any, cel.Duration:
			continue
		}
		if c, err := store.Coerce(f, v); err == nil {
			s.rec[f.Name] = c
		}
	}
	want := map[string]bool{}
	for _, f := range fields {
		if f.Compute != "" && !empty[f.Name] {
			want[f.Name] = true
		}
	}
	for _, f := range computeOrder(s.m) {
		if !want[f.Name] {
			continue
		}
		set = append(set, f)
		v, err := s.eval(f.Compute)
		if err != nil || v == nil {
			s.rec[f.Name] = store.Zero(f)
			continue
		}
		c, err := store.Coerce(f, v)
		if err != nil {
			c = store.Zero(f)
		}
		if d, ok := c.(money.Decimal); ok && money.IsIDR(s.currencyOf(f)) {
			c = d.Round(0)
		}
		s.rec[f.Name] = c
	}
	s.settleOwn()
	// a twin goes with its money field
	in := map[string]bool{}
	for _, f := range set {
		in[f.Name] = true
	}
	for _, f := range fields {
		if f.MoneyOf() != "" && in[f.MoneyOf()] {
			set = append(set, f)
		}
	}
	return set
}

// nullRows lists, per org|name, the default fields whose column is NULL (all of them right after add column).
func nullRows(ctx context.Context, q pg.Querier, m *meta.Model, fields []*meta.Field) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	if m == nil {
		return out, nil
	}
	for _, f := range fields {
		if f.Compute != "" || f.Default == "" || f.MoneyOf() != "" {
			continue
		}
		res, err := q.Query(ctx, fmt.Sprintf("select org_id, name from %s where %s is null", pg.QuoteIdent(store.TableName(m.Name)), pg.QuoteIdent(f.Name)))
		if err != nil {
			return nil, err
		}
		for _, r := range res.Rows {
			k := fmt.Sprint(r[0]) + "|" + fmt.Sprint(r[1])
			if out[k] == nil {
				out[k] = map[string]bool{}
			}
			out[k][f.Name] = true
		}
	}
	return out, nil
}

// linkMemberRe matches a member read (x.y) in an expression.
var linkMemberRe = regexp.MustCompile(`\b([a-z_][a-z0-9_]*)\s*\.`)

var statusRe = regexp.MustCompile(`\bstatus\b`)

// readsRecord reports an expression that reads another record: a link field's member or the parent row/document,
// or the derived status (it depends on today).
func readsRecord(m *meta.Model, expr string) bool {
	for _, x := range linkMemberRe.FindAllStringSubmatch(expr, -1) {
		if x[1] == "parent" {
			return true
		}
		if f := m.Field(x[1]); f != nil && (f.Type == "link" || f.Type == "dynamic_link") {
			return true
		}
	}
	return m.Field("status") == nil && statusRe.MatchString(expr)
}
