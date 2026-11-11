package meta

import "fmt"

// Slot checks of the roll, handover, clock and route screens (contract 0030 §2, WS7-WS9b); the same checks as
// check_screen_roll, _handover, _clock and _route in rangka_validate.py.

var (
	geoKind      = []string{"geo"}
	imageKind    = []string{"image"}
	sigKind      = []string{"signature"}
	datetimeKind = []string{"datetime"}
	selectKind   = []string{"select"}
	textKinds    = []string{"data", "text"}
)

// screenDocument checks a parent document slot (roll, handover): model, find, set, title, subtitle.
func (v *validator) screenDocument(p, typ string, b map[string]any, visible map[string]vis) (*Model, map[string]map[string]any) {
	doc := asMap(b["document"])
	if doc == nil {
		return nil, nil
	}
	m, fields := v.slotModel(p, typ+"/document/model", doc, visible)
	if m == nil {
		return nil, nil
	}
	v.parens(p, typ+"/document/find", doc["find"])
	v.slotSet(p, typ+"/document/set", m, fields, doc["set"])
	v.slotField(p, typ+"/document/title", m, fields, doc["title"], nil)
	v.slotField(p, typ+"/document/subtitle", m, fields, doc["subtitle"], nil)
	if b["submit"] != false && !boolean(m.Raw, "is_submittable") {
		v.rep.extra("error", p, typ+"/document/model", "screen_not_submittable", fmt.Sprintf(`%s is not submittable; set submit: false or pick a submittable document`, m.Name))
	}
	return m, fields
}

// linkedModel is the visible model a link field points at.
func linkedModel(f map[string]any, visible map[string]vis) (*Model, map[string]map[string]any) {
	if f == nil || str(f, "type") != "link" {
		return nil, nil
	}
	x, ok := visible[str(f, "to")]
	if !ok {
		return nil, nil
	}
	return x.m, fieldMap(x.m)
}

func (v *validator) screenRoll(p string, b map[string]any, visible map[string]vis) {
	m, fields := v.screenDocument(p, "roll", b, visible)
	if m == nil {
		return
	}
	rows := asMap(b["rows"])
	table := v.slotField(p, "roll/rows/table", m, fields, rows["table"], tableKind)
	rm, rf := childOf(table, visible)
	if rm == nil {
		return
	}
	v.slotField(p, "roll/rows/label", rm, rf, rows["label"], nil)
	v.slotField(p, "roll/rows/note", rm, rf, rows["note"], textKinds)
	v.slotField(p, "roll/rows/qty", rm, rf, rows["qty"], numericKinds)
	mark := asMap(rows["mark"])
	if mark != nil && rows["qty"] != nil {
		v.rep.extra("error", p, "roll/rows", "screen_slot_type", "rows take mark or qty, not both")
	}
	if mark != nil {
		if f := v.slotField(p, "roll/rows/mark/field", rm, rf, mark["field"], selectKind); f != nil {
			opts := optionSet(f)
			for i, x := range asList(mark["values"]) {
				if val, ok := x.(string); ok && !opts[val] {
					v.rep.extra("error", p, fmt.Sprintf("roll/rows/mark/values/%d", i), "screen_slot_type", fmt.Sprintf(`"%s" is not an option of %s`, val, pyStr(mark["field"])))
				}
			}
			if d, ok := mark["default"].(string); ok && !opts[d] {
				v.rep.extra("error", p, "roll/rows/mark/default", "screen_slot_type", fmt.Sprintf(`"%s" is not an option of %s`, d, pyStr(mark["field"])))
			}
		}
	}
	addBy, _ := b["add_by"].(string)
	sc := asMap(b["scan"])
	if (addBy == "scan" || addBy == "search") && sc == nil {
		v.rep.extra("error", p, "roll/add_by", "screen_slot_type", "add_by "+addBy+" needs the scan slot (the row link it fills)")
	}
	if sc != nil {
		lf := v.slotField(p, "roll/scan/field", rm, rf, sc["field"], linkKind)
		if lm, lmf := linkedModel(lf, visible); lm != nil {
			v.slotField(p, "roll/scan/match", lm, lmf, sc["match"], nil)
		}
	}
}

func (v *validator) screenHandover(p string, b map[string]any, visible map[string]vis) {
	m, fields := v.screenDocument(p, "handover", b, visible)
	if m == nil {
		return
	}
	rows := asMap(b["rows"])
	table := v.slotField(p, "handover/rows/table", m, fields, rows["table"], tableKind)
	if rm, rf := childOf(table, visible); rm != nil {
		unit := v.slotField(p, "handover/rows/unit", rm, rf, rows["unit"], linkKind)
		v.slotField(p, "handover/rows/condition", rm, rf, rows["condition"], selectKind)
		v.slotField(p, "handover/rows/qty", rm, rf, rows["qty"], numericKinds)
		v.slotField(p, "handover/rows/note", rm, rf, rows["note"], textKinds)
		if sc := asMap(b["scan"]); sc != nil {
			if um, uf := linkedModel(unit, visible); um != nil {
				v.slotField(p, "handover/scan/match", um, uf, sc["match"], nil)
			}
		}
	}
	if ph := asMap(b["photos"]); ph != nil {
		v.slotField(p, "handover/photos/field", m, fields, ph["field"], imageKind)
	}
	if sg := asMap(b["signature"]); sg != nil {
		v.slotField(p, "handover/signature/field", m, fields, sg["field"], sigKind)
		v.slotField(p, "handover/signature/by", m, fields, sg["by"], textKinds)
	}
	v.screenPrint(p, "handover/print", b["print"], m)
}

// screenPrint checks that a print slot names a print format of model m.
func (v *validator) screenPrint(p, where string, x any, m *Model) {
	pr, ok := x.(string)
	if !ok {
		return
	}
	for _, f := range v.reg.printFiles {
		if f.Name == pr && f.Model == m.Name {
			return
		}
	}
	v.rep.extra("error", p, where, "unknown_print", fmt.Sprintf(`"%s" is not a print format of %s`, pr, m.Name))
}

func (v *validator) screenClock(p string, b map[string]any, visible map[string]vis) {
	rec := asMap(b["record"])
	if rec == nil {
		return
	}
	m, fields := v.slotModel(p, "clock/record/model", rec, visible)
	if m == nil {
		return
	}
	v.parens(p, "clock/record/filter", rec["filter"])
	v.slotSet(p, "clock/record/set", m, fields, rec["set"])
	for _, side := range []string{"in", "out"} {
		sd := asMap(b[side])
		v.slotField(p, "clock/"+side+"/field", m, fields, sd["field"], datetimeKind)
		v.slotField(p, "clock/"+side+"/location", m, fields, sd["location"], geoKind)
		v.slotField(p, "clock/"+side+"/photo", m, fields, sd["photo"], imageKind)
	}
	v.slotField(p, "clock/reason", m, fields, b["reason"], textKinds)
	if b["submit"] == true && !boolean(m.Raw, "is_submittable") {
		v.rep.extra("error", p, "clock/submit", "screen_not_submittable", fmt.Sprintf(`%s is not submittable; drop submit: true`, m.Name))
	}
	if site := asMap(b["site"]); site != nil {
		sm, sf := v.slotModel(p, "clock/site/model", site, visible)
		if sm != nil {
			if l := v.slotField(p, "clock/site/link", m, fields, site["link"], linkKind); l != nil && str(l, "to") != sm.Name {
				v.rep.extra("error", p, "clock/site/link", "screen_slot_type", fmt.Sprintf(`"%s" links to %s; the site is %s`, pyStr(site["link"]), str(l, "to"), sm.Name))
			}
			v.parens(p, "clock/site/filter", site["filter"])
			v.slotField(p, "clock/site/title", sm, sf, site["title"], nil)
			v.slotField(p, "clock/site/location", sm, sf, site["location"], geoKind)
			v.slotField(p, "clock/site/radius_m", sm, sf, site["radius_m"], numericKinds)
		}
	}
}

func (v *validator) screenRoute(p string, b map[string]any, visible map[string]vis) {
	st := asMap(b["stops"])
	if st == nil {
		return
	}
	m, fields := v.slotModel(p, "route/stops/model", st, visible)
	if m == nil {
		return
	}
	v.parens(p, "route/stops/filter", st["filter"])
	for _, k := range []string{"title", "subtitle", "address"} {
		v.slotField(p, "route/stops/"+k, m, fields, st[k], nil)
	}
	v.slotField(p, "route/stops/location", m, fields, st["location"], geoKind)
	if vi := asMap(b["visit"]); vi != nil {
		vm, vf := v.slotModel(p, "route/visit/model", vi, visible)
		if vm != nil {
			if l := v.slotField(p, "route/visit/link", vm, vf, vi["link"], linkKind); l != nil && str(l, "to") != m.Name {
				v.rep.extra("error", p, "route/visit/link", "screen_slot_type", fmt.Sprintf(`"%s" links to %s; the stops are %s`, pyStr(vi["link"]), str(l, "to"), m.Name))
			}
			v.slotSet(p, "route/visit/set", vm, vf, vi["set"])
			v.slotField(p, "route/visit/location", vm, vf, vi["location"], geoKind)
			v.slotField(p, "route/visit/photo", vm, vf, vi["photo"], imageKind)
			if vi["submit"] == true && !boolean(vm.Raw, "is_submittable") {
				v.rep.extra("error", p, "route/visit/submit", "screen_not_submittable", fmt.Sprintf(`%s is not submittable; drop submit: true`, vm.Name))
			}
		}
	}
	for i, x := range asList(b["actions"]) {
		a := asMap(x)
		at := fmt.Sprintf("route/actions/%d", i)
		scr, hasScr := a["screen"].(string)
		cr, hasCr := a["create"].(string)
		if hasScr == hasCr {
			v.rep.extra("error", p, at, "screen_slot_type", "an action opens a screen or creates a record (one of screen, create)")
			continue
		}
		if hasScr {
			if v.reg.Screens[scr] == nil {
				v.rep.extra("error", p, at+"/screen", "unknown_screen", fmt.Sprintf(`"%s" is not a screen of this site`, scr))
			}
			if a["set"] != nil {
				v.rep.extra("error", p, at+"/set", "screen_slot_type", "an action that opens a screen takes no set: (the screen's own set: reads stop)")
			}
			continue
		}
		x, ok := visible[cr]
		if !ok {
			v.rep.extra("error", p, at+"/create", "unknown_model", fmt.Sprintf(`"%s" is not a model of this module or its requires%s`, cr, suggest(cr, keys(visible))))
			continue
		}
		v.slotSet(p, at+"/set", x.m, fieldMap(x.m), a["set"])
	}
}
