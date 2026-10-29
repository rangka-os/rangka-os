package meta

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
	"github.com/rangka-os/rangka-os/kernel/internal/theme"
)

// Pass 3 of rangka_validate.py (v0.1): flows, roles, connectors, the site and company overlays. The order
// and messages follow validate_more() so the output stays byte-identical.

var flowEvents = []string{"created", "updated", "submitted", "cancelled", "deleted"}

func (v *validator) schemaErr(kind string, d *rk.Doc) {
	for _, e := range v.schemaErrors(kind, d, true) {
		v.rep.extra("error", d.Path, e.PathString(), "schema", truncMsg(e.Message))
	}
}

func (v *validator) parens(p, where string, x any) {
	if s, ok := x.(string); ok && strings.Count(s, "(") != strings.Count(s, ")") {
		v.rep.extra("error", p, where, "cel_syntax", fmt.Sprintf("unbalanced parentheses in `%s`", s))
	}
}

// allModels is every model as other files see it: with the fields of its company overlay appended
// (merge_overlay in rangka_validate.py), so links, dashboards, flows and other overlays can use x_ fields.
func (v *validator) allModels() map[string]vis {
	all := map[string]vis{}
	for _, a := range v.reg.Apps {
		for _, mn := range a.Models {
			own := v.reg.Models[mn]
			all[mn] = vis{a.Name, v.withOverlayFields(own, mn), own}
		}
	}
	return all
}

func (v *validator) withOverlayFields(m *Model, name string) *Model {
	d := v.reg.Overlays[name]
	if d == nil || v.reg.Site == nil {
		return m
	}
	o, ok := d.Data.(map[string]any)
	if !ok || o["model"] != name {
		return m
	}
	names := map[string]bool{}
	for _, f := range asList(m.Raw["fields"]) {
		if fm, ok := f.(map[string]any); ok {
			names[pyStr(fm["name"])] = true
			if fm["name"] == nil {
				names["None"] = true
			}
		}
	}
	var extra []any
	for _, f := range asList(o["fields"]) {
		fm, ok := f.(map[string]any)
		if !ok {
			continue
		}
		if _, has := fm["name"]; has && !names[pyStr(fm["name"])] {
			extra = append(extra, fm)
		}
	}
	if len(extra) == 0 {
		return m
	}
	raw := map[string]any{}
	for k, x := range m.Raw {
		raw[k] = x
	}
	raw["fields"] = append(append([]any{}, asList(m.Raw["fields"])...), extra...)
	cp := *m
	cp.Raw, cp.byName = raw, nil
	return &cp
}

func (v *validator) more() {
	all := v.allModels()
	conns := v.reg.Connectors
	for _, fn := range sortedKeysM(v.reg.Flows) {
		f := v.reg.Flows[fn]
		if v.skip(f.App) {
			continue
		}
		p := f.Doc.Path
		v.schemaErr("flow", f.Doc)
		if got, ok := f.Raw["name"].(string); !ok || got != fn {
			v.rep.extra("error", p, "name", "name_mismatch", fmt.Sprintf(`flow name "%s" must match the file name "%s"`, pyStr(f.Raw["name"]), fn))
		}
		if !truthy(f.Raw["on"]) && !truthy(f.Raw["schedule"]) {
			v.rep.extra("warn", p, "(root)", "never_runs", "a flow needs on: (an event) or schedule: (cron)")
		}
		if on, ok := f.Raw["on"].(string); ok && strings.Contains(on, ".") {
			head, ev, _ := strings.Cut(on, ".")
			if _, isModel := all[head]; isModel {
				if !contains(flowEvents, ev) {
					v.rep.extra("error", p, "on", "unknown_event", fmt.Sprintf(`"%s" is not an event of model %s (%s)`, ev, head, strings.Join(flowEvents, ", ")))
				}
			} else if len(conns) > 0 {
				if c := conns[head]; c == nil {
					v.rep.extra("error", p, "on", "unknown_connector", fmt.Sprintf(`"%s" is not a connector%s`, head, suggest(head, keys(conns))))
				} else if !contains(namesOf(asMap(c.Data)["triggers"]), ev) {
					v.rep.extra("error", p, "on", "unknown_trigger", fmt.Sprintf(`connector %s has no trigger "%s"`, head, ev))
				}
			}
		}
		v.parens(p, "if", f.Raw["if"])
		v.steps(p, "steps", f.Raw["steps"], all)
	}
	for _, a := range v.reg.Apps {
		if v.skip(a.Name) {
			continue
		}
		for _, rn := range a.Roles {
			d := v.reg.Docs["apps/"+a.Name+"/roles/"+rn+".rk"]
			if d == nil {
				continue
			}
			v.schemaErr("role", d)
			if got, ok := asMap(d.Data)["name"].(string); !ok || got != rn {
				v.rep.extra("error", d.Path, "name", "name_mismatch", fmt.Sprintf(`role name "%s" must match the file name "%s"`, pyStr(asMap(d.Data)["name"]), rn))
			}
		}
	}
	for _, cn := range sortedKeysM(conns) {
		d := conns[cn]
		v.schemaErr("connector", d)
		if got, ok := asMap(d.Data)["name"].(string); !ok || got != cn {
			v.rep.extra("error", d.Path, "name", "name_mismatch", fmt.Sprintf(`connector name "%s" must match the directory name "%s"`, pyStr(asMap(d.Data)["name"]), cn))
		}
	}
	if v.reg.Site != nil {
		v.site(all)
		v.overlays(all)
	}
}

func truthy(x any) bool {
	switch t := x.(type) {
	case nil:
		return false
	case string:
		return t != ""
	case bool:
		return t
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	case int64:
		return t != 0
	case float64:
		return t != 0
	}
	return true
}

func namesOf(l any) []string {
	var out []string
	for _, x := range asList(l) {
		out = append(out, pyStr(asMap(x)["name"]))
	}
	return out
}

func (v *validator) steps(p, at string, steps any, all map[string]vis) {
	list, ok := steps.([]any)
	if !ok {
		return
	}
	conns := v.reg.Connectors
	for i, sv := range list {
		st, ok := sv.(map[string]any)
		if !ok {
			continue
		}
		w := fmt.Sprintf("%s/%d", at, i)
		for _, k := range []string{"find", "find_all", "create"} {
			mn, ok := st[k].(string)
			if !ok {
				continue
			}
			x, exists := all[mn]
			if !exists {
				v.rep.extra("error", p, w+"/"+k, "unknown_model", fmt.Sprintf(`"%s" is not a model%s`, mn, suggest(mn, keys(all))))
			} else if set, ok := st["set"].(map[string]any); ok {
				known := union(fieldNames(x.m), SystemFields)
				for _, fk := range sortedKeys(set) {
					if !known[fk] {
						v.rep.extra("error", p, w+"/set/"+fk, "unknown_field", fmt.Sprintf(`"%s" is not a field of %s`, fk, mn))
					}
				}
			}
		}
		if call, ok := st["call"].(string); ok && strings.Contains(call, ".") && len(conns) > 0 {
			cn, act, _ := strings.Cut(call, ".")
			if c := conns[cn]; c == nil {
				v.rep.extra("error", p, w+"/call", "unknown_connector", fmt.Sprintf(`"%s" is not a connector%s`, cn, suggest(cn, keys(conns))))
			} else if !contains(namesOf(asMap(c.Data)["actions"]), act) {
				v.rep.extra("error", p, w+"/call", "unknown_action", fmt.Sprintf(`connector %s has no action "%s"`, cn, act))
			}
		}
		for _, k := range []string{"where", "if"} {
			v.parens(p, w+"/"+k, st[k])
		}
		if set, ok := st["set"].(map[string]any); ok {
			for _, fk := range sortedKeys(set) {
				v.parens(p, w+"/set/"+fk, set[fk])
			}
		}
		for _, k := range []string{"then", "else"} {
			v.steps(p, w+"/"+k, st[k], all)
		}
	}
}

// templateExists looks for rangka-os/templates/<kind>/<name>.rk (true when the templates dir is unknown).
func templateExists(kind, name string, dir string) bool {
	if dir == "" {
		return true
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return true
	}
	for _, n := range []string{name, strings.ReplaceAll(name, "_", "-"), strings.ReplaceAll(name, "-", "_")} {
		if _, err := os.Stat(filepath.Join(dir, kind, n+".rk")); err == nil {
			return true
		}
	}
	return false
}

// TemplatesDir finds rangka-os/templates from an apps dir (workspace layout), or "".
func TemplatesDir(appsDir string) string {
	if env := os.Getenv("RANGKA_TEMPLATES"); env != "" {
		return env
	}
	abs, _ := filepath.Abs(appsDir)
	for d := abs; d != filepath.Dir(d); d = filepath.Dir(d) {
		if st, err := os.Stat(filepath.Join(d, "rangka-os", "templates")); err == nil && st.IsDir() {
			return filepath.Join(d, "rangka-os", "templates")
		}
	}
	return ""
}

func planApps(features map[string]any, plan string) []string {
	plans := map[string]map[string]any{}
	for _, x := range asList(features["plans"]) {
		if pm := asMap(x); pm != nil {
			plans[pyStr(pm["name"])] = pm
		}
	}
	var apps []string
	seen := map[string]bool{}
	name := plan
	for depth := 0; name != "" && plans[name] != nil && depth < 10 && !seen[name]; depth++ {
		seen[name] = true
		for _, a := range strList(plans[name]["apps"]) {
			if !contains(apps, a) {
				apps = append(apps, a)
			}
		}
		name, _ = plans[name]["extends"].(string)
	}
	return apps
}

func (v *validator) site(all map[string]vis) {
	s := v.reg.Site
	for _, key := range sortedKeysM(s.Docs) {
		d := s.Docs[key]
		kind := d.Kind()
		if kind == "" {
			if strings.Contains(key, "/") {
				kind = strings.TrimSuffix(strings.SplitN(key, "/", 2)[0], "s")
			} else {
				kind = strings.TrimSuffix(key, ".rk")
			}
		}
		if v.S.Has(kind) {
			v.schemaErr(kind, d)
		}
		if kind == "theme" || kind == "layout" {
			stem := strings.TrimSuffix(strings.SplitN(key, "/", 2)[1], ".rk")
			if got, ok := asMap(d.Data)["name"].(string); !ok || got != stem {
				v.rep.extra("error", d.Path, "name", "name_mismatch", fmt.Sprintf(`%s name "%s" must match the file name "%s"`, kind, pyStr(asMap(d.Data)["name"]), stem))
			}
		}
	}
	apps := v.reg.App
	appNames := keys(apps)
	plans := map[string]bool{}
	var planNames []string
	feat := s.Docs["features.rk"]
	if feat != nil {
		for _, x := range asList(s.Features["plans"]) {
			if pm := asMap(x); pm != nil {
				plans[pyStr(pm["name"])] = true
				planNames = append(planNames, pyStr(pm["name"]))
			}
		}
		for i, x := range asList(s.Features["plans"]) {
			for j, a := range asList(asMap(x)["apps"]) {
				as := pyStr(a)
				if apps[as] == nil {
					v.rep.extra("error", feat.Path, fmt.Sprintf("plans/%d/apps/%d", i, j), "unknown_app", fmt.Sprintf(`"%s" is not an app in %s%s`, as, v.reg.AppsDir, suggest(as, appNames)))
				}
			}
		}
		for i, x := range asList(s.Features["plans"]) {
			if ext, ok := asMap(x)["extends"].(string); ok && ext != "" && !plans[ext] {
				v.rep.extra("error", feat.Path, fmt.Sprintf("plans/%d/extends", i), "unknown_plan", fmt.Sprintf(`plan "%s" does not exist`, ext))
			}
		}
	}
	installed := map[string]bool{}
	for _, a := range appNames {
		installed[a] = true
	}
	tdir := TemplatesDir(v.reg.AppsDir)
	if cfg := s.Docs["config.rk"]; cfg != nil {
		c := s.Config
		plan, _ := c["plan"].(string)
		var allowed []string
		hasAllowed := plan != "" && plans[plan]
		if hasAllowed {
			allowed = planApps(s.Features, plan)
		}
		if plan != "" && feat != nil && !plans[plan] {
			v.rep.extra("error", cfg.Path, "plan", "unknown_plan", fmt.Sprintf(`plan "%s" is not in features.rk%s`, plan, suggest(plan, planNames)))
		}
		if list, ok := c["apps"].([]any); ok {
			installed = map[string]bool{}
			var listed []string
			for _, x := range list {
				listed = append(listed, pyStr(x))
			}
			for i, a := range listed {
				if apps[a] == nil {
					v.rep.extra("error", cfg.Path, fmt.Sprintf("apps/%d", i), "unknown_app", fmt.Sprintf(`"%s" is not an app in %s%s`, a, v.reg.AppsDir, suggest(a, appNames)))
					continue
				}
				installed[a] = true
				if hasAllowed && !contains(allowed, a) {
					v.rep.extra("error", cfg.Path, fmt.Sprintf("apps/%d", i), "app_not_in_plan", fmt.Sprintf(`plan "%s" does not include "%s" (it allows %s)`, plan, a, strings.Join(allowed, ", ")))
				}
				for _, dep := range orderedKeys(apps[a]) {
					if apps[dep] != nil && !contains(listed, dep) {
						v.rep.extra("error", cfg.Path, fmt.Sprintf("apps/%d", i), "missing_dependency", fmt.Sprintf(`"%s" requires "%s"; add it to apps`, a, dep))
					}
				}
			}
		} else if hasAllowed {
			installed = map[string]bool{}
			for _, a := range allowed {
				if apps[a] != nil {
					installed[a] = true
				}
			}
		}
		if th, _ := c["theme"].(string); th != "" && s.Themes[th] == nil && !templateExists("themes", th, tdir) {
			v.rep.extra("error", cfg.Path, "theme", "unknown_theme", fmt.Sprintf(`theme "%s" has no file themes/%s.rk%s`, th, th, suggest(th, keysDoc(s.Themes))))
		}
		if ly, _ := c["layout"].(string); ly != "" && s.Layouts[ly] == nil && !templateExists("layouts", ly, tdir) {
			v.rep.extra("error", cfg.Path, "layout", "unknown_layout", fmt.Sprintf(`layout "%s" has no file layouts/%s.rk%s`, ly, ly, suggest(ly, keysDoc(s.Layouts))))
		}
		for _, mn := range sortedKeys(asMap(c["numbering"])) {
			if x, ok := all[mn]; !ok {
				v.rep.extra("error", cfg.Path, "numbering/"+mn, "unknown_model", fmt.Sprintf(`"%s" is not a model%s`, mn, suggest(mn, keys(all))))
			} else if !installed[x.app] {
				v.rep.extra("warn", cfg.Path, "numbering/"+mn, "not_installed", fmt.Sprintf(`"%s" comes from app "%s", which this site does not install`, mn, x.app))
			}
		}
	}
	for _, tn := range sortedKeysM(s.Themes) {
		d := s.Themes[tn]
		data := asMap(d.Data)
		if ext, _ := data["extends"].(string); strings.HasPrefix(ext, "rangka/") && !templateExists("themes", strings.TrimPrefix(ext, "rangka/"), tdir) {
			v.rep.extra("warn", d.Path, "extends", "unknown_template", fmt.Sprintf(`no theme template "%s" in rangka-os/templates/themes`, ext))
		}
		if cc, ok := data["contrast_check"].(bool); !ok || cc {
			light, dark, over := ThemeColours(d)
			for _, pr := range theme.Check(light, dark, over) {
				v.rep.extra("error", d.Path, "color/"+pr.Token, "low_contrast", pr.Msg)
			}
		}
	}
	dashboards := map[string]*Dashboard{}
	for n, d := range v.reg.Dashboards {
		dashboards[n] = d
	}
	for _, ln := range sortedKeysM(s.Layouts) {
		v.layout(s.Layouts[ln].Path, asMap(s.Layouts[ln].Data), all, dashboards, installed)
	}
}

// orderedKeys returns an app's requires in file order.
func orderedKeys(a *App) []string {
	if a.Doc == nil || a.Doc.Root == nil {
		return sortedKeys(a.Requires)
	}
	var out []string
	if n := a.Doc.Root.Get("requires"); n != nil && n.Kind == rk.MapNode {
		for _, k := range n.Keys {
			out = append(out, k.Value)
		}
	}
	return out
}

// ThemeColours applies a theme document's colours (in file order) to the base tokens.
func ThemeColours(d *rk.Doc) (map[string]string, map[string]string, map[string]theme.Override) {
	color := asMap(asMap(d.Data)["color"])
	var order []string
	if d.Root != nil {
		if n := d.Root.Get("color"); n != nil && n.Kind == rk.MapNode {
			for _, k := range n.Keys {
				if !contains(order, k.Value) {
					order = append(order, k.Value)
				}
			}
		}
	}
	if order == nil {
		order = sortedKeys(color)
	}
	return theme.Colours(color, order)
}

func keysDoc[V any](m map[string]V) []string { return sortedKeysM(m) }

func (v *validator) layout(p string, l map[string]any, all map[string]vis, dashboards map[string]*Dashboard, installed map[string]bool) {
	target := func(where string, to any, cond string) {
		ts, ok := to.(string)
		if !ok || !strings.Contains(ts, ":") {
			return
		}
		kind, name, _ := strings.Cut(ts, ":")
		switch kind {
		case "model":
			x, ok := all[name]
			if !ok {
				v.rep.extra("error", p, where, "unknown_model", fmt.Sprintf(`"%s" is not a model%s`, name, suggest(name, keys(all))))
			} else if !installed[x.app] && !strings.Contains(cond, "app_installed('"+x.app+"')") {
				v.rep.extra("error", p, where, "optional_model", fmt.Sprintf(`"%s" comes from app "%s", which this site does not install; add  if: app_installed('%s')`, name, x.app, x.app))
			}
		case "dashboard":
			if dashboards[name] == nil {
				v.rep.extra("error", p, where, "unknown_dashboard", fmt.Sprintf(`"%s" is not a dashboard%s`, name, suggest(name, keys(dashboards))))
			}
		}
	}
	if truthy(l["home"]) {
		target("home", l["home"], "")
	}
	nav := asMap(l["nav"])
	for _, sec := range sortedKeys(nav) {
		for i, it := range asList(nav[sec]) {
			switch x := it.(type) {
			case map[string]any:
				cond, _ := x["if"].(string)
				target(fmt.Sprintf("nav/%s/%d/to", sec, i), x["to"], cond)
			case string:
				if _, ok := all[x]; sec == "create" && !ok {
					v.rep.extra("error", p, fmt.Sprintf("nav/%s/%d", sec, i), "unknown_model", fmt.Sprintf(`"%s" is not a model%s`, x, suggest(x, keys(all))))
				}
			}
		}
	}
	var blocks func(where string, bl any)
	blocks = func(where string, bl any) {
		for i, b := range asList(bl) {
			bm, ok := b.(map[string]any)
			if !ok {
				continue
			}
			at := fmt.Sprintf("%s/%d", where, i)
			props := asMap(bm["props"])
			if dn := pyStrOr(props["dashboard"]); dn != "" {
				if d := dashboards[dn]; d == nil {
					v.rep.extra("error", p, at+"/props/dashboard", "unknown_dashboard", fmt.Sprintf(`"%s" is not a dashboard%s`, dn, suggest(dn, keys(dashboards))))
				} else if wn := pyStrOr(props["widget"]); wn != "" {
					var names []string
					for _, w := range asList(d.Raw["widgets"]) {
						names = append(names, pyStr(asMap(w)["name"]))
					}
					if !contains(names, wn) {
						sort.Strings(names)
						v.rep.extra("error", p, at+"/props/widget", "unknown_widget", fmt.Sprintf(`dashboard %s has no widget "%s"%s`, dn, wn, suggest(wn, names)))
					}
				}
			}
			if mn := pyStrOr(props["model"]); mn != "" {
				if _, ok := all[mn]; !ok {
					v.rep.extra("error", p, at+"/props/model", "unknown_model", fmt.Sprintf(`"%s" is not a model%s`, mn, suggest(mn, keys(all))))
				}
			}
			blocks(at+"/children", bm["children"])
		}
	}
	for i, pg := range asList(l["pages"]) {
		blocks(fmt.Sprintf("pages/%d/blocks", i), asMap(pg)["blocks"])
	}
}

func pyStrOr(x any) string {
	if !truthy(x) {
		return ""
	}
	return pyStr(x)
}

func (v *validator) overlays(all map[string]vis) {
	for _, st := range sortedKeysM(v.reg.Overlays) {
		d := v.reg.Overlays[st]
		p := d.Path
		o := asMap(d.Data)
		v.schemaErr("overlay", d)
		mn, _ := o["model"].(string)
		if mn != st || o["model"] == nil {
			v.rep.extra("error", p, "model", "name_mismatch", fmt.Sprintf(`overlay model "%s" must match the file name "%s"`, pyStr(o["model"]), st))
		}
		x, ok := all[mn]
		if !ok || o["model"] == nil {
			v.rep.extra("error", p, "model", "unknown_model", fmt.Sprintf(`"%s" is not a model%s`, pyStr(o["model"]), suggest(mn, keys(all))))
			continue
		}
		a := v.reg.App[x.app]
		visible := map[string]vis{}
		for k, y := range all {
			_, r := a.Requires[y.app]
			_, op := a.Optional[y.app]
			if y.app == x.app || r || op {
				visible[k] = y
			}
		}
		own := x.own
		base := fieldMap(own)
		merged := map[string]map[string]any{}
		var mergedList []map[string]any
		for _, f := range asList(own.Raw["fields"]) {
			if fm := asMap(f); fm != nil {
				if _, ok := fm["name"]; ok {
					mergedList = append(mergedList, fm)
				}
			}
		}
		for k, f := range base {
			merged[k] = f
		}
		for _, f := range asList(o["fields"]) {
			fm := asMap(f)
			if fm == nil {
				continue
			}
			if _, ok := fm["name"]; !ok {
				continue
			}
			if _, dup := merged[pyStr(fm["name"])]; !dup {
				merged[pyStr(fm["name"])] = fm
				mergedList = append(mergedList, fm)
			}
		}
		mergedNames := map[string]bool{}
		for k := range merged {
			mergedNames[k] = true
		}
		known := union(mergedNames, SystemFields)
		seen := map[string]bool{}
		for k := range base {
			seen[k] = true
		}
		for i, fv := range asList(o["fields"]) {
			f, ok := fv.(map[string]any)
			if !ok {
				continue
			}
			n := pyStr(f["name"])
			if seen[n] {
				v.rep.extra("error", p, fmt.Sprintf("fields/%d", i), "duplicate_field", fmt.Sprintf(`"%s" is already a field of %s`, n, mn))
			}
			seen[n] = true
			for _, key := range []string{"to", "of"} {
				if t, _ := f[key].(string); t != "" {
					if _, ok := visible[t]; !ok {
						v.rep.extra("error", p, fmt.Sprintf("fields/%d/%s", i, key), "unknown_model", fmt.Sprintf(`"%s" is not a model in this app or its dependencies%s`, t, suggest(t, keys(visible))))
					}
				}
			}
			for _, pk := range asList(f["peek"]) {
				to, _ := f["to"].(string)
				if tm, ok := visible[to]; ok && !union(fieldNames(tm.m), SystemFields)[pyStr(pk)] {
					v.rep.extra("error", p, fmt.Sprintf("fields/%d/peek", i), "unknown_field", fmt.Sprintf(`"%s" is not a field of %s`, pyStr(pk), to))
				}
			}
			if rt, ok := f["relative_to"]; ok && truthy(rt) {
				if _, ok := merged[pyStr(rt)]; !ok {
					v.rep.extra("error", p, fmt.Sprintf("fields/%d/relative_to", i), "unknown_field", fmt.Sprintf(`"%s" is not a field`, pyStr(rt)))
				}
			}
			for _, key := range []string{"default", "compute", "depends_on"} {
				if e, ok := f[key]; ok {
					v.exprTo(true, p, fmt.Sprintf("fields/%d/%s", i, key), e, merged, visible)
				}
			}
		}
		for i, r := range asList(o["rules"]) {
			rm, ok := r.(map[string]any)
			if !ok {
				continue
			}
			chk, ok := rm["check"]
			if !ok {
				chk = ""
			}
			v.exprTo(true, p, fmt.Sprintf("rules/%d/check", i), chk, merged, visible)
		}
		views := asMap(own.Raw["views"])
		var placed, cols []string
		for _, sv := range asList(asMap(views["form"])["layout"]) {
			sm := asMap(sv)
			for _, n := range asList(sm["fields"]) {
				placed = append(placed, pyStr(n))
			}
			for _, col := range asList(sm["columns"]) {
				for _, n := range asList(col) {
					placed = append(placed, pyStr(n))
				}
			}
		}
		for _, c := range asList(asMap(views["list"])["columns"]) {
			cols = append(cols, pyStr(c))
		}
		for _, view := range []string{"form", "list"} {
			have := placed
			if view == "list" {
				have = cols
			}
			for i, av := range asList(asMap(asMap(o["views"])[view])["add"]) {
				ad, ok := av.(map[string]any)
				if !ok {
					continue
				}
				fn := pyStr(ad["field"])
				okField := mergedNames[fn]
				if view == "list" {
					okField = known[fn]
				}
				if !okField {
					v.rep.extra("error", p, fmt.Sprintf("views/%s/add/%d", view, i), "unknown_field", fmt.Sprintf(`"%s" is not a field of %s%s`, fn, mn, suggest(fn, keys(merged))))
				}
				if after := pyStrOr(ad["after"]); after != "" && !contains(have, after) {
					v.rep.extra("error", p, fmt.Sprintf("views/%s/add/%d/after", view, i), "unknown_field", fmt.Sprintf(`"%s" is not in the %s view of %s`, after, view, mn))
				}
			}
		}
		for _, it := range cycleItems(mergedList) {
			hasX := false
			for _, n := range strings.Split(it[1], " → ") {
				hasX = hasX || strings.HasPrefix(n, "x_")
			}
			if hasX {
				v.rep.extra("error", p, it[0], "compute_cycle", it[1])
			}
		}
	}
}
