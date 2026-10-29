package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/rk"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// LoadFixtures reads every fixtures/*.rk file (model → list of records) and merges them.
func LoadFixtures(dir string) (map[string][]map[string]any, error) {
	out := map[string][]map[string]any{}
	for _, e := range rk.ListDir(dir) {
		d, err := rk.Load(e.Path)
		if err != nil {
			return nil, err
		}
		for model, v := range meta.AsMap(d.Data) {
			for _, r := range meta.AsList(v) {
				if rm := meta.AsMap(r); rm != nil {
					out[model] = append(out[model], rm)
				}
			}
		}
	}
	return out, nil
}

// seedOrder sorts models so linked models are seeded before the models that link to them.
func seedOrder(reg *meta.Registry, models []string) []string {
	sort.Strings(models)
	in := map[string]bool{}
	for _, m := range models {
		in[m] = true
	}
	var out []string
	state := map[string]int{}
	var visit func(string)
	visit = func(n string) {
		if state[n] != 0 {
			return
		}
		state[n] = 1
		if m := reg.Models[n]; m != nil {
			var deps []string
			for _, f := range m.Fields {
				switch {
				case f.Type == "link" && f.To != n && in[f.To]:
					deps = append(deps, f.To)
				case f.Type == "table":
					if cm := reg.Models[f.Of]; cm != nil {
						for _, cf := range cm.Fields {
							if cf.Type == "link" && in[cf.To] {
								deps = append(deps, cf.To)
							}
						}
					}
				}
			}
			sort.Strings(deps)
			for _, d := range deps {
				if state[d] == 0 {
					visit(d)
				}
			}
		}
		state[n] = 2
		out = append(out, n)
	}
	for _, m := range models {
		visit(m)
	}
	return out
}

// SeedStats counts what Seed did.
type SeedStats struct {
	Created map[string]int `json:"created"`
	Skipped map[string]int `json:"skipped"`
}

// Seed inserts fixture records for an org as the prototype loads them: names and docstatus as given,
// missing values from defaults, computed fields computed. Existing records are left alone.
func (e *Engine) Seed(ctx context.Context, a Actor, fixtures map[string][]map[string]any) (*SeedStats, error) {
	st := &SeedStats{Created: map[string]int{}, Skipped: map[string]int{}}
	var models []string
	for m := range fixtures {
		models = append(models, m)
	}
	err := e.run(ctx, a, func(o *op) error {
		for _, mn := range seedOrder(e.Reg(), models) {
			m := e.Reg().Model(mn)
			if m == nil {
				st.Skipped[mn] += len(fixtures[mn])
				continue
			}
			for _, in := range fixtures[mn] {
				name := str(in["name"])
				if name != "" {
					ok, err := store.Exists(o.ctx, o.tx, a.Org, mn, name)
					if err != nil {
						return err
					}
					if ok {
						st.Skipped[mn]++
						continue
					}
				}
				rec, issues := e.normalize(m, in, nil)
				if len(issues) > 0 {
					return fmt.Errorf("fixture %s %s: %s", mn, name, issues[0].Message)
				}
				o.prepare(m, rec, true)
				if name == "" {
					n, err := store.NextName(o.ctx, o.tx, a.Org, mn, e.naming(m), e.Now().In(e.Location()))
					if err != nil {
						return err
					}
					name = n
				}
				rec["name"] = name
				if m.IsSubmittable {
					rec["docstatus"] = toInt(in["docstatus"])
					rec["amended_from"] = str(in["amended_from"])
				}
				rec["created_by"] = a.ID
				if err := store.Insert(o.ctx, o.tx, e.Reg(), a.Org, m, rec); err != nil {
					return fmt.Errorf("fixture %s %s: %w", mn, name, err)
				}
				o.touched[mn] = true
				st.Created[mn]++
			}
		}
		if err := store.ResetCounters(o.ctx, o.tx, a.Org); err != nil {
			return err
		}
		return store.Audit(o.ctx, o.tx, a.Org, a.ID, "seed.loaded", "fixtures", fmt.Sprintf("%v", st.Created), st)
	})
	return st, err
}

// FixturesDir guesses <apps>/../fixtures.
func FixturesDir(appsDir string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(appsDir)), "fixtures")
}
