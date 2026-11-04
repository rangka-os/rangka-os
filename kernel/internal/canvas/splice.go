package canvas

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// Splice replaces the single-line scalar at pointer (a JSON pointer into doc) with value and returns the new
// file text. Every byte outside the scalar's span is kept, comments and formatting included (contract 0014
// ruling 3). The scalar keeps its quoting style when that still reads back as value; otherwise it is written
// with rk.Dump's quoting rules. ok is false when the target is missing, not a scalar, spans several lines, or
// value is not a single-line scalar: the caller then falls back to a JSON Patch.
func Splice(doc *rk.Doc, pointer string, value any) (text string, ok bool) {
	if doc == nil || doc.Root == nil || strings.ContainsAny(doc.Text, "\r") || strings.HasPrefix(doc.Text, "\ufeff") {
		return "", false
	}
	node, inFlow, err := resolve(doc.Root, pointer)
	if err != nil || node.Kind != rk.ScalarNode || (node.Tag == "null" && node.Value == "") {
		return "", false
	}
	start, end, ok := span(doc.Text, node, inFlow)
	if !ok {
		return "", false
	}
	value = scalarValue(value, node.Tag)
	var candidates []string
	switch v := value.(type) {
	case map[string]any, []any:
		return "", false
	case string:
		if strings.ContainsAny(v, "\n\r") {
			return "", false
		}
		switch node.Style {
		case 0:
			candidates = append(candidates, v)
		case '\'':
			candidates = append(candidates, "'"+strings.ReplaceAll(v, "'", "''")+"'")
		case '"':
			candidates = append(candidates, doubleQuoted(v))
		}
	}
	candidates = append(candidates, rk.Scalar(value, inFlow))
	want := meta.DeepCopy(doc.Data)
	if setPointer(&want, pointer, value) != nil {
		return "", false
	}
	for _, c := range candidates {
		out := doc.Text[:start] + c + doc.Text[end:]
		root, perr := rk.Parse(out, doc.Path)
		if perr != nil || root == nil {
			continue
		}
		if reflect.DeepEqual(root.Data(), want) {
			return out, true
		}
	}
	return "", false
}

// scalarValue turns a JSON number into the YAML type of the scalar it replaces (an integral float64 onto an
// int stays an int) and an int into int64, the type rk reads back.
func scalarValue(v any, tag string) any {
	switch x := v.(type) {
	case int:
		return int64(x)
	case float64:
		if tag == "int" && x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			return int64(x)
		}
	}
	return v
}

func doubleQuoted(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			sb.WriteByte('\\')
			sb.WriteRune(r)
		case r == '\t':
			sb.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&sb, `\x%02x`, r)
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// pointerParts splits a JSON pointer ("/a/0/b", "/" or "" for the root) into unescaped segments.
func pointerParts(pointer string) ([]string, error) {
	if pointer == "" || pointer == "/" {
		return nil, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("pointer %q does not start with /", pointer)
	}
	parts := strings.Split(pointer[1:], "/")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
	}
	return parts, nil
}

// joinPointer appends a dot-separated key path (an editable key) to a node's JSON pointer.
func joinPointer(base, key string) string {
	base = strings.TrimSuffix(base, "/")
	for _, k := range strings.Split(key, ".") {
		base += "/" + strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
	}
	return base
}

// resolve finds the node at pointer and reports whether it sits inside a flow collection.
func resolve(root *rk.Node, pointer string) (*rk.Node, bool, error) {
	parts, err := pointerParts(pointer)
	if err != nil {
		return nil, false, err
	}
	cur, inFlow := root, false
	for _, p := range parts {
		if cur == nil {
			break
		}
		if cur.Style == '{' || cur.Style == '[' {
			inFlow = true
		}
		switch cur.Kind {
		case rk.MapNode:
			cur = cur.Get(p)
		case rk.SeqNode:
			i, err := strconv.Atoi(p)
			if err != nil {
				return nil, false, fmt.Errorf("%s: %q is not an index", pointer, p)
			}
			cur = cur.Get(i)
		default:
			cur = nil
		}
	}
	if cur == nil {
		return nil, false, fmt.Errorf("%s: not found", pointer)
	}
	return cur, inFlow, nil
}

// span returns the byte range [start, end) of a single-line scalar in text: a quoted scalar ends at its closing
// quote, a plain one before " #", the end of the line or (in a flow collection) , ] }.
func span(text string, n *rk.Node, inFlow bool) (int, int, bool) {
	off, line := 0, 1
	for line < n.Line {
		i := strings.IndexByte(text[off:], '\n')
		if i < 0 {
			return 0, 0, false
		}
		off += i + 1
		line++
	}
	start := off + n.Col - 1
	if start > len(text) {
		return 0, 0, false
	}
	eol := len(text)
	if i := strings.IndexByte(text[start:], '\n'); i >= 0 {
		eol = start + i
	}
	switch n.Style {
	case '\'':
		for i := start + 1; i < eol; i++ {
			if text[i] == '\'' {
				if i+1 < eol && text[i+1] == '\'' {
					i++
					continue
				}
				return start, i + 1, strings.ReplaceAll(text[start+1:i], "''", "'") == n.Value
			}
		}
	case '"':
		for i := start + 1; i < eol; i++ {
			switch text[i] {
			case '\\':
				i++
			case '"':
				return start, i + 1, true
			}
		}
	case 0:
		end := start
		for end < eol {
			c := text[end]
			if c == '#' && end > start && (text[end-1] == ' ' || text[end-1] == '\t') {
				break
			}
			if inFlow && (c == ',' || c == ']' || c == '}') {
				break
			}
			end++
		}
		for end > start && (text[end-1] == ' ' || text[end-1] == '\t') {
			end--
		}
		return start, end, text[start:end] == n.Value
	}
	return 0, 0, false
}

// setPointer sets the value at pointer inside plain data (maps and lists), in place.
func setPointer(data *any, pointer string, v any) error {
	parts, err := pointerParts(pointer)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		*data = v
		return nil
	}
	cur := *data
	for i, p := range parts {
		last := i == len(parts)-1
		switch c := cur.(type) {
		case map[string]any:
			if _, ok := c[p]; !ok {
				return fmt.Errorf("%s: no %q", pointer, p)
			}
			if last {
				c[p] = v
				return nil
			}
			cur = c[p]
		case []any:
			k, err := strconv.Atoi(p)
			if err != nil || k < 0 || k >= len(c) {
				return fmt.Errorf("%s: bad index %q", pointer, p)
			}
			if last {
				c[k] = v
				return nil
			}
			cur = c[k]
		default:
			return fmt.Errorf("%s: %q is not inside a map or list", pointer, p)
		}
	}
	return nil
}
