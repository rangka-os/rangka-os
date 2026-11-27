package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Module defaults (contract 0076): a module may ship master data every business needs from day one (units, payment
// methods) in <module>/defaults/*.rk, in the fixtures format (`model: [records]`). Without them a freshly built
// business has empty pick lists and its first item cannot be saved (24 Nov 2026: no unit existed).

// ModuleDefaults loads the defaults of every installed unit. A unit fills only its own models, so one module can
// never write into another's tables; records for any other model are left out and named in skipped
// ("<unit>/<model>"). A file that does not load is an error.
func ModuleDefaults(reg *meta.Registry) (fx map[string][]map[string]any, skipped []string, err error) {
	fx = map[string][]map[string]any{}
	for _, a := range reg.Apps {
		if a.Kind == meta.KindSite || !reg.IsInstalled(a.Name) || a.Dir == "" {
			continue
		}
		got, err := LoadFixtures(filepath.Join(a.Dir, "defaults"))
		if err != nil {
			return nil, nil, fmt.Errorf("defaults of %s: %w", a.Name, err)
		}
		for model, recs := range got {
			if m := reg.Model(model); m == nil || m.App != a.Name {
				skipped = append(skipped, a.Name+"/"+model)
				continue
			}
			fx[model] = append(fx[model], recs...)
		}
	}
	sort.Strings(skipped)
	return fx, skipped, nil
}

// SeedDefaults fills an org's empty tables from the installed modules' defaults, as the system. A model that
// already holds a record of this org is left alone entirely (the owner's own units are never mixed with ours), so
// running it again, or after the owner deleted a default, adds nothing back.
func (e *Engine) SeedDefaults(ctx context.Context, org string) (*SeedStats, error) {
	fx, _, err := ModuleDefaults(e.Reg())
	if err != nil {
		return nil, err
	}
	if len(fx) == 0 {
		return &SeedStats{Created: map[string]int{}, Skipped: map[string]int{}}, nil
	}
	empty := map[string][]map[string]any{}
	a := Actor{Org: org, ID: "system", System: true}
	err = e.run(ctx, a, func(o *op) error {
		for model, recs := range fx {
			n, err := store.Count(o.ctx, o.tx, org, model)
			if err != nil {
				return err
			}
			if n == 0 {
				empty[model] = recs
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(empty) == 0 {
		return &SeedStats{Created: map[string]int{}, Skipped: map[string]int{}}, nil
	}
	return e.Seed(ctx, a, empty)
}
