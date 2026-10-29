// Package rk reads and writes Rangka metadata files (.rk): strict YAML with positions.
//
// The rules follow rangka-os/tools/rkfile.py: YAML 1.2 core types only; plain values that YAML 1.1
// tools read differently (yes/no/on/off/y/n, leading zeros, 1_000, 08:30) are refused unless quoted;
// duplicate keys, tab indentation, anchors/aliases and merge keys are refused; one document per file.
package rk

import (
	"fmt"
	"strconv"
	"strings"
)

// Kind of a YAML node.
type Kind uint8

const (
	ScalarNode Kind = iota
	MapNode
	SeqNode
	AliasNode
)

// Node is a parsed YAML node with its 1-based position.
type Node struct {
	Kind      Kind
	Line, Col int
	Value     string // scalar text (after unquoting) or alias name
	Style     byte   // 0 plain, '\'' single, '"' double, '|' literal, '>' folded
	Tag       string // resolved scalar tag: str, int, float, bool, null
	Anchor    string
	Keys      []*Node // MapNode keys (scalars)
	Vals      []*Node // MapNode values
	Items     []*Node // SeqNode items
}

// Error is a problem in a .rk file at a position, with a stable code.
type Error struct {
	Path      string
	Line, Col int
	Code      string
	Msg       string
	All       []*Error // every problem found (the first equals the error itself)
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s:%d:%d %s: %s", e.Path, e.Line, e.Col, e.Code, e.Msg)
}

// Data converts a node tree into plain Go values: map[string]any, []any, string, int64, float64, bool, nil.
func (n *Node) Data() any { return toValue(n) }

func toValue(n *Node) any {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case MapNode:
		m := make(map[string]any, len(n.Keys))
		for i, k := range n.Keys {
			m[k.Value] = toValue(n.Vals[i])
		}
		return m
	case SeqNode:
		l := make([]any, len(n.Items))
		for i, it := range n.Items {
			l[i] = toValue(it)
		}
		return l
	case ScalarNode:
		return scalarValue(n)
	}
	return nil
}

func scalarValue(n *Node) any {
	switch n.Tag {
	case "null":
		return nil
	case "bool":
		return strings.ToLower(n.Value) == "true"
	case "int":
		s := n.Value
		if strings.HasPrefix(s, "0x") {
			if v, err := strconv.ParseInt(s[2:], 16, 64); err == nil {
				return v
			}
		}
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
		return s
	case "float":
		s := strings.ToLower(n.Value)
		switch strings.TrimLeft(s, "+-") {
		case ".inf":
			if strings.HasPrefix(s, "-") {
				return negInf()
			}
			return posInf()
		case ".nan":
			return nan()
		}
		if f, err := strconv.ParseFloat(n.Value, 64); err == nil {
			return f
		}
		return n.Value
	}
	return n.Value
}

// Get returns the child node at a path of string keys / int indexes, or nil.
func (n *Node) Get(path ...any) *Node {
	cur := n
	for _, p := range path {
		if cur == nil {
			return nil
		}
		switch k := p.(type) {
		case string:
			if cur.Kind != MapNode {
				return nil
			}
			var next *Node
			for i, kk := range cur.Keys {
				if kk.Value == k {
					next = cur.Vals[i]
				}
			}
			cur = next
		case int:
			if cur.Kind != SeqNode || k < 0 || k >= len(cur.Items) {
				return nil
			}
			cur = cur.Items[k]
		default:
			return nil
		}
	}
	return cur
}
