package canvas

import (
	"fmt"
	"math"
	"reflect"

	"github.com/rangka-os/rangka-os/kernel/internal/changeset"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// Edit sets one editable key of a node.
type Edit struct {
	Node  string `json:"node"`
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// EditError is an edit that names an unknown node or key (code bad_edit).
type EditError struct {
	Code, Msg string
}

func (e *EditError) Error() string { return e.Code + ": " + e.Msg }

type fileEdit struct {
	pointer string
	value   any
}

// Ops turns edits of graph g into change-set ops (contract 0014 §5): one op per file, in the order the files are
// first edited. Scalar edits are spliced into the file text (op replace, every other byte kept); when any edit of
// a file cannot be spliced (a list, a multi-line value), the file gets one patch op with all its edits. Edits
// whose value equals the current one produce nothing. title names the first changed edit.
func Ops(reg *meta.Registry, g *Graph, edits []Edit) (string, []changeset.Op, error) {
	byID := map[string]*Node{}
	for i := range g.Nodes {
		byID[g.Nodes[i].ID] = &g.Nodes[i]
	}
	var files []string
	perFile := map[string][]fileEdit{}
	var first *Node
	var firstKey string
	changed := 0
	for _, e := range edits {
		n := byID[e.Node]
		if n == nil {
			return "", nil, &EditError{"bad_edit", fmt.Sprintf("node %q is not on the canvas of %s", e.Node, g.Name)}
		}
		cur, ok := n.Editable[e.Key]
		if !ok {
			return "", nil, &EditError{"bad_edit", fmt.Sprintf("node %q has no editable key %q", e.Node, e.Key)}
		}
		if reg.Docs[n.Source.File] == nil {
			return "", nil, &EditError{"bad_edit", fmt.Sprintf("node %q: file %s is not loaded", e.Node, n.Source.File)}
		}
		if same(cur, e.Value) {
			continue
		}
		if first == nil {
			first, firstKey = n, e.Key
		}
		changed++
		if perFile[n.Source.File] == nil {
			files = append(files, n.Source.File)
		}
		perFile[n.Source.File] = append(perFile[n.Source.File], fileEdit{joinPointer(n.Source.Path, e.Key), e.Value})
	}
	if first == nil {
		return title(g, edits, byID), []changeset.Op{}, nil
	}
	ops := []changeset.Op{}
	for _, f := range files {
		ops = append(ops, fileOp(reg.Docs[f], f, perFile[f]))
	}
	t := fmt.Sprintf("Kanvas %s: ubah %s di %s", g.Name, firstKey, nodeName(first))
	if changed > 1 {
		t += fmt.Sprintf(" dan %d lainnya", changed-1)
	}
	return t, ops, nil
}

func fileOp(doc *rk.Doc, file string, edits []fileEdit) changeset.Op {
	cur := doc
	for _, e := range edits {
		text, ok := Splice(cur, e.pointer, e.value)
		if ok {
			cur, _ = rk.LoadBytes(doc.Path, []byte(text))
		}
		if !ok || cur == nil {
			patch := make([]map[string]any, len(edits))
			for i, x := range edits {
				patch[i] = map[string]any{"op": "replace", "path": x.pointer, "value": x.value}
			}
			return changeset.Op{Op: "patch", Path: file, Patch: patch}
		}
	}
	return changeset.Op{Op: "replace", Path: file, Content: cur.Text}
}

func title(g *Graph, edits []Edit, byID map[string]*Node) string {
	if len(edits) == 0 {
		return "Kanvas " + g.Name
	}
	return fmt.Sprintf("Kanvas %s: ubah %s di %s", g.Name, edits[0].Key, nodeName(byID[edits[0].Node]))
}

func nodeName(n *Node) string {
	if t := n.Label.Text(); t != "" {
		return t
	}
	return n.ID
}

// same compares a current value with an edit's JSON value (numbers compared by value).
func same(a, b any) bool { return reflect.DeepEqual(norm(a), norm(b)) }

func norm(v any) any {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case float64:
		if math.IsNaN(x) {
			return "NaN"
		}
		return x
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = norm(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = norm(e)
		}
		return out
	}
	return v
}
