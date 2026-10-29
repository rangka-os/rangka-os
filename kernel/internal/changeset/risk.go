package changeset

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// Layer returns the layer a change-set path belongs to (CONTRACTS "Layers").
func Layer(path string) int {
	switch {
	case path == "system.rk":
		return 2
	case strings.HasPrefix(path, "connectors/"):
		return 3
	case path == "features.rk" || path == "config.rk" || strings.HasPrefix(path, "custom/"):
		return 4
	case strings.HasPrefix(path, "apps/"):
		return 5
	}
	return 6 // themes, layouts
}

var riskRank = map[string]int{"low": 0, "medium": 1, "high": 2}

// Assessment explains the computed risk.
type Assessment struct {
	Layers  []int    `json:"layers"`
	Risk    string   `json:"risk"`
	Reasons []string `json:"reasons"`
}

func (a *Assessment) raise(level, why string) {
	if riskRank[level] > riskRank[a.Risk] {
		a.Risk = level
	}
	a.Reasons = append(a.Reasons, level+": "+why)
}

// sensitiveField is a money field (money: true; currency fields unless money: false) or a stock field (stock: true).
func sensitiveField(f map[string]any) bool {
	if b, ok := f["money"].(bool); ok {
		if b {
			return true
		}
	} else if t, _ := f["type"].(string); t == "currency" {
		return true
	}
	return f["stock"] == true
}

func layerOf(path string) int { return Layer(path) }

// Assess computes layers and risk (CONTRACTS v0.1 decisions 2 and 3). High: money/stock fields, system.rk,
// roles and permissions, config.tax (and config.ai, which decides approvals), deletions, existing fields of
// submittable models, and edits to existing app files (app files belong to app authors; organisations use
// custom/<model>.rk). Medium: features.rk, flows, new fields/rules/overlays, config.apps and other settings,
// connectors, new models. Low: themes, layouts, labels, dashboards.
func Assess(ops []Op, before, after func(path string) (map[string]any, bool), model func(name string) map[string]any) *Assessment {
	a := &Assessment{Risk: "low"}
	layers := map[int]bool{}
	for _, op := range ops {
		p := op.Path
		layers[layerOf(p)] = true
		old, existed := before(p)
		nw, _ := after(p)
		if op.Op == "delete" {
			a.raise("high", "deletes "+p)
			continue
		}
		switch {
		case p == "system.rk":
			a.raise("high", "changes system capabilities (system.rk)")
		case strings.Contains(p, "/roles/"):
			a.raise("high", "changes roles ("+p+")")
		case strings.HasPrefix(p, "custom/"):
			name := strings.TrimSuffix(strings.TrimPrefix(p, "custom/"), ".rk")
			var target map[string]any
			if model != nil {
				target = model(name)
			}
			assessOverlay(a, p, old, existed, nw, target)
		case strings.HasPrefix(p, "apps/") && existed:
			if strings.Contains(p, "/models/") {
				assessModel(a, p, old, nw)
			}
			a.raise("high", "edits app file "+p+" (app files belong to app authors; organisations change models through custom/<model>.rk)")
		case strings.HasPrefix(p, "apps/"):
			switch {
			case strings.Contains(p, "/dashboards/"):
				a.raise("low", "new dashboard "+p)
			case strings.Contains(p, "/models/"):
				for _, f := range meta.AsList(nw["fields"]) {
					if sensitiveField(meta.AsMap(f)) {
						a.raise("high", "new model "+p+" with money/stock field "+meta.Str(meta.AsMap(f), "name"))
					}
				}
				a.raise("medium", "new model "+p)
			default:
				a.raise("medium", "new file "+p)
			}
		case strings.HasPrefix(p, "themes/") || strings.HasPrefix(p, "layouts/"):
			a.raise("low", "presentation ("+p+")")
		case p == "config.rk":
			assessConfig(a, old, nw)
		case p == "features.rk":
			a.raise("medium", "changes plans (features.rk)")
		default:
			a.raise("medium", "changes "+p)
		}
	}
	for l := range layers {
		a.Layers = append(a.Layers, l)
	}
	sort.Ints(a.Layers)
	return a
}

func assessConfig(a *Assessment, old, nw map[string]any) {
	keys := map[string]bool{}
	for k := range old {
		keys[k] = true
	}
	for k := range nw {
		keys[k] = true
	}
	var changed []string
	for k := range keys {
		if !reflect.DeepEqual(normalize(old[k]), normalize(nw[k])) {
			changed = append(changed, k)
		}
	}
	sort.Strings(changed)
	if len(changed) == 0 {
		a.raise("low", "config.rk unchanged")
	}
	for _, k := range changed {
		switch k {
		case "tax", "ai":
			a.raise("high", "changes config."+k)
		case "theme", "layout", "locale":
			a.raise("low", "changes config."+k)
		default:
			a.raise("medium", "changes config."+k)
		}
	}
}

func fieldsByName(m map[string]any) (map[string]map[string]any, []string) {
	out := map[string]map[string]any{}
	var order []string
	for _, f := range meta.AsList(m["fields"]) {
		fm := meta.AsMap(f)
		if n, ok := fm["name"].(string); ok {
			out[n] = fm
			order = append(order, n)
		}
	}
	return out, order
}

// onlyLabels reports whether a and b differ only in label-like keys.
func onlyLabels(a, b map[string]any, extra ...string) bool {
	strip := func(m map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range m {
			skip := k == "label" || k == "help" || k == "description" || k == "icon"
			for _, e := range extra {
				skip = skip || k == e
			}
			if !skip {
				out[k] = v
			}
		}
		return out
	}
	return reflect.DeepEqual(normalize(strip(a)), normalize(strip(b)))
}

// assessFields compares field lists; submittable makes changes to existing fields high.
func assessFields(a *Assessment, p string, old, nw map[string]any, submittable bool) {
	of, _ := fieldsByName(old)
	nf, order := fieldsByName(nw)
	var removed []string
	for name := range of {
		if _, ok := nf[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(removed)
	for _, name := range removed {
		a.raise("high", fmt.Sprintf("removes field %s from %s (its data stays)", name, p))
	}
	for _, name := range order {
		f := nf[name]
		o, ok := of[name]
		switch {
		case !ok && sensitiveField(f):
			a.raise("high", fmt.Sprintf("adds money/stock field %s to %s", name, p))
		case !ok:
			a.raise("medium", fmt.Sprintf("adds field %s to %s", name, p))
		case reflect.DeepEqual(normalize(o), normalize(f)):
		case onlyLabels(o, f):
			a.raise("low", fmt.Sprintf("relabels field %s of %s", name, p))
		case sensitiveField(o) || sensitiveField(f):
			a.raise("high", fmt.Sprintf("changes money/stock field %s of %s", name, p))
		case submittable:
			a.raise("high", fmt.Sprintf("changes field %s of a submittable model (%s)", name, p))
		default:
			a.raise("medium", fmt.Sprintf("changes field %s of %s", name, p))
		}
	}
	if !reflect.DeepEqual(normalize(old["rules"]), normalize(nw["rules"])) {
		a.raise("medium", "changes rules of "+p)
	}
}

func assessModel(a *Assessment, p string, old, nw map[string]any) {
	submittable, _ := old["is_submittable"].(bool)
	assessFields(a, p, old, nw, submittable)
	if !reflect.DeepEqual(normalize(old["permissions"]), normalize(nw["permissions"])) {
		a.raise("high", "changes permissions of "+p)
	}
}

func assessOverlay(a *Assessment, p string, old map[string]any, existed bool, nw map[string]any, target map[string]any) {
	submittable, _ := target["is_submittable"].(bool)
	if !existed {
		old = map[string]any{}
		a.raise("medium", "new overlay "+p)
	}
	assessFields(a, p, old, nw, submittable && existed)
	if !reflect.DeepEqual(normalize(old["views"]), normalize(nw["views"])) {
		a.raise("low", "changes views of "+p)
	}
}
