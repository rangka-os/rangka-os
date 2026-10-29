package rk

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Dump writes data as strict YAML that Parse reads back to the same data. Key order follows hint (the
// parsed original, when there is one) with new keys appended in alphabetical order; small mappings and
// lists of scalars use flow style like rkfile.dumps. schemaURL, when set, becomes the first-line comment.
func Dump(data any, hint *Node, schemaURL string) string {
	var sb strings.Builder
	if schemaURL != "" {
		sb.WriteString("# yaml-language-server: $schema=" + schemaURL + "\n")
	}
	switch v := data.(type) {
	case map[string]any:
		if len(v) == 0 {
			sb.WriteString("{}\n")
			break
		}
		dumpMap(&sb, v, hint, 0)
	case []any:
		if len(v) == 0 {
			sb.WriteString("[]\n")
			break
		}
		dumpSeq(&sb, v, hint, 0)
	default:
		sb.WriteString(scalar(v, false) + "\n")
	}
	return sb.String()
}

func orderedKeys(m map[string]any, hint *Node) []string {
	var out []string
	seen := map[string]bool{}
	if hint != nil && hint.Kind == MapNode {
		for _, k := range hint.Keys {
			if _, ok := m[k.Value]; ok && !seen[k.Value] {
				out = append(out, k.Value)
				seen[k.Value] = true
			}
		}
	}
	var rest []string
	for k := range m {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func isScalar(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return false
	}
	return true
}

// flowOK mirrors rkfile._flow_ok: short maps of scalars and short lists of scalars go on one line.
func flowOK(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 || len(x) > 4 {
			return false
		}
		for _, e := range x {
			if !isScalar(e) {
				return false
			}
		}
		b, _ := json.Marshal(x)
		return len([]rune(string(b))) <= 72
	case []any:
		if len(x) == 0 || len(x) > 8 {
			return false
		}
		for _, e := range x {
			switch e.(type) {
			case string, int64, float64, int:
			default:
				return false
			}
		}
		b, _ := json.Marshal(x)
		return len([]rune(string(b))) <= 72
	}
	return false
}

func flow(v any, hint *Node) string {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			return "{}"
		}
		var parts []string
		for _, k := range orderedKeys(x, hint) {
			parts = append(parts, key(k)+": "+scalar(x[k], true))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []any:
		if len(x) == 0 {
			return "[]"
		}
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = scalar(e, true)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return scalar(v, true)
}

func childHint(hint *Node, k any) *Node {
	if hint == nil {
		return nil
	}
	return hint.Get(k)
}

func dumpMap(sb *strings.Builder, m map[string]any, hint *Node, indent int) {
	pad := strings.Repeat(" ", indent)
	for _, k := range orderedKeys(m, hint) {
		v := m[k]
		h := childHint(hint, k)
		sb.WriteString(pad + key(k) + ":")
		writeValue(sb, v, h, indent)
	}
}

func writeValue(sb *strings.Builder, v any, h *Node, indent int) {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 || flowOK(x) {
			sb.WriteString(" " + flow(x, h) + "\n")
			return
		}
		sb.WriteString("\n")
		dumpMap(sb, x, h, indent+2)
	case []any:
		if len(x) == 0 || flowOK(x) {
			sb.WriteString(" " + flow(x, h) + "\n")
			return
		}
		sb.WriteString("\n")
		dumpSeq(sb, x, h, indent+2)
	case string:
		if strings.Contains(x, "\n") {
			sb.WriteString(" " + literal(x, indent+2))
			return
		}
		sb.WriteString(" " + scalar(x, false) + "\n")
	default:
		sb.WriteString(" " + scalar(v, false) + "\n")
	}
}

func dumpSeq(sb *strings.Builder, l []any, hint *Node, indent int) {
	pad := strings.Repeat(" ", indent)
	for i, v := range l {
		h := childHint(hint, i)
		switch x := v.(type) {
		case map[string]any:
			if len(x) == 0 || flowOK(x) {
				sb.WriteString(pad + "- " + flow(x, h) + "\n")
				continue
			}
			var inner strings.Builder
			dumpMap(&inner, x, h, indent+2)
			s := inner.String()
			sb.WriteString(pad + "- " + strings.TrimPrefix(s, pad+"  "))
		case []any:
			if len(x) == 0 || flowOK(x) {
				sb.WriteString(pad + "- " + flow(x, h) + "\n")
				continue
			}
			sb.WriteString(pad + "-\n")
			dumpSeq(sb, x, h, indent+2)
		case string:
			if strings.Contains(x, "\n") {
				sb.WriteString(pad + "- " + literal(x, indent+2))
				continue
			}
			sb.WriteString(pad + "- " + scalar(x, false) + "\n")
		default:
			sb.WriteString(pad + "- " + scalar(v, false) + "\n")
		}
	}
}

func literal(s string, indent int) string {
	pad := strings.Repeat(" ", indent)
	chomp := "-"
	body := s
	if strings.HasSuffix(s, "\n") {
		chomp = ""
		body = strings.TrimSuffix(s, "\n")
		if strings.HasSuffix(body, "\n") {
			chomp = "+"
		}
	}
	var sb strings.Builder
	sb.WriteString("|" + chomp + "\n")
	for _, l := range strings.Split(body, "\n") {
		if l == "" {
			sb.WriteString("\n")
		} else {
			sb.WriteString(pad + l + "\n")
		}
	}
	return sb.String()
}

func key(k string) string {
	if needsQuote(k, false) && !(Ambiguous.MatchString(k) && Resolve(k) == "str") {
		return quote(k)
	}
	return k
}

func needsQuote(s string, inFlow bool) bool {
	if s == "" || Resolve(s) != "str" || Ambiguous.MatchString(s) {
		return true
	}
	if strings.TrimSpace(s) != s || strings.IndexByte("?:,[]{}#&*!|>'\"%@`", s[0]) >= 0 {
		return true
	}
	if s[0] == '-' && (len(s) == 1 || s[1] == ' ') {
		return true
	}
	if strings.Contains(s, ": ") || strings.Contains(s, " #") || strings.HasSuffix(s, ":") || strings.ContainsAny(s, "\t\n") {
		return true
	}
	if inFlow && strings.ContainsAny(s, ",[]{}") {
		return true
	}
	return false
}

func quote(s string) string {
	if strings.ContainsAny(s, "\n\t\\") {
		b, _ := json.Marshal(s)
		return string(b)
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func scalar(v any, inFlow bool) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		switch {
		case math.IsInf(x, 1):
			return ".inf"
		case math.IsInf(x, -1):
			return "-.inf"
		case math.IsNaN(x):
			return ".nan"
		}
		s := strconv.FormatFloat(x, 'f', -1, 64)
		if !strings.ContainsAny(s, ".e") {
			s += ".0"
		}
		return s
	case string:
		if needsQuote(x, inFlow) {
			return quote(x)
		}
		return x
	case map[string]any, []any:
		return flow(x, nil)
	}
	return quote(fmt.Sprint(v))
}
