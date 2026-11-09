package meta

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// Registry is the loaded metadata of an installation: apps (models, dashboards, flows, roles) and a site.
type Registry struct {
	AppsDir, SiteDir string
	ModulesDir       string
	Apps             []*App // modules sorted by name, then apps and legacy units sorted by name
	App              map[string]*App
	Models           map[string]*Model
	Dashboards       map[string]*Dashboard
	BaseDashboards   map[string]*Dashboard // the apps' own dashboards, before a site dashboard replaces one in Dashboards
	Flows            map[string]*Flow
	Imports          map[string]*Import // by name; a site import replaces an app's of the same name
	Site             *Site
	Installed        map[string]bool // config.apps (else the plan's apps, else every app)
	Docs             map[string]*rk.Doc
	Roles            map[string]*rk.Doc  // by role name
	Connectors       map[string]*rk.Doc  // by connector name (directory)
	Overlays         map[string]*rk.Doc  // <site>/custom/<model>.rk by file stem
	Prints           map[string]*Print   // print formats by name; a site format replaces an app's one
	Processes        map[string]*Process // processes by name (contract 0014); a site process replaces a unit's one
	Screens          map[string]*Screen  // work screens by name (contract 0030); the first unit that ships a name keeps it
	Orgs             map[string]*OrgDoc  // config.rk orgs: by org id
	ConnectorsDir    string
	order            []string
	printFiles       []*Print   // every loaded print file, apps first (validation)
	processFiles     []*Process // every loaded process file, units first (validation)
	screenFiles      []*Screen  // every loaded screen file, in unit order (validation)
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

// Options name the metadata directories. ModulesDir defaults to the modules directory next to AppsDir
// when it exists (DefaultModulesDir).
type Options struct {
	AppsDir, SiteDir, ConnectorsDir, ModulesDir string
}

// DefaultModulesDir is <parent of appsDir>/modules when that directory exists, else "".
func DefaultModulesDir(appsDir string) string {
	d := filepath.Join(filepath.Dir(filepath.Clean(appsDir)), "modules")
	if st, err := os.Stat(d); err == nil && st.IsDir() {
		return d
	}
	return ""
}

func sortedDir(dir string) []string {
	ents, _ := os.ReadDir(dir)
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// LoadOpts is Load with connectors and modules directories.
func LoadOpts(o Options, rep *Report) *Registry {
	appsDir, siteDir := o.AppsDir, o.SiteDir
	if o.ModulesDir == "" {
		o.ModulesDir = DefaultModulesDir(appsDir)
	}
	r := &Registry{AppsDir: appsDir, SiteDir: siteDir, ConnectorsDir: o.ConnectorsDir, ModulesDir: o.ModulesDir, App: map[string]*App{}, Models: map[string]*Model{},
		Dashboards: map[string]*Dashboard{}, BaseDashboards: map[string]*Dashboard{}, Flows: map[string]*Flow{}, Imports: map[string]*Import{}, Installed: map[string]bool{}, Docs: map[string]*rk.Doc{},
		Roles: map[string]*rk.Doc{}, Connectors: map[string]*rk.Doc{}, Overlays: map[string]*rk.Doc{}, Prints: map[string]*Print{}, Processes: map[string]*Process{}, Screens: map[string]*Screen{}}
	ld := &loader{rep: rep}
	// modules first, then apps (rangka_validate.py order); a name in both is reported on the app
	type unit struct{ name, dir, stem, prefix string }
	var units []unit
	if o.ModulesDir != "" {
		for _, n := range sortedDir(o.ModulesDir) {
			units = append(units, unit{n, filepath.Join(o.ModulesDir, n), "module", "modules/" + n})
		}
	}
	for _, n := range sortedDir(appsDir) {
		units = append(units, unit{n, filepath.Join(appsDir, n), "app", "apps/" + n})
	}
	for _, u := range units {
		name, dir := u.name, u.dir
		ap := rk.Find(dir, u.stem)
		if ap == "" {
			if old := rk.Find(dir, "app"); u.stem == "module" && old != "" && rep != nil {
				rep.add("warn", old, "(root)", "module_manifest_name", fmt.Sprintf("modules/%s has app.rk, which is not read; rename to module.rk (contract 0001)", name))
			}
			continue
		}
		a := &App{Name: name, Dir: dir, Prefix: u.prefix, File: ap}
		if d := ld.load(ap); d != nil {
			a.Doc, a.Raw = d, asMap(d.Data)
		}
		if a.Raw == nil {
			a.Raw = map[string]any{}
		}
		_, hasModules := a.Raw["modules"]
		switch {
		case u.stem == "module":
			a.Kind = KindModule
		case hasModules:
			a.Kind = KindApp
		default:
			a.Kind = KindLegacy
		}
		if prev := r.App[name]; prev != nil {
			if rep != nil {
				rep.add("error", ap, "name", "duplicate_unit", fmt.Sprintf(`"%s" is already a module (%s); modules and apps share one namespace`, name, rep.rel(prev.File)))
			}
			continue
		}
		if a.Doc != nil {
			r.Docs[u.prefix+"/"+u.stem+".rk"] = a.Doc
		}
		a.Version, a.Title = str(a.Raw, "version"), label(a.Raw["title"])
		a.Requires, a.Optional, a.Modules = asMap(a.Raw["requires"]), asMap(a.Raw["optional"]), asMap(a.Raw["modules"])
		a.Hooks, a.Dashboards = strList(a.Raw["hooks"]), strList(a.Raw["dashboards"])
		if a.Kind != KindApp { // an app defines no models (app_defines_model)
			for _, e := range rk.ListDir(filepath.Join(dir, "models")) {
				if d := ld.load(e.Path); d != nil {
					m := parseModel(name, d)
					m.Name = e.Stem // registry key is the file name; name_mismatch is reported by validate
					r.Models[e.Stem] = m
					a.Models = append(a.Models, e.Stem)
					r.Docs[u.prefix+"/models/"+e.Stem+".rk"] = d
				}
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
				r.BaseDashboards[e.Stem] = db
				r.Docs[u.prefix+"/dashboards/"+e.Stem+".rk"] = d
			}
		}
		r.Apps = append(r.Apps, a)
		r.App[name] = a
	}
	if siteDir != "" {
		r.loadSiteUnit(ld, siteDir)
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
				r.Docs[a.DocKey("flows/"+e.Stem+".rk")] = d
			}
		}
		if a.Kind == KindSite { // the rest of the site is loaded by loadSite
			continue
		}
		for _, e := range rk.ListDir(filepath.Join(a.Dir, "imports")) {
			if d := ld.load(e.Path); d != nil {
				im := parseImport(d, a.Name)
				im.Name = e.Stem
				r.Imports[e.Stem] = im
				r.Docs[a.Prefix+"/imports/"+e.Stem+".rk"] = d
			}
		}
		for _, e := range rk.ListDir(filepath.Join(a.Dir, "roles")) {
			if d := ld.load(e.Path); d != nil {
				r.Docs[a.Prefix+"/roles/"+e.Stem+".rk"] = d
				r.Roles[e.Stem] = d
			}
		}
		r.loadPrints(ld, a.Name, a.Dir, a.Prefix+"/")
		r.loadProcesses(ld, a.Name, a.Dir, a.Prefix+"/")
		r.loadScreens(ld, a.Name, a.Dir, a.Prefix+"/")
	}
	if siteDir != "" {
		r.Site = loadSite(siteDir, ld, r)
	}
	r.loadOrgs()
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
	r.resolvePrints()
	r.resolveProcesses()
	return r
}

// loadSiteUnit adds the site company module (contract 0016): <site>/models/<m>.rk and <site>/flows/<f>.rk as the
// unit "site", when either folder holds a file. A model named like a unit's model is duplicate_model and not loaded.
func (r *Registry) loadSiteUnit(ld *loader, dir string) {
	models, flows := rk.ListDir(filepath.Join(dir, "models")), rk.ListDir(filepath.Join(dir, "flows"))
	if len(models) == 0 && len(flows) == 0 {
		return
	}
	if prev := r.App[SiteUnit]; prev != nil {
		if ld.rep != nil {
			first := append(models, flows...)[0].Path
			ld.rep.add("error", first, "(root)", "duplicate_unit", fmt.Sprintf(`"%s" is already a unit (%s); the site company module is named %s (contract 0016)`, SiteUnit, ld.rep.rel(prev.File), SiteUnit))
		}
		return
	}
	a := &App{Name: SiteUnit, Dir: dir, Kind: KindSite, Raw: map[string]any{}, Title: Label{"id": "Perusahaan", "en": "Company"}}
	for _, e := range models {
		d := ld.load(e.Path)
		if d == nil {
			continue
		}
		if prev := r.Models[e.Stem]; prev != nil {
			if ld.rep != nil {
				ld.rep.add("error", e.Path, "name", "duplicate_model", fmt.Sprintf(`"%s" is already a model of %s; a company model needs its own name (x_…), contract 0016`, e.Stem, prev.App))
			}
			continue
		}
		m := parseModel(SiteUnit, d)
		m.Name = e.Stem
		r.Models[e.Stem] = m
		a.Models = append(a.Models, e.Stem)
		r.Docs["models/"+e.Stem+".rk"] = d
	}
	r.Apps = append(r.Apps, a)
	r.App[SiteUnit] = a
}

func loadSite(dir string, ld *loader, r *Registry) *Site {
	s := &Site{Dir: dir, Name: filepath.Base(dir), Themes: map[string]*rk.Doc{}, Layouts: map[string]*rk.Doc{}, Docs: map[string]*rk.Doc{},
		Letterheads: map[string]*rk.Doc{}, Dashboards: map[string]*Dashboard{}}
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
	for _, e := range rk.ListDir(filepath.Join(dir, "imports")) {
		if d := ld.load(e.Path); d != nil {
			im := parseImport(d, "")
			im.Name = e.Stem
			r.Imports[e.Stem] = im
			r.Docs["imports/"+e.Stem+".rk"] = d
		}
	}
	for _, e := range rk.ListDir(filepath.Join(dir, "custom")) {
		if d := ld.load(e.Path); d != nil {
			r.Overlays[e.Stem] = d
			r.Docs["custom/"+e.Stem+".rk"] = d
		}
	}
	for _, e := range rk.ListDir(filepath.Join(dir, "dashboards")) {
		if d := ld.load(e.Path); d != nil {
			key := "dashboards/" + e.Stem + ".rk"
			s.Docs[key] = d
			r.Docs[key] = d
			app := ""
			if base := r.BaseDashboards[e.Stem]; base != nil {
				app = base.App
			}
			db := parseDashboard(app, d)
			db.Name = e.Stem
			s.Dashboards[e.Stem] = db
			if app != "" { // a site dashboard replaces the installed one of its name (contract 0015 §3)
				r.Dashboards[e.Stem] = db
			}
		}
	}
	r.loadPrints(ld, "", dir, "")
	r.loadProcesses(ld, "", dir, "")
	loadLetterheads(dir, ld, s, r)
	return s
}

// PlanApps returns the apps of the site's plan (following extends), or nil when the site names no plan.
func (r *Registry) PlanApps() []string {
	if r.Site == nil || r.Site.Config == nil {
		return nil
	}
	return r.planApps(str(r.Site.Config, "plan"))
}

func (r *Registry) planApps(plan string) []string {
	if r.Site == nil || r.Site.Features == nil || plan == "" {
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

// resolveInstalled installs config.apps and config.modules (else the plan's apps, else every unit) plus,
// transitively, each app's modules and every unit's requires, dependencies first (contracts 0001, 0032). Apps come
// in config.apps order, then the modules config.modules names, by name.
func (r *Registry) resolveInstalled() {
	var cfg map[string]any
	var mods []string
	if r.Site != nil {
		cfg, mods = r.Site.Config, r.ConfigModules()
	}
	want := r.selected(cfg, mods)
	if r.App[SiteUnit] != nil && r.App[SiteUnit].Kind == KindSite {
		defer func() { // after every app: nothing depends on it (contract 0016)
			r.Installed[SiteUnit] = true
			r.order = append(r.order, SiteUnit)
		}()
	}
	var visit func(string, int)
	visit = func(name string, depth int) {
		a := r.App[name]
		if a == nil || r.Installed[name] || depth > 20 {
			return
		}
		r.Installed[name] = true // before the dependencies, so a cycle ends
		for _, dep := range sortedKeys(a.Modules) {
			visit(dep, depth+1)
		}
		for _, dep := range sortedKeys(a.Requires) {
			visit(dep, depth+1)
		}
		r.order = append(r.order, name)
	}
	for _, n := range want {
		visit(n, 0)
	}
}

// Closure returns names plus, transitively, each app's modules and every unit's requires (units that exist),
// in rangka_validate.py closure() order.
func (r *Registry) Closure(names []string) []string {
	var out []string
	seen := map[string]bool{}
	todo := append([]string{}, names...)
	for len(todo) > 0 {
		n := todo[0]
		todo = todo[1:]
		a := r.App[n]
		if a == nil || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
		todo = append(append(todo, orderedKeys(a, "modules")...), orderedKeys(a, "requires")...)
	}
	return out
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
func DeepCopy(v any) any                    { return deepCopy(v) }
func AsList(v any) []any                    { return asList(v) }
func Str(m map[string]any, k string) string { return str(m, k) }
func StrList(v any) []string                { return strList(v) }
func LabelOf(v any) Label                   { return label(v) }
