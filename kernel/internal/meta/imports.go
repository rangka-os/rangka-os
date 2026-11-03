package meta

import (
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// MapValue is one entry of an import's map: a column header or a CEL expression (without the leading "="),
// and for link fields the target field to match on.
type MapValue struct{ Column, CEL, Match string }

// ImportLines maps the rows of a group onto the lines of a table field.
type ImportLines struct {
	Field    string
	Map      map[string]MapValue
	MapOrder []string
}

// Import is the kind `import` (contract 0012): how the rows of an uploaded file become records of Model.
type Import struct {
	Name, App, Model, Source, Sheet, Per string // Source default "csv", Per default "org"; App is "" for site imports
	Label                                Label
	HeaderRow                            int // 1-based, default 1
	Key, Group                           []string
	Map                                  map[string]MapValue
	MapOrder                             []string // YAML key order of Map
	Lines                                *ImportLines
	Raw                                  map[string]any
	Doc                                  *rk.Doc
}

func parseImport(d *rk.Doc, app string) *Import {
	raw := asMap(d.Data)
	im := &Import{Name: str(raw, "name"), App: app, Model: str(raw, "model"), Source: str(raw, "source"), Sheet: str(raw, "sheet"),
		Per: str(raw, "per"), Label: label(raw["label"]), HeaderRow: integer(raw, "header_row"),
		Key: strList(raw["key"]), Group: strList(raw["group"]), Raw: raw, Doc: d}
	if im.Source == "" {
		im.Source = "csv"
	}
	if im.Per == "" {
		im.Per = "org"
	}
	if im.HeaderRow == 0 {
		im.HeaderRow = 1
	}
	var root *rk.Node
	if d.Root != nil {
		root = d.Root
	}
	im.Map, im.MapOrder = parseImportMap(asMap(raw["map"]), childNode(root, "map"))
	if lm := asMap(raw["lines"]); lm != nil {
		l := &ImportLines{Field: str(lm, "field")}
		l.Map, l.MapOrder = parseImportMap(asMap(lm["map"]), childNode(childNode(root, "lines"), "map"))
		im.Lines = l
	}
	return im
}

func childNode(n *rk.Node, key string) *rk.Node {
	if n == nil || n.Kind != rk.MapNode {
		return nil
	}
	return n.Get(key)
}

// parseImportMap reads a map: block. The order comes from the YAML node (Go maps lose it); without a node
// (a legacy .json file) it falls back to sorted keys.
func parseImportMap(m map[string]any, n *rk.Node) (map[string]MapValue, []string) {
	out := map[string]MapValue{}
	if m == nil {
		return out, nil
	}
	var order []string
	if n != nil && n.Kind == rk.MapNode {
		for _, k := range n.Keys {
			if _, ok := m[k.Value]; ok {
				order = append(order, k.Value)
			}
		}
	}
	if len(order) != len(m) {
		order = sortedKeys(m)
	}
	for k, v := range m {
		out[k] = importValue(v)
	}
	return out, order
}

func importValue(v any) MapValue {
	if m := asMap(v); m != nil {
		mv := importValue(m["value"])
		mv.Match = str(m, "match")
		return mv
	}
	s, _ := v.(string)
	if strings.HasPrefix(s, "=") {
		return MapValue{CEL: strings.TrimSpace(s[1:])}
	}
	return MapValue{Column: s}
}
