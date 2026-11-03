// Package imports reads uploaded CSV/XLSX files and converts cells to field values (contract 0012).
package imports

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Table is a sheet reduced to a header row and data rows.
type Table struct {
	Headers   []string
	HeaderRow int
	Rows      []Row
}

// Row is one data row; Num is the 1-based sheet row number.
type Row struct {
	Num   int
	Cells map[string]string
}

const (
	MaxBytes = 20 << 20
	MaxRows  = 50000
)

var (
	ErrUnreadable  = errors.New("File tidak bisa dibaca sebagai CSV atau XLSX")
	ErrTooBig      = errors.New("File lebih dari 20 MB")
	ErrTooManyRows = errors.New("File lebih dari 50.000 baris")
)

// Read parses data as XLSX (PK\x03\x04 prefix) or CSV. sheet is an XLSX sheet name ("" = first);
// headerRow is the 1-based row holding the column names (<1 means 1).
func Read(data []byte, sheet string, headerRow int) (*Table, error) {
	if len(data) > MaxBytes {
		return nil, ErrTooBig
	}
	if headerRow < 1 {
		headerRow = 1
	}
	var grid [][]string
	var err error
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		grid, err = readXLSX(data, sheet)
	} else {
		grid, err = readCSV(data, headerRow)
	}
	if err != nil {
		return nil, err
	}
	if headerRow > len(grid) {
		return nil, ErrUnreadable
	}
	t := &Table{HeaderRow: headerRow}
	for _, h := range grid[headerRow-1] {
		t.Headers = append(t.Headers, strings.TrimSpace(h))
	}
	for i := headerRow; i < len(grid); i++ {
		cells := make(map[string]string, len(t.Headers))
		blank := true
		for j, h := range t.Headers {
			v := ""
			if j < len(grid[i]) {
				v = strings.TrimSpace(grid[i][j])
			}
			if v != "" {
				blank = false
			}
			if h != "" {
				cells[h] = v
			}
		}
		if blank {
			continue
		}
		if len(t.Rows) >= MaxRows {
			return nil, ErrTooManyRows
		}
		t.Rows = append(t.Rows, Row{Num: i + 1, Cells: cells})
	}
	return t, nil
}

func readCSV(data []byte, headerRow int) ([][]string, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	// The delimiter is chosen from the header line.
	first := data
	if lines := bytes.Split(data, []byte("\n")); headerRow <= len(lines) {
		first = lines[headerRow-1]
	}
	r := csv.NewReader(bytes.NewReader(data))
	if bytes.Count(first, []byte(";")) > bytes.Count(first, []byte(",")) {
		r.Comma = ';'
	}
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	// encoding/csv skips blank lines; keep them as empty rows so row numbers match the file.
	var grid [][]string
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, ErrUnreadable
		}
		line, _ := r.FieldPos(0)
		for len(grid) < line-1 {
			grid = append(grid, nil)
		}
		grid = append(grid, rec)
	}
	return grid, nil
}

func readXLSX(data []byte, sheet string) ([][]string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{UnzipSizeLimit: 200 << 20})
	if err != nil {
		return nil, ErrUnreadable
	}
	defer f.Close()
	if sheet == "" {
		sheet = f.GetSheetName(0)
	}
	rows, err := f.GetRows(sheet, excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, ErrUnreadable
	}
	return rows, nil
}
