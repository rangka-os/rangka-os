package meta

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// Item is one problem: file:line:col level code path message.
type Item struct {
	Level   string `json:"level"` // error | warn
	File    string `json:"file"`  // relative to the report root
	Pos     string `json:"pos"`   // line:col
	Where   string `json:"path"`  // path inside the document, or (syntax)/(root)
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Report collects problems. Items reproduce rangka_validate.py; Extra holds the kernel's additional checks
// (flows, site files), printed after them.
type Report struct {
	Root  string
	Items []Item
	Extra []Item
	docs  map[string]*rk.Doc
}

// NewReport makes a report whose file names are relative to root (the apps dir).
func NewReport(root string) *Report { return &Report{Root: root, docs: map[string]*rk.Doc{}} }

func (r *Report) rel(path string) string {
	root, _ := filepath.Abs(r.Root)
	abs, _ := filepath.Abs(path)
	if rel, err := filepath.Rel(root, abs); err == nil {
		return rel
	}
	return path
}

func (r *Report) addAt(level, path string, line, col int, where, code, msg string, extra bool) {
	it := Item{level, r.rel(path), fmt.Sprintf("%d:%d", line, col), where, code, msg}
	if extra {
		r.Extra = append(r.Extra, it)
	} else {
		r.Items = append(r.Items, it)
	}
}

// add records a problem at a document path like "fields/3/compute" (rangka_validate.py Report.add).
func (r *Report) add(level, path, where, code, msg string) {
	r.addTo(level, path, where, code, msg, false)
}

func (r *Report) extra(level, path, where, code, msg string) {
	r.addTo(level, path, where, code, msg, true)
}

func (r *Report) addTo(level, path, where, code, msg string, extra bool) {
	pos := "1:1"
	if d := r.docs[path]; d != nil && where != "(root)" {
		var parts []any
		for _, x := range strings.Split(where, "/") {
			if n, err := strconv.Atoi(x); err == nil && isDigits(x) {
				parts = append(parts, n)
			} else {
				parts = append(parts, x)
			}
		}
		pos = d.Where(parts...)
	}
	it := Item{level, r.rel(path), pos, where, code, msg}
	if extra {
		r.Extra = append(r.Extra, it)
	} else {
		r.Items = append(r.Items, it)
	}
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// All returns every item (reference checks first, then kernel extensions).
func (r *Report) All() []Item { return append(append([]Item{}, r.Items...), r.Extra...) }

// Errors counts errors.
func (r *Report) Errors() int {
	n := 0
	for _, it := range r.All() {
		if it.Level == "error" {
			n++
		}
	}
	return n
}

// Print writes the report in rangka_validate.py format and returns the error count.
func (r *Report) Print(w io.Writer) int {
	all := r.All()
	for _, it := range all {
		lvl := "error"
		if it.Level != "error" {
			lvl = "warn "
		}
		fmt.Fprintf(w, "%s:%s  %s  %s  %s  %s\n", it.File, it.Pos, lvl, it.Code, it.Where, it.Message)
	}
	e := r.Errors()
	fmt.Fprintf(w, "\n%d error(s), %d warning(s)\n", e, len(all)-e)
	return e
}

// JSON writes the report as JSON.
func (r *Report) JSON(w io.Writer) int {
	all := r.All()
	if all == nil {
		all = []Item{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(map[string]any{"items": all, "errors": r.Errors(), "warnings": len(all) - r.Errors()})
	return r.Errors()
}
