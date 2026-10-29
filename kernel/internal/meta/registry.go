package meta

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// Registry is the loaded metadata of an installation: apps (models, dashboards, flows, roles) and a site.
type Registry struct {
	AppsDir, SiteDir string
	Apps             []*App // sorted by name
	App              map[string]*App
	Models           map[string]*Model
	Dashboards       map[string]*Dashboard
	Flows            map[string]*Flow
	Site             *Site
	Installed        map[string]bool // config.apps (else the plan's apps, else every app)
	Docs             map[string]*rk.Doc
	Roles            map[string]*rk.Doc // by role name
	Connectors       map[string]*rk.Doc // by connector name (directory)
	Overlays         map[string]*rk.Doc // <site>/custom/<model>.rk by file stem
	ConnectorsDir    string
	order            []string
}

// AppOrder returns installed app names in a stable order (dependencies first).
func (r *Registry) AppOrder() []string { return r.order }

// IsInstalled reports whether app is installed.
func (r *Registry) IsInstalled(app string) bool { return r.Installed[app] }

// Model returns an installed model.
func (r *Registry) Model(name string) *Model {
	m := r.Models[name]
	if m == nil || !r.Installed[m.App] {
		return nil
	}
	return m
}

// Dashboard returns a dashboard of an installed app.
func (r *Registry) Dashboard(name string) *Dashboard {
	d := r.Dashboards[name]
	if d == nil || !r.Installed[d.App] {
		return nil
	}
	return d
}

// InstalledModels returns installed models sorted by name.
func (r *Registry) InstalledModels() []*Model {
	var out []*Model
	for _, n := range sortedKeysM(r.Models) {
		if m := r.Model(n); m != nil {
			out = append(out, m)
		}
	}
	return out
}

func sortedKeysM[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// loader loads files and records syntax problems in the report in rangka_validate.py order.
type loader struct {
	rep   *Report
	extra bool
}

func (l *loader) load(path string) *rk.Doc {
	d, err := rk.Load(path)
	if err != nil {
		if l.rep != nil {
			if e, ok := err.(*rk.Error); ok {
				for _, x := range e.All {
					l.rep.addAt("error", path, x.Line, x.Col, "(syntax)", x.Code, x.Msg, l.extra)
				}
			} else {
				l.rep.addAt("error", path, 1, 1, "(syntax)", "io", err.Error(), l.extra)
			}
		}
		return nil
	}
	if l.rep != nil {
		l.rep.docs[path] = d
	}
	return d
}

// Load reads every app in appsDir and, when siteDir is set, the site. Problems that stop a file from
// loading are recorded in rep (which may be nil).
func Load(appsDir, siteDir string, rep *Report) *Registry {
	return LoadOpts(Options{AppsDir: appsDir, SiteDir: siteDir}, rep)
}

// Options name the metadata directories.
type Options struct {
	AppsDir, SiteDir, ConnectorsDir string
}

// LoadOpts is Load with a connectors directory.
func LoadOpts(o Options, rep *Report) *Registry {
	appsDir, siteDir := o.AppsDir, o.SiteDir
	r := &Registry{AppsDir: appsDir, SiteDir: siteDir, ConnectorsDir: o.ConnectorsDir, App: map[string]*App{}, Models: map[string]*Model{},
		Dashboards: map[string]*Dashboard{}, Flows: map[string]*Flow{}, Installed: map[string]bool{}, Docs: map[string]*rk.Doc{},
		Roles: map[string]*rk.Doc{}, Connectors: map[string]*rk.Doc{}, Overlays: map[string]*rk.Doc{}}
	ld := &loader{rep: rep}
	names, _ := os.ReadDir(appsDir)
	var appNames []string
	for _, e := range names {
		appNames = append(appNames, e.Name())
	}
	sort.Strings(appNames)
	for _, name := range appNames {
		dir := filepath.Join(appsDir, name)
		ap := rk.Find(dir, "app")
		if ap == "" {
			continue
		}
		a := &App{Name: name, Dir: dir}
		if d := ld.load(ap); d != nil {
			a.Doc, a.Raw = d, asMap(d.Data)
			r.Docs["apps/"+name+"/app.rk"] = d
		}
		if a.Raw == nil {
			a.Raw = map[string]any{}
		}
		a.Version, a.Title = str(a.Raw, "version"), label(a.Raw["title"])
		a.Requires, a.Optional = asMap(a.Raw["requires"]), asMap(a.Raw["optional"])
		a.Hooks, a.Dashboards = strList(a.Raw["hooks"]), strList(a.Raw["dashboards"])
		for _, e := range rk.ListDir(filepath.Join(dir, "models")) {
			if d := ld.load(e.Path); d != nil {
				m := parseModel(name, d)
				m.Name = e.Stem // registry key is the file name; name_mismatch is reported by validate
				r.Models[e.Stem] = m
				a.Models = append(a.Models, e.Stem)
				r.Docs["apps/"+name+"/models/"+e.Stem+".rk"] = d
			}
		}
		for _, e := range rk.ListDir(filepath.Join(dir, "roles")) {
			a.Roles = append(a.Roles, e.Stem)
		}
		for _, e := range rk.ListDir(filepath.Join(dir, "dashboards")) {
			if d := ld.load(e.Path); d != nil {
				db := parseDashboard(name, d)
				db.Name = e.Stem
				r.Dashboards[e.Stem] = db
				r.Docs["apps/"+name+"/dashboards/"+e.Stem+".rk"] = d
			}
		}
		r.Apps = append(r.Apps, a)
		r.App[name] = a
	}
	// kernel extensions to the Python reference: flows, roles as documents, the site
	ld.extra = true
	for _, a := range r.Apps {
		for _, e := range rk.ListDir(filepath.Join(a.Dir, "flows")) {
			if d := ld.load(e.Path); d != nil {
				f := parseFlow(a.Name, d)
				f.Name = e.Stem
				r.Flows[e.Stem] = f
				a.Flows = append(a.Flows, e.Stem)
				r.Docs["apps/"+a.Name+"/flows/"+e.Stem+".rk"] = d
			}
		}
		for _, e := range rk.ListDir(filepath.Join(a.Dir, "roles")) {
			if d := ld.load(e.Path); d != nil {
				r.Docs["apps/"+a.Name+"/roles/"+e.Stem+".rk"] = d
				r.Roles[e.Stem] = d
			}
		}
	}
	if siteDir != "" {
		r.Site = loadSite(siteDir, ld, r)
	}
	if o.ConnectorsDir != "" {
		ents, _ := os.ReadDir(o.ConnectorsDir)
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, n := range names {
			if p := rk.Find(filepath.Join(o.ConnectorsDir, n), "connector"); p != "" {
				if d := ld.load(p); d != nil {
					r.Connectors[n] = d
					r.Docs["connectors/"+n+".rk"] = d
				}
			}
		}
	}
	r.resolveInstalled()
	return r
}

func loadSite(dir string, ld *loader, r *Registry) *Site {
	s := &Site{Dir: dir, Name: filepath.Base(dir), Themes: map[string]*rk.Doc{}, Layouts: map[string]*rk.Doc{}, Docs: map[string]*rk.Doc{}}
	for _, stem := range []string{"system", "features", "config"} {
		p := rk.Find(dir, stem)
		if p == "" {
			continue
		}
		d := ld.load(p)
		if d == nil {
			continue
		}
		s.Docs[stem+".rk"] = d
		r.Docs[stem+".rk"] = d
		switch stem {
		case "system":
			s.System = asMap(d.Data)
		case "features":
			s.Features = asMap(d.Data)
		case "config":
			s.Config = asMap(d.Data)
		}
	}
	for _, sub := range []string{"themes", "layouts"} {
		for _, e := range rk.ListDir(filepath.Join(dir, sub)) {
			if d := ld.load(e.Path); d != nil {
				key := sub + "/" + e.Stem + ".rk"
				s.Docs[key] = d
				r.Docs[key] = d
				if sub == "themes" {
					s.Themes[e.Stem] = d
				} else {
					s.Layouts[e.Stem] = d
				}
			}
		}
	}
	for _, e := range rk.ListDir(filepath.Join(dir, "custom")) {
		if d := ld.load(e.Path); d != nil {
			r.Overlays[e.Stem] = d
			r.Docs["custom/"+e.Stem+".rk"] = d
		}
	}
	return s
}

// PlanApps returns the apps of the site's plan (following extends), or nil when the site names no plan.
func (r *Registry) PlanApps() []string {
	if r.Site == nil || r.Site.Config == nil || r.Site.Features == nil {
		return nil
	}
	plan := str(r.Site.Config, "plan")
	if plan == "" {
		return nil
	}
	plans := map[string]map[string]any{}
	for _, p := range asList(r.Site.Features["plans"]) {
		pm := asMap(p)
		plans[str(pm, "name")] = pm
	}
	seen := map[string]bool{}
	var out []string
	for name, depth := plan, 0; name != "" && depth < 10; depth++ {
		pm := plans[name]
		if pm == nil {
			break
		}
		for _, a := range strList(pm["apps"]) {
			if !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
		}
		name = str(pm, "extends")
	}
	return out
}

// Capabilities returns the capabilities of the site's plan (following extends).
func (r *Registry) Capabilities() map[string]bool {
	out := map[string]bool{}
	if r.Site == nil || r.Site.Features == nil {
		return out
	}
	plans := map[string]map[string]any{}
	for _, p := range asList(r.Site.Features["plans"]) {
		pm := asMap(p)
		plans[str(pm, "name")] = pm
	}
	for name, depth := str(r.Site.Config, "plan"), 0; name != "" && depth < 10; depth++ {
		pm := plans[name]
		if pm == nil {
			break
		}
		for _, c := range strList(pm["capabilities"]) {
			out[c] = true
		}
		name = str(pm, "extends")
	}
	return out
}

func (r *Registry) resolveInstalled() {
	if r.Site != nil {
		if l, ok := r.Site.Config["apps"].([]any); ok {
			for _, x := range l {
				if n, _ := x.(string); r.App[n] != nil && !r.Installed[n] {
					r.Installed[n] = true
					r.order = append(r.order, n)
				}
			}
			return
		}
	}
	want := r.PlanApps()
	if want == nil {
		for _, a := range r.Apps {
			want = append(want, a.Name)
		}
	}
	var visit func(string, int)
	visit = func(name string, depth int) {
		a := r.App[name]
		if a == nil || r.Installed[name] || depth > 20 {
			return
		}
		for _, dep := range sortedKeys(a.Requires) {
			visit(dep, depth+1)
		}
		if !r.Installed[name] {
			r.Installed[name] = true
			r.order = append(r.order, name)
		}
	}
	for _, n := range want {
		visit(n, 0)
	}
}

// SiteConfig returns a value from config.rk by dotted path ("ai.approvers").
func (r *Registry) SiteConfig(path string) any {
	if r.Site == nil {
		return nil
	}
	var cur any = r.Site.Config
	for _, p := range strings.Split(path, ".") {
		cur = asMap(cur)[p]
	}
	return cur
}

// SystemConfig returns a value from system.rk by dotted path ("auth.issuer").
func (r *Registry) SystemConfig(path string) any {
	if r.Site == nil {
		return nil
	}
	var cur any = r.Site.System
	for _, p := range strings.Split(path, ".") {
		cur = asMap(cur)[p]
	}
	return cur
}

// AsMap and helpers are exported for other packages reading raw metadata.
func AsMap(v any) map[string]any            { return asMap(v) }
func AsList(v any) []any                    { return asList(v) }
func Str(m map[string]any, k string) string { return str(m, k) }
func StrList(v any) []string                { return strList(v) }
func LabelOf(v any) Label                   { return label(v) }
