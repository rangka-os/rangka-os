// Package canvas builds the process canvas of contract 0014: a process (declared in processes/<name>.rk or
// inferred from an app's menu) drawn as stages of nodes taken from the metadata, and the change-set ops that
// edit one text value of a node without touching any other byte of its file.
package canvas

import (
	"fmt"
	"sort"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// Processes returns the declared processes of installed units and the site, plus one inferred process per
// installed app (kind app or legacy) with a menu and no declared process of its name (ruling 1), sorted by name.
// An inferred process has File "" and App set; its stages are the app's non-child menu models in menu order.
func Processes(reg *meta.Registry) []*meta.Process {
	out := reg.ProcessList()
	declared := map[string]bool{}
	for name := range reg.Processes {
		declared[name] = true
	}
	for _, a := range reg.Apps {
		if !a.IsApp() || !reg.Installed[a.Name] || declared[a.Name] {
			continue
		}
		if p := inferred(reg, a); p != nil {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func inferred(reg *meta.Registry, a *meta.App) *meta.Process {
	p := &meta.Process{Name: a.Name, App: a.Name, Label: meta.LabelOf(a.Raw["label"])}
	if p.Label == nil {
		p.Label = a.Title
	}
	if p.Label == nil {
		p.Label = meta.Label{"id": a.Name}
	}
	seen := map[string]bool{}
	for _, mi := range menuItems(a) {
		m := reg.Model(mi.model)
		if m == nil || m.IsChild || seen[mi.model] {
			continue
		}
		seen[mi.model] = true
		lbl := m.Label
		if lbl == nil {
			lbl = meta.Label{"id": m.Name}
		}
		p.Stages = append(p.Stages, meta.Stage{Name: m.Name, Model: m.Name, Label: lbl, Index: len(p.Stages)})
	}
	if len(p.Stages) == 0 {
		return nil
	}
	return p
}

type menuItem struct {
	model string
	path  string // JSON pointer into the app manifest
}

func menuItems(a *meta.App) []menuItem {
	var out []menuItem
	for g, gv := range meta.AsList(a.Raw["menu"]) {
		for i, iv := range meta.AsList(meta.AsMap(gv)["items"]) {
			if s, ok := iv.(string); ok {
				out = append(out, menuItem{s, fmt.Sprintf("/menu/%d/items/%d", g, i)})
			}
		}
	}
	return out
}
