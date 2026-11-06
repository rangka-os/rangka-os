package api

import (
	"encoding/csv"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Export (contract 0024): what an owner downloads to open in Excel or Google Sheets.
//
//	GET /api/v1/export/r/{model}?filter=&q=&sort=   the rows of the list, same filter as GET /r/{model}
//	GET /api/v1/export/dashboards/{name}?period=     every widget the actor may export, one block per widget
//
// The file is CSV, UTF-8 with a byte-order mark so Excel reads it as UTF-8; ?sep=semicolon writes ';' for an Excel
// whose list separator is ';' (Windows with Indonesian regional settings). Numbers are written plain (12500000,
// not Rp 12.500.000) so a spreadsheet can sum them; selects are written as their label, yes/no as Ya/Tidak.
// Every export needs the model's export grant (permissions: export), on top of read.

// pickColumns: the columns the list shows (?columns=a,b,c, what the owner chose under Kolom); unknown names and
// table fields are skipped. Nil when none is left, so the default columns apply.
func pickColumns(m *meta.Model, raw string) []*meta.Field {
	var out []*meta.Field
	seen := map[string]bool{}
	for _, n := range strings.Split(raw, ",") {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		switch f := m.Field(n); {
		case n == "name":
			out = append(out, &meta.Field{Name: "name", Type: "data", Label: meta.Label{"id": "Nomor"}})
		case n == "status" && f == nil:
			out = append(out, &meta.Field{Name: "status", Type: "data", Label: meta.Label{"id": "Status"}})
		case f != nil && f.Type != "table" && f.Type != "password":
			out = append(out, f)
		}
	}
	return out
}

// exportColumns: name, then the in_list fields in declaration order (all scalar fields when the model marks none),
// then the computed status when the model has a lifecycle.
func exportColumns(m *meta.Model, rows []store.Record) []*meta.Field {
	scalar := func(f *meta.Field) bool {
		switch f.Type {
		case "table", "section", "column", "tab", "html", "json", "attach", "image", "password":
			return false
		}
		return f.Name != "name"
	}
	var cols []*meta.Field
	for _, f := range m.Fields {
		if scalar(f) && f.Raw["in_list"] == true {
			cols = append(cols, f)
		}
	}
	if len(cols) == 0 {
		for _, f := range m.Fields {
			if scalar(f) {
				cols = append(cols, f)
			}
		}
	}
	nameLabel := meta.Label{"id": "Nomor"}
	out := append([]*meta.Field{{Name: "name", Type: "data", Label: nameLabel}}, cols...)
	if m.Field("status") == nil {
		for _, r := range rows {
			if _, ok := r["status"]; ok {
				out = append(out, &meta.Field{Name: "status", Type: "data", Label: meta.Label{"id": "Status"}})
				break
			}
		}
	}
	return out
}

func fieldHeader(f *meta.Field) string {
	if t := f.Label.Text(); t != "" {
		return t
	}
	return f.Name
}

// formulaStart: a text cell starting with these is run as a formula by Excel and Sheets (CSV injection).
var formulaStart = regexp.MustCompile(`^[=+\-@\t\r]`)

// cell writes one value for a spreadsheet.
func cell(f *meta.Field, v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case bool:
		if x {
			return "Ya"
		}
		return "Tidak"
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case fmt.Stringer:
		if f != nil && meta.Numeric[f.Type] {
			return x.String()
		}
		v = x.String()
	}
	s := fmt.Sprint(v)
	if f != nil && (f.Type == "check" || f.Type == "bool") {
		switch s {
		case "1", "true":
			return "Ya"
		case "0", "false":
			return "Tidak"
		}
	}
	if f != nil && len(f.Options) > 0 {
		if o := f.Option(s); o != nil && o.Label.Text() != "" {
			s = o.Label.Text()
		}
	}
	// a plain number (also a negative one, or a money Decimal as text) is not a formula
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return s
	}
	if formulaStart.MatchString(s) {
		return "'" + s
	}
	return s
}

// value is the cell of rec for column f: a link as the linked record's title (its code when there is none or it may
// not be read), the computed status as its word, else cell().
func value(t *engine.Titles, m *meta.Model, f *meta.Field, rec store.Record) string {
	v := rec[f.Name]
	if f.Type == "link" && f.To != "" && v != nil {
		if title := t.Of(f.To, fmt.Sprint(v)); title != "" {
			return cell(nil, title)
		}
	}
	if f.Name == "status" && (m == nil || m.Field("status") == nil) {
		return cell(nil, engine.StatusLabel(fmt.Sprint(v)))
	}
	return cell(f, v)
}

// csvOut starts a CSV download named file.
func csvOut(w http.ResponseWriter, r *http.Request, file string) *csv.Writer {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+file+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte{0xEF, 0xBB, 0xBF}) // UTF-8 byte-order mark
	cw := csv.NewWriter(w)
	if s := r.URL.Query().Get("sep"); s == "semicolon" || s == ";" {
		cw.Comma = ';'
	}
	return cw
}

var unsafeFile = regexp.MustCompile(`[^a-z0-9_-]+`)

func exportFile(name, today string) string {
	return unsafeFile.ReplaceAllString(strings.ToLower(name), "_") + "-" + today + ".csv"
}

func (s *Server) exportRecords(w http.ResponseWriter, r *http.Request, c *caller) {
	opts := listOptsOf(r)
	m, rows, more, err := s.Engine.Export(r.Context(), c.actor, r.PathValue("model"), opts)
	if err != nil {
		fail(w, err)
		return
	}
	cols := pickColumns(m, r.URL.Query().Get("columns"))
	if len(cols) == 0 {
		cols = exportColumns(m, rows)
	}
	if more {
		w.Header().Set("X-Export-Truncated", strconv.Itoa(len(rows)))
	}
	cw := csvOut(w, r, exportFile(m.Name, s.Engine.Today()))
	head := make([]string, len(cols))
	for i, f := range cols {
		head[i] = fieldHeader(f)
	}
	cw.Write(head)
	titles := s.Engine.Titles(r.Context(), c.actor)
	line := make([]string, len(cols))
	for _, rec := range rows {
		for i, f := range cols {
			line[i] = value(titles, m, f, rec)
		}
		cw.Write(line)
	}
	cw.Flush()
}

// exportDashboard writes one block per widget (only widgets whose model the caller may export without an if:): a title line, then the widget's numbers. KPI: Nilai (and the
// previous period); chart: Label, Nilai, Jumlah; list: the list's columns. A widget on a model the actor may not
// export is left out with a line saying so; a hidden or failed widget is skipped.
func (s *Server) exportDashboard(w http.ResponseWriter, r *http.Request, c *caller) {
	name := r.PathValue("name")
	d, _, err := s.Engine.Dashboard(r.Context(), c.actor, name, intParam(r, "period", 0))
	if err != nil {
		fail(w, err)
		return
	}
	reg := s.Engine.Reg()
	def := reg.Dashboard(name)
	titles := s.Engine.Titles(r.Context(), c.actor)
	cw := csvOut(w, r, exportFile(name, s.Engine.Today()))
	if lbl, ok := d["label"].(meta.Label); ok && lbl.Text() != "" {
		cw.Write([]string{lbl.Text()})
	}
	if p, ok := d["period"].(int); ok && p > 0 {
		cw.Write([]string{"Periode", strconv.Itoa(p) + " hari sampai " + s.Engine.Today()})
	}
	widgets, _ := d["widgets"].([]any)
	for i, x := range widgets {
		wo, _ := x.(map[string]any)
		if wo == nil || wo["hidden"] == true || wo["error"] != nil {
			continue
		}
		var wd *meta.Widget
		if def != nil && i < len(def.Widgets) {
			wd = def.Widgets[i]
		}
		title := ""
		if wd != nil {
			title = wd.Label.Text()
			if title == "" {
				title = wd.Name
			}
		}
		cw.Write(nil)
		if wd != nil && wd.Model != "" && !s.Engine.CanExportAll(c.actor, wd.Model) {
			cw.Write([]string{title, s.Engine.ExportRefusal(c.actor, wd.Model)})
			continue
		}
		cw.Write([]string{title})
		var m *meta.Model
		if wd != nil {
			m = reg.Model(wd.Model)
		}
		field := func(n string) *meta.Field {
			if m == nil {
				return nil
			}
			return m.Field(n)
		}
		switch {
		case wo["value"] != nil || wd != nil && wd.Type == "kpi":
			var f *meta.Field
			if wd != nil {
				f = field(wd.Field)
			}
			cw.Write([]string{"Nilai", cell(f, wo["value"])})
			if wo["prev"] != nil {
				cw.Write([]string{"Periode sebelumnya", cell(f, wo["prev"])})
			}
			if wo["count"] != nil {
				cw.Write([]string{"Jumlah dokumen", cell(nil, wo["count"])})
			}
		case wo["data"] != nil:
			cw.Write([]string{"Label", "Nilai", "Jumlah"})
			points, _ := wo["data"].([]any)
			for _, p := range points {
				pm, _ := p.(map[string]any)
				cw.Write([]string{cell(nil, pm["label"]), cell(nil, pm["value"]), cell(nil, pm["count"])})
			}
		case wo["rows"] != nil:
			rows, _ := wo["rows"].([]any)
			var cols []string
			if wd != nil && len(wd.Columns) > 0 {
				cols = wd.Columns
			} else if m != nil {
				recs := make([]store.Record, 0, len(rows))
				for _, x := range rows {
					if rec, ok := x.(store.Record); ok {
						recs = append(recs, rec)
					}
				}
				for _, f := range exportColumns(m, recs) {
					cols = append(cols, f.Name)
				}
			}
			head := make([]string, len(cols))
			for j, n := range cols {
				if f := field(n); f != nil {
					head[j] = fieldHeader(f)
				} else if n == "name" {
					head[j] = "Nomor"
				} else {
					head[j] = n
				}
			}
			cw.Write(head)
			for _, x := range rows {
				rec, _ := x.(store.Record)
				line := make([]string, len(cols))
				for j, n := range cols {
					if f := field(n); f != nil {
						line[j] = value(titles, m, f, rec)
					} else if n == "status" {
						line[j] = cell(nil, engine.StatusLabel(fmt.Sprint(rec[n])))
					} else {
						line[j] = cell(nil, rec[n])
					}
				}
				cw.Write(line)
			}
		}
	}
	cw.Flush()
}
