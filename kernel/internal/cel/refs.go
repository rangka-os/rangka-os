package cel

import (
	"regexp"
	"strings"
)

// Ref is a reference found in an expression: a function call ("fn") or a variable ("ref") with an optional
// member (customer.payment_days → head customer, member payment_days).
type Ref struct {
	Kind   string // "fn" or "ref"
	Head   string
	Member string
}

var reQuoted = regexp.MustCompile(`'[^']*'`)

// Macros are the comprehension macros the parser expands (parse.go macro()); their first argument is a
// loop variable bound inside the macro body.
const macroAlt = `(?:all|exists_one|exists|map|filter)`

var reLambda = regexp.MustCompile(`\.` + macroAlt + `\(\s*([a-z_]\w*)\s*,`)
var reMacroRange = regexp.MustCompile(`([A-Za-z_]\w*)\s*\.\s*` + macroAlt + `\(\s*([a-z_]\w*)\s*,`)

// LoopRef is a member read on a macro loop variable whose range is a plain identifier:
// lines.all(l, l.qty > 0) → {Var: l, Range: lines, Member: qty}.
type LoopRef struct {
	Var, Range, Member string
}

// Idents ports rangka_validate.py idents(): top-level identifiers and link.member pairs in an expression,
// ignoring single-quoted strings, loop variables of the macros (all, exists, exists_one, map, filter) and
// functions. It is deliberately the same regex-level scan so the validator reports exactly what the Python
// reference does.
func Idents(expr string) []Ref {
	refs, _ := scan(expr)
	return refs
}

// LoopRefs ports rangka_validate.py loop_refs(): member reads on macro loop variables, with the identifier
// the macro ranges over (the first range seen for a variable name wins).
func LoopRefs(expr string) []LoopRef {
	_, loops := scan(expr)
	return loops
}

func scan(expr string) ([]Ref, []LoopRef) {
	s := reQuoted.ReplaceAllString(expr, "''")
	lam := map[string]bool{}
	for _, m := range reLambda.FindAllStringSubmatch(s, -1) {
		lam[m[1]] = true
	}
	rng := map[string]string{}
	for _, m := range reMacroRange.FindAllStringSubmatchIndex(s, -1) {
		if m[0] > 0 && (isWordByte(s[m[0]-1]) || s[m[0]-1] == '.') {
			continue // (?<![\w.]) in the Python reference
		}
		v := s[m[4]:m[5]]
		if _, ok := rng[v]; !ok {
			rng[v] = s[m[2]:m[3]]
		}
	}
	var out []Ref
	var loops []LoopRef
	isWord := isWordByte
	isStart := func(c byte) bool { return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
	skipWS := func(i int) int {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' || s[i] == '\f' || s[i] == '\v') {
			i++
		}
		return i
	}
	i := 0
	for i < len(s) {
		if !isStart(s[i]) || (i > 0 && (isWord(s[i-1]) || s[i-1] == '.')) {
			i++
			continue
		}
		j := i
		for j < len(s) && isWord(s[j]) {
			j++
		}
		head := s[i:j]
		end := j
		member := ""
		// (\s*\.\s*([A-Za-z_]\w*))?
		k := skipWS(j)
		if k < len(s) && s[k] == '.' {
			m := skipWS(k + 1)
			if m < len(s) && isStart(s[m]) {
				n := m
				for n < len(s) && isWord(s[n]) {
					n++
				}
				member = s[m:n]
				end = n
			}
		}
		// (\s*\()?
		call := false
		k = skipWS(end)
		if k < len(s) && s[k] == '(' {
			call = true
			end = k + 1
		}
		switch {
		case head == "in" && member == "" && !call:
			// the CEL membership operator
		case call && member == "":
			out = append(out, Ref{"fn", head, ""})
		case !lam[head]:
			if member != "" && call {
				member = ""
			}
			out = append(out, Ref{"ref", head, member})
		case member != "" && !call && rng[head] != "":
			loops = append(loops, LoopRef{head, rng[head], member})
		}
		i = end
	}
	return out, loops
}

var reDoubleQuoted = regexp.MustCompile(`"[^"]*"`)
var reMethodCall = regexp.MustCompile(`\.\s*([A-Za-z_]\w*)\s*\(`)

// MethodNames ports rangka_validate.py method_names(): the names called as methods (x.name(…)) in an expression,
// each once in order of first use, ignoring quoted strings.
func MethodNames(expr string) []string {
	s := reDoubleQuoted.ReplaceAllString(reQuoted.ReplaceAllString(expr, "''"), `""`)
	var out []string
	seen := map[string]bool{}
	for _, m := range reMethodCall.FindAllStringSubmatch(s, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

func isWordByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// FreeVars returns the variables an expression reads (excluding macro variables), with the member read
// on each when it is a simple selection (x.f). Used for compute ordering and dependency analysis.
func FreeVars(src string) ([]Ref, error) {
	p, err := Compile(src)
	if err != nil {
		return nil, err
	}
	var out []Ref
	var walk func(e Expr, bound map[string]bool)
	walk = func(e Expr, bound map[string]bool) {
		switch n := e.(type) {
		case *Ident:
			if !bound[n.Name] {
				out = append(out, Ref{"ref", n.Name, ""})
			}
		case *Select:
			if id, ok := n.X.(*Ident); ok && !bound[id.Name] {
				out = append(out, Ref{"ref", id.Name, n.Field})
				return
			}
			walk(n.X, bound)
		case *Has:
			walk(n.X, bound)
		case *Index:
			walk(n.X, bound)
			walk(n.I, bound)
		case *List:
			for _, x := range n.Elems {
				walk(x, bound)
			}
		case *MapLit:
			for i := range n.Keys {
				walk(n.Keys[i], bound)
				walk(n.Vals[i], bound)
			}
		case *Unary:
			walk(n.X, bound)
		case *Binary:
			walk(n.L, bound)
			walk(n.R, bound)
		case *Cond:
			walk(n.C, bound)
			walk(n.T, bound)
			walk(n.F, bound)
		case *Call:
			out = append(out, Ref{"fn", n.Fn, ""})
			if n.Target != nil {
				walk(n.Target, bound)
			}
			for _, a := range n.Args {
				walk(a, bound)
			}
		case *Comp:
			walk(n.Range, bound)
			inner := map[string]bool{n.Var: true}
			for k := range bound {
				inner[k] = true
			}
			if n.Filter != nil {
				walk(n.Filter, inner)
			}
			if n.Body != nil {
				walk(n.Body, inner)
			}
		}
	}
	walk(p.Root, map[string]bool{})
	return out, nil
}

// Interpolate replaces {expr} parts of a template with their values ("Ada {size(due)} aktivitas").
func Interpolate(tmpl string, env *Env) string {
	var sb strings.Builder
	for {
		i := strings.IndexByte(tmpl, '{')
		if i < 0 {
			sb.WriteString(tmpl)
			return sb.String()
		}
		j := strings.IndexByte(tmpl[i:], '}')
		if j < 0 {
			sb.WriteString(tmpl)
			return sb.String()
		}
		sb.WriteString(tmpl[:i])
		v, err := Eval(tmpl[i+1:i+j], env)
		if err != nil {
			sb.WriteString(tmpl[i : i+j+1])
		} else {
			sb.WriteString(Format(v))
		}
		tmpl = tmpl[i+j+1:]
	}
}

// Format renders a value as text.
func Format(v any) string {
	switch x := Unwrap(v).(type) {
	case nil:
		return ""
	case float64:
		if x == float64(int64(x)) {
			return strings.TrimSuffix(strings.TrimSuffix(trimFloat(x), ".0"), ".")
		}
		return trimFloat(x)
	case string:
		return x
	default:
		return strings.TrimSpace(strings.Trim(strings.ReplaceAll(fmtAny(x), "\n", " "), "[]"))
	}
}
