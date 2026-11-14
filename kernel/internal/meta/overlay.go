package meta

import (
	"fmt"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// applyOverlays merges company overlays (<site>/custom/<model>.rk) onto app models: the overlay's label and field
// labels replace the app's (0049 §5), fields, rules and approvals are appended, select options are appended, form layout and list columns are replaced before view additions
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
		// the company's own words (contract 0049 §5): the model's label (page title, "Buat …", widget titles,
		// references) and the labels of the app's fields; the app's name and field names stay
		if l, ok := o["label"]; ok && l != nil {
			raw["label"] = deepCopy(l)
		}
		for _, fn := range sortedKeys(asMap(o["labels"])) {
			for _, f := range fields {
				if fm := asMap(f); fm != nil && pyStr(fm["name"]) == fn {
					fm["label"] = deepCopy(asMap(o["labels"])[fn])
				}
			}
		}
		// the IDR twin of a relabelled money field follows the new word, "<label> (IDR)" as WithTwins names it, unless
		// labels names the twin itself (contract 0013; audit overlay label N1)
		labels := asMap(o["labels"])
		for _, fn := range sortedKeys(labels) {
			l := asMap(labels[fn])
			if l == nil {
				continue
			}
			for _, f := range fields {
				tm := asMap(f)
				if tm == nil || pyStr(tm["money_of"]) != fn {
					continue
				}
				if _, own := labels[pyStr(tm["name"])]; own {
					continue
				}
				tl := map[string]any{}
				for k, x := range l {
					tl[k] = pyStr(x) + " (IDR)"
				}
				tm["label"] = tl
			}
		}
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

// applyAppOverlays sets the company's words for an app's menu (<site>/custom/app.<app>.rk, contract 0049): menu_labels
// maps a group or sub-menu label of the app's menu, by its Indonesian text as written in app.rk, to the company's
// label. Only labels change; the groups, their order and their models stay the app's. App files are never changed.
func (r *Registry) applyAppOverlays() {
	for _, name := range sortedKeysM(r.AppOverlays) {
		a := r.App[name]
		o := asMap(r.AppOverlays[name].Data)
		ml := asMap(o["menu_labels"])
		if a == nil || len(ml) == 0 || pyStr(o["app"]) != name {
			continue
		}
		raw := deepCopy(a.Raw).(map[string]any)
		for _, g := range asList(raw["menu"]) {
			gm := asMap(g)
			if gm == nil {
				continue
			}
			if l, ok := ml[menuLabelKey(gm["group"])]; ok && l != nil {
				gm["group"] = deepCopy(l)
			}
			for _, it := range asList(gm["items"]) {
				if sm := asMap(it); sm != nil {
					if l, ok := ml[menuLabelKey(sm["label"])]; ok && l != nil {
						sm["label"] = deepCopy(l)
					}
				}
			}
		}
		a.Raw = raw
	}
}

// menuLabelKey is the key menu_labels uses for a group or sub-menu label: its Indonesian text ("" when none).
func menuLabelKey(l any) string {
	s, _ := asMap(l)["id"].(string)
	return s
}

// menuLabelKeys lists the keys menu_labels may use for unit a: the Indonesian text of each group and sub-menu label
// of its menu, and for a module its title (the group a composed site's menu shows for it, composedMenu).
func menuLabelKeys(a *App) []string {
	var out []string
	if a.Kind == KindModule && a.Title["id"] != "" {
		out = append(out, a.Title["id"])
	}
	for _, g := range asList(a.Raw["menu"]) {
		gm := asMap(g)
		if k := menuLabelKey(gm["group"]); k != "" {
			out = append(out, k)
		}
		for _, it := range asList(gm["items"]) {
			if k := menuLabelKey(asMap(it)["label"]); k != "" {
				out = append(out, k)
			}
		}
	}
	return out
}

// moduleMenuLabel is the company's label for module n's group in a composed site's menu (custom/app.<n>.rk keyed by
// the module title), if any. The module's title stays the same elsewhere (catalogue, install, Console apps list).
func (r *Registry) moduleMenuLabel(n string, title Label) (Label, bool) {
	d := r.AppOverlays[n]
	if d == nil {
		return nil, false
	}
	o := asMap(d.Data)
	if pyStr(o["app"]) != n {
		return nil, false
	}
	l := asMap(asMap(o["menu_labels"])[title["id"]])
	id, _ := l["id"].(string)
	if id == "" {
		return nil, false
	}
	out := Label{"id": id}
	if en, ok := l["en"].(string); ok && en != "" {
		out["en"] = en
	}
	return out, true
}
