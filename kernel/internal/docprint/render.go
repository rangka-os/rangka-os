package docprint

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// Input is everything a print format is rendered from.
type Input struct {
	Format  *meta.Print
	Model   *meta.Model
	Doc     map[string]any            // record incl. child tables
	Links   map[string]map[string]any // top-level link field → linked record
	Related map[string][]map[string]any
	// RelatedTruncated marks the related lists cut at their limit (more rows matched), as .Truncated.<name>
	RelatedTruncated map[string]bool
	Letterhead       map[string]any         // from Registry.Letterhead (logo = abs path)
	Org              map[string]any         // config.org
	Tax              map[string]any         // Registry.TaxSettings(org); missing keys take taxDefaults
	Printed          time.Time              // already in the org time zone
	Children         map[string]*meta.Model // child models of table fields, by model name
}

// Render executes the format's template inside the page shell (contract 0008 §3) and returns the HTML page.
func Render(in Input) ([]byte, error) {
	f, m := in.Format, in.Model
	if f == nil || m == nil {
		return nil, errors.New("print: format and model are required")
	}
	p, ok := paper[f.Paper]
	if f.Paper == "" {
		p, ok = paper["A4"], true
	}
	if !ok {
		return nil, fmt.Errorf("print %s: unknown paper %q", f.Name, f.Paper)
	}
	if !filepath.IsAbs(f.Template) || !filepath.IsAbs(f.Root) || !meta.Inside(filepath.Clean(f.Root), filepath.Clean(f.Template)) {
		return nil, fmt.Errorf("print %s: template is outside %s", f.Name, f.Root)
	}
	src, err := os.ReadFile(f.Template)
	if err != nil {
		return nil, fmt.Errorf("print %s: %w", f.Name, err)
	}
	letterhead, err := withLogo(in.Letterhead)
	if err != nil {
		return nil, fmt.Errorf("print %s: %w", f.Name, err)
	}

	funcs := baseFuncs(in.Printed)
	funcs["label"] = func(field string) string { return fieldLabel(m, field) }
	funcs["option"] = func(field string, v any) string { return optionLabel(m, field, v) }
	t := template.New("page").Funcs(funcs)
	if _, err := t.Parse(shell(p.page, p.class)); err != nil {
		return nil, err
	}
	if _, err := t.New("partials").Parse(partials); err != nil {
		return nil, err
	}
	if _, err := t.New(bodyName).Parse(string(src)); err != nil {
		return nil, fmt.Errorf("print %s: %w", f.Name, err)
	}

	paperName := f.Paper
	if paperName == "" {
		paperName = "A4"
	}
	data := map[string]any{
		"Doc":        fill(in.Doc, m, in.Children),
		"Links":      orEmpty(in.Links),
		"Related":    orEmpty(in.Related),
		"Truncated":  orEmpty(in.RelatedTruncated),
		"Letterhead": letterhead,
		"Org":        orEmpty(in.Org),
		"Tax":        withTaxDefaults(in.Tax),
		"Model":      map[string]any{"name": m.Name, "label": m.Label.Text()},
		"Format":     map[string]any{"name": f.Name, "label": f.Label.Text(), "paper": paperName},
		"Printed":    in.Printed,
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "page", data); err != nil {
		return nil, fmt.Errorf("print %s: %w", f.Name, err)
	}
	return buf.Bytes(), nil
}

// taxDefaults are the PPN rate and DPP factor of an org whose config tax leaves them out (schema config.json).
var taxDefaults = map[string]any{"ppn_rate": 12, "dpp_ratio": "11/12", "pkp": false}

// withTaxDefaults copies tax, filling the keys it lacks (or holds as null) from taxDefaults.
func withTaxDefaults(tax map[string]any) map[string]any {
	out := make(map[string]any, len(taxDefaults)+len(tax))
	for k, v := range tax {
		out[k] = v
	}
	for k, v := range taxDefaults {
		if out[k] == nil {
			out[k] = v
		}
	}
	return out
}

func orEmpty[V any](m map[string]V) map[string]V {
	if m == nil {
		return map[string]V{}
	}
	return m
}

// fill copies doc with every model field present: missing ones are "", table fields are lists of rows
// filled the same way from their child model. The caller's maps are not changed.
func fill(doc map[string]any, m *meta.Model, children map[string]*meta.Model) map[string]any {
	out := make(map[string]any, len(doc)+len(m.Fields))
	for k, v := range doc {
		out[k] = v
	}
	for _, fd := range m.Fields {
		if fd.Type == "table" {
			rows := []map[string]any{}
			for _, r := range rowsOf(out[fd.Name]) {
				if cm := children[fd.Of]; cm != nil {
					r = fill(r, cm, children)
				}
				rows = append(rows, r)
			}
			out[fd.Name] = rows
			continue
		}
		if _, ok := out[fd.Name]; !ok {
			out[fd.Name] = ""
		}
	}
	return out
}

var logoTypes = map[string]string{".svg": "image/svg+xml", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg"}

// withLogo copies the letterhead, replacing the logo path with logo_src, the file as a data: URI.
func withLogo(lh map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(lh)+1)
	for k, v := range lh {
		out[k] = v
	}
	logo, _ := out["logo"].(string)
	delete(out, "logo") // a server path, never printed
	if logo == "" {
		return out, nil
	}
	mime := logoTypes[strings.ToLower(filepath.Ext(logo))]
	if mime == "" {
		return out, nil
	}
	b, err := os.ReadFile(logo)
	if err != nil {
		return nil, fmt.Errorf("letterhead logo: %w", err)
	}
	out["logo_src"] = template.URL("data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b))
	return out, nil
}

// fieldLabel is the field's Indonesian label, else its name.
func fieldLabel(m *meta.Model, field string) string {
	if f := m.Field(field); f != nil {
		if t := f.Label.Text(); t != "" {
			return t
		}
	}
	return field
}

// optionLabel is the Indonesian label of the field's option v, else v itself.
func optionLabel(m *meta.Model, field string, v any) string {
	if v == nil {
		return ""
	}
	s := fmt.Sprint(v)
	if f := m.Field(field); f != nil {
		for _, o := range f.Options {
			if o.Value == s {
				if t := o.Label.Text(); t != "" {
					return t
				}
				break
			}
		}
	}
	return s
}
