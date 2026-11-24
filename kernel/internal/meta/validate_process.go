package meta

import (
	"fmt"
)

// validateProcesses checks process files (contract 0014 §8): the same checks as check_processes in
// rangka_validate.py. A stage may name any model of any loaded unit, installed or not.
func (v *validator) validateProcesses() {
	all := v.allModels()
	for _, p := range v.reg.processFiles {
		if p.App != "" && v.skip(p.App) {
			continue
		}
		v.process(p, all)
	}
}

func (v *validator) process(p *Process, all map[string]vis) {
	d, raw := p.Doc, p.Raw
	v.schemaErr("process", d)
	if got, ok := raw["name"].(string); !ok || got != p.Name {
		v.rep.extra("error", d.Path, "name", "process_name_mismatch", fmt.Sprintf(`process name "%s" must match the file name "%s"`, pyStr(raw["name"]), p.Name))
	}
	seen := map[string]bool{}
	for i, st := range p.Stages {
		at := fmt.Sprintf("stages/%d", i)
		sm := asMap(asList(raw["stages"])[i])
		if _, has := sm["name"]; has {
			if seen[st.Name] {
				v.rep.extra("error", d.Path, at+"/name", "process_duplicate_stage", fmt.Sprintf(`stage "%s" is declared twice`, st.Name))
			}
			seen[st.Name] = true
		}
		model, known := all[st.Model]
		if _, has := sm["model"]; has && !known {
			v.rep.extra("error", d.Path, at+"/model", "process_unknown_model", fmt.Sprintf(`"%s" is not a model%s`, st.Model, suggest(st.Model, keys(all))))
		}
		for j, mn := range strList(sm["models"]) {
			if _, ok := all[mn]; !ok {
				v.rep.extra("error", d.Path, fmt.Sprintf("%s/models/%d", at, j), "process_unknown_model", fmt.Sprintf(`"%s" is not a model%s`, mn, suggest(mn, keys(all))))
			}
		}
		if _, has := sm["when"]; has && known {
			v.processWhen(d.Path, at+"/when", sm["when"], model.m, all)
		}
		if _, has := sm["starter"]; has {
			var m *Model
			if known {
				m = model.m
			}
			v.processStarter(d.Path, at+"/starter", asList(sm["starter"]), st.Model, m)
		}
	}
}

// starterTypes are the field types a stage's starter may fill (contract 0076 §4): plain text the owner said.
var starterTypes = map[string]bool{"data": true, "small_text": true, "text": true}

// processStarter checks a stage's starter (contract 0076 §4), as check_starter in rangka_validate.py: the stage names
// a model that is master data (not submittable), and each key is a plain text field of it that is not computed,
// read-only or hidden from a role. m is nil when the stage has no known model (an unknown one is reported above).
func (v *validator) processStarter(path, at string, rows []any, model string, m *Model) {
	if model == "" {
		v.rep.extra("error", path, at, "process_starter_model", "starter needs the stage's model")
		return
	}
	if m == nil {
		return
	}
	if m.IsSubmittable {
		v.rep.extra("error", path, at, "process_starter_document", fmt.Sprintf(`"%s" is a document; starter holds master data only`, model))
		return
	}
	fields := fieldMap(m)
	for i, r := range rows {
		for _, k := range keys(asMap(r)) {
			where := fmt.Sprintf("%s/%d/%s", at, i, k)
			f, ok := fields[k]
			if !ok {
				v.rep.extra("error", path, where, "process_starter_field", fmt.Sprintf(`"%s" is not a field of %s%s`, k, model, suggest(k, keys(fields))))
				continue
			}
			if !starterTypes[str(f, "type")] || f["compute"] != nil || boolean(f, "read_only") || len(asList(f["hidden_for"])) > 0 {
				v.rep.extra("error", path, where, "process_starter_field", fmt.Sprintf(`field "%s" of %s cannot come from the interview: plain text fields only, not computed, read-only or hidden from a role`, k, model))
			}
		}
	}
}

// processWhen runs the CEL field check of a rule's check over the stage model and reports its first problem
// as process_when.
func (v *validator) processWhen(path, where string, x any, m *Model, all map[string]vis) {
	saved := v.rep
	v.rep = NewReport(saved.Root)
	v.exprTo(true, path, where, x, fieldMap(m), all)
	got := v.rep.Extra
	v.rep = saved
	if len(got) > 0 {
		v.rep.extra("error", path, where, "process_when", got[0].Message)
	}
}
