package schema

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Repr formats a value the way Python's repr() does, so messages match rangka_validate.py (jsonschema).
func Repr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return reprFloat(x)
	case string:
		return reprString(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = Repr(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *Inst:
		return x.repr()
	case *OMap:
		parts := make([]string, 0, len(x.Keys))
		for _, k := range x.Keys {
			parts = append(parts, reprString(k)+": "+Repr(x.Vals[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, reprString(k)+": "+Repr(x[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprint(v)
}

func reprFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		return "nan"
	}
	if f == 0 {
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	exp := int(math.Floor(math.Log10(math.Abs(f))))
	if exp >= -4 && exp < 16 {
		s := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	mant, e, _ := strings.Cut(s, "e")
	sign := e[0]
	digits := strings.TrimLeft(e[1:], "0")
	if len(digits) < 2 {
		digits = fmt.Sprintf("%02s", digits)
	}
	return mant + "e" + string(sign) + digits
}

func reprString(s string) string {
	q := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
		q = '"'
	}
	var sb strings.Builder
	sb.WriteByte(q)
	for _, r := range s {
		switch {
		case r == '\\':
			sb.WriteString(`\\`)
		case r == rune(q):
			sb.WriteByte('\\')
			sb.WriteRune(r)
		case r == '\n':
			sb.WriteString(`\n`)
		case r == '\r':
			sb.WriteString(`\r`)
		case r == '\t':
			sb.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&sb, `\x%02x`, r)
		case r > 0x7f && !unicode.IsPrint(r):
			if r <= 0xff {
				fmt.Fprintf(&sb, `\x%02x`, r)
			} else if r <= 0xffff {
				fmt.Fprintf(&sb, `\u%04x`, r)
			} else {
				fmt.Fprintf(&sb, `\U%08x`, r)
			}
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte(q)
	return sb.String()
}
