package meta

import (
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	ttemplate "text/template"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
)

// PrintFuncNames are the helpers print templates may call (contract 0008 §3). The renderer (docprint)
// provides them; validation parses templates with no-op stand-ins of the same names.
var PrintFuncNames = []string{"rupiah", "money", "number", "date", "terbilang", "label", "option", "sub", "sum", "overdue", "aging", "upper", "taxrate", "dppfactor",
	"add", "mul", "div", "round", "dec", "digits", "isodate", "qr"}

func printFuncStubs() template.FuncMap {
	fm := template.FuncMap{}
	for _, n := range PrintFuncNames {
		fm[n] = func(...any) any { return nil }
	}
	return fm
}

// validatePrints checks print formats and letterheads (kernel extension, after more()).
func (v *validator) validatePrints() {
	all := v.allModels()
	for _, p := range v.reg.printFiles {
		if p.App != "" && v.skip(p.App) {
			continue
		}
		v.printFormat(p, all)
	}
	if s := v.reg.Site; s != nil {
		for _, org := range sortedKeysM(s.Letterheads) {
			d := s.Letterheads[org]
			if logo := str(asMap(d.Data), "logo"); logo != "" {
				if abs, ok := v.reg.siteFile(logo); !ok {
					v.rep.extra("error", d.Path, "logo", "logo_missing", fmt.Sprintf(`logo "%s" must be a file inside the site folder %s`, logo, v.rep.rel(s.Dir)))
				} else if !fileExists(abs) {
					v.rep.extra("error", d.Path, "logo", "logo_missing", fmt.Sprintf(`logo file "%s" does not exist in %s`, logo, v.rep.rel(s.Dir)))
				}
			}
		}
		if cfg := s.Docs["config.rk"]; cfg != nil {
			for _, id := range sortedKeysM(v.reg.Orgs) {
				if s.Letterheads[id] == nil {
					v.rep.extra("warn", cfg.Path, "orgs/"+id, "letterhead_missing", fmt.Sprintf(`org "%s" has no letterheads/%s.rk; its documents print without address, bank or signatory`, id, id))
				}
			}
		}
	}
}

func (v *validator) printFormat(p *Print, all map[string]vis) {
	d, raw := p.Doc, p.Raw
	v.schemaErr("print", d)
	if got, ok := raw["name"].(string); !ok || got != p.Name {
		v.rep.extra("error", d.Path, "name", "name_mismatch", fmt.Sprintf(`print name "%s" must match the file name "%s"`, pyStr(raw["name"]), p.Name))
	}
	model, known := all[p.Model]
	if _, has := raw["model"]; has && !known {
		v.rep.extra("error", d.Path, "model", "unknown_model", fmt.Sprintf(`"%s" is not a model%s`, p.Model, suggest(p.Model, keys(all))))
	}
	related := asMap(raw["related"])
	for _, rn := range sortedKeys(related) {
		rel := p.Related[rn]
		at := "related/" + rn
		target, ok := all[rel.Model]
		if !ok {
			if rel.Model != "" {
				v.rep.extra("error", d.Path, at+"/model", "unknown_model", fmt.Sprintf(`"%s" is not a model%s`, rel.Model, suggest(rel.Model, keys(all))))
			}
			continue
		}
		relFields := union(fieldNames(target.m), SystemFields)
		for _, k := range sortedKeys(asMap(asMap(related[rn])["match"])) {
			if !relFields[k] {
				v.rep.extra("error", d.Path, at+"/match/"+k, "unknown_field", fmt.Sprintf(`"%s" is not a field of %s`, k, rel.Model))
			}
			if dv, ok := rel.Match[k]; ok && known && !union(fieldNames(model.m), SystemFields)[dv] {
				v.rep.extra("error", d.Path, at+"/match/"+k, "unknown_field", fmt.Sprintf(`"%s" is not a field of %s`, dv, p.Model))
			}
		}
		for _, k := range sortedKeys(asMap(asMap(related[rn])["range"])) {
			if !relFields[k] {
				v.rep.extra("error", d.Path, at+"/range/"+k, "unknown_field", fmt.Sprintf(`"%s" is not a field of %s`, k, rel.Model))
			}
			for _, dv := range rel.Range[k] {
				if known && !union(fieldNames(model.m), SystemFields)[dv] {
					v.rep.extra("error", d.Path, at+"/range/"+k, "unknown_field", fmt.Sprintf(`"%s" is not a field of %s`, dv, p.Model))
				}
			}
		}
		for i, q := range rel.Require {
			if _, err := cel.Compile(q.Check); err != nil {
				v.rep.extra("error", d.Path, fmt.Sprintf("%s/require/%d/check", at, i), "cel_syntax", truncMsg(err.Error()))
			}
		}
		// a field of the row named doc wins over the printed document in require (Engine.Holds): warn (contract 0029 §4)
		if len(rel.Require) > 0 && relFields["doc"] {
			v.rep.extra("warn", d.Path, at+"/require", "require_doc_shadowed",
				fmt.Sprintf(`%s has a field named "doc": in require it hides the printed document (doc.*); rename the field`, rel.Model))
		}
	}
	v.printTemplate(p)
}

func (v *validator) printTemplate(p *Print) {
	d := p.Doc
	t := str(p.Raw, "template")
	if t == "" {
		return // the schema reports it
	}
	if filepath.IsAbs(t) || !Inside(p.Root, p.Template) {
		v.rep.extra("error", d.Path, "template", "template_outside", fmt.Sprintf(`template "%s" must stay inside %s`, t, v.rep.rel(p.Root)))
		return
	}
	src, err := os.ReadFile(p.Template)
	if err != nil {
		v.rep.extra("error", d.Path, "template", "template_missing", fmt.Sprintf(`template file "%s" does not exist`, t))
		return
	}
	if p.Output == "xml" {
		if _, err := ttemplate.New(filepath.Base(p.Template)).Funcs(ttemplate.FuncMap(printFuncStubs())).Parse(string(src)); err != nil {
			v.rep.extra("error", d.Path, "template", "template_error", truncMsg(err.Error()))
		}
		return
	}
	if _, err := template.New(filepath.Base(p.Template)).Funcs(printFuncStubs()).Parse(string(src)); err != nil {
		v.rep.extra("error", d.Path, "template", "template_error", truncMsg(err.Error()))
	}
}
