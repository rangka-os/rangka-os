package meta

import (
	"fmt"
	"sort"
	"strings"
)

// offlineTypes may queue writes in the browser (contract 0030 §7).
var offlineTypes = map[string]bool{"counter": true, "roll": true, "route": true, "clock": true, "entry": true}

// validateScreens checks screen files and the work: lists of apps (contract 0030 §1, §3): the same checks as
// check_screens in rangka_validate.py.
func (v *validator) validateScreens() {
	all := v.allModels()
	roles := map[string]bool{"owner": true}
	for _, a := range v.reg.Apps {
		for _, r := range a.Roles {
			roles[r] = true
		}
	}
	seen := map[string]string{}
	for _, s := range v.reg.screenFiles {
		first, dup := seen[s.Name]
		if !dup {
			seen[s.Name] = s.App
		}
		if v.skip(s.App) {
			continue
		}
		if dup {
			v.rep.extra("error", s.Doc.Path, "name", "duplicate_screen", fmt.Sprintf(`screen "%s" is already shipped by %s; screen names are unique per site`, s.Name, first))
			continue
		}
		v.screen(s, all, roles)
	}
	for _, a := range v.reg.Apps {
		if a.Kind != KindApp || v.skip(a.Name) || a.Doc == nil {
			continue
		}
		v.work(a)
	}
}

func (v *validator) screen(s *Screen, all map[string]vis, roles map[string]bool) {
	d, raw := s.Doc, s.Raw
	p := d.Path
	v.schemaErr("screen", d)
	if got, ok := raw["name"].(string); !ok || got != s.Name {
		v.rep.extra("error", p, "name", "name_mismatch", fmt.Sprintf(`screen name "%s" must match the file name "%s"`, pyStr(raw["name"]), s.Name))
	}
	typ, _ := raw["type"].(string)
	known := false
	for _, t := range ScreenTypes {
		known = known || t == typ
	}
	if known {
		if _, ok := raw[typ]; !ok {
			v.rep.extra("error", p, "type", "screen_type_mismatch", fmt.Sprintf(`a %s screen needs its %s: block`, typ, typ))
		}
	}
	for _, t := range ScreenTypes {
		if _, ok := raw[t]; ok && t != typ {
			v.rep.extra("error", p, t, "screen_type_mismatch", fmt.Sprintf(`"%s" is the block of the %s type; this screen is type %s`, t, t, pyStr(raw["type"])))
		}
	}
	for i, r := range asList(raw["roles"]) {
		if rs, ok := r.(string); ok && !roles[rs] {
			v.rep.extra("error", p, fmt.Sprintf("roles/%d", i), "unknown_role", fmt.Sprintf(`role "%s" is not a role of any module or app%s`, rs, suggest(rs, keys(roles))))
		}
	}
	if off, _ := raw["offline"].(string); (off == "queue" || off == "edge") && known && !offlineTypes[typ] {
		v.rep.extra("error", p, "offline", "screen_offline", fmt.Sprintf(`a %s screen cannot work offline (only counter, roll, route, clock and entry)`, typ))
	}
	v.parens(p, "if", raw["if"])
	visible := map[string]vis{}
	for _, u := range v.reg.Closure([]string{s.App}) {
		for mn, x := range all {
			if x.app == u {
				visible[mn] = x
			}
		}
	}
	if typ == "entry" {
		if b := asMap(raw["entry"]); b != nil {
			v.screenEntry(p, b, visible)
		}
	}
	if typ == "counter" {
		if b := asMap(raw["counter"]); b != nil {
			v.screenCounter(p, b, visible)
		}
	}
}

// screenModel resolves the model a slot names among the models the screen's unit sees, reporting unknown_model.
func (v *validator) screenModel(p, where string, b map[string]any, visible map[string]vis) *Model {
	mn, ok := b["model"].(string)
	if !ok {
		return nil
	}
	x, found := visible[mn]
	if !found {
		v.rep.extra("error", p, where, "unknown_model", fmt.Sprintf(`"%s" is not a model of this module or its requires%s`, mn, suggest(mn, keys(visible))))
		return nil
	}
	return x.m
}

func (v *validator) screenEntry(p string, b map[string]any, visible map[string]vis) {
	m := v.screenModel(p, "entry/model", b, visible)
	if m == nil {
		return
	}
	fields := fieldMap(m)
	for i, x := range asList(b["fields"]) {
		fn, ok := x.(string)
		if !ok {
			continue
		}
		at := fmt.Sprintf("entry/fields/%d", i)
		f, has := fields[fn]
		switch {
		case !has:
			v.rep.extra("error", p, at, "unknown_field", fmt.Sprintf(`"%s" is not a field of %s%s`, fn, m.Name, suggest(fn, keys(fields))))
		case str(f, "type") == "table":
			v.rep.extra("error", p, at, "screen_slot_type", fmt.Sprintf(`"%s" is a table; an entry screen takes plain fields`, fn))
		case f["compute"] != nil:
			v.rep.extra("error", p, at, "screen_slot_type", fmt.Sprintf(`"%s" is computed; nobody types it`, fn))
		}
	}
	set := asMap(b["set"])
	ks := make([]string, 0, len(set))
	for k := range set {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		if _, has := fields[k]; !has {
			v.rep.extra("error", p, "entry/set/"+k, "unknown_field", fmt.Sprintf(`"%s" is not a field of %s%s`, k, m.Name, suggest(k, keys(fields))))
			continue
		}
		if f := fields[k]; f["compute"] != nil || f["read_only"] == true {
			v.rep.extra("error", p, "entry/set/"+k, "screen_slot_type", fmt.Sprintf(`"%s" is computed or read-only; set: cannot fill it`, k))
			continue
		}
		v.parens(p, "entry/set/"+k, set[k])
	}
	if b["submit"] == true && !boolean(m.Raw, "is_submittable") {
		v.rep.extra("error", p, "entry/submit", "screen_not_submittable", fmt.Sprintf(`%s is not submittable; drop submit: true`, m.Name))
	}
}

// work checks an app's work: list: each entry names a screen of the app's modules and narrows its roles.
func (v *validator) work(a *App) {
	var mods []string
	for _, k := range orderedKeys(a, "modules") {
		if u := v.reg.App[k]; u != nil && u.IsModule() {
			mods = append(mods, k)
		}
	}
	units := map[string]bool{}
	for _, u := range v.reg.Closure(mods) {
		units[u] = true
	}
	var mine []string
	for _, s := range v.reg.screenFiles {
		if units[s.App] && v.reg.Screens[s.Name] == s {
			mine = append(mine, s.Name)
		}
	}
	sort.Strings(mine)
	homes := map[string]int{}
	for i, x := range asList(a.Raw["work"]) {
		w := asMap(x)
		name, ok := w["screen"].(string)
		if !ok {
			continue
		}
		at := fmt.Sprintf("work/%d", i)
		s := v.reg.Screens[name]
		if s == nil || !units[s.App] {
			v.rep.extra("error", a.Doc.Path, at+"/screen", "unknown_screen", fmt.Sprintf(`"%s" is not a screen of this app's modules%s`, name, suggest(name, mine)))
			continue
		}
		allowed := map[string]bool{}
		for _, r := range s.Roles {
			allowed[r] = true
		}
		for j, r := range asList(w["roles"]) {
			if rs, ok := r.(string); ok && !allowed[rs] {
				v.rep.extra("error", a.Doc.Path, fmt.Sprintf("%s/roles/%d", at, j), "screen_role_not_allowed",
					fmt.Sprintf(`screen %s allows only %s; work: can narrow its roles, not add "%s"`, name, strings.Join(s.Roles, ", "), rs))
			}
		}
		if w["home"] == true {
			rs := strList(w["roles"])
			if len(rs) == 0 {
				rs = s.Roles
			}
			for _, r := range rs {
				if prev, dup := homes[r]; dup {
					v.rep.extra("warn", a.Doc.Path, at+"/home", "duplicate_home", fmt.Sprintf(`role %s already lands on work/%d; only the first home: true counts`, r, prev))
				}
				if _, dup := homes[r]; !dup {
					homes[r] = i
				}
			}
		}
	}
}

var (
	numericKinds = []string{"currency", "money", "float", "int", "percent"}
	linkKind     = []string{"link"}
	tableKind    = []string{"table"}
)

// slotField checks that a slot names a field of model m (fields) and, when kinds is set, that the field has one of
// those types; it returns the field or nil. A slot that is absent or not a string is skipped.
func (v *validator) slotField(p, where string, m *Model, fields map[string]map[string]any, x any, kinds []string) map[string]any {
	fn, ok := x.(string)
	if !ok {
		return nil
	}
	f, has := fields[fn]
	if !has {
		v.rep.extra("error", p, where, "unknown_field", fmt.Sprintf(`"%s" is not a field of %s%s`, fn, m.Name, suggest(fn, keys(fields))))
		return nil
	}
	if len(kinds) > 0 {
		t := str(f, "type")
		okType := false
		for _, k := range kinds {
			okType = okType || k == t
		}
		if !okType {
			v.rep.extra("error", p, where, "screen_slot_type", fmt.Sprintf(`"%s" is a field of type %s; this slot needs %s`, fn, t, strings.Join(kinds, " or ")))
			return nil
		}
	}
	return f
}

// slotModel resolves the model a slot block names (unknown_model) with its fields.
func (v *validator) slotModel(p, where string, b map[string]any, visible map[string]vis) (*Model, map[string]map[string]any) {
	m := v.screenModel(p, where, b, visible)
	if m == nil {
		return nil, nil
	}
	return m, fieldMap(m)
}

// childOf is the row model of a table field, when it is a visible model.
func childOf(f map[string]any, visible map[string]vis) (*Model, map[string]map[string]any) {
	if f == nil {
		return nil, nil
	}
	x, ok := visible[str(f, "of")]
	if !ok {
		return nil, nil
	}
	return x.m, fieldMap(x.m)
}

// slotSet checks a set: map: fields of m that a client may fill (sorted keys, like entry).
func (v *validator) slotSet(p, where string, m *Model, fields map[string]map[string]any, x any) {
	set := asMap(x)
	ks := make([]string, 0, len(set))
	for k := range set {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		f, has := fields[k]
		switch {
		case !has:
			v.rep.extra("error", p, where+"/"+k, "unknown_field", fmt.Sprintf(`"%s" is not a field of %s%s`, k, m.Name, suggest(k, keys(fields))))
		case f["compute"] != nil || f["read_only"] == true:
			v.rep.extra("error", p, where+"/"+k, "screen_slot_type", fmt.Sprintf(`"%s" is computed or read-only; set: cannot fill it`, k))
		default:
			v.parens(p, where+"/"+k, set[k])
		}
	}
}

// screenCounter checks the slots of a counter screen (contract 0030 §2).
func (v *validator) screenCounter(p string, b map[string]any, visible map[string]vis) {
	cat := asMap(b["catalogue"])
	var catModel *Model
	if cat != nil {
		m, fields := v.slotModel(p, "counter/catalogue/model", cat, visible)
		catModel = m
		if m != nil {
			v.slotField(p, "counter/catalogue/price", m, fields, cat["price"], numericKinds)
			for _, k := range []string{"title", "group_by", "barcode", "image"} {
				v.slotField(p, "counter/catalogue/"+k, m, fields, cat[k], nil)
			}
			v.slotField(p, "counter/catalogue/available", m, fields, cat["available"], numericKinds)
			for i, x := range asList(cat["search"]) {
				v.slotField(p, fmt.Sprintf("counter/catalogue/search/%d", i), m, fields, x, nil)
			}
			v.parens(p, "counter/catalogue/filter", cat["filter"])
		}
	}
	doc := asMap(b["document"])
	var docModel *Model
	var docFields map[string]map[string]any
	if doc != nil {
		docModel, docFields = v.slotModel(p, "counter/document/model", doc, visible)
	}
	if docModel != nil {
		if b["submit"] != false && !boolean(docModel.Raw, "is_submittable") {
			v.rep.extra("error", p, "counter/document/model", "screen_not_submittable", fmt.Sprintf(`%s is not submittable; set submit: false or pick a submittable document`, docModel.Name))
		}
		lines := v.slotField(p, "counter/document/lines", docModel, docFields, doc["lines"], tableKind)
		if lm, lf := childOf(lines, visible); lm != nil {
			line := asMap(doc["line"])
			if item := v.slotField(p, "counter/document/line/item", lm, lf, line["item"], linkKind); item != nil && catModel != nil && str(item, "to") != catModel.Name {
				v.rep.extra("error", p, "counter/document/line/item", "screen_slot_type", fmt.Sprintf(`"%s" links to %s; the catalogue is %s`, pyStr(line["item"]), str(item, "to"), catModel.Name))
			}
			v.slotField(p, "counter/document/line/qty", lm, lf, line["qty"], numericKinds)
			v.slotField(p, "counter/document/line/price", lm, lf, line["price"], numericKinds)
			v.slotField(p, "counter/document/line/note", lm, lf, line["note"], nil)
		}
		v.slotField(p, "counter/document/total", docModel, docFields, doc["total"], numericKinds)
		v.slotField(p, "counter/document/change", docModel, docFields, doc["change"], numericKinds)
		for i, x := range asList(doc["summary"]) {
			v.slotField(p, fmt.Sprintf("counter/document/summary/%d", i), docModel, docFields, x, numericKinds)
		}
		v.slotSet(p, "counter/document/set", docModel, docFields, doc["set"])
		if c := asMap(b["customer"]); c != nil {
			v.slotField(p, "counter/customer/field", docModel, docFields, c["field"], nil)
		}
		if pr, ok := b["print"].(string); ok {
			found := false
			for _, f := range v.reg.printFiles {
				found = found || (f.Name == pr && f.Model == docModel.Name)
			}
			if !found {
				v.rep.extra("error", p, "counter/print", "unknown_print", fmt.Sprintf(`"%s" is not a print format of %s`, pr, docModel.Name))
			}
		}
	}
	if pay := asMap(b["payments"]); pay != nil && docModel != nil {
		table := v.slotField(p, "counter/payments/table", docModel, docFields, pay["table"], tableKind)
		if pm, pf := childOf(table, visible); pm != nil {
			method := v.slotField(p, "counter/payments/method", pm, pf, pay["method"], nil)
			v.slotField(p, "counter/payments/amount", pm, pf, pay["amount"], numericKinds)
			v.slotField(p, "counter/payments/ref", pm, pf, pay["ref"], nil)
			if method != nil && str(method, "type") == "select" {
				opts := map[string]bool{}
				for _, o := range asList(method["options"]) {
					opts[pyStr(asMap(o)["value"])] = true
				}
				for i, x := range asList(pay["methods"]) {
					if ms, ok := x.(string); ok && !opts[ms] {
						v.rep.extra("error", p, fmt.Sprintf("counter/payments/methods/%d", i), "screen_slot_type", fmt.Sprintf(`"%s" is not an option of %s`, ms, pyStr(pay["method"])))
					}
				}
			}
		}
	}
	if ses := asMap(b["session"]); ses != nil {
		sm, sf := v.slotModel(p, "counter/session/model", ses, visible)
		if sm != nil {
			if docModel != nil {
				if l := v.slotField(p, "counter/session/link", docModel, docFields, ses["link"], linkKind); l != nil && str(l, "to") != sm.Name {
					v.rep.extra("error", p, "counter/session/link", "screen_slot_type", fmt.Sprintf(`"%s" links to %s; the session is %s`, pyStr(ses["link"]), str(l, "to"), sm.Name))
				}
			}
			st := asMap(ses["state"])
			if f := v.slotField(p, "counter/session/state/field", sm, sf, st["field"], []string{"select", "data"}); f != nil && str(f, "type") == "select" {
				opts := map[string]bool{}
				for _, o := range asList(f["options"]) {
					opts[pyStr(asMap(o)["value"])] = true
				}
				for _, k := range []string{"open", "closed"} {
					if val, ok := st[k].(string); ok && !opts[val] {
						v.rep.extra("error", p, "counter/session/state/"+k, "screen_slot_type", fmt.Sprintf(`"%s" is not an option of %s`, val, pyStr(st["field"])))
					}
				}
			}
			for i, x := range asList(ses["open"]) {
				v.slotField(p, fmt.Sprintf("counter/session/open/%d", i), sm, sf, x, nil)
			}
			v.slotSet(p, "counter/session/set", sm, sf, ses["set"])
			v.parens(p, "counter/session/filter", ses["filter"])
			if cl := asMap(ses["close"]); cl != nil {
				cm, cf := v.slotModel(p, "counter/session/close/model", cl, visible)
				if cm != nil {
					if !boolean(cm.Raw, "is_submittable") {
						v.rep.extra("error", p, "counter/session/close/model", "screen_not_submittable", fmt.Sprintf(`%s is not submittable; a session closes with a submitted document`, cm.Name))
					}
					if l := v.slotField(p, "counter/session/close/link", cm, cf, cl["link"], linkKind); l != nil && str(l, "to") != sm.Name {
						v.rep.extra("error", p, "counter/session/close/link", "screen_slot_type", fmt.Sprintf(`"%s" links to %s; the session is %s`, pyStr(cl["link"]), str(l, "to"), sm.Name))
					}
					for i, x := range asList(cl["fields"]) {
						v.slotField(p, fmt.Sprintf("counter/session/close/fields/%d", i), cm, cf, x, nil)
					}
					v.slotSet(p, "counter/session/close/set", cm, cf, cl["set"])
				}
			}
		}
	}
}
