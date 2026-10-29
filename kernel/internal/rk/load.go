package rk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Exts are the metadata file extensions, in preference order (.rk first, legacy .json last).
var Exts = []string{".rk", ".rk.yaml", ".rk.yml", ".json"}

// Doc is a loaded metadata file.
type Doc struct {
	Path string
	Text string
	Root *Node // nil for legacy .json files
	Data any
}

// Where returns "line:col" of the deepest node along path (string keys, int indexes), like rkfile's at().
func (d *Doc) Where(path ...any) string {
	if d == nil || d.Root == nil {
		if d != nil && strings.HasSuffix(d.Path, ".json") {
			return "?"
		}
		return "1:1"
	}
	cur := d.Root
	for _, p := range path {
		next := cur.Get(p)
		if next == nil {
			break
		}
		cur = next
	}
	return fmt.Sprintf("%d:%d", cur.Line, cur.Col)
}

// Kind returns the schema kind named on the first line (# yaml-language-server: $schema=.../<kind>.json).
func (d *Doc) Kind() string { return KindOf(d.Text) }

// KindOf extracts the kind from the schema comment on the first line.
func KindOf(text string) string {
	first, _, _ := strings.Cut(text, "\n")
	i := strings.Index(first, "$schema=")
	if i < 0 {
		return ""
	}
	u := strings.TrimSpace(first[i+len("$schema="):])
	u = u[strings.LastIndex(u, "/")+1:]
	return strings.TrimSuffix(u, ".json")
}

// Load reads and parses a metadata file.
func Load(path string) (*Doc, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadBytes(path, b)
}

// LoadBytes parses metadata text that belongs to path.
func LoadBytes(path string, b []byte) (*Doc, error) {
	text := string(b)
	if strings.HasSuffix(path, ".json") {
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			line, col := 1, 1
			if se, ok := err.(*json.SyntaxError); ok {
				line, col = lineCol(text, int(se.Offset))
			}
			return nil, newErr(path, line, col, "json_syntax", err.Error())
		}
		return &Doc{Path: path, Text: text, Data: normalizeJSON(v)}, nil
	}
	root, perr := Parse(text, path)
	if perr != nil {
		return nil, perr
	}
	d := &Doc{Path: path, Text: text, Root: root}
	if root == nil {
		d.Data = map[string]any{}
	} else {
		d.Data = root.Data()
	}
	return d, nil
}

func lineCol(text string, off int) (int, int) {
	if off > len(text) {
		off = len(text)
	}
	line := strings.Count(text[:off], "\n") + 1
	col := off - strings.LastIndex(text[:off], "\n")
	return line, col
}

// normalizeJSON turns float64 integers into int64 so JSON and YAML data look the same.
func normalizeJSON(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, vv := range x {
			x[k] = normalizeJSON(vv)
		}
	case []any:
		for i, vv := range x {
			x[i] = normalizeJSON(vv)
		}
	case float64:
		if x == float64(int64(x)) && x < 1e15 && x > -1e15 {
			return int64(x)
		}
	}
	return v
}

// Stem returns the file name without a metadata extension, or "" when fn is not a metadata file.
func Stem(fn string) string {
	for _, e := range Exts {
		if strings.HasSuffix(fn, e) {
			return strings.TrimSuffix(fn, e)
		}
	}
	return ""
}

// Find returns the metadata file for stem in dir, or "".
func Find(dir, stem string) string {
	for _, e := range Exts {
		p := filepath.Join(dir, stem+e)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// Entry is one metadata file in a directory.
type Entry struct{ Stem, Path string }

// ListDir lists metadata files in dir sorted by stem, preferring .rk over legacy .json.
func ListDir(dir string) []Entry {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	out := map[string]string{}
	for _, fn := range names {
		s := Stem(fn)
		if s == "" {
			continue
		}
		if prev, ok := out[s]; !ok || strings.HasSuffix(prev, ".json") {
			out[s] = filepath.Join(dir, fn)
		}
	}
	res := make([]Entry, 0, len(out))
	for s, p := range out {
		res = append(res, Entry{s, p})
	}
	sort.Slice(res, func(i, j int) bool { return res[i].Stem < res[j].Stem })
	return res
}
