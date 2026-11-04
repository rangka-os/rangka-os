package meta

import (
	"fmt"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// applyOverlays merges company overlays (<site>/custom/<model>.rk) onto app models: fields, rules and approvals
// are appended, select options are appended, form layout and list columns are replaced before view additions
// are placed after the named field or column, and the kanban column order is set. App files are never changed.
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
		if extra := asList(o["approvals"]); len(extra) > 0 {
			raw["approvals"] = append(asList(raw["approvals"]), deepCopy(extra).([]any)...)
		}
		addOptions(fields, asMap(o["options"]))
		views := asMap(raw["views"])
		if views == nil {
			views = map[string]any{}
			raw["views"] = views
		}
		ov := asMap(o["views"])
		if layout := asList(asMap(ov["form"])["layout"]); len(layout) > 0 {
			form := asMap(views["form"])
			if form == nil {
				form = map[string]any{}
				views["form"] = form
			}
			form["layout"] = deepCopy(layout)
		}
		for _, av := range asList(asMap(ov["form"])["add"]) {
			placeInForm(views, asMap(av))
		}
		if cols := asList(asMap(ov["list"])["columns"]); len(cols) > 0 {
			list := asMap(views["list"])
			if list == nil {
				list = map[string]any{}
				views["list"] = list
			}
			list["columns"] = deepCopy(cols)
		}
		for _, av := range asList(asMap(ov["list"])["add"]) {
			ad := asMap(av)
			list := asMap(views["list"])
			if list == nil {
				continue
			}
			if contains(strList(asList(list["columns"])), pyStr(ad["field"])) {
				continue
			}
			list["columns"] = insertAfter(asList(list["columns"]), pyStrOr(ad["after"]), ad["field"])
		}
		if cols := asList(asMap(ov["kanban"])["columns"]); len(cols) > 0 {
			if kb := asMap(views["kanban"]); kb != nil {
				kb["columns"] = deepCopy(cols)
			}
		}
		nm := parseModel(m.App, &rk.Doc{Path: m.Doc.Path, Text: m.Doc.Text, Root: m.Doc.Root, Data: raw})
		nm.Doc, nm.Name, nm.Overlay = m.Doc, mn, "custom/"+st+".rk"
		r.Models[mn] = nm
	}
}

// addOptions appends an overlay's options to the select fields they name; a value the field already has is skipped
// (validation reports it as bad_option).
func addOptions(fields []any, options map[string]any) {
	for _, fn := range sortedKeys(options) {
		for _, f := range fields {
			fm := asMap(f)
			if fm == nil || pyStr(fm["name"]) != fn || fm["type"] != "select" {
				continue
			}
			have := map[string]bool{}
			opts := asList(fm["options"])
			for _, o := range opts {
				have[pyStr(asMap(o)["value"])] = true
			}
			for _, o := range asList(options[fn]) {
				if om := asMap(o); om != nil && !have[pyStr(om["value"])] {
					have[pyStr(om["value"])] = true
					opts = append(opts, deepCopy(om))
				}
			}
			fm["options"] = opts
		}
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
	for _, sv := range layout {
		sm := asMap(sv)
		if contains(strList(asList(sm["fields"])), pyStr(ad["field"])) {
			return
		}
		for _, c := range asList(sm["columns"]) {
			if contains(strList(asList(c)), pyStr(ad["field"])) {
				return
			}
		}
	}
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
