package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// The catalogue (contract 0036 §3): every module, app and connector this kernel can install without a download,
// with plain words for the owner. Read only in this phase: installing (§4) and connecting (§5) come later.
// Agents read the catalogue through MCP rangka_catalogue (contract 0035, meta.Registry.Catalogue); this route serves
// the screens of the web app and the Console.

// ErrCatalogueForbidden is the answer for a caller who may not propose change sets.
var ErrCatalogueForbidden = errors.New("Katalog hanya untuk yang boleh mengusulkan perubahan")

// mayReadCatalogue is the read rule of §3: /meta.changesets.propose, or a delegated assistant token (auth Via ai)
// whose person may propose. Plain API tokens and OIDC clients are not people and get 403.
func (s *Server) mayReadCatalogue(a engine.Actor, assistant bool) bool {
	if a.Org == "" {
		return false
	}
	if assistant {
		a.Human = true // the person the assistant acts for
	}
	return s.changesetsMeta(a)["propose"] == true
}

func (s *Server) getCatalogue(w http.ResponseWriter, r *http.Request, c *caller) {
	if !s.mayReadCatalogue(c.actor, c.p.Via == "ai") {
		writeError(w, 403, "forbidden", ErrCatalogueForbidden.Error())
		return
	}
	writeJSON(w, 200, s.Catalogue(r.Context()))
}

// Catalogue builds {modules, apps, connectors}. It holds no secret values and no file contents.
func (s *Server) Catalogue(ctx context.Context) map[string]any {
	reg := s.Engine.Reg()
	return map[string]any{"modules": catalogueUnits(reg, false), "apps": catalogueUnits(reg, true),
		"connectors": s.catalogueConnectors(ctx, reg)}
}

// catalogueKeys reads summary, category and icon from a manifest (or from experimental: until the schema ships, 0000 §4).
func catalogueKeys(raw map[string]any) (summary, category, icon any) {
	exp := meta.AsMap(raw["experimental"])
	pick := func(k string) any {
		if v, ok := raw[k]; ok && v != nil {
			return v
		}
		return exp[k]
	}
	return pick("summary"), pick("category"), pick("icon")
}

func catalogueUnits(reg *meta.Registry, apps bool) []any {
	cfgApps, listed := reg.SiteConfig("apps").([]any)
	inConfig := map[string]bool{}
	for _, x := range cfgApps {
		if n, ok := x.(string); ok {
			inConfig[n] = true
		}
	}
	plan, _ := reg.SiteConfig("plan").(string)
	planApps := reg.PlanApps()
	inPlan := map[string]bool{}
	for _, n := range reg.Closure(planApps) {
		inPlan[n] = true
	}
	// who brings each installed unit in: installed apps whose closure holds it
	broughtBy := map[string][]string{}
	for _, a := range reg.Apps {
		if !a.IsApp() || !reg.IsInstalled(a.Name) {
			continue
		}
		for _, n := range reg.Closure([]string{a.Name}) {
			if n != a.Name {
				broughtBy[n] = append(broughtBy[n], a.Name)
			}
		}
	}
	out := []any{}
	for _, a := range reg.Apps {
		// a 0.4 unit is both a module and an app: it is listed once, as an app
		if (apps && !a.IsApp()) || (!apps && a.Kind != meta.KindModule) || a.Kind == meta.KindSite {
			continue
		}
		kind := "module"
		if apps {
			kind = "app"
		}
		installed := reg.IsInstalled(a.Name)
		by := []string{}
		if installed {
			switch {
			case inConfig[a.Name]:
				by = append(by, "config")
			case !listed && planApps != nil && a.IsApp():
				by = append(by, "plan")
			case !listed && planApps == nil && a.IsApp():
				by = append(by, "default")
			}
			by = append(by, broughtBy[a.Name]...)
		}
		requires := []string{}
		requires = append(requires, meta.OrderedKeys(a, "modules")...)
		for _, n := range meta.OrderedKeys(a, "requires") {
			if reg.App[n] != nil {
				requires = append(requires, n)
			}
		}
		requiredBy := []string{}
		for _, b := range reg.Apps {
			if b.Name == a.Name || !reg.IsInstalled(b.Name) {
				continue
			}
			if _, ok := b.Modules[a.Name]; ok {
				requiredBy = append(requiredBy, b.Name)
			} else if _, ok := b.Requires[a.Name]; ok {
				requiredBy = append(requiredBy, b.Name)
			}
		}
		models := []any{}
		for _, mn := range a.Models {
			if m := reg.Models[mn]; m != nil && !m.IsChild {
				models = append(models, map[string]any{"name": mn, "label": m.Label})
			}
		}
		summary, category, icon := catalogueKeys(a.Raw)
		title := any(a.Title)
		if a.Title == nil {
			title = map[string]any{"id": a.Name}
		}
		out = append(out, map[string]any{
			"name": a.Name, "kind": kind, "title": title, "summary": summary, "category": categoryOr(category), "icon": icon,
			"version": a.Version, "installed": installed, "installed_by": by, "requires": requires, "required_by": requiredBy,
			"models": models, "in_plan": planApps == nil || inPlan[a.Name], "plan": plan, "source": "directory",
		})
	}
	return out
}

func categoryOr(c any) any {
	if s, ok := c.(string); ok && s != "" {
		return s
	}
	return "other"
}

func (s *Server) catalogueConnectors(ctx context.Context, reg *meta.Registry) []any {
	installed := map[string]bool{}
	for _, n := range InstalledConnectors(reg) {
		installed[n] = true
	}
	health := map[string]map[string]any{}
	for _, x := range s.connectorHealth(ctx) {
		h := meta.AsMap(x)
		health[meta.Str(h, "name")] = h
	}
	users := connectorUsers(reg)
	ports := meta.AsMap(reg.SystemConfig("ports"))
	names := make([]string, 0, len(reg.Connectors))
	for n := range reg.Connectors {
		names = append(names, n)
	}
	sort.Strings(names)
	out := []any{}
	for _, name := range names {
		c := meta.AsMap(reg.Connectors[name].Data)
		exp := meta.AsMap(c["experimental"])
		summary, category, icon := catalogueKeys(c)
		// auth fields: name, label, secret and set. No value is ever included; set stays false until the secrets
		// API of contract 0017 exists.
		auth, needsAuth := []any{}, false
		for _, x := range meta.AsList(meta.AsMap(c["auth"])["fields"]) {
			f := meta.AsMap(x)
			required := f["required"] != false // an auth field is needed unless it says required: false
			auth = append(auth, map[string]any{"name": meta.Str(f, "name"), "label": f["label"], "secret": f["secret"] == true,
				"required": required, "set": false})
			needsAuth = needsAuth || required
		}
		provides := []string{}
		pv := meta.AsMap(c["provides"])
		if pv == nil {
			pv = meta.AsMap(exp["provides"])
		}
		for p := range pv {
			provides = append(provides, p)
		}
		sort.Strings(provides)
		selected := []string{}
		for p, v := range ports {
			if v == name {
				selected = append(selected, p)
			}
		}
		sort.Strings(selected)
		hostsAny := c["hosts"]
		if hostsAny == nil {
			hostsAny = exp["hosts"]
		}
		hosts := meta.StrList(hostsAny)
		if hosts == nil {
			hosts = requestHosts(c)
		}
		money := c["money"] == true || exp["money"] == true
		usedBy := users[name]
		if usedBy == nil {
			usedBy = []string{}
		}
		h := health[name]
		errs, last := any(int64(0)), any(nil)
		if h != nil {
			errs, last = h["errors_24h"], h["last_event_at"]
		}
		state := "available"
		if installed[name] {
			switch {
			case h != nil && h["ok"] == false:
				state = "error"
			case needsAuth:
				state = "needs_auth"
			default:
				state = "ready"
			}
		}
		out = append(out, map[string]any{
			"name": name, "label": c["label"], "summary": summary, "category": categoryOr(category), "icon": icon,
			"vendor": c["vendor"], "docs": c["docs"], "version": c["version"], "company": strings.HasPrefix(name, "x_"),
			"money": money, "hosts": hosts, "provides": provides, "ports_selected": selected, "used_by": usedBy,
			"auth": auth, "installed": installed[name], "state": state, "errors_24h": errs, "last_event_at": last,
		})
	}
	return out
}

// requestHosts lists the hosts of the connector's request URLs, skipping templated hosts (${...}, {{...}}).
func requestHosts(c map[string]any) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, list := range []any{c["actions"], c["triggers"]} {
		for _, x := range meta.AsList(list) {
			raw := meta.Str(meta.AsMap(meta.AsMap(x)["request"]), "url")
			u, err := url.Parse(raw)
			if err != nil || u.Host == "" || strings.ContainsAny(u.Host, "${}") || seen[u.Host] {
				continue
			}
			seen[u.Host] = true
			out = append(out, u.Hostname())
		}
	}
	sort.Strings(out)
	return out
}
