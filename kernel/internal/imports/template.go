package imports

import (
	"fmt"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// TemplateColumn is one column of an import's template file (contract 0012 §7): the header the importer looks
// for, an example cell and a short note in Bahasa Indonesia on what to write there.
type TemplateColumn struct {
	Header   string `json:"header"`
	Example  string `json:"example"`
	Note     string `json:"note"`
	Required bool   `json:"required"`
}

// Template is what an owner downloads before an import: the columns in the order the import maps them, one
// example row and general notes.
type Template struct {
	Import    string           `json:"import"`
	Label     string           `json:"label"`
	Sheet     string           `json:"sheet"`      // the data sheet name: the import's sheet, else "Data"
	HeaderRow int              `json:"header_row"` // rows above it are left empty, so the template reads back as is
	Columns   []TemplateColumn `json:"columns"`
	Notes     []string         `json:"notes"`
}

// BuildTemplate derives the template of imp from its map, lines, key and group (the columns checkColumns asks
// for, in the same order). Examples follow the target field's type in a form Convert accepts; today is the
// example date. Nothing is read from the org's data, so a template never shows records.
func BuildTemplate(reg *meta.Registry, imp *meta.Import, today time.Time) *Template {
	model, lineModel := models(reg, imp)
	t := &Template{Import: imp.Name, Label: imp.Label.Text(), Sheet: imp.Sheet, HeaderRow: imp.HeaderRow}
	if t.Label == "" {
		t.Label = imp.Name
	}
	if t.Sheet == "" {
		t.Sheet = "Data"
	}
	if t.HeaderRow < 1 {
		t.HeaderRow = 1
	}
	idx := map[string]int{}
	add := func(c TemplateColumn) int {
		if i, ok := idx[c.Header]; ok {
			return i
		}
		idx[c.Header] = len(t.Columns)
		t.Columns = append(t.Columns, c)
		return len(t.Columns) - 1
	}
	fromMap := func(m *meta.Model, mp map[string]meta.MapValue, order []string) {
		for _, name := range order {
			mv := mp[name]
			if mv.Column == "" {
				continue // a formula: no column in the file
			}
			var f *meta.Field
			if m != nil {
				f = m.Field(name)
			}
			add(fieldColumn(reg, f, name, mv, today))
		}
	}
	fromMap(model, imp.Map, imp.MapOrder)
	if imp.Lines != nil {
		fromMap(lineModel, imp.Lines.Map, imp.Lines.MapOrder)
	}
	for _, k := range imp.Key {
		i := add(TemplateColumn{Header: k, Example: "Contoh " + strings.ToLower(k), Note: "Teks bebas."})
		t.Columns[i].Note = strings.TrimSpace(t.Columns[i].Note + " Bagian dari kunci baris: baris yang kuncinya sudah pernah diimpor dilewati.")
	}
	modelLabel := "data"
	if model != nil && model.Label.Text() != "" {
		modelLabel = strings.ToLower(model.Label.Text())
	}
	for _, g := range imp.Group {
		i := add(TemplateColumn{Header: g, Example: "Contoh " + strings.ToLower(g), Note: "Teks bebas."})
		t.Columns[i].Note = strings.TrimSpace(t.Columns[i].Note + " Baris dengan isi kolom ini yang sama jadi satu " + modelLabel + ".")
	}

	t.Notes = []string{
		"Isi satu baris per " + rowUnit(imp, lineModel, modelLabel) + ". Judul kolom jangan diubah; urutan kolom boleh bebas.",
		"Baris contoh hanya contoh: ganti atau hapus sebelum mengunggah.",
		"Angka boleh ditulis 1250000, 1.250.000 atau Rp 1.250.000. Tanggal ditulis dd/mm/yyyy.",
	}
	if t.HeaderRow > 1 {
		t.Notes = append(t.Notes, fmt.Sprintf("Judul kolom ada di baris %d; baris di atasnya tidak dibaca.", t.HeaderRow))
	}
	if imp.Per == "outlet" {
		t.Notes = append(t.Notes, "Satu file untuk satu outlet. Outletnya dipilih saat mengunggah.")
	}
	if len(imp.Key) > 0 {
		t.Notes = append(t.Notes, "File yang sama boleh diunggah lagi: baris yang sudah diimpor dilewati.")
	}
	if model != nil && model.IsSubmittable {
		t.Notes = append(t.Notes, "Data masuk sebagai draf. Periksa lalu ajukan dari daftar.")
	}
	return t
}

func rowUnit(imp *meta.Import, lineModel *meta.Model, modelLabel string) string {
	if imp.Lines != nil && lineModel != nil && lineModel.Label.Text() != "" {
		return strings.ToLower(lineModel.Label.Text())
	}
	if imp.Lines != nil {
		return "baris rincian"
	}
	return modelLabel
}

// fieldColumn is the template column of a mapped field; f is nil when the model does not know the field (the
// validator reports that), and the column then reads as free text.
func fieldColumn(reg *meta.Registry, f *meta.Field, name string, mv meta.MapValue, today time.Time) TemplateColumn {
	c := TemplateColumn{Header: mv.Column}
	if f == nil {
		c.Example, c.Note = "Contoh", "Teks bebas."
		return c
	}
	c.Required = f.Required
	label := f.Label.Text()
	if label == "" {
		label = name
	}
	switch f.Type {
	case "currency":
		c.Example, c.Note = "15000", "Rupiah bulat, tanpa desimal."
	case "int", "rating":
		c.Example, c.Note = "1", "Bilangan bulat."
	case "float":
		c.Example, c.Note = "1,5", "Angka; desimal boleh pakai koma."
	case "percent":
		c.Example, c.Note = "10", "Persen, tanpa tanda %."
	case "date":
		c.Example, c.Note = today.Format("02/01/2006"), "Tanggal dd/mm/yyyy."
	case "datetime":
		c.Example, c.Note = today.Format("02/01/2006")+" 08:30", "Tanggal dan jam: dd/mm/yyyy HH:MM."
	case "check":
		c.Example, c.Note = "Ya", "Ya atau Tidak."
	case "select":
		var vals []string
		for _, o := range f.Options {
			vals = append(vals, o.Value)
		}
		if len(vals) > 0 {
			c.Example = vals[0]
			c.Note = "Salah satu dari: " + strings.Join(vals, ", ") + "."
		} else {
			c.Example, c.Note = "Contoh", "Teks bebas."
		}
	case "link":
		target := f.To
		var tm *meta.Model
		if reg != nil {
			tm = reg.Models[f.To]
		}
		if tm != nil && tm.Label.Text() != "" {
			target = strings.ToLower(tm.Label.Text())
		}
		by := "kodenya"
		if mv.Match != "" && mv.Match != "name" {
			by = mv.Match
			if tm != nil {
				if mf := tm.Field(mv.Match); mf != nil && mf.Label.Text() != "" {
					by = strings.ToLower(mf.Label.Text())
				}
			}
		}
		c.Example = "Contoh " + strings.ToLower(label)
		c.Note = fmt.Sprintf("%s yang sudah ada, ditulis sesuai %s.", capitalize(target), by)
	default:
		c.Example, c.Note = "Contoh "+strings.ToLower(label), "Teks."
	}
	c.Note = label + ": " + lowerFirst(c.Note)
	if f.Required {
		c.Note += " Wajib diisi."
	}
	return c
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
