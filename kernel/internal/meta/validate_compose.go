package meta

import (
	"fmt"
	"sort"
	"strings"
)

// Contract 0032 checks of config.rk, in rangka_validate.py check_config_modules / check_uninstalled_models order.

// configModules checks config.modules (keys in file order): each a module, in range, and allowed by the plan (plan
// is "" when the site names no known plan). It returns the keys that name a module.
func (v *validator) configModules(path string, rng map[string]any, keys []string, plan string) []string {
	var modNames []string
	for _, a := range v.reg.Apps {
		if a.Kind == KindModule {
			modNames = append(modNames, a.Name)
		}
	}
	var allowed map[string]bool
	if plan != "" {
		allowed = v.reg.planModules(v.reg.Site.Features, plan)
	}
	var out []string
	for _, k := range keys {
		where := "modules/" + k
		u := v.reg.App[k]
		switch {
		case k == SiteUnit:
			v.rep.extra("error", path, where, "unknown_module", fmt.Sprintf(`"%s" is the company module (contract 0016): it comes with the site and cannot be named`, k))
		case u != nil && u.IsApp():
			v.rep.extra("error", path, where, "unknown_module", fmt.Sprintf(`"%s" is an app, not a module; list it under apps`, k))
		case !contains(modNames, k):
			v.rep.extra("error", path, where, "unknown_module", fmt.Sprintf(`"%s" is not a module%s`, k, suggest(k, modNames)))
		case !Satisfies(u.Raw["version"], rng[k]):
			v.rep.extra("error", path, where, "version_mismatch", fmt.Sprintf(`"%s" is %s; this needs %s`, k, pyStr(u.Raw["version"]), pyStr(rng[k])))
		default:
			out = append(out, k)
			if allowed != nil && !allowed[k] {
				names := sortedKeys(toAny(allowed))
				list := strings.Join(names, ", ")
				if list == "" {
					list = "no modules"
				}
				v.rep.extra("error", path, where, "module_not_in_plan", fmt.Sprintf(`plan "%s" does not include module "%s" (it allows %s)`, plan, k, list))
			}
		}
	}
	return out
}

func toAny(m map[string]bool) map[string]any {
	out := map[string]any{}
	for k, b := range m {
		out[k] = b
	}
	return out
}

func uninstalledMsg(model, unit string) string {
	return fmt.Sprintf(`"%s" comes from module "%s", which this site does not install; add the module to config.modules`, model, unit)
}

// uninstalledModels reports uses_uninstalled_model on a site that names modules: overlays, company models and flows,
// and site dashboards naming a model of a unit the site does not install. Layouts and numbering report it where they
// check models.
func (v *validator) uninstalledModels(all map[string]vis, installed map[string]bool) {
	gone := func(x any) (string, bool) {
		mn, ok := x.(string)
		if !ok {
			return "", false
		}
		m, ok := all[mn]
		return m.app, ok && !installed[m.app]
	}
	say := func(p, where string, mn any) {
		unit, _ := gone(mn)
		v.rep.extra("error", p, where, "uses_uninstalled_model", uninstalledMsg(mn.(string), unit))
	}
	for _, st := range sortedKeysM(v.reg.Overlays) {
		d := v.reg.Overlays[st]
		if mn := asMap(d.Data)["model"]; d != nil {
			if _, g := gone(mn); g {
				say(d.Path, "model", mn)
			}
		}
	}
	if co := v.reg.App[SiteUnit]; co != nil && co.Kind == KindSite {
		models := append([]string{}, co.Models...)
		sort.Strings(models)
		for _, mn := range models {
			m := v.reg.Models[mn]
			if m == nil || m.Doc == nil {
				continue
			}
			for i, f := range asList(asMap(m.Doc.Data)["fields"]) {
				for _, key := range []string{"to", "of"} {
					if _, g := gone(asMap(f)[key]); g {
						say(m.Doc.Path, fmt.Sprintf("fields/%d/%s", i, key), asMap(f)[key])
					}
				}
			}
		}
	}
	for _, fn := range sortedKeysM(v.reg.Flows) {
		f := v.reg.Flows[fn]
		if f.App != SiteUnit || f.Doc == nil {
			continue
		}
		raw := asMap(f.Doc.Data)
		if on, ok := raw["on"].(string); ok {
			head, _, _ := strings.Cut(on, ".")
			if _, g := gone(head); g {
				say(f.Doc.Path, "on", head)
			}
		}
		v.flowStepsUninstalled(f.Doc.Path, "steps", raw["steps"], gone, say)
	}
	if s := v.reg.Site; s != nil {
		for _, dn := range sortedKeysM(s.Dashboards) {
			d := s.Dashboards[dn]
			for i, w := range asList(asMap(d.Doc.Data)["widgets"]) {
				if _, g := gone(asMap(w)["model"]); g {
					say(d.Doc.Path, fmt.Sprintf("widgets/%d/model", i), asMap(w)["model"])
				}
			}
		}
	}
}

func (v *validator) flowStepsUninstalled(p, at string, steps any, gone func(any) (string, bool), say func(string, string, any)) {
	for i, x := range asList(steps) {
		st, ok := x.(map[string]any)
		if !ok {
			continue
		}
		for _, k := range []string{"find", "find_all", "create"} {
			if _, g := gone(st[k]); g {
				say(p, fmt.Sprintf("%s/%d/%s", at, i, k), st[k])
			}
		}
		for _, k := range []string{"then", "else", "do"} {
			v.flowStepsUninstalled(p, fmt.Sprintf("%s/%d/%s", at, i, k), st[k], gone, say)
		}
	}
}
