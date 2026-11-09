package meta

import "sort"

// ConnectorsRun reports whether this kernel runs connectors (contract 0002): calls run as queued jobs and webhooks
// are served at /hooks/<org>/<connector>/<trigger>. A connector still needs its keys before it works.
const ConnectorsRun = true

// Catalogue is what exists for this site and what is installed (contract 0035 §5): every app, module and
// connector the kernel can see, with its state. Agents read it before naming an app, never from memory.
type Catalogue struct {
	Plan          string             `json:"plan,omitempty"`
	Apps          []CatalogueApp     `json:"apps"`
	Modules       []CatalogueModule  `json:"modules"`
	Connectors    []CatalogueConnect `json:"connectors"`
	ConnectorsRun bool               `json:"connectors_run"`
}

// Catalogue states.
const (
	StateInstalled = "installed"
	StateAvailable = "available"
	StateNotInPlan = "not_in_plan"
)

// CatalogueApp is one app. Shares are the modules it uses that are already installed (their data is the same data
// the installed apps use); Adds are the modules installing it would add.
type CatalogueApp struct {
	Name    string   `json:"name"`
	Title   Label    `json:"title,omitempty"`
	Summary Label    `json:"summary,omitempty"`
	State   string   `json:"state"`
	Modules []string `json:"modules"`
	Shares  []string `json:"shares"`
	Adds    []string `json:"adds"`
}

// CatalogueModule is one module and the installed apps that bring it in.
type CatalogueModule struct {
	Name        string   `json:"name"`
	Title       Label    `json:"title,omitempty"`
	State       string   `json:"state"`
	InstalledBy []string `json:"installed_by,omitempty"`
}

// CatalogueConnect is one connector. Runs is ConnectorsRun.
type CatalogueConnect struct {
	Name   string `json:"name"`
	Label  Label  `json:"label,omitempty"`
	Vendor string `json:"vendor,omitempty"`
	Docs   string `json:"docs,omitempty"`
	Runs   bool   `json:"runs"`
}

// Catalogue lists the apps, modules and connectors, sorted by name. With no site (the Cloud's interview, before a
// business exists) nothing is installed and everything is available.
func (r *Registry) Catalogue() Catalogue {
	c := Catalogue{Apps: []CatalogueApp{}, Modules: []CatalogueModule{}, Connectors: []CatalogueConnect{}, ConnectorsRun: ConnectorsRun}
	installed := map[string]bool{}
	if r.Site != nil {
		installed = r.Installed
	}
	var allowed map[string]bool
	if r.Site != nil && r.Site.Config != nil {
		c.Plan = str(r.Site.Config, "plan")
		if plan := r.PlanApps(); plan != nil {
			allowed = map[string]bool{}
			for _, a := range plan {
				allowed[a] = true
			}
		}
	}
	var planMods map[string]bool // contract 0032 §6: nil = every module
	if r.Site != nil {
		planMods = r.PlanModules()
	}
	for _, a := range r.Apps {
		switch {
		case a.IsApp():
			app := CatalogueApp{Name: a.Name, Title: a.Title, Summary: label(a.Raw["summary"]), State: StateAvailable,
				Modules: []string{}, Shares: []string{}, Adds: []string{}}
			if installed[a.Name] {
				app.State = StateInstalled
			} else if allowed != nil && !allowed[a.Name] {
				app.State = StateNotInPlan
			}
			for _, m := range r.Closure([]string{a.Name}) {
				if m == a.Name {
					continue
				}
				app.Modules = append(app.Modules, m)
				if installed[m] {
					app.Shares = append(app.Shares, m)
				} else {
					app.Adds = append(app.Adds, m)
				}
			}
			sort.Strings(app.Modules)
			sort.Strings(app.Shares)
			sort.Strings(app.Adds)
			c.Apps = append(c.Apps, app)
		case a.Kind == KindModule:
			m := CatalogueModule{Name: a.Name, Title: a.Title, State: StateAvailable}
			if installed[a.Name] {
				m.State = StateInstalled
				if by := r.InstalledBy(a.Name); len(by) > 0 {
					m.InstalledBy = by // "config" (config.modules, contract 0032) and/or the installed apps
				}
			} else if planMods != nil && !planMods[a.Name] {
				m.State = StateNotInPlan
			}
			c.Modules = append(c.Modules, m)
		}
	}
	sort.Slice(c.Apps, func(i, j int) bool { return c.Apps[i].Name < c.Apps[j].Name })
	sort.Slice(c.Modules, func(i, j int) bool { return c.Modules[i].Name < c.Modules[j].Name })
	for _, name := range sortedKeysM(r.Connectors) {
		d := r.Connectors[name]
		if d == nil {
			continue
		}
		m := asMap(d.Data)
		c.Connectors = append(c.Connectors, CatalogueConnect{Name: name, Label: label(m["label"]), Vendor: str(m, "vendor"), Docs: str(m, "docs"), Runs: ConnectorsRun})
	}
	return c
}
