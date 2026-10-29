package api

import (
	"context"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

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

// Meta is GET /api/v1/meta: installed apps, models, dashboards, theme (with design tokens) and layout.
func (s *Server) Meta(ctx context.Context, a engine.Actor) (map[string]any, error) {
	reg := s.Engine.Reg()
	apps := []any{}
	for _, name := range reg.AppOrder() {
		ap := reg.App[name]
		dash := ap.Dashboards
		if dash == nil {
			dash = []string{}
		}
		apps = append(apps, map[string]any{"name": ap.Name, "version": ap.Version, "title": ap.Title, "menu": orEmpty(ap.Raw["menu"]),
			"dashboards": dash, "requires": ap.Requires, "optional": ap.Optional})
	}
	models := map[string]any{}
	for _, m := range reg.InstalledModels() {
		raw := map[string]any{}
		for k, v := range m.Raw {
			raw[k] = v
		}
		raw["app"] = m.App
		if m.Overlay != "" {
			raw["overlay"] = m.Overlay
		}
		if m.IsSubmittable && m.Field("status") == nil {
			raw["status_field"] = true // records carry a derived status
		}
		models[m.Name] = raw
	}
	dashboards := map[string]any{}
	for name, d := range reg.Dashboards {
		if reg.Dashboard(name) != nil {
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
	roles := map[string]any{}
	for _, an := range reg.AppOrder() {
		for _, rn := range reg.App[an].Roles {
			if d := reg.Docs["apps/"+an+"/roles/"+rn+".rk"]; d != nil {
				raw := map[string]any{}
				for k, v := range meta.AsMap(d.Data) {
					raw[k] = v
				}
				raw["app"] = an
				roles[rn] = raw
			}
		}
	}
	out := map[string]any{
		"apps": apps, "models": models, "dashboards": dashboards, "flows": flows, "roles": roles,
		"connectors": connectorsMeta(reg), "site": siteMeta(reg),
		"theme": s.theme(reg), "layout": s.layout(reg),
		"installed": reg.AppOrder(), "org": a.Org,
		"config": siteConfig(reg), "capabilities": capabilityList(reg),
		"user":  map[string]any{"email": a.ID, "name": a.UserName, "roles": a.Roles},
		"today": s.Engine.Today(),
	}
	return out, nil
}

// InstalledConnectors are the connectors a flow triggers or calls, or system.rk notify uses.
func InstalledConnectors(reg *meta.Registry) []string {
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
		}
	}
	for _, f := range reg.Flows {
		if !reg.IsInstalled(f.App) {
			continue
		}
		if head, _, ok := strings.Cut(f.On, "."); ok && reg.Models[head] == nil {
			used[head] = true
		}
		walk(meta.AsList(f.Raw["steps"]))
	}
	for _, ch := range []string{"email", "push", "whatsapp"} {
		if c := meta.Str(meta.AsMap(reg.SystemConfig("notify."+ch)), "connector"); c != "" {
			used[c] = true
		}
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

func (s *Server) layout(reg *meta.Registry) map[string]any {
	name, _ := reg.SiteConfig("layout").(string)
	if name == "" {
		return map[string]any{"name": "side_only", "shell": "side"}
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
		}
		out = append(out, f)
	}
	writeJSON(w, 200, map[string]any{"data": out})
}

func layerOf(p string) int {
	switch {
	case p == "system.rk":
		return 2
	case strings.HasPrefix(p, "connectors/"):
		return 3
	case p == "features.rk" || p == "config.rk" || strings.HasPrefix(p, "custom/"):
		return 4
	case strings.HasPrefix(p, "apps/"):
		return 5
	}
	return 6
}
