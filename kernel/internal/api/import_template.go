package api

import (
	"bytes"
	"net/http"

	"github.com/xuri/excelize/v2"

	"github.com/rangka-os/rangka-os/kernel/internal/imports"
)

// importTemplate downloads the template of an import (contract 0012 §7):
//
//	GET /api/v1/imports/{import}/template?format=csv|xlsx   default: the import's source
//
// The data sheet holds the header row (at the import's header_row) and one example row; an XLSX file adds a
// "Petunjuk" sheet with general notes and one line per column. A CSV has no second sheet, so it carries the
// columns and the example only. Every cell is text (no formula), so the file reads back through the importer.
func (s *Server) importTemplate(w http.ResponseWriter, r *http.Request, c *caller) {
	format := r.URL.Query().Get("format")
	switch format {
	case "", "csv", "xlsx":
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "format harus csv atau xlsx")
		return
	}
	t, err := s.Imports.Template(r.Context(), c.actor, r.PathValue("import"))
	if err != nil {
		fail(w, err)
		return
	}
	if format == "" {
		format = "csv"
		if im := s.Engine.Reg().Imports[t.Import]; im != nil && im.Source == "xlsx" {
			format = "xlsx"
		}
	}
	base := "template-" + unsafeFile.ReplaceAllString(t.Import, "_")
	if format == "xlsx" {
		data, err := templateXLSX(t)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "export_failed", "File Excel gagal dibuat.")
			return
		}
		h := w.Header()
		h.Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		h.Set("Content-Disposition", `attachment; filename="`+base+`.xlsx"`)
		h.Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		w.Write(data)
		return
	}
	cw := csvSheet{csvOut(w, r, base+".csv")}
	head, example := templateRows(t)
	for i := 1; i < t.HeaderRow; i++ {
		cw.Write(make([]string, len(head))) // empty rows above the header, as the import reads it
	}
	cw.Write(head)
	cw.Write(example)
	cw.Flush()
}

func templateRows(t *imports.Template) (head, example []string) {
	for _, c := range t.Columns {
		head = append(head, c.Header)
		example = append(example, c.Example)
	}
	return head, example
}

// templateXLSX writes the data sheet and the "Petunjuk" sheet. Every cell is a string cell (SetSheetRow stores Go
// strings as shared strings, never as a formula), so a header or an example starting with = stays text.
func templateXLSX(t *imports.Template) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", t.Sheet); err != nil {
		return nil, err
	}
	head, example := templateRows(t)
	put := func(sheet string, row int, vals []string) error {
		cells := make([]any, len(vals))
		for i, v := range vals {
			cells[i] = v
		}
		at, _ := excelize.CoordinatesToCellName(1, row)
		return f.SetSheetRow(sheet, at, &cells)
	}
	if err := put(t.Sheet, t.HeaderRow, head); err != nil {
		return nil, err
	}
	if err := put(t.Sheet, t.HeaderRow+1, example); err != nil {
		return nil, err
	}
	bold, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if len(head) > 0 {
		from, _ := excelize.CoordinatesToCellName(1, t.HeaderRow)
		to, _ := excelize.CoordinatesToCellName(len(head), t.HeaderRow)
		f.SetCellStyle(t.Sheet, from, to, bold)
		last, _ := excelize.ColumnNumberToName(len(head))
		f.SetColWidth(t.Sheet, "A", last, 20)
	}

	const notes = "Petunjuk"
	if _, err := f.NewSheet(notes); err != nil {
		return nil, err
	}
	row := 1
	if err := put(notes, row, []string{t.Label}); err != nil {
		return nil, err
	}
	f.SetCellStyle(notes, "A1", "A1", bold)
	row += 2
	for _, n := range t.Notes {
		if err := put(notes, row, []string{n}); err != nil {
			return nil, err
		}
		row++
	}
	row++
	if err := put(notes, row, []string{"Kolom", "Isi", "Wajib", "Contoh"}); err != nil {
		return nil, err
	}
	hr, _ := excelize.CoordinatesToCellName(1, row)
	hrEnd, _ := excelize.CoordinatesToCellName(4, row)
	f.SetCellStyle(notes, hr, hrEnd, bold)
	row++
	for _, c := range t.Columns {
		req := "Tidak"
		if c.Required {
			req = "Ya"
		}
		if err := put(notes, row, []string{c.Header, c.Note, req, c.Example}); err != nil {
			return nil, err
		}
		row++
	}
	f.SetColWidth(notes, "A", "A", 24)
	f.SetColWidth(notes, "B", "B", 70)
	f.SetColWidth(notes, "C", "D", 14)
	f.SetActiveSheet(0)
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
