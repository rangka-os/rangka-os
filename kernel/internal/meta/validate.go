package meta

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/rk"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
)

// Validate loads appsDir (and siteDir when set) and checks them in the passes of rangka_validate.py:
// syntax, shape (JSON Schema) and meaning (cross-file references). only limits checks to some apps.
func Validate(appsDir, siteDir string, only []string, schemas *schema.Set) (*Registry, *Report) {
	return ValidateOpts(Options{AppsDir: appsDir, SiteDir: siteDir}, only, schemas)
}

// ValidateOpts validates apps, site and connectors (pass 3 of rangka_validate.py) and then merges company
// overlays into the registry's models.
func ValidateOpts(o Options, only []string, schemas *schema.Set) (*Registry, *Report) {
	rep := NewReport(o.AppsDir)
	reg := LoadOpts(o, rep)
	v := &validator{reg: reg, rep: rep, S: schemas, only: map[string]bool{}}
	for _, x := range only {
		v.only[x] = true
	}
	v.shape()
	v.meaning()
	v.more()
	reg.applyOverlays()
	return reg, rep
}

type validator struct {
	reg  *Registry
	rep  *Report
	S    *schema.Set
	only map[string]bool
}

func (v *validator) skip(app string) bool { return len(v.only) > 0 && !v.only[app] }

func pathKey(p []any) []string {
	out := make([]string, len(p))
	for i, x := range p {
		out[i] = fmt.Sprint(x)
	}
	return out
}

func lessStrings(a, b []string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func truncMsg(m string) string {
	r := []rune(m)
	if len(r) < 160 {
		return m
	}
	return string(r[:157]) + "…"
}

func (v *validator) schemaErrors(kind string, d *rk.Doc, sorted bool) []schema.Error {
	var in *schema.Inst
	if d.Root != nil {
		in = schema.FromNode(d.Root)
	} else {
		in = schema.FromData(d.Data)
	}
	errs := v.S.Validate(kind, in)
	if sorted {
		sort.SliceStable(errs, func(i, j int) bool { return lessStrings(pathKey(errs[i].Path), pathKey(errs[j].Path)) })
	}
	return errs
}

// ---------- pass 1: shape

func (v *validator) shape() {
	for _, a := range v.reg.Apps {
		if v.skip(a.Name) {
			continue
		}
		if a.Doc != nil {
			for _, e := range v.schemaErrors("app", a.Doc, false) {
				v.rep.add("error", a.Doc.Path, e.PathString(), "schema", e.Message)
			}
		}
		for _, mn := range a.Models {
			m := v.reg.Models[mn]
			for _, e := range v.schemaErrors("model", m.Doc, true) {
				v.rep.add("error", m.Doc.Path, e.PathString(), "schema", truncMsg(e.Message))
			}
			if got, _ := m.Raw["name"].(string); got != mn || m.Raw["name"] == nil {
				v.rep.add("error", m.Doc.Path, "name", "name_mismatch", fmt.Sprintf(`model name "%s" must match the file name "%s"`, pyStr(m.Raw["name"]), mn))
			}
		}
		for _, dn := range v.appDashboards(a) {
			d := v.reg.Dashboards[dn]
			for _, e := range v.schemaErrors("dashboard", d.Doc, true) {
				v.rep.add("error", d.Doc.Path, e.PathString(), "schema", truncMsg(e.Message))
			}
			if got, _ := d.Raw["name"].(string); got != dn || d.Raw["name"] == nil {
				v.rep.add("error", d.Doc.Path, "name", "name_mismatch", fmt.Sprintf(`dashboard name "%s" must match the file name "%s"`, pyStr(d.Raw["name"]), dn))
			}
		}
	}
}

// pyStr formats a value as Python's str() would inside an f-string.
func pyStr(x any) string {
	if s, ok := x.(string); ok {
		return s
	}
	return schema.Repr(x)
}

func (v *validator) appDashboards(a *App) []string {
	var out []string
	for _, dn := range sortedKeysM(v.reg.Dashboards) {
		if v.reg.Dashboards[dn].App == a.Name {
			out = append(out, dn)
		}
	}
	return out
}

// ---------- pass 2: meaning

type vis struct {
	app string
	m   *Model // with the site's overlay fields, as other files see it
	own *Model // the app's own model
}

func (v *validator) meaning() {
	all := v.allModels()
	for _, a := range v.reg.Apps {
		if v.skip(a.Name) {
			continue
		}
		reqs := map[string]bool{}
		for k := range a.Requires {
			reqs[k] = true
		}
		for k := range a.Optional {
			reqs[k] = true
		}
		visible := map[string]vis{}
		for mn, x := range all {
			if x.app == a.Name || reqs[x.app] {
				visible[mn] = x
			}
		}
		roles := map[string]bool{}
		for _, x := range append([]string{a.Name}, keys(reqs)...) {
			if ap := v.reg.App[x]; ap != nil {
				for _, r := range ap.Roles {
					roles[r] = true
				}
			}
		}
		hooks := map[string]bool{}
		for _, h := range CoreHooks {
			hooks[h] = true
		}
		for _, h := range a.Hooks {
			hooks[h] = true
		}
		appPath := ""
		if a.Doc != nil {
			appPath = a.Doc.Path
		}
		for gi, g := range asList(a.Raw["menu"]) {
			for i, it := range asList(asMap(g)["items"]) {
				s := pyStr(it)
				if _, ok := visible[s]; !ok {
					v.rep.add("error", appPath, fmt.Sprintf("menu/%d/items/%d", gi, i), "unknown_model",
						fmt.Sprintf(`"%s" is not a model in this app or its dependencies%s`, s, suggest(s, keys(visible))))
				}
			}
		}
		for _, mn := range a.Models {
			v.model(v.reg.Models[mn], visible, roles, hooks)
		}
		listed := a.Dashboards
		appDash := v.appDashboards(a)
		for i, dn := range listed {
			if v.reg.Dashboards[dn] == nil || v.reg.Dashboards[dn].App != a.Name {
				v.rep.add("error", appPath, fmt.Sprintf("dashboards/%d", i), "unknown_dashboard",
					fmt.Sprintf(`"%s" has no file dashboards/%s.rk%s`, dn, dn, suggest(dn, appDash)))
			}
		}
		optional := map[string]bool{}
		for k := range a.Optional {
			optional[k] = true
		}
		for _, dn := range appDash {
			d := v.reg.Dashboards[dn]
			isListed := false
			for _, l := range listed {
				isListed = isListed || l == dn
			}
			if !isListed {
				v.rep.add("warn", d.Doc.Path, "name", "unlisted_dashboard", fmt.Sprintf(`add "%s" to dashboards in app.rk or nobody can open it`, dn))
			}
			v.dashboard(d, visible, optional, all, a.Name)
		}
	}
}

func fieldMap(m *Model) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, f := range asList(m.Raw["fields"]) {
		fm := asMap(f)
		if fm == nil {
			continue
		}
		if n, ok := fm["name"]; ok {
			out[pyStr(n)] = fm
		}
	}
	return out
}

func fieldNames(m *Model) map[string]bool {
	out := map[string]bool{}
	for k := range fieldMap(m) {
		out[k] = true
	}
	return out
}

func union(a map[string]bool, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

func boolKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (v *validator) model(m *Model, visible map[string]vis, roles, hooks map[string]bool) {
	p := m.Doc.Path
	fields := fieldMap(m)
	known := union(fieldNames(m), SystemFields)
	seen := map[string]bool{}
	for i, fv := range asList(m.Raw["fields"]) {
		f := asMap(fv)
		if f == nil {
			continue
		}
		n := pyStr(f["name"])
		if seen[n] {
			v.rep.add("error", p, fmt.Sprintf("fields/%d", i), "duplicate_field", fmt.Sprintf(`"%s" is declared twice`, n))
		}
		seen[n] = true
		for _, key := range []string{"to", "of"} {
			t, _ := f[key].(string)
			if t != "" {
				if _, ok := visible[t]; !ok {
					v.rep.add("error", p, fmt.Sprintf("fields/%d/%s", i, key), "unknown_model",
						fmt.Sprintf(`"%s" is not a model in this app or its dependencies%s`, t, suggest(t, keys(visible))))
				}
			}
		}
		if of, _ := f["of"].(string); of != "" {
			if x, ok := visible[of]; ok && !boolean(x.m.Raw, "is_child") {
				v.rep.add("error", p, fmt.Sprintf("fields/%d/of", i), "not_child", fmt.Sprintf(`"%s" must set is_child: true to be used in a table field`, of))
			}
		}
		for _, pk := range asList(f["peek"]) {
			to, _ := f["to"].(string)
			if tm, ok := visible[to]; ok {
				ps := pyStr(pk)
				if !union(fieldNames(tm.m), SystemFields)[ps] {
					v.rep.add("error", p, fmt.Sprintf("fields/%d/peek", i), "unknown_field", fmt.Sprintf(`"%s" is not a field of %s`, ps, to))
				}
			}
		}
		if rt, ok := f["relative_to"]; ok && rt != nil && rt != "" {
			if _, ok := fields[pyStr(rt)]; !ok {
				v.rep.add("error", p, fmt.Sprintf("fields/%d/relative_to", i), "unknown_field", fmt.Sprintf(`"%s" is not a field`, pyStr(rt)))
			}
		}
		for _, key := range []string{"default", "compute", "depends_on"} {
			if x, ok := f[key]; ok {
				v.expr(p, fmt.Sprintf("fields/%d/%s", i, key), x, fields, visible)
			}
		}
	}
	if tf, ok := m.Raw["title_field"]; ok && tf != nil && tf != "" {
		if _, ok := fields[pyStr(tf)]; !ok {
			v.rep.add("error", p, "title_field", "unknown_field", fmt.Sprintf(`"%s" is not a field%s`, pyStr(tf), suggest(pyStr(tf), keys(fields))))
		}
	}
	for i, r := range asList(m.Raw["rules"]) {
		chk, ok := asMap(r)["check"]
		if !ok {
			chk = ""
		}
		v.expr(p, fmt.Sprintf("rules/%d/check", i), chk, fields, visible)
	}
	for i, av := range asList(m.Raw["actions"]) {
		ac := asMap(av)
		if !hooks[pyStr(ac["hook"])] {
			v.rep.add("error", p, fmt.Sprintf("actions/%d/hook", i), "unknown_hook", fmt.Sprintf(`"%s" is not declared in app.rk hooks or core`, pyStr(ac["hook"])))
		}
		if x, ok := ac["if"]; ok {
			v.expr(p, fmt.Sprintf("actions/%d/if", i), x, fields, visible)
		}
	}
	for _, key := range []string{"on_submit", "on_cancel"} {
		for i, hk := range asList(m.Raw[key]) {
			if !hooks[pyStr(hk)] {
				v.rep.add("error", p, fmt.Sprintf("%s/%d", key, i), "unknown_hook", fmt.Sprintf(`"%s" is not declared in app.rk hooks or core`, pyStr(hk)))
			}
		}
	}
	if len(asList(m.Raw["on_submit"])) > 0 && !boolean(m.Raw, "is_submittable") {
		v.rep.add("error", p, "on_submit", "not_submittable", "on_submit hooks need is_submittable: true")
	}
	for i, pv := range asList(m.Raw["permissions"]) {
		role := pyStr(asMap(pv)["role"])
		if !roles[role] {
			v.rep.add("warn", p, fmt.Sprintf("permissions/%d/role", i), "unknown_role", fmt.Sprintf(`role "%s" has no file in roles/ (it must exist at install)`, role))
		}
	}
	views := asMap(m.Raw["views"])
	for i, c := range asList(asMap(views["list"])["columns"]) {
		cs := pyStr(c)
		if !known[cs] {
			v.rep.add("error", p, fmt.Sprintf("views/list/columns/%d", i), "unknown_field", fmt.Sprintf(`"%s" is not a field%s`, cs, suggest(cs, boolKeys(known))))
		}
	}
	if kb := asMap(views["kanban"]); kb != nil {
		g := fields[pyStr(kb["group_by"])]
		if g == nil {
			v.rep.add("error", p, "views/kanban/group_by", "unknown_field", fmt.Sprintf(`"%s" is not a field`, pyStr(kb["group_by"])))
		} else if str(g, "type") != "select" {
			v.rep.add("error", p, "views/kanban/group_by", "bad_group_by", fmt.Sprintf(`kanban columns need a select field; "%s" is %s`, pyStr(g["name"]), pyStr(g["type"])))
		}
		names := []any{kb["title"], kb["sum"]}
		names = append(names, asList(kb["card"])...)
		for _, x := range names {
			if x == nil || x == "" {
				continue
			}
			xs := pyStr(x)
			if !known[xs] {
				v.rep.add("error", p, "views/kanban", "unknown_field", fmt.Sprintf(`"%s" is not a field%s`, xs, suggest(xs, boolKeys(known))))
			}
		}
		if sf := fields[pyStr(kb["sum"])]; sf != nil && kb["sum"] != nil {
			if t := str(sf, "type"); t != "currency" && t != "int" && t != "float" {
				v.rep.add("error", p, "views/kanban/sum", "not_numeric", fmt.Sprintf(`"%s" cannot be summed`, pyStr(kb["sum"])))
			}
		}
	}
	placed := map[string]bool{}
	for i, sv := range asList(asMap(views["form"])["layout"]) {
		s := asMap(sv)
		var names []string
		for _, x := range asList(s["fields"]) {
			names = append(names, pyStr(x))
		}
		for _, col := range asList(s["columns"]) {
			for _, x := range asList(col) {
				names = append(names, pyStr(x))
			}
		}
		for _, n := range names {
			if _, ok := fields[n]; !ok {
				v.rep.add("error", p, fmt.Sprintf("views/form/layout/%d", i), "unknown_field", fmt.Sprintf(`"%s" is not a field%s`, n, suggest(n, keys(fields))))
			} else if placed[n] {
				v.rep.add("warn", p, fmt.Sprintf("views/form/layout/%d", i), "placed_twice", fmt.Sprintf(`"%s" appears twice in the form`, n))
			}
			placed[n] = true
		}
	}
	v.cycles(p, m)
}

var optionRe = regexp.MustCompile(`option\(\s*'(\w+)'`)

var funcSet = func() map[string]bool {
	m := map[string]bool{}
	for _, f := range Functions {
		m[f] = true
	}
	return m
}()

// expr ports check_expr: balanced parentheses, known functions, fields and link members, option() targets.
func (v *validator) expr(p, where string, x any, fields map[string]map[string]any, visible map[string]vis) {
	v.exprTo(false, p, where, x, fields, visible)
}

func (v *validator) exprTo(extra bool, p, where string, x any, fields map[string]map[string]any, visible map[string]vis) {
	add := v.rep.add
	if extra {
		add = v.rep.extra
	}
	e, ok := x.(string)
	if !ok {
		return // a non-text expression is a schema error
	}
	if strings.Count(e, "(") != strings.Count(e, ")") {
		add("error", p, where, "cel_syntax", fmt.Sprintf("unbalanced parentheses in `%s`", e))
		return
	}
	for _, r := range cel.Idents(e) {
		if r.Kind == "fn" {
			if !funcSet[r.Head] {
				add("error", p, where, "unknown_function", fmt.Sprintf("`%s()` is not a CEL or Rangka function%s", r.Head, suggest(r.Head, Functions)))
			}
			continue
		}
		if Globals[r.Head] {
			continue
		}
		f, isField := fields[r.Head]
		if !isField && !SystemFields[r.Head] {
			add("error", p, where, "unknown_field", fmt.Sprintf("`%s` in `%s` is not a field%s", r.Head, e, suggest(r.Head, keys(fields))))
			continue
		}
		if r.Member != "" && f != nil && str(f, "type") == "link" {
			if tm, ok := visible[str(f, "to")]; ok {
				target := union(fieldNames(tm.m), SystemFields)
				if !target[r.Member] {
					add("error", p, where, "unknown_field", fmt.Sprintf("`%s.%s`: %s has no field \"%s\"%s", r.Head, r.Member, str(f, "to"), r.Member, suggest(r.Member, boolKeys(target))))
				}
			}
		}
	}
	for _, m := range optionRe.FindAllStringSubmatch(e, -1) {
		f := fields[m[1]]
		if f == nil || str(f, "type") != "select" {
			add("error", p, where, "bad_option", fmt.Sprintf(`option() needs a select field; "%s" is not one`, m[1]))
		}
	}
}

func (v *validator) cycles(p string, m *Model) {
	var list []map[string]any
	for _, fv := range asList(m.Raw["fields"]) {
		if f := asMap(fv); f != nil {
			list = append(list, f)
		}
	}
	for _, it := range cycleItems(list) {
		v.rep.add("error", p, it[0], "compute_cycle", it[1])
	}
}

// cycleItems ports check_cycles: fields in order, each compute's references in order of appearance.
// It returns (path, message) pairs.
func cycleItems(list []map[string]any) [][2]string {
	fields := map[string]bool{}
	for _, f := range list {
		if _, ok := f["name"]; ok {
			fields[pyStr(f["name"])] = true
		}
	}
	var order []string
	deps := map[string][]string{}
	for _, f := range list {
		if _, ok := f["name"]; !ok {
			continue
		}
		n := pyStr(f["name"])
		c, _ := f["compute"].(string)
		if c == "" {
			continue
		}
		if _, dup := deps[n]; !dup {
			order = append(order, n)
		}
		var ds []string
		seen := map[string]bool{}
		for _, r := range cel.Idents(c) {
			if r.Kind == "ref" && fields[r.Head] && !seen[r.Head] {
				seen[r.Head] = true
				ds = append(ds, r.Head)
			}
		}
		deps[n] = ds
	}
	var out [][2]string
	state := map[string]int{}
	var visit func(n string, path []string)
	visit = func(n string, path []string) {
		if state[n] == 1 {
			out = append(out, [2]string{"fields/" + n + "/compute", strings.Join(append(path, n), " → ")})
			return
		}
		if _, ok := deps[n]; state[n] == 2 || !ok {
			return
		}
		state[n] = 1
		for _, d := range deps[n] {
			visit(d, append(append([]string{}, path...), n))
		}
		state[n] = 2
	}
	for _, n := range order {
		visit(n, nil)
	}
	return out
}

var numericTypes = []string{"currency", "int", "float", "percent"}
var groupable = []string{"select", "link", "check", "date", "data"}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func (v *validator) dashboard(d *Dashboard, visible map[string]vis, optional map[string]bool, all map[string]vis, app string) {
	p := d.Doc.Path
	seen := map[string]bool{}
	for i, wv := range asList(d.Raw["widgets"]) {
		w := asMap(wv)
		at := fmt.Sprintf("widgets/%d", i)
		wn := pyStr(w["name"])
		if seen[wn] {
			v.rep.add("error", p, at+"/name", "duplicate_widget", fmt.Sprintf(`widget "%s" appears twice`, wn))
		}
		seen[wn] = true
		mn := pyStr(w["model"])
		if w["model"] == nil {
			mn = ""
		}
		wif, _ := w["if"].(string)
		var m *Model
		if x, ok := visible[mn]; !ok {
			owner := all[mn].app
			if _, exists := all[mn]; exists && optional[owner] {
				if !strings.Contains(wif, "app_installed('"+owner+"')") {
					v.rep.add("error", p, at+"/model", "optional_model", fmt.Sprintf(`"%s" comes from optional app "%s"; add  if: app_installed('%s')`, mn, owner, owner))
				}
				m = all[mn].m
			} else {
				v.rep.add("error", p, at+"/model", "unknown_model", fmt.Sprintf(`"%s" is not a model in this app or its dependencies%s`, mn, suggest(mn, keys(visible))))
				continue
			}
		} else {
			m = x.m
			if optional[x.app] && x.app != app && !strings.Contains(wif, "app_installed('"+x.app+"')") {
				v.rep.add("error", p, at+"/model", "optional_model", fmt.Sprintf(`"%s" comes from optional app "%s"; add  if: app_installed('%s')`, mn, x.app, x.app))
			}
		}
		fields := fieldMap(m)
		known := union(fieldNames(m), SystemFields)
		need := func(key string, val any, kinds []string, what string) map[string]any {
			if val == nil {
				return nil
			}
			vs := pyStr(val)
			if !known[vs] {
				v.rep.add("error", p, at+"/"+key, "unknown_field", fmt.Sprintf(`"%s" is not a field of %s%s`, vs, mn, suggest(vs, boolKeys(known))))
				return nil
			}
			f := fields[vs]
			if kinds != nil && f != nil && !contains(kinds, str(f, "type")) {
				v.rep.add("error", p, at+"/"+key, "bad_field_type", fmt.Sprintf(`"%s" is %s; %s`, vs, pyStr(f["type"]), what))
			}
			return f
		}
		agg := "count"
		if a, ok := w["aggregate"]; ok {
			agg = pyStr(a)
		}
		var kinds []string
		if agg != "count" {
			kinds = numericTypes
		}
		need("field", w["field"], kinds, fmt.Sprintf("%s needs a number field (%s)", agg, strings.Join(numericTypes, ", ")))
		g := need("group_by", w["group_by"], groupable, "group by a select, link, check, date or data field")
		if gr, _ := w["grain"].(string); gr != "" && (g == nil || str(g, "type") != "date") {
			v.rep.add("error", p, at+"/grain", "grain_without_date", "grain only applies when group_by is a date field")
		}
		if g != nil && str(g, "type") == "date" && (w["grain"] == nil || w["grain"] == "") {
			v.rep.add("warn", p, at+"/grain", "missing_grain", "date grouping without grain uses one bar per day; set grain: week or month")
		}
		if str(w, "chart") == "line" && g != nil && str(g, "type") != "date" {
			v.rep.add("warn", p, at+"/chart", "line_without_time", "line charts read as a trend over time; group by a date field or use bar")
		}
		need("period", w["period"], []string{"date", "datetime"}, "period needs a date field")
		for j, c := range asList(w["columns"]) {
			need(fmt.Sprintf("columns/%d", j), c, nil, "")
		}
		srt := strings.TrimLeft(str(w, "sort"), "-")
		if srt != "" && !(str(w, "type") == "chart" && (srt == "value" || srt == "label")) {
			need("sort", srt, nil, "")
		}
		if str(w, "type") == "number" && w["group_by"] != nil && w["group_by"] != "" {
			v.rep.add("warn", p, at+"/group_by", "ignored", "number widgets ignore group_by")
		}
		if fx, ok := w["filter"]; ok {
			v.expr(p, at+"/filter", fx, fields, visible)
		}
	}
}
