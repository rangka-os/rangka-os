package meta

import (
	"sort"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// Free module composition (contract 0032): config.modules names modules next to config.apps. The installed set is
// the closure of both; the validators, never an LLM, compute it.

// ConfigModules returns the keys of config.modules in file order, or nil when config.rk has no modules key.
func (r *Registry) ConfigModules() []string {
	if r.Site == nil {
		return nil
	}
	return configModules(r.Site.Config, r.Site.Docs["config.rk"])
}

func configModules(cfg map[string]any, d *rk.Doc) []string {
	m, ok := cfg["modules"].(map[string]any)
	if !ok {
		return nil
	}
	out := []string{}
	if d != nil && d.Root != nil {
		if n := d.Root.Get("modules"); n != nil && n.Kind == rk.MapNode {
			for _, k := range n.Keys {
				if !contains(out, k.Value) {
					out = append(out, k.Value)
				}
			}
			return out
		}
	}
	return append(out, sortedKeys(m)...)
}

// Composed reports whether config.rk names modules directly (contract 0032).
func (r *Registry) Composed() bool { return r.ConfigModules() != nil }

// configListed reports whether config.rk sets apps or modules: either replaces the plan's apps as the fallback.
func (r *Registry) configListed() bool {
	if r.Site == nil {
		return false
	}
	_, apps := r.Site.Config["apps"].([]any)
	return apps || r.Composed()
}

// PlanModules returns the modules the site's plan allows (contract 0032 §6): the closure of its apps and modules,
// following extends. Nil when the site names no plan or no plan in the chain has apps or modules (every module).
func (r *Registry) PlanModules() map[string]bool {
	if r.Site == nil || r.Site.Features == nil {
		return nil
	}
	return r.planModules(r.Site.Features, str(r.Site.Config, "plan"))
}

func (r *Registry) planModules(features map[string]any, plan string) map[string]bool {
	plans := map[string]map[string]any{}
	for _, p := range asList(features["plans"]) {
		if pm := asMap(p); pm != nil {
			plans[pyStr(pm["name"])] = pm
		}
	}
	var names []string
	keyed := false
	seen := map[string]bool{}
	for name, depth := plan, 0; name != "" && depth < 10 && !seen[name]; depth++ {
		pm := plans[name]
		if pm == nil {
			break
		}
		seen[name] = true
		for _, key := range []string{"apps", "modules"} {
			if l, ok := pm[key].([]any); ok {
				keyed = true
				for _, x := range l {
					if s, ok := x.(string); ok {
						names = append(names, s)
					}
				}
			}
		}
		name, _ = pm["extends"].(string)
	}
	if !keyed {
		return nil
	}
	out := map[string]bool{}
	for _, n := range r.Closure(names) {
		if r.App[n].Kind == KindModule {
			out[n] = true
		}
	}
	return out
}

// InstalledBy names what brings an installed unit in (contract 0032 §4): "config" when config.apps or
// config.modules lists it, then the installed apps whose closure holds it, by name. Empty when it is not installed.
func (r *Registry) InstalledBy(name string) []string {
	out := []string{}
	if !r.Installed[name] {
		return out
	}
	listed := false
	if r.Site != nil {
		for _, x := range asList(r.Site.Config["apps"]) {
			listed = listed || x == name
		}
		listed = listed || (contains(r.ConfigModules(), name) && r.App[name].Kind == KindModule)
	}
	if listed {
		out = append(out, "config")
	}
	var apps []string
	for _, a := range r.Apps {
		if a.Name == name || !a.IsApp() || !r.Installed[a.Name] {
			continue
		}
		if contains(r.Closure([]string{a.Name}), name) {
			apps = append(apps, a.Name)
		}
	}
	sort.Strings(apps)
	return append(out, apps...)
}

// MenuGroup is one side group of the default menu of a composed site: a module's models that are not child tables.
type MenuGroup struct {
	Group      Label    `json:"group"`
	Module     string   `json:"module"`
	Items      []string `json:"items"`
	Dashboards []string `json:"dashboards"`
}

// DefaultMenu is the kernel's menu for the modules a composed site names directly (contract 0032 §5): one group per
// installed module that no installed app brings in, config.modules file order first, then the modules they require
// by name. Nil when the site names no modules. /meta leaves it out when the layout sets nav.side (that is the menu).
func (r *Registry) DefaultMenu() []MenuGroup {
	if !r.Composed() {
		return nil
	}
	covered := map[string]bool{}
	for _, a := range r.Apps {
		if a.IsApp() && r.Installed[a.Name] {
			for _, n := range r.Closure([]string{a.Name}) {
				covered[n] = true
			}
		}
	}
	var order []string
	for _, n := range r.ConfigModules() {
		if u := r.App[n]; u != nil && u.Kind == KindModule && r.Installed[n] && !contains(order, n) {
			order = append(order, n)
		}
	}
	var rest []string
	for _, n := range r.order {
		if u := r.App[n]; u.Kind == KindModule && !contains(order, n) {
			rest = append(rest, n)
		}
	}
	sort.Strings(rest)
	out := []MenuGroup{}
	for _, n := range append(order, rest...) {
		if covered[n] {
			continue
		}
		u := r.App[n]
		g := MenuGroup{Group: u.Title, Module: n, Items: []string{}, Dashboards: []string{}}
		if g.Group == nil {
			g.Group = Label{"id": n}
		}
		models := append([]string{}, u.Models...)
		sort.Strings(models)
		for _, mn := range models {
			if m := r.Models[mn]; m != nil && !m.IsChild {
				g.Items = append(g.Items, mn)
			}
		}
		g.Dashboards = append(g.Dashboards, u.Dashboards...)
		if len(g.Items) > 0 || len(g.Dashboards) > 0 {
			out = append(out, g)
		}
	}
	return out
}
