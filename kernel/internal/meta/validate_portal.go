package meta

import (
	"fmt"
)

// portalRefused are field types a portal page never shows (contract 0073 §2).
var portalRefused = map[string]bool{"attach": true, "image": true, "json": true, "signature": true, "geo": true,
	"dynamic_link": true, "table": true, "password": true}

// validatePortals checks portal page files (contract 0073 "Validation"): the same checks as check_portals in
// rangka_validate.py.
func (v *validator) validatePortals() {
	all := v.allModels()
	roles := map[string]bool{"owner": true}
	for _, a := range v.reg.Apps {
		for _, r := range a.Roles {
			roles[r] = true
		}
	}
	seen := map[string]string{}
	for _, p := range v.reg.portalFiles {
		first, dup := seen[p.Name]
		if !dup {
			seen[p.Name] = p.App
		}
		if v.skip(p.App) {
			continue
		}
		if dup {
			v.rep.extra("error", p.Doc.Path, "(root)", "duplicate_portal", fmt.Sprintf(`portal page "%s" is already shipped by %s; page names are unique per site`, p.Name, first))
			continue
		}
		v.portal(p, all, roles)
	}
}

func (v *validator) portal(p *Portal, all map[string]vis, roles map[string]bool) {
	d, raw, path := p.Doc, p.Raw, p.Doc.Path
	v.schemaErr("portal", d)
	if t, ok := raw["type"].(string); ok && !PortalBuilt[t] && contains(PortalTypes, t) {
		v.rep.extra("error", path, "type", "portal_type_unbuilt", fmt.Sprintf(`page type "%s" is reserved and not built yet; use document`, t))
	}
	for i, r := range strList(raw["share"]) {
		if !roles[r] {
			v.rep.extra("error", path, fmt.Sprintf("share/%d", i), "portal_unknown_role", fmt.Sprintf(`role "%s" is not a role of any module or app%s`, r, suggest(r, keys(roles))))
		}
	}
	mv, ok := all[p.Model]
	if _, has := raw["model"].(string); has && !ok {
		v.rep.extra("error", path, "model", "portal_unknown_model", fmt.Sprintf(`"%s" is not a model%s`, p.Model, suggest(p.Model, keys(all))))
	}
	if !v.portalSlots(p) || !ok {
		return
	}
	m := mv.m
	if p.Type == "order_page" {
		v.orderPage(p, m, all)
		return
	}
	if f := v.portalField(path, "customer", m, raw["customer"]); f != nil && f.Type != "link" {
		v.rep.extra("error", path, "customer", "portal_field_type", fmt.Sprintf(`customer "%s" must be a link field, not %s`, f.Name, f.Type))
	}
	if f := v.portalField(path, "amount", m, raw["amount"]); f != nil && !f.IsNumeric() {
		v.rep.extra("error", path, "amount", "portal_field_type", fmt.Sprintf(`amount "%s" must be a number field, not %s`, f.Name, f.Type))
	}
	if f := v.portalField(path, "due", m, raw["due"]); f != nil && f.Type != "date" && f.Type != "datetime" {
		v.rep.extra("error", path, "due", "portal_field_type", fmt.Sprintf(`due "%s" must be a date field, not %s`, f.Name, f.Type))
	}
	for i, x := range asList(raw["fields"]) {
		v.portalField(path, fmt.Sprintf("fields/%d", i), m, x)
	}
	if l := asMap(raw["lines"]); l != nil {
		if fn, ok := l["field"].(string); ok {
			f := m.Field(fn)
			switch {
			case f == nil:
				v.rep.extra("error", path, "lines/field", "portal_unknown_field", fmt.Sprintf(`"%s" is not a field of %s`, fn, m.Name))
			case f.Type != "table":
				v.rep.extra("error", path, "lines/field", "portal_field_type", fmt.Sprintf(`lines "%s" must be a table field, not %s`, fn, f.Type))
			default:
				if cv, ok := all[f.Of]; ok {
					for i, x := range asList(l["columns"]) {
						v.portalField(path, fmt.Sprintf("lines/columns/%d", i), cv.m, x)
					}
				}
			}
		}
	}
	if pn, ok := raw["print"].(string); ok {
		found := false
		for _, pf := range v.reg.printFiles {
			if pf.Name == pn && pf.Model == m.Name {
				found = true
			}
		}
		if !found {
			v.rep.extra("error", path, "print", "portal_print_model", fmt.Sprintf(`"%s" is not a print format of %s`, pn, m.Name))
		}
		for _, pf := range v.reg.printFiles {
			if pf.Name == pn && pf.Model == m.Name && !PortalPrintAnchored(pf) {
				v.rep.extra("error", path, "print", "portal_print_related", fmt.Sprintf(`print "%s" has a related list not matched on the document name; a portal page cannot show it`, pn))
				break
			}
		}
	}
	pay := asMap(raw["payment"])
	if pay == nil {
		return
	}
	pmn, _ := pay["model"].(string)
	pv, ok := all[pmn]
	if !ok {
		if _, has := pay["model"].(string); has {
			v.rep.extra("error", path, "payment/model", "portal_unknown_model", fmt.Sprintf(`"%s" is not a model%s`, pmn, suggest(pmn, keys(all))))
		}
		return
	}
	if f := v.portalField(path, "payment/link", pv.m, pay["link"]); f != nil && (f.Type != "link" || f.To != m.Name) {
		v.rep.extra("error", path, "payment/link", "portal_field_type", fmt.Sprintf(`payment link "%s" must be a link to %s`, f.Name, m.Name))
	}
	for i, x := range asList(pay["fields"]) {
		v.portalField(path, fmt.Sprintf("payment/fields/%d", i), pv.m, x)
	}
	if f := v.portalField(path, "payment/qris", pv.m, pay["qris"]); f != nil && f.Type != "text" && f.Type != "data" {
		v.rep.extra("error", path, "payment/qris", "portal_field_type", fmt.Sprintf(`qris "%s" must be a text field, not %s`, f.Name, f.Type))
	}
	for i, x := range asList(pay["qris_label"]) {
		v.portalField(path, fmt.Sprintf("payment/qris_label/%d", i), pv.m, x)
	}
}

// portalField checks one field a page shows: it exists, is not hidden from anyone, and is of a type a page shows.
// It returns the field when it exists (x a string), else nil.
func (v *validator) portalField(path, where string, m *Model, x any) *Field {
	name, ok := x.(string)
	if !ok {
		return nil
	}
	f := m.Field(name)
	if f == nil {
		v.rep.extra("error", path, where, "portal_unknown_field", fmt.Sprintf(`"%s" is not a field of %s`, name, m.Name))
		return nil
	}
	hiddenFor := len(strList(f.Raw["hidden_for"])) > 0
	if base := f.MoneyOf(); base != "" { // an IDR twin follows its money field
		if bf := m.Field(base); bf != nil && len(strList(bf.Raw["hidden_for"])) > 0 {
			hiddenFor = true
		}
	}
	if hiddenFor || boolean(f.Raw, "hidden") {
		v.rep.extra("error", path, where, "portal_field_hidden", fmt.Sprintf(`"%s" of %s is hidden in the app; a portal page never shows it`, name, m.Name))
	}
	if portalRefused[f.Type] {
		v.rep.extra("error", path, where, "portal_field_type", fmt.Sprintf(`"%s" is a %s field; a portal page does not show %s fields`, name, f.Type, f.Type))
	}
	return f
}

// portalDocumentOnly and portalOrderOnly are the slots of one page type (contract 0073 §1, §9).
var (
	portalDocumentOnly = []string{"customer", "amount", "due", "fields", "print", "payment"}
	portalOrderOnly    = []string{"anchor", "menu", "guest", "note", "status"}
)

// portalSlots checks that a page fills the slots of its type and none of another (portal_slot_missing,
// portal_slot_unused), and the document lifetime cap. It reports whether the slot checks can go on.
func (v *validator) portalSlots(p *Portal) bool {
	raw, path := p.Raw, p.Doc.Path
	if _, ok := raw["type"].(string); !ok {
		return false
	}
	lines := asMap(raw["lines"])
	unused := portalOrderOnly
	need := []string{}
	lineNeed, lineUnused := []string{"columns"}, []string{"item", "qty", "note"}
	if p.Type == "order_page" {
		unused, need = portalDocumentOnly, []string{"anchor", "menu", "lines", "status"}
		lineNeed, lineUnused = []string{"item", "qty"}, []string{"columns"}
	} else if n := integer(raw, "lifetime_days"); n > PortalMaxLifetimeDays {
		v.rep.extra("error", path, "lifetime_days", "schema", fmt.Sprintf("a document link lives at most %d days", PortalMaxLifetimeDays))
	}
	okay := true
	for _, k := range unused {
		if _, has := raw[k]; has {
			v.rep.extra("error", path, k, "portal_slot_unused", fmt.Sprintf(`"%s" is not a slot of %s pages`, k, p.Type))
		}
	}
	for _, k := range need {
		if _, has := raw[k]; !has {
			v.rep.extra("error", path, "(root)", "portal_slot_missing", fmt.Sprintf(`%s pages need "%s"`, p.Type, k))
			okay = false
		}
	}
	if lines != nil {
		for _, k := range lineUnused {
			if _, has := lines[k]; has {
				v.rep.extra("error", path, "lines/"+k, "portal_slot_unused", fmt.Sprintf(`"lines.%s" is not a slot of %s pages`, k, p.Type))
			}
		}
		for _, k := range lineNeed {
			if _, has := lines[k]; !has {
				v.rep.extra("error", path, "lines", "portal_slot_missing", fmt.Sprintf(`%s pages need "lines.%s"`, p.Type, k))
				okay = false
			}
		}
	}
	return okay
}

// orderPage checks the slots of an order_page (contract 0073 §9): every named field exists, is shown to nobody
// it is hidden from, and has the type its slot needs.
func (v *validator) orderPage(p *Portal, m *Model, all map[string]vis) {
	path := p.Doc.Path
	typeErr := func(where, name, want, got string) {
		v.rep.extra("error", path, where, "portal_field_type", fmt.Sprintf(`%s "%s" must be %s, not %s`, where, name, want, got))
	}
	text := func(t string) bool { return t == "data" || t == "text" || t == "small_text" }
	av, ok := all[p.AnchorModel]
	if !ok {
		v.rep.extra("error", path, "anchor/model", "portal_unknown_model", fmt.Sprintf(`"%s" is not a model%s`, p.AnchorModel, suggest(p.AnchorModel, keys(all))))
	} else {
		if f := v.portalField(path, "anchor/field", m, p.AnchorField); f != nil && (f.Type != "link" || f.To != p.AnchorModel) {
			typeErr("anchor/field", f.Name, "a link to "+p.AnchorModel, f.Type)
		}
		if p.AnchorTitle != "" {
			if f := v.portalField(path, "anchor/title", av.m, p.AnchorTitle); f != nil && !text(f.Type) {
				typeErr("anchor/title", f.Name, "a text field", f.Type)
			}
		}
	}
	mv, ok := all[p.MenuModel]
	if !ok {
		v.rep.extra("error", path, "menu/model", "portal_unknown_model", fmt.Sprintf(`"%s" is not a model%s`, p.MenuModel, suggest(p.MenuModel, keys(all))))
	} else {
		mm := mv.m
		if f := v.portalField(path, "menu/title", mm, p.MenuTitle); f != nil && !text(f.Type) {
			typeErr("menu/title", f.Name, "a text field", f.Type)
		}
		if f := v.portalField(path, "menu/price", mm, p.MenuPrice); f != nil && !f.IsNumeric() {
			typeErr("menu/price", f.Name, "a number field", f.Type)
		}
		if p.MenuGroup != "" {
			if f := v.portalField(path, "menu/group_by", mm, p.MenuGroup); f != nil && f.Type != "link" && f.Type != "select" && !text(f.Type) {
				typeErr("menu/group_by", f.Name, "a link, select or text field", f.Type)
			}
		}
		if p.MenuSoldOut != "" {
			if f := v.portalField(path, "menu/sold_out", mm, p.MenuSoldOut); f != nil && f.Type != "check" {
				typeErr("menu/sold_out", f.Name, "a check field", f.Type)
			}
		}
	}
	if f := m.Field(p.LinesField); f == nil {
		v.rep.extra("error", path, "lines/field", "portal_unknown_field", fmt.Sprintf(`"%s" is not a field of %s`, p.LinesField, m.Name))
	} else if f.Type != "table" {
		typeErr("lines/field", f.Name, "a table field", f.Type)
	} else if cv, ok := all[f.Of]; ok {
		if lf := v.portalField(path, "lines/item", cv.m, p.LinesItem); lf != nil && (lf.Type != "link" || lf.To != p.MenuModel) {
			typeErr("lines/item", lf.Name, "a link to "+p.MenuModel, lf.Type)
		}
		if lf := v.portalField(path, "lines/qty", cv.m, p.LinesQty); lf != nil && !lf.IsNumeric() {
			typeErr("lines/qty", lf.Name, "a number field", lf.Type)
		}
		if p.LinesNote != "" {
			if lf := v.portalField(path, "lines/note", cv.m, p.LinesNote); lf != nil && !text(lf.Type) {
				typeErr("lines/note", lf.Name, "a text field", lf.Type)
			}
		}
	}
	for _, slot := range [][2]string{{"guest", p.Guest}, {"note", p.Note}} {
		if slot[1] == "" {
			continue
		}
		if f := v.portalField(path, slot[0], m, slot[1]); f != nil && !text(f.Type) {
			typeErr(slot[0], f.Name, "a text field", f.Type)
		}
	}
	if f := v.portalField(path, "status/field", m, p.StatusField); f != nil {
		if f.Type != "select" {
			typeErr("status/field", f.Name, "a select field", f.Type)
		} else {
			vals := append(append([]string{}, p.StatusOpen...), p.StatusCancel)
			for _, s := range vals {
				if s != "" && f.Option(s) == nil {
					v.rep.extra("error", path, "status", "portal_unknown_field", fmt.Sprintf(`"%s" is not an option of %s.%s`, s, m.Name, f.Name))
				}
			}
		}
	}
}
