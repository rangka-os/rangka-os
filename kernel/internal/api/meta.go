package api

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/changeset"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/rk"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/theme"
)

func (s *Server) getMeta(w http.ResponseWriter, r *http.Request, c *caller) {
	m, err := s.Meta(r.Context(), c.actor)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, m)
}

// Meta is GET /api/v1/meta: installed apps and modules, models, dashboards, theme (with design tokens) and
// layout. apps lists apps and 0.4 units, modules lists modules and 0.4 units (a 0.4 unit is both), every
// model names its module (contract 0001).
func (s *Server) Meta(ctx context.Context, a engine.Actor) (map[string]any, error) {
	return s.metaFor(ctx, s.Engine, a)
}

// metaFor builds the /meta body from engine e's registry (the live engine, or a preview engine).
func (s *Server) metaFor(ctx context.Context, e *engine.Engine, a engine.Actor) (map[string]any, error) {
	reg := e.Reg()
	apps, modules := []any{}, []any{}
	for _, name := range reg.AppOrder() {
		ap := reg.App[name]
		dash := ap.Dashboards
		if dash == nil {
			dash = []string{}
		}
		if ap.IsApp() {
			mods := []string{}
			if ap.Kind == meta.KindLegacy {
				mods = append(mods, ap.Name)
			}
			for _, m := range meta.OrderedKeys(ap, "modules") {
				if reg.App[m] != nil {
					mods = append(mods, m)
				}
			}
			apps = append(apps, map[string]any{"name": ap.Name, "kind": ap.Kind, "version": ap.Version, "title": ap.Title, "menu": orEmpty(ap.Raw["menu"]),
				"dashboards": dash, "requires": ap.Requires, "optional": ap.Optional, "modules": mods})
		}
		if ap.IsModule() || ap.Kind == meta.KindSite {
			req := ap.Requires
			if req == nil {
				req = map[string]any{}
			}
			// installed_by: "config" when config.apps / config.modules names it, then the installed apps that bring it (contract 0032 §4)
			modules = append(modules, map[string]any{"name": ap.Name, "kind": ap.Kind, "version": ap.Version, "title": ap.Title, "requires": req,
				"installed_by": reg.InstalledBy(ap.Name)})
		}
	}
	models := map[string]any{}
	for _, m := range reg.InstalledModels() {
		raw := map[string]any{}
		for k, v := range m.Raw {
			raw[k] = v
		}
		raw["app"] = m.App
		raw["module"] = m.App
		if m.Overlay != "" {
			raw["overlay"] = m.Overlay
		}
		if m.IsSubmittable && m.Field("status") == nil {
			raw["status_field"] = true // records carry a derived status
		}
		raw["prints"] = printsMeta(reg, m.Name)
		raw["can"] = e.Allowed(a, m.Name) // model level: a conditional grant (if:) counts as true
		// contract 0027: a field hidden from the caller is not described to them either
		if fl, ok := raw["fields"].([]any); ok {
			kept := make([]any, 0, len(fl))
			for _, x := range fl {
				f := m.Field(meta.Str(meta.AsMap(x), "name"))
				if f != nil && e.FieldHidden(a, m, f) {
					continue
				}
				// read_only_for: the field is shown read-only to the caller, so a form does not offer what is dropped
				if fm := meta.AsMap(x); f != nil && fm != nil && e.FieldReadOnlyFor(a, m, f) {
					cp := make(map[string]any, len(fm)+1)
					for k, v := range fm {
						cp[k] = v
					}
					cp["read_only"] = true
					x = cp
				}
				kept = append(kept, x)
			}
			raw["fields"] = kept
		}
		models[m.Name] = raw
	}
	dashboards := map[string]any{}
	for name, d := range reg.Dashboards {
		if reg.Dashboard(name) != nil && e.DashboardAllowed(a, d) { // contract 0027: roles: hides it
			raw := map[string]any{}
			for k, v := range d.Raw {
				raw[k] = v
			}
			raw["app"] = d.App
			dashboards[name] = raw
		}
	}
	flows := map[string]any{}
	for name, f := range reg.Flows {
		if reg.IsInstalled(f.App) {
			raw := map[string]any{}
			for k, v := range f.Raw {
				raw[k] = v
			}
			raw["app"] = f.App
			flows[name] = raw
		}
	}
	imports := map[string]any{} // for the import wizard (contract 0012); site imports have app ""
	for name, im := range reg.Imports {
		if (im.App == "" || reg.IsInstalled(im.App)) && reg.Model(im.Model) != nil &&
			e.Can(ctx, a, im.Model, "import", nil) && e.Can(ctx, a, im.Model, "create", nil) {
			imports[name] = map[string]any{"name": name, "model": im.Model, "label": im.Label, "source": im.Source, "per": im.Per, "app": im.App}
		}
	}
	roles := map[string]any{}
	for _, an := range reg.AppOrder() {
		for _, rn := range reg.App[an].Roles {
			if d := reg.Docs[reg.App[an].DocKey("roles/"+rn+".rk")]; d != nil {
				raw := map[string]any{}
				for k, v := range meta.AsMap(d.Data) {
					raw[k] = v
				}
				raw["app"] = an
				roles[rn] = raw
			}
		}
	}
	// contract 0030 §3: the work screens the caller may open, and in which order (the first home: true is their home)
	work := e.Work(a)
	screens := map[string]any{}
	for _, w := range work {
		screens[w.Screen] = screenMeta(reg.Screen(w.Screen))
	}
	if work == nil {
		work = []engine.WorkItem{}
	}
	layout := s.layout(reg)
	out := map[string]any{
		"apps": apps, "modules": modules, "models": models, "dashboards": dashboards, "flows": flows, "imports": imports, "roles": roles,
		"screens": screens, "work": work,
		"connectors": connectorsMeta(reg), "site": siteMeta(reg),
		"theme": s.theme(reg), "layout": layout, "menu": defaultMenu(reg, layout),
		"installed": reg.AppOrder(), "org": a.Org,
		"config": siteConfig(reg), "capabilities": capabilityList(reg),
		"user":       map[string]any{"email": a.ID, "name": a.UserName, "roles": a.Roles},
		"changesets": s.changesetsMeta(a),
		"today":      e.Today(),
		"features":   s.aiFeatures(ctx, reg.SiteConfig("ai.enabled") != false),
	}
	return out, nil
}

// screenMeta is a screen as /meta and /screens describe it: the file plus the module that ships it.
func screenMeta(sc *meta.Screen) map[string]any {
	raw := map[string]any{}
	for k, v := range sc.Raw {
		raw[k] = v
	}
	delete(raw, "$schema")
	raw["module"] = sc.App
	return raw
}

// getScreen is GET /api/v1/screens/{name} (contract 0030): the screen and its set: values evaluated for the
// caller now (set: the block's own, sets: nested slots such as document and session of a counter). A screen the caller may not open is 404, like one that does not exist.
func (s *Server) getScreen(w http.ResponseWriter, r *http.Request, c *caller) {
	name := r.PathValue("name")
	sc := s.Engine.ScreenAllowed(c.actor, name)
	if sc == nil {
		fail(w, &engine.StatusError{Status: 404, Code: "not_found", Msg: "no work screen " + name})
		return
	}
	vars, err := s.screenVars(r, c, sc)
	if err != nil {
		fail(w, err)
		return
	}
	sets, err := s.Engine.ScreenSetsFor(c.actor, sc, vars)
	if err != nil {
		fail(w, err)
		return
	}
	set, _ := sets[""].(map[string]any)
	if set == nil {
		set = map[string]any{}
	}
	delete(sets, "")
	writeJSON(w, 200, map[string]any{"name": sc.Name, "screen": screenMeta(sc), "set": set, "sets": sets})
}

// screenVars loads the records a screen is opened for: ?stop=<model>/<name> (a route stop) and ?doc=<name> (the
// parent of a roll or handover, a record of its document model), each through engine.Get, so a record the caller
// may not read is a 404 like a missing one.
func (s *Server) screenVars(r *http.Request, c *caller, sc *meta.Screen) (map[string]any, error) {
	vars := map[string]any{}
	if st := r.URL.Query().Get("stop"); st != "" {
		model, name, ok := strings.Cut(st, "/")
		if !ok || model == "" || name == "" {
			return nil, &engine.StatusError{Status: 400, Code: "bad_request", Msg: "stop must be <model>/<name>"}
		}
		rec, err := s.screenRecord(r, c, model, name)
		if err != nil {
			return nil, err
		}
		vars["stop"] = rec
	}
	if d := r.URL.Query().Get("doc"); d != "" {
		model, _ := asMapAny(sc.Block()["document"])["model"].(string)
		if model == "" {
			return nil, &engine.StatusError{Status: 400, Code: "bad_request", Msg: "screen " + sc.Name + " has no document"}
		}
		rec, err := s.screenRecord(r, c, model, d)
		if err != nil {
			return nil, err
		}
		vars["doc"] = rec
	}
	return vars, nil
}

func asMapAny(x any) map[string]any { m, _ := x.(map[string]any); return m }

// screenRecord reads a record for a screen's set: through engine.Get (read permission, row scope, hidden fields).
// A record the caller may not read is 404 like a missing one (audit F4); a field hidden from the caller reads as
// its zero value, so a set: over it neither fails the screen nor learns the value (audit F5).
func (s *Server) screenRecord(r *http.Request, c *caller, model, name string) (map[string]any, error) {
	rec, err := s.Engine.Get(r.Context(), c.actor, model, name)
	var pe *engine.PermissionError
	if errors.As(err, &pe) {
		return nil, &engine.StatusError{Status: 404, Code: "not_found", Msg: model + " " + name + " not found"}
	}
	if err != nil {
		return nil, err
	}
	out := map[string]any(rec)
	if m := s.Engine.Reg().Model(model); m != nil {
		for _, f := range m.Fields {
			if _, ok := out[f.Name]; !ok {
				out[f.Name] = store.Zero(f)
			}
		}
	}
	return out, nil
}

// InstalledConnectors are the connectors a flow triggers or calls, or system.rk notify uses.
func InstalledConnectors(reg *meta.Registry) []string {
	used := map[string]bool{}
	for c := range connectorUsers(reg) {
		used[c] = true
	}
	for _, ch := range []string{"email", "push", "whatsapp"} {
		if c := meta.Str(meta.AsMap(reg.SystemConfig("notify."+ch)), "connector"); c != "" {
			used[c] = true
		}
	}
	// config.connectors switches a connector on before any flow uses it (contract 0036 §5)
	for _, c := range meta.StrList(reg.SiteConfig("connectors")) {
		used[c] = true
	}
	var out []string
	for c := range used {
		if reg.Connectors[c] != nil {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// connectorUsers maps each connector to the installed flows that trigger on it or call it, sorted by flow name.
func connectorUsers(reg *meta.Registry) map[string][]string {
	out := map[string][]string{}
	for _, f := range reg.Flows {
		if !reg.IsInstalled(f.App) {
			continue
		}
		used := map[string]bool{}
		var walk func(steps []any)
		walk = func(steps []any) {
			for _, st := range steps {
				sm := meta.AsMap(st)
				if c, ok := sm["call"].(string); ok {
					used[strings.SplitN(c, ".", 2)[0]] = true
				}
				walk(meta.AsList(sm["then"]))
				walk(meta.AsList(sm["else"]))
				walk(meta.AsList(sm["do"]))
			}
		}
		if head, _, ok := strings.Cut(f.On, "."); ok && reg.Models[head] == nil {
			used[head] = true
		}
		walk(meta.AsList(f.Raw["steps"]))
		for c := range used {
			out[c] = append(out[c], f.Name)
		}
	}
	for c := range out {
		sort.Strings(out[c])
	}
	return out
}

func connectorsMeta(reg *meta.Registry) map[string]any {
	out := map[string]any{}
	for _, name := range InstalledConnectors(reg) {
		c := meta.AsMap(reg.Connectors[name].Data)
		names := func(l any) []string {
			res := []string{}
			for _, x := range meta.AsList(l) {
				res = append(res, meta.Str(meta.AsMap(x), "name"))
			}
			return res
		}
		secrets := []string{}
		for _, l := range []any{meta.AsMap(c["auth"])["fields"], c["settings"]} {
			for _, x := range meta.AsList(l) {
				if meta.AsMap(x)["secret"] == true {
					secrets = append(secrets, meta.Str(meta.AsMap(x), "name"))
				}
			}
		}
		out[name] = map[string]any{"name": name, "label": c["label"], "version": c["version"], "vendor": c["vendor"],
			"triggers": names(c["triggers"]), "actions": names(c["actions"]), "secrets": secrets}
	}
	return out
}

var secretKey = regexp.MustCompile(`(?i)(password|secret|token|api_key|apikey|private_key|dsn|^url$|^key$)`)

// Redact hides secret-looking values (an ${ENV} reference stays, it is not a secret).
func Redact(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if s, ok := e.(string); ok && secretKey.MatchString(k) && !(strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}")) {
				out[k] = "***"
				continue
			}
			out[k] = Redact(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = Redact(e)
		}
		return out
	}
	return v
}

func siteMeta(reg *meta.Registry) map[string]any {
	out := map[string]any{"config": map[string]any{}, "features": map[string]any{}, "themes": []string{}, "layouts": []string{}, "system": map[string]any{}}
	if reg.Site == nil {
		return out
	}
	out["name"] = reg.Site.Name
	if reg.Site.Config != nil {
		out["config"] = reg.Site.Config
	}
	if reg.Site.Features != nil {
		out["features"] = reg.Site.Features
	}
	themes, layouts := []string{}, []string{}
	for n := range reg.Site.Themes {
		themes = append(themes, n)
	}
	for n := range reg.Site.Layouts {
		layouts = append(layouts, n)
	}
	sort.Strings(themes)
	sort.Strings(layouts)
	out["themes"], out["layouts"] = themes, layouts
	out["system"] = Redact(reg.Site.System)
	return out
}

func orEmpty(v any) any {
	if v == nil {
		return []any{}
	}
	return v
}

func capabilityList(reg *meta.Registry) []string {
	var out []string
	for c := range reg.Capabilities() {
		out = append(out, c)
	}
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out
}

// siteConfig is the client-safe part of config.rk.
func siteConfig(reg *meta.Registry) map[string]any {
	out := map[string]any{}
	if reg.Site == nil {
		return out
	}
	for _, k := range []string{"locale", "currency", "timezone", "fiscal_year_start", "tax", "plan", "theme", "layout"} {
		if v, ok := reg.Site.Config[k]; ok {
			out[k] = v
		}
	}
	return out
}

// template loads rangka-os/templates/<kind>/<name>.rk.
func (s *Server) template(kind, name string) map[string]any {
	if s.TemplatesDir == "" {
		return nil
	}
	for _, n := range []string{name, strings.ReplaceAll(name, "_", "-"), strings.ReplaceAll(name, "-", "_")} {
		if d, err := rk.Load(filepath.Join(s.TemplatesDir, kind, n+".rk")); err == nil {
			return meta.AsMap(d.Data)
		}
	}
	return nil
}

func merge(base, over map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		if bm, ok := out[k].(map[string]any); ok {
			if om, ok := v.(map[string]any); ok {
				out[k] = merge(bm, om)
				continue
			}
		}
		out[k] = v
	}
	return out
}

func (s *Server) resolve(reg *meta.Registry, kind, name string) map[string]any {
	var data map[string]any
	if reg.Site != nil {
		docs := reg.Site.Themes
		if kind == "layouts" {
			docs = reg.Site.Layouts
		}
		if d := docs[name]; d != nil {
			data = meta.AsMap(d.Data)
		}
	}
	if data == nil {
		data = s.template(kind, name)
	}
	if data == nil {
		return nil
	}
	if ext, _ := data["extends"].(string); strings.HasPrefix(ext, "rangka/") {
		if base := s.template(kind, strings.TrimPrefix(ext, "rangka/")); base != nil {
			delete(base, "name")
			delete(base, "label")
			data = merge(base, data)
		}
	}
	return data
}

func (s *Server) theme(reg *meta.Registry) map[string]any {
	name, _ := reg.SiteConfig("theme").(string)
	if name == "" {
		name = "standard"
	}
	th := s.resolve(reg, "themes", name)
	if th == nil {
		th = map[string]any{"name": name}
	}
	// colours in file order when the theme is a site file
	var light, dark map[string]string
	var over map[string]theme.Override
	if reg.Site != nil && reg.Site.Themes[name] != nil {
		light, dark, over = meta.ThemeColours(reg.Site.Themes[name])
	} else {
		color := meta.AsMap(th["color"])
		var keys []string
		for k := range color {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		light, dark, over = theme.Colours(color, keys)
	}
	out := map[string]any{"name": name, "mode": th["mode"], "tokens": map[string]any{"light": light, "dark": dark}, "overrides": over}
	if reg.Site != nil && reg.Site.Themes[name] != nil {
		out["path"] = "themes/" + name + ".rk"
	}
	for _, k := range []string{"label", "radius", "density", "font", "logo", "extends", "contrast_check"} {
		if v, ok := th[k]; ok {
			out[k] = v
		}
	}
	if out["mode"] == nil {
		out["mode"] = "system"
	}
	return out
}

// defaultMenu is /meta.menu (contract 0032 §5): one side group per module a composed site names and no installed app
// brings in, then the company tables as the last group "Perusahaan" (contract 0045 §5; also for app-only sites), when the
// layout sets no nav.side. Empty for layouts with nav.side and for sites with neither.
func defaultMenu(reg *meta.Registry, layout map[string]any) []meta.MenuGroup {
	if side, _ := meta.AsMap(layout["nav"])["side"].([]any); len(side) > 0 {
		return []meta.MenuGroup{}
	}
	if m := reg.DefaultMenu(); m != nil {
		return m
	}
	return []meta.MenuGroup{}
}

func (s *Server) layout(reg *meta.Registry) map[string]any {
	name, _ := reg.SiteConfig("layout").(string)
	if name == "" {
		return map[string]any{"name": "side_only", "shell": "side-only"}
	}
	l := s.resolve(reg, "layouts", name)
	if l == nil {
		return map[string]any{"name": name}
	}
	return l
}

// File is GET /files/{path}: {path, kind, layer, text, parsed} (and the v0 keys data, hash).
func (s *Server) File(path string) (map[string]any, error) {
	d := s.Engine.Reg().Docs[path]
	if d == nil {
		return nil, &engine.StatusError{Status: 404, Code: "not_found", Msg: "no metadata file " + path}
	}
	return map[string]any{"path": path, "kind": d.Kind(), "layer": layerOf(path), "text": d.Text, "parsed": d.Data,
		"data": d.Data, "hash": store.FileHash(d.Text), "sha": store.FileHash(d.Text)}, nil
}

func (s *Server) getFile(w http.ResponseWriter, r *http.Request, c *caller) {
	f, err := s.File(r.PathValue("path"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, f)
}

// listFiles is GET /files: [{path, kind, layer, app?, lines, sha}].
func (s *Server) listFiles(w http.ResponseWriter, r *http.Request, c *caller) {
	reg := s.Engine.Reg()
	var paths []string
	for p := range reg.Docs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := []any{}
	for _, p := range paths {
		d := reg.Docs[p]
		f := map[string]any{"path": p, "kind": d.Kind(), "layer": layerOf(p), "lines": strings.Count(strings.TrimSuffix(d.Text, "\n"), "\n") + 1, "sha": store.FileHash(d.Text)}
		if strings.HasPrefix(p, "apps/") {
			f["app"] = strings.SplitN(p, "/", 3)[1]
		} else if strings.HasPrefix(p, "modules/") {
			f["module"] = strings.SplitN(p, "/", 3)[1]
		}
		out = append(out, f)
	}
	writeJSON(w, 200, map[string]any{"data": out})
}

// layerOf is the change-set layer of a metadata path (changeset.Layer).
func layerOf(p string) int { return changeset.Layer(p) }
