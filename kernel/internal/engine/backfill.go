package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

func init() { store.FieldBackfill = backfillFields }

// backfillFields is store.FieldBackfill: for every org, it loads the records that hold m (m itself, or the
// documents whose table fields are m) with their child rows, fills the new default fields of rows whose column
// is NULL, computes every compute as a save would (submitted documents included: their stored values are the
// ones a save of that time would have written), and writes only the given fields. Values that would break a
// unique field are reported together, record by record, and nothing is written.
func backfillFields(ctx context.Context, q pg.Querier, reg *meta.Registry, m *meta.Model, fields []*meta.Field) error {
	var docs []*meta.Model
	if m.IsChild {
		for _, pm := range reg.InstalledModels() {
			for _, f := range pm.Fields {
				if f.Type == "table" && f.Of == m.Name {
					docs = append(docs, pm)
					break
				}
			}
		}
	} else {
		docs = []*meta.Model{m}
	}
	nulls, err := nullRows(ctx, q, m, fields)
	if err != nil {
		return err
	}
	e := New(nil, reg)
	res, err := q.Query(ctx, fmt.Sprintf("select distinct org_id from %s order by 1", pg.QuoteIdent(store.TableName(m.Name))))
	if err != nil {
		return err
	}
	type write struct {
		org, name string
		rec       store.Record
	}
	var writes []write
	seen := map[string]map[string][]string{} // unique field → org|value → record names
	for _, row := range res.Rows {
		org, _ := row[0].(string)
		a := Actor{Org: org, ID: "system"}
		for _, dm := range docs {
			recs, err := store.All(ctx, q, reg, org, dm, true)
			if err != nil {
				return err
			}
			for _, rec := range recs {
				s := e.newScope(ctx, q, a, dm, rec, nil)
				var targets []store.Record
				if m.IsChild {
					for _, f := range dm.Fields {
						if f.Type == "table" && f.Of == m.Name {
							for _, r := range rows(rec[f.Name]) {
								targets = append(targets, r)
							}
						}
					}
				} else {
					targets = []store.Record{rec}
				}
				for _, t := range targets {
					ts := s
					if m.IsChild {
						ts = e.newScope(ctx, q, a, m, t, s.links)
						ts.parent = s
					}
					ts.backfillDefaults(fields, nulls[org+"|"+str(t["name"])])
				}
				s.compute()
				for _, t := range targets {
					writes = append(writes, write{org, str(t["name"]), t})
					for _, f := range fields {
						if !f.Unique {
							continue
						}
						v := t[f.Name]
						if v == nil || v == "" {
							continue
						}
						if seen[f.Name] == nil {
							seen[f.Name] = map[string][]string{}
						}
						k := org + "|" + fmt.Sprint(v)
						seen[f.Name][k] = append(seen[f.Name][k], str(t["name"]))
					}
				}
			}
		}
	}
	var clashes []string
	for _, f := range fields {
		for k, names := range seen[f.Name] {
			if len(names) > 1 {
				sort.Strings(names)
				org, v, _ := strings.Cut(k, "|")
				clashes = append(clashes, fmt.Sprintf("%s.%s = %q in org %s: %s", m.Name, f.Name, v, org, strings.Join(names, ", ")))
			}
		}
	}
	if len(clashes) > 0 {
		sort.Strings(clashes)
		return fmt.Errorf("the new unique field would hold the same value twice; fix these records with the old metadata, then migrate again: %s", strings.Join(clashes, "; "))
	}
	for _, w := range writes {
		var set []*meta.Field
		for _, f := range fields {
			if f.Compute != "" || f.MoneyOf() != "" || nulls[w.org+"|"+w.name][f.Name] {
				set = append(set, f)
			}
		}
		if err := store.WriteColumns(ctx, q, w.org, m, w.name, set, w.rec); err != nil {
			return err
		}
	}
	return nil
}

// nullRows lists, per org|name, the default fields whose column is NULL (all of them right after add column,
// unless the step itself gave the column a default).
func nullRows(ctx context.Context, q pg.Querier, m *meta.Model, fields []*meta.Field) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
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

// backfillDefaults sets the default of each listed field that is NULL in the row (applyDefaults for an old row).
func (s *scope) backfillDefaults(fields []*meta.Field, null map[string]bool) {
	for _, f := range fields {
		if !null[f.Name] {
			continue
		}
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
}
