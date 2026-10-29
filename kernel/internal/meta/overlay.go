package meta

import (
	"fmt"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// applyOverlays merges company overlays (<site>/custom/<model>.rk) onto app models: fields and rules are
// appended, view additions are placed after the named field or column. App files are never changed.
func (r *Registry) applyOverlays() {
	for _, st := range sortedKeysM(r.Overlays) {
		d := r.Overlays[st]
		o := asMap(d.Data)
		mn, _ := o["model"].(string)
		m := r.Models[mn]
		if m == nil || mn != st {
			continue
		}
		raw := deepCopy(m.Raw).(map[string]any)
		names := map[string]bool{}
		fields := asList(raw["fields"])
		for _, f := range fields {
			names[pyStr(asMap(f)["name"])] = true
		}
		for _, f := range asList(o["fields"]) {
			fm := asMap(f)
			if fm == nil || names[pyStr(fm["name"])] {
				continue
			}
			names[pyStr(fm["name"])] = true
			fields = append(fields, deepCopy(fm))
		}
		raw["fields"] = fields
		if extra := asList(o["rules"]); len(extra) > 0 {
			raw["rules"] = append(asList(raw["rules"]), deepCopy(extra).([]any)...)
		}
		views := asMap(raw["views"])
		if views == nil {
			views = map[string]any{}
			raw["views"] = views
		}
		ov := asMap(o["views"])
		for _, av := range asList(asMap(ov["form"])["add"]) {
			placeInForm(views, asMap(av))
		}
		for _, av := range asList(asMap(ov["list"])["add"]) {
			ad := asMap(av)
			list := asMap(views["list"])
			if list == nil {
				continue
			}
			list["columns"] = insertAfter(asList(list["columns"]), pyStrOr(ad["after"]), ad["field"])
		}
		nm := parseModel(m.App, &rk.Doc{Path: m.Doc.Path, Text: m.Doc.Text, Root: m.Doc.Root, Data: raw})
		nm.Doc, nm.Name, nm.Overlay = m.Doc, mn, "custom/"+st+".rk"
		r.Models[mn] = nm
	}
}

func insertAfter(l []any, after string, v any) []any {
	for i, x := range l {
		if after != "" && pyStr(x) == after {
			out := append([]any{}, l[:i+1]...)
			out = append(out, v)
			return append(out, l[i+1:]...)
		}
	}
	return append(l, v)
}

func placeInForm(views map[string]any, ad map[string]any) {
	form := asMap(views["form"])
	if form == nil {
		return
	}
	layout := asList(form["layout"])
	after := pyStrOr(ad["after"])
	for _, sv := range layout {
		sm := asMap(sv)
		if l := asList(sm["fields"]); after != "" && contains(strList(l), after) {
			sm["fields"] = insertAfter(l, after, ad["field"])
			return
		}
		cols := asList(sm["columns"])
		for ci, c := range cols {
			if l := asList(c); after != "" && contains(strList(l), after) {
				cols[ci] = insertAfter(l, after, ad["field"])
				return
			}
		}
	}
	if len(layout) == 0 {
		return
	}
	last := asMap(layout[len(layout)-1])
	if cols := asList(last["columns"]); len(cols) > 0 {
		cols[len(cols)-1] = append(asList(cols[len(cols)-1]), ad["field"])
		return
	}
	last["fields"] = append(asList(last["fields"]), ad["field"])
}

func deepCopy(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[k] = deepCopy(e)
		}
		return m
	case []any:
		l := make([]any, len(x))
		for i, e := range x {
			l[i] = deepCopy(e)
		}
		return l
	}
	return v
}

var _ = fmt.Sprint
