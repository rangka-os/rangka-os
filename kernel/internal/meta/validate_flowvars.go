package meta

import (
	"fmt"
	"regexp"
	"sort"
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
			res = append(res, strings.ReplaceAll(spaceRe.ReplaceAllString(x, ""), `"`, "'"))
		}
	}
	return res
}

// enclosingGroups are the contents of the parenthesised groups of expr that hold position pos, innermost first, then
// expr itself; rangka_validate.py enclosing_groups.
func enclosingGroups(expr string, pos int) []string {
	type group struct {
		size int
		text string
	}
	var stack []int
	var groups []group
	var q byte
	for i := 0; i < len(expr); i++ {
		ch := expr[i]
		switch {
		case q != 0:
			if ch == '\\' {
				i++
				continue
			}
			if ch == q {
				q = 0
			}
		case ch == '\'' || ch == '"':
			q = ch
		case ch == '(':
			stack = append(stack, i)
		case ch == ')' && len(stack) > 0:
			start := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if start < pos && pos < i {
				groups = append(groups, group{i - start, expr[start+1 : i]})
			}
		}
	}
	sort.SliceStable(groups, func(a, b int) bool { return groups[a].size < groups[b].size })
	out := make([]string, 0, len(groups)+1)
	for _, g := range groups {
		out = append(out, g.text)
	}
	return append(out, expr)
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
			// a null check of the name itself (not event.x or y.x): no letter, digit, _ or . just before it
			found, at := false, 0
			for _, loc := range re.FindAllStringIndex(nc, -1) {
				if strings.HasPrefix(nc[loc[0]:], name) && loc[0] > 0 {
					c := nc[loc[0]-1]
					if c == '_' || c == '.' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
						continue
					}
				}
				found, at = true, loc[0]
				break
			}
			if !found {
				continue
			}
			// guarded when every top-level conjunct of the declaring condition is a top-level conjunct of this if:
			// (CEL && absorbs an error on either side, so the order does not matter; || or a ternary does not guard)
			guarded := false
			for _, g := range enclosingGroups(nc, at) {
				have := map[string]bool{}
				for _, k := range topConjuncts(g) {
					have[k] = true
				}
				all := true
				for _, k := range cs {
					all = all && have[k]
				}
				guarded = guarded || all
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
