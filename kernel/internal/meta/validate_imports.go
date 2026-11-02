package meta

import (
	"fmt"
	"sort"
	"strings"
)

// Rules for the kind `import` (contract 0012 §6). The order and messages follow check_imports() in
// rangka_validate.py so the output stays byte-identical.

// imports validates every import file: the apps' (sorted by app, then file stem) when site is false, else the
// site's. Files are read from Registry.Docs, so an app import a site import replaces is still checked.
func (v *validator) imports(all map[string]vis, site bool) {
	type item struct {
		app, stem string
		im        *Import
	}
	var list []item
	for key, d := range v.reg.Docs {
		var app, rest string
		if site {
			if !strings.HasPrefix(key, "imports/") {
				continue
			}
			rest = strings.TrimPrefix(key, "imports/")
		} else {
			parts := strings.SplitN(key, "/", 4)
			if len(parts) != 4 || parts[0] != "apps" || parts[2] != "imports" {
				continue
			}
			app, rest = parts[1], parts[3]
			if v.skip(app) {
				continue
			}
		}
		if strings.Contains(rest, "/") {
			continue
		}
		stem := strings.TrimSuffix(rest, ".rk")
		list = append(list, item{app, stem, parseImport(d, app)})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].app != list[j].app {
			return list[i].app < list[j].app
		}
		return list[i].stem < list[j].stem
	})
	for _, it := range list {
		v.checkImport(it.stem, it.im, all)
	}
}

func (v *validator) checkImport(stem string, im *Import, all map[string]vis) {
	p := im.Doc.Path
	v.schemaErr("import", im.Doc)
	if _, ok := im.Doc.Data.(map[string]any); !ok {
		return // not a mapping: the schema errors say so
	}
	if got, ok := im.Raw["name"].(string); !ok || got != stem {
		v.rep.extra("error", p, "name", "name_mismatch", fmt.Sprintf(`import name "%s" must match the file name "%s"`, pyStr(im.Raw["name"]), stem))
	}
	if im.Model == "" {
		return
	}
	x, ok := all[im.Model]
	if !ok {
		v.rep.extra("error", p, "model", "unknown_model", fmt.Sprintf(`"%s" is not a model%s`, im.Model, suggest(im.Model, keys(all))))
		return
	}
	fields := fieldMap(x.m)
	var child map[string]map[string]any
	var childName string
	if im.Lines != nil && im.Lines.Field != "" {
		f, exists := fields[im.Lines.Field]
		switch {
		case !exists:
			v.rep.extra("error", p, "lines/field", "unknown_field", fmt.Sprintf(`"%s" is not a field of %s`, im.Lines.Field, im.Model))
		case f["type"] != "table":
			v.rep.extra("error", p, "lines/field", "not_a_table", fmt.Sprintf(`"%s" is not a table field of %s`, im.Lines.Field, im.Model))
		default:
			if cn, _ := f["of"].(string); all[cn].m != nil {
				childName, child = cn, fieldMap(all[cn].m)
			}
		}
	}
	v.importMap(p, "map", im.Map, im.MapOrder, im.Model, fields, all)
	if im.Lines != nil && child != nil {
		v.importMap(p, "lines/map", im.Lines.Map, im.Lines.MapOrder, childName, child, all)
	}
	if im.Per == "outlet" {
		if f := fields["outlet"]; f == nil || f["type"] != "link" || f["to"] != "outlet" {
			v.rep.extra("error", p, "per", "import_per_outlet", fmt.Sprintf(`per: outlet needs a link field "outlet" to outlet on %s`, im.Model))
		}
		if len(im.Key) == 0 {
			v.rep.extra("warn", p, "per", "import_no_key", "per: outlet imports should set key so overlapping files are not imported twice")
		}
	}
}

func (v *validator) importMap(p, at string, m map[string]MapValue, order []string, model string, fields map[string]map[string]any, all map[string]vis) {
	for _, k := range order {
		mv := m[k]
		w := at + "/" + k
		f, exists := fields[k]
		if !exists && !SystemFields[k] {
			v.rep.extra("error", p, w, "unknown_field", fmt.Sprintf(`"%s" is not a field of %s`, k, model))
		}
		if mv.CEL != "" {
			v.parens(p, w, mv.CEL)
		}
		if mv.Match == "" || !exists {
			continue
		}
		if f["type"] != "link" {
			v.rep.extra("error", p, w+"/match", "not_a_link", fmt.Sprintf(`"%s" is not a link field`, k))
			continue
		}
		if to, _ := f["to"].(string); all[to].m != nil {
			if !fieldNames(all[to].m)[mv.Match] && !SystemFields[mv.Match] {
				v.rep.extra("error", p, w+"/match", "unknown_field", fmt.Sprintf(`"%s" is not a field of %s`, mv.Match, to))
			}
		}
	}
}
