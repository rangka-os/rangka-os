package meta

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Kernel notices (contract 0066 §1, §6): the notices: of every module and the notify steps of every flow. The checks
// mirror tools/rangka_validate.py check_module_notices and check_flow_notices.

const (
	noticeTitleMax = 80
	noticeTextMax  = 300
)

var (
	noticeExprPart = regexp.MustCompile(`\{[^{}]*\}`)
	noticeFieldRef = regexp.MustCompile(`\b([a-z_][a-z0-9_]*)\.([a-z_][a-z0-9_]*)\b`)
	noticeVarName  = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
)

// noticePersonal are field names that hold a person's contact data in the users and contacts modules (§6).
var noticePersonal = map[string]bool{"email": true, "phone": true, "mobile": true, "whatsapp": true, "address": true, "nik": true,
	"bank_account": true, "npwp": true}

// noticeLiteral is the longest literal text of a title or template: every language, {expr} parts left out.
func noticeLiteral(x any) int {
	var texts []string
	switch t := x.(type) {
	case string:
		texts = []string{t}
	case map[string]any:
		for _, k := range sortedKeys(t) {
			if s, ok := t[k].(string); ok {
				texts = append(texts, s)
			}
		}
	}
	longest := 0
	for _, s := range texts {
		if n := utf8.RuneCountInString(noticeExprPart.ReplaceAllString(s, "")); n > longest {
			longest = n
		}
	}
	return longest
}

// noticeTexts are a title's or template's texts, every language in key order.
func noticeTexts(x any) []string {
	switch t := x.(type) {
	case string:
		return []string{t}
	case map[string]any:
		var out []string
		for _, k := range sortedKeys(t) {
			if s, ok := t[k].(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// allRoleNames is every role of every unit, plus owner.
func (v *validator) allRoleNames() map[string]bool {
	out := map[string]bool{"owner": true}
	for _, a := range v.reg.Apps {
		for _, r := range a.Roles {
			out[r] = true
		}
	}
	return out
}

// moduleNotices checks the notices: of every module (§1.1): names unique, labels a title long at most, roles that
// exist, a can: model that exists.
func (v *validator) moduleNotices(all map[string]vis) {
	roles := v.allRoleNames()
	names := make([]string, 0, len(v.reg.App))
	for n, a := range v.reg.App {
		if a.Kind == KindModule {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		a := v.reg.App[name]
		if v.skip(name) || a.Doc == nil {
			continue
		}
		p := a.Doc.Path
		seen := map[string]bool{}
		for i, x := range asList(a.Raw["notices"]) {
			n := asMap(x)
			if n == nil {
				continue
			}
			w := fmt.Sprintf("notices/%d", i)
			nm := pyStr(n["name"])
			if seen[nm] {
				v.rep.extra("error", p, w+"/name", "duplicate_notice", fmt.Sprintf(`notice "%s" is declared twice`, nm))
			}
			seen[nm] = true
			if l := noticeLiteral(n["label"]); l > noticeTitleMax {
				v.rep.extra("error", p, w+"/label", "notice_too_long", fmt.Sprintf("the label is %d characters; a notice title is at most %d", l, noticeTitleMax))
			}
			for j, r := range asList(n["to"]) {
				if !roles[pyStr(r)] {
					v.rep.extra("warn", p, fmt.Sprintf("%s/to/%d", w, j), "unknown_role", fmt.Sprintf(`role "%s" has no file in roles/ (it must exist at install)`, pyStr(r)))
				}
			}
			if c := asMap(n["can"]); c != nil {
				if mn, ok := c["model"].(string); ok {
					if _, exists := all[mn]; !exists {
						v.rep.extra("error", p, w+"/can/model", "unknown_model", fmt.Sprintf(`"%s" is not a model%s`, mn, suggest(mn, keys(all))))
					}
				}
			}
		}
	}
}

// flowVarModels maps the flow's variables to the model of the records they hold: find, find_all and create with
// as:, a for_each over such a variable, and event for a flow on <model>.<event>.
func flowVarModels(f map[string]any, all map[string]vis) map[string]string {
	out := map[string]string{}
	if on, ok := f["on"].(string); ok {
		if head, _, cut := strings.Cut(on, "."); cut {
			if _, isModel := all[head]; isModel {
				out["event"] = head
			}
		}
	}
	var walk func(steps any)
	walk = func(steps any) {
		for _, sv := range asList(steps) {
			st := asMap(sv)
			if st == nil {
				continue
			}
			as, _ := st["as"].(string)
			for _, k := range []string{"find", "find_all", "create"} {
				if mn, ok := st[k].(string); ok && as != "" {
					if _, exists := all[mn]; exists {
						out[as] = mn
					}
				}
			}
			if src, ok := st["for_each"].(string); ok && as != "" && out[src] != "" {
				out[as] = out[src]
			}
			for _, k := range []string{"then", "else", "do"} {
				walk(st[k])
			}
		}
	}
	walk(f["steps"])
	return out
}

// roleCanRead reports whether role may read model m at all: a model without permissions: is open to every role; else
// a grant with read: true (an if: counts).
func roleCanRead(m *Model, role string) bool {
	if role == "owner" || m == nil {
		return true
	}
	raw, present := m.Raw["permissions"]
	if !present || raw == nil {
		return true
	}
	for _, g := range asList(raw) {
		gm := asMap(g)
		if pyStr(gm["role"]) == role && gm["read"] == true {
			return true
		}
	}
	return false
}

// flowNotices checks the notify steps of flow f of unit app (§1.2, §1.4, §6).
func (v *validator) flowNotices(p string, f *Flow, all map[string]vis) {
	roles := v.allRoleNames()
	decls := map[string]map[string]any{}
	var declNames []string
	if a := v.reg.App[f.App]; a != nil && a.Kind == KindModule {
		for _, x := range asList(a.Raw["notices"]) {
			if n := asMap(x); n != nil {
				if nm, ok := n["name"].(string); ok && decls[nm] == nil {
					decls[nm] = n
					declNames = append(declNames, nm)
				}
			}
		}
	}
	vars := flowVarModels(f.Raw, all)
	var walk func(at string, steps any)
	walk = func(at string, steps any) {
		for i, sv := range asList(steps) {
			st := asMap(sv)
			if st == nil {
				continue
			}
			w := fmt.Sprintf("%s/%d", at, i)
			if target, isNotify := st["notify"].(string); isNotify {
				v.notifyStep(p, w, f, st, target, decls, declNames, vars, roles, all)
			}
			for _, k := range []string{"then", "else", "do"} {
				walk(w+"/"+k, st[k])
			}
		}
	}
	walk("steps", f.Raw["steps"])
}

func (v *validator) notifyStep(p, w string, f *Flow, st map[string]any, target string, decls map[string]map[string]any, declNames []string,
	vars map[string]string, roles map[string]bool, all map[string]vis) {
	var decl map[string]any
	if name, ok := st["notice"].(string); ok {
		if decl = decls[name]; decl == nil {
			v.rep.extra("error", p, w+"/notice", "unknown_notice", fmt.Sprintf(`"%s" is not a notice of %s%s`, name, f.App, suggest(name, declNames)))
		}
	}
	if l := noticeLiteral(st["title"]); l > noticeTitleMax {
		v.rep.extra("error", p, w+"/title", "notice_too_long", fmt.Sprintf("the title is %d characters; a notice title is at most %d", l, noticeTitleMax))
	}
	if l := noticeLiteral(st["template"]); l > noticeTextMax {
		v.rep.extra("error", p, w+"/template", "notice_too_long", fmt.Sprintf("the text is %d characters; a notice text is at most %d", l, noticeTextMax))
	}
	// the record's readers (§1.4): every role told must be able to open it
	if rv, ok := st["record"].(string); ok && noticeVarName.MatchString(rv) {
		if mn := vars[rv]; mn != "" {
			told := []string{}
			if target != "owner" && roles[target] {
				told = append(told, target)
			}
			for _, r := range asList(decl["to"]) {
				if s := pyStr(r); s != "owner" && !contains(told, s) {
					told = append(told, s)
				}
			}
			for _, r := range told {
				if !roleCanRead(all[mn].m, r) {
					v.rep.extra("error", p, w+"/record", "notice_role_cannot_read", fmt.Sprintf(`role "%s" cannot read %s, so it may not be told about %s (contract 0066 §6)`, r, mn, rv))
				}
			}
		}
	}
	// contact data in the text (§6)
	for _, k := range []string{"title", "template"} {
		seen := map[string]bool{}
		for _, text := range noticeTexts(st[k]) {
			for _, part := range noticeExprPart.FindAllString(text, -1) {
				for _, m := range noticeFieldRef.FindAllStringSubmatch(part, -1) {
					mn := vars[m[1]]
					if mn == "" || seen[m[1]+"."+m[2]] {
						continue
					}
					x := all[mn]
					if x.app != "users" && x.app != "contacts" {
						continue
					}
					fd := x.m.Field(m[2])
					if fd == nil {
						continue
					}
					if noticePersonal[m[2]] || fd.Type == "phone" || fd.Type == "npwp" {
						seen[m[1]+"."+m[2]] = true
						v.rep.extra("warn", p, w+"/"+k, "notice_personal_field", fmt.Sprintf(`%s.%s is contact data of %s: keep it behind the link, not in the notice (contract 0066 §6)`, m[1], m[2], mn))
					}
				}
			}
		}
	}
}
