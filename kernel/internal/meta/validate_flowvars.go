package meta

import (
	"fmt"
	"regexp"
	"strings"
)

var spaceRe = regexp.MustCompile(`\s+`)

// topConjuncts are the top-level && parts of a CEL expression, whitespace removed (quotes and brackets kept whole);
// rangka_validate.py top_conjuncts.
func topConjuncts(expr string) []string {
	var out []string
	var cur strings.Builder
	depth := 0
	var q byte
	for i := 0; i < len(expr); i++ {
		ch := expr[i]
		switch {
		case q != 0:
			cur.WriteByte(ch)
			if ch == '\\' && i+1 < len(expr) {
				cur.WriteByte(expr[i+1])
				i++
				continue
			}
			if ch == q {
				q = 0
			}
		case ch == '\'' || ch == '"':
			q = ch
			cur.WriteByte(ch)
		case ch == '(' || ch == '[' || ch == '{':
			depth++
			cur.WriteByte(ch)
		case ch == ')' || ch == ']' || ch == '}':
			depth--
			cur.WriteByte(ch)
		case depth == 0 && strings.HasPrefix(expr[i:], "&&"):
			out = append(out, cur.String())
			cur.Reset()
			i++
		default:
			cur.WriteByte(ch)
		}
	}
	out = append(out, cur.String())
	var res []string
	for _, x := range out {
		if strings.TrimSpace(x) != "" {
			res = append(res, spaceRe.ReplaceAllString(x, ""))
		}
	}
	return res
}

// conditionalAs warns when a step's `as:` is declared under `if:` and a later step's if: null-checks that name
// (x != null, x == null, size(x), x.size()) without the declaring condition first: when the declaring step is
// skipped the name is undeclared and the flow fails at run time (rangka-apps pos_sale_on_submit, 13 Nov). Top-level
// steps only; rangka_validate.py conditional_as.
func (v *validator) conditionalAs(p string, steps any) {
	list := asList(steps)
	uncond := map[string]bool{}
	for i, x := range list {
		st, ok := x.(map[string]any)
		if !ok {
			continue
		}
		name, _ := st["as"].(string)
		cond, isStr := st["if"].(string)
		if name != "" && !truthy(st["if"]) {
			uncond[name] = true
		}
		if name == "" || !isStr || strings.TrimSpace(cond) == "" || uncond[name] {
			continue
		}
		cs := topConjuncts(cond)
		xe := regexp.QuoteMeta(name)
		re := regexp.MustCompile(`\b` + xe + `(!=|==)null\b|\bsize\(` + xe + `\)|\b` + xe + `\.size\(\)`)
		for j := i + 1; j < len(list); j++ {
			t, ok := list[j].(map[string]any)
			if !ok {
				continue
			}
			if t["as"] == name && !truthy(t["if"]) {
				break
			}
			ts, _ := t["if"].(string)
			nc := spaceRe.ReplaceAllString(ts, "")
			loc := re.FindStringIndex(nc)
			if loc == nil {
				continue
			}
			guarded := true
			for _, k := range cs {
				if !strings.Contains(nc[:loc[0]], k) {
					guarded = false
				}
			}
			if guarded {
				continue
			}
			c := strings.TrimSpace(cond)
			v.rep.extra("warn", p, fmt.Sprintf("steps/%d/if", j), "conditional_as_unguarded",
				fmt.Sprintf(`step %d reads "%s", which step %d declares only when %s; a null check does not cover a skipped step: start this if: with %s && ...`, j+1, name, i+1, c, c))
		}
	}
}
