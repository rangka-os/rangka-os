package imports

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// RowError is a problem in one source row (Row is the 1-based sheet row; the header row for file-level problems).
type RowError struct {
	Row     int    `json:"row"`
	Column  string `json:"column"`
	Message string `json:"message"`
}

// Lookup resolves link values inside the caller's org.
type Lookup interface {
	// Find returns the names of the records of model whose field equals value; field "name" matches by name.
	Find(model, field, value string) ([]string, error)
}

// Planned is one record to create. Rows are the source rows it came from; Keys holds one hash per source
// row (see keyer), empty when the import has no key.
type Planned struct {
	Input map[string]any
	Rows  []int
	Keys  []string
}

// Mapping is the result of Map. A record with row errors is still returned so the preview can show it.
type Mapping struct {
	Records []Planned
	Errors  []RowError
}

type mapper struct {
	reg   *meta.Registry
	lk    Lookup
	now   time.Time
	errs  []RowError
	found map[lookupKey]lookupResult // Lookup results of this Map call
}

type lookupKey struct{ model, field, value string }

type lookupResult struct {
	names []string
	err   error
}

// Map turns the rows of t into record inputs for imp.Model. scope holds fixed parent values (per: outlet).
func Map(reg *meta.Registry, imp *meta.Import, t *Table, scope map[string]any, lk Lookup, now time.Time) *Mapping {
	out := &Mapping{}
	if errs := checkColumns(imp, t); len(errs) > 0 {
		out.Errors = errs
		return out
	}
	mp := &mapper{reg: reg, lk: lk, now: now, found: map[lookupKey]lookupResult{}}
	model, lineModel := models(reg, imp)
	var linesField string
	if imp.Lines != nil {
		linesField = imp.Lines.Field
	}
	kr := newKeyer(reg, imp)

	var groups [][]Row
	idx := map[string]int{}
	for _, r := range t.Rows {
		if len(imp.Group) == 0 {
			groups = append(groups, []Row{r})
			continue
		}
		k := joinCells(r, imp.Group)
		if i, ok := idx[k]; ok {
			groups[i] = append(groups[i], r)
			continue
		}
		idx[k] = len(groups)
		groups = append(groups, []Row{r})
	}

	firstSeen := map[string]int{} // key hash -> first row
	for _, g := range groups {
		p := Planned{Input: map[string]any{}}
		for k, v := range scope {
			p.Input[k] = v
		}
		first := g[0]
		for _, name := range imp.MapOrder {
			mp.setValue(p.Input, model, name, imp.Map[name], first)
		}
		var lines []map[string]any
		for _, r := range g {
			p.Rows = append(p.Rows, r.Num)
			if len(imp.Key) > 0 {
				kk := kr.hash(r)
				p.Keys = append(p.Keys, kk)
				if prev, ok := firstSeen[kk]; ok {
					mp.errs = append(mp.errs, RowError{Row: r.Num, Column: imp.Key[0], Message: fmt.Sprintf("Baris ganda: sama dengan baris %d", prev)})
				} else {
					firstSeen[kk] = r.Num
				}
			}
			if imp.Lines != nil {
				line := map[string]any{}
				for _, name := range imp.Lines.MapOrder {
					mp.setValue(line, lineModel, name, imp.Lines.Map[name], r)
				}
				lines = append(lines, line)
			}
		}
		if imp.Lines != nil {
			p.Input[linesField] = lines
		}
		out.Records = append(out.Records, p)
	}
	out.Errors = mp.errs
	return out
}

func checkColumns(imp *meta.Import, t *Table) []RowError {
	count := map[string]int{}
	for _, h := range t.Headers {
		count[h]++
	}
	var cols []string
	add := func(c string) {
		if c == "" {
			return
		}
		for _, x := range cols {
			if x == c {
				return
			}
		}
		cols = append(cols, c)
	}
	for _, n := range imp.MapOrder {
		add(imp.Map[n].Column)
	}
	if imp.Lines != nil {
		for _, n := range imp.Lines.MapOrder {
			add(imp.Lines.Map[n].Column)
		}
	}
	for _, c := range imp.Key {
		add(c)
	}
	for _, c := range imp.Group {
		add(c)
	}
	var errs []RowError
	for _, c := range cols {
		switch {
		case count[c] == 0:
			errs = append(errs, RowError{Row: t.HeaderRow, Column: c, Message: fmt.Sprintf(`Kolom "%s" tidak ada di file`, c)})
		case count[c] > 1:
			errs = append(errs, RowError{Row: t.HeaderRow, Column: c, Message: fmt.Sprintf(`Kolom "%s" ada lebih dari satu di file`, c)})
		}
	}
	return errs
}

func joinCells(r Row, cols []string) string {
	vals := make([]string, len(cols))
	for i, c := range cols {
		vals[i] = r.Cells[c]
	}
	return strings.Join(vals, "\x1f")
}

// models returns the import's model and the model of its lines (nil when unknown or without lines).
func models(reg *meta.Registry, imp *meta.Import) (model, lineModel *meta.Model) {
	model = reg.Models[imp.Model]
	if imp.Lines != nil && model != nil {
		if f := model.Field(imp.Lines.Field); f != nil {
			lineModel = reg.Models[f.Of]
		}
	}
	return model, lineModel
}

// typedKey lists the field types whose cells are normalised before hashing, so a key does not depend on how
// a file writes the value (02/11/2026 in CSV, Excel serial 46328 in XLSX; "1.000" or 1000).
var typedKey = map[string]bool{"int": true, "currency": true, "rating": true, "float": true, "percent": true,
	"date": true, "datetime": true, "check": true}

// keyer hashes the key columns of a row: hex SHA-256 of the values joined by "\x1f". A key column that is
// also mapped as a plain column (map or lines.map, no formula) to a typed field contributes the value Convert
// gives (yyyy-mm-dd, canonical integer or decimal); any other column, or a cell Convert rejects, its trimmed
// text.
type keyer struct {
	cols   []string
	fields []*meta.Field // per column; nil = text
}

func newKeyer(reg *meta.Registry, imp *meta.Import) *keyer {
	model, lineModel := models(reg, imp)
	typed := func(m *meta.Model, mv meta.MapValue, name, col string) *meta.Field {
		if m == nil || mv.CEL != "" || mv.Column != col {
			return nil
		}
		if f := m.Field(name); f != nil && typedKey[f.Type] {
			return f
		}
		return nil
	}
	k := &keyer{cols: imp.Key, fields: make([]*meta.Field, len(imp.Key))}
	for i, c := range imp.Key {
		for _, name := range imp.MapOrder {
			if f := typed(model, imp.Map[name], name, c); f != nil {
				k.fields[i] = f
				break
			}
		}
		if k.fields[i] != nil || imp.Lines == nil {
			continue
		}
		for _, name := range imp.Lines.MapOrder {
			if f := typed(lineModel, imp.Lines.Map[name], name, c); f != nil {
				k.fields[i] = f
				break
			}
		}
	}
	return k
}

func (k *keyer) hash(r Row) string {
	vals := make([]string, len(k.cols))
	for i, c := range k.cols {
		cell := strings.TrimSpace(r.Cells[c])
		vals[i] = cell
		if f := k.fields[i]; f != nil {
			if v, msg := Convert(f, c, cell); msg == "" && v != nil {
				vals[i] = fmt.Sprint(v)
			}
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(vals, "\x1f")))
	return hex.EncodeToString(sum[:])
}

// rowKeys returns the key hash of every row of t by row number; nil when the import has no key or the file
// lacks a column Map needs (Map reports that).
func rowKeys(reg *meta.Registry, imp *meta.Import, t *Table) map[int]string {
	if len(imp.Key) == 0 || len(checkColumns(imp, t)) > 0 {
		return nil
	}
	k := newKeyer(reg, imp)
	out := make(map[int]string, len(t.Rows))
	for _, r := range t.Rows {
		out[r.Num] = k.hash(r)
	}
	return out
}

// setValue resolves one map entry for row r and stores it under name in dst; problems go to mp.errs.
func (mp *mapper) setValue(dst map[string]any, model *meta.Model, name string, mv meta.MapValue, r Row) {
	var f *meta.Field
	if model != nil {
		f = model.Field(name)
	}
	if f == nil {
		f = &meta.Field{Name: name, Type: "data"}
	}
	column := mv.Column
	cell := r.Cells[mv.Column]
	if mv.CEL != "" {
		column = name
		env := &cel.Env{
			Vars: func(n string) (any, bool) {
				if n != "row" {
					return nil, false
				}
				m := make(map[string]any, len(r.Cells))
				for k, v := range r.Cells {
					m[k] = v
				}
				return m, true
			},
			Today: func() string { return mp.now.Format("2006-01-02") },
			Now:   func() time.Time { return mp.now },
		}
		v, err := cel.Eval(mv.CEL, env)
		if err != nil {
			mp.fail(r, column, fmt.Sprintf("Rumus untuk %s gagal: %s", name, err))
			return
		}
		if v == nil {
			cell = ""
		} else {
			cell = fmt.Sprint(v)
		}
	}
	if f.Type == "link" {
		cell = strings.TrimSpace(cell)
		if cell == "" {
			return
		}
		v, msg := mp.link(f, mv, cell)
		if msg != "" {
			mp.fail(r, column, msg)
			return
		}
		dst[name] = v
		return
	}
	v, msg := Convert(f, column, cell)
	if msg != "" {
		mp.fail(r, column, msg)
		return
	}
	if v != nil {
		dst[name] = v
	}
}

func (mp *mapper) fail(r Row, column, msg string) {
	mp.errs = append(mp.errs, RowError{Row: r.Num, Column: column, Message: msg})
}

func (mp *mapper) link(f *meta.Field, mv meta.MapValue, cell string) (any, string) {
	target := mp.reg.Models[f.To]
	tname := f.To
	if target != nil && target.Label["id"] != "" {
		tname = target.Label["id"]
	}
	field := "name"
	if mv.Match != "" {
		field = mv.Match
	}
	lk := lookupKey{f.To, field, cell}
	res, ok := mp.found[lk]
	if !ok {
		res.names, res.err = mp.lk.Find(f.To, field, cell)
		mp.found[lk] = res
	}
	names, err := res.names, res.err
	if err != nil {
		return nil, fmt.Sprintf("Gagal mencari %s: %s", tname, err)
	}
	if mv.Match != "" {
		mf := mv.Match
		if target != nil {
			if tf := target.Field(mv.Match); tf != nil && tf.Label["id"] != "" {
				mf = tf.Label["id"]
			}
		}
		mf = strings.ToLower(mf)
		switch len(names) {
		case 0:
			return nil, fmt.Sprintf(`%s dengan %s "%s" tidak ditemukan`, tname, mf, cell)
		case 1:
			return names[0], ""
		}
		return nil, fmt.Sprintf(`%s dengan %s "%s" lebih dari satu`, tname, mf, cell)
	}
	switch len(names) {
	case 0:
		return nil, fmt.Sprintf(`%s "%s" tidak ditemukan`, tname, cell)
	case 1:
		return names[0], ""
	}
	return nil, fmt.Sprintf(`%s "%s" lebih dari satu`, tname, cell)
}
