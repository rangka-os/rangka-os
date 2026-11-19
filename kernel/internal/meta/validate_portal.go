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
	if !ok {
		return
	}
	m := mv.m
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
	if len(strList(f.Raw["hidden_for"])) > 0 || boolean(f.Raw, "hidden") {
		v.rep.extra("error", path, where, "portal_field_hidden", fmt.Sprintf(`"%s" of %s is hidden in the app; a portal page never shows it`, name, m.Name))
	}
	if portalRefused[f.Type] {
		v.rep.extra("error", path, where, "portal_field_type", fmt.Sprintf(`"%s" is a %s field; a portal page does not show %s fields`, name, f.Type, f.Type))
	}
	return f
}
