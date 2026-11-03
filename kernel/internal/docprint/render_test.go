package docprint

import (
	"encoding/base64"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

func testModel() *meta.Model {
	return &meta.Model{Name: "sales_invoice", Label: meta.Label{"id": "Faktur penjualan"}, Fields: []*meta.Field{
		{Name: "name", Type: "data"},
		{Name: "customer", Type: "link", To: "customer"},
		{Name: "notes", Type: "text", Label: meta.Label{"id": "Catatan"}},
		{Name: "due_date", Type: "date", Label: meta.Label{"id": "Jatuh tempo", "en": "Due date"}},
		{Name: "kind", Type: "select", Options: []meta.Option{
			{Value: "full", Label: meta.Label{"id": "Pelunasan"}},
			{Value: "dp", Label: meta.Label{"id": "Uang muka", "en": "Down payment"}},
		}},
		{Name: "items", Type: "table", Of: "sales_invoice_item"},
	}}
}

func testChildren() map[string]*meta.Model {
	return map[string]*meta.Model{"sales_invoice_item": {Name: "sales_invoice_item", IsChild: true, Fields: []*meta.Field{
		{Name: "item", Type: "data"}, {Name: "qty", Type: "int"},
	}}}
}

// renderBody writes body as the format's template in a temp folder and renders doc with it.
func renderBody(t *testing.T, body, paper string, doc map[string]any, edit func(*Input)) string {
	t.Helper()
	dir := t.TempDir()
	tpl := filepath.Join(dir, "print", "invoice.html")
	if err := os.MkdirAll(filepath.Dir(tpl), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tpl, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	in := Input{
		Format:   &meta.Print{Name: "invoice", Model: "sales_invoice", Paper: paper, Template: tpl, Root: dir, Label: meta.Label{"id": "Faktur"}},
		Model:    testModel(),
		Doc:      doc,
		Printed:  time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC),
		Children: testChildren(),
	}
	if edit != nil {
		edit(&in)
	}
	out, err := Render(in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return string(out)
}

func mustContain(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

func TestRenderShell(t *testing.T) {
	out := renderBody(t, `<p>{{.Doc.name}}</p>`, "A5", map[string]any{"name": "INV-1"}, nil)
	if !strings.HasPrefix(out, "<!doctype html>\n<html lang=\"id\">\n<head>\n<meta charset=\"utf-8\">\n"+cspMeta+"\n<title>INV-1</title>\n<style>") {
		t.Errorf("bad shell start:\n%s", out)
	}
	if !strings.HasSuffix(out, "</style>\n</head>\n<body class=\"print-a5\">\n<p>INV-1</p>\n</body>\n</html>\n") {
		t.Errorf("bad shell end:\n%s", out)
	}
	mustContain(t, out, `<html lang="id">`, `<title>INV-1</title>`, `@page{size:A5;margin:10mm}`, `class="print-a5"`, `<p>INV-1</p>`,
		"#1C1917", "#D6D3D1", "#57534E", "tabular-nums", "10pt")
}

// cspMeta is the shell's CSP meta tag: the PDF is a file:// page Chromium opens without the HTTP header.
const cspMeta = `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline'">`

// The CSP meta is the first element after charset in <head>, before anything a template can add.
func TestRenderCSPMeta(t *testing.T) {
	out := renderBody(t, `{{define "style"}}p{color:red}{{end}}<img src="http://127.0.0.1:9/x.png"><script>alert(1)</script>`, "A4",
		map[string]any{"name": "INV-1"}, nil)
	head := out[:strings.Index(out, "</head>")]
	if !strings.Contains(head, "<meta charset=\"utf-8\">\n"+cspMeta+"\n<title>") || strings.Count(out, "Content-Security-Policy") != 1 {
		t.Errorf("CSP meta not right after charset:\n%s", out)
	}
	if !strings.Contains(cspMeta, CSP) {
		t.Errorf("cspMeta out of step with CSP %q", CSP)
	}
}

func TestRenderPapers(t *testing.T) {
	cases := []struct{ paper, page, class, size string }{
		{"A4", "@page{size:A4;margin:15mm}", "print-a4", "10pt"},
		{"", "@page{size:A4;margin:15mm}", "print-a4", "10pt"},
		{"80mm", "@page{size:80mm auto;margin:4mm}", "print-80mm", "8pt"},
	}
	for _, c := range cases {
		out := renderBody(t, `x`, c.paper, map[string]any{"name": "INV-1"}, nil)
		mustContain(t, out, c.page, `class="`+c.class+`"`, c.size)
	}
}

func TestRenderEscapes(t *testing.T) {
	out := renderBody(t, `<p>{{.Doc.customer}}</p>`, "A4", map[string]any{"name": "<i>INV</i>", "customer": "<b>x</b><script>"}, nil)
	mustContain(t, out, "&lt;b&gt;x&lt;/b&gt;&lt;script&gt;", "<title>&lt;i&gt;INV&lt;/i&gt;</title>")
	if strings.Contains(out, "<script>") {
		t.Errorf("unescaped script:\n%s", out)
	}
}

func TestRenderMissingIsEmpty(t *testing.T) {
	body := `[{{.Doc.notes}}][{{.Doc.due_date}}][{{.Doc.not_a_field}}][{{.Links.customer.nope}}][{{.Org.pkp}}][{{.Related.invoices}}][{{.Letterhead.tagline}}]` +
		`{{range .Doc.items}}({{.item}}|{{.qty}}){{end}}`
	doc := map[string]any{"name": "INV-1", "due_date": nil, "items": []any{map[string]any{"item": "Inspeksi"}}}
	out := renderBody(t, body, "A4", doc, nil)
	mustContain(t, out, "[][][][][][][](Inspeksi|)")
	if strings.Contains(out, "no value") {
		t.Errorf("printed <no value>:\n%s", out)
	}
	if _, ok := doc["notes"]; ok {
		t.Error("Render changed the caller's Doc")
	}
}

func TestRenderOverridePartial(t *testing.T) {
	out := renderBody(t, `{{define "letterhead"}}KOP{{end}}{{define "style"}}.x{color:red}{{end}}{{template "letterhead" .}}`, "A4",
		map[string]any{"name": "INV-1"}, nil)
	mustContain(t, out, "KOP", ".x{color:red}</style>")
}

func TestRenderLogo(t *testing.T) {
	logo := filepath.Join(t.TempDir(), "logo.svg")
	if err := os.WriteFile(logo, []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 0o644); err != nil {
		t.Fatal(err)
	}
	lh := map[string]any{"legal_name": "PT Contoh Jasa Teknik", "logo": logo}
	out := renderBody(t, `{{template "letterhead" .}}`, "A4", map[string]any{"name": "INV-1"}, func(in *Input) { in.Letterhead = lh })
	// html/template writes "+" as &#43; in attributes; a browser reads the original URI back.
	want := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`))
	mustContain(t, out, `src="data:image/svg&#43;xml;base64,`, "PT Contoh Jasa Teknik")
	mustContain(t, html.UnescapeString(out), `src="`+want+`"`)
	if _, ok := lh["logo_src"]; ok {
		t.Error("Render changed the caller's Letterhead")
	}
}

func TestRenderLabelOption(t *testing.T) {
	out := renderBody(t, `[{{label "due_date"}}][{{option "kind" "dp"}}][{{option "kind" .Doc.kind}}][{{label "nope"}}][{{option "kind" "zz"}}]`, "A4",
		map[string]any{"name": "INV-1", "kind": "full"}, nil)
	mustContain(t, out, "[Jatuh tempo][Uang muka][Pelunasan][nope][zz]")
}

func TestRenderPartials(t *testing.T) {
	lh := map[string]any{
		"legal_name": "PT Contoh Jasa Teknik", "tagline": "Inspeksi dan pengujian",
		"address": "Jl. Contoh No. 1\nJakarta 10110", "phone": "+62 21 000 0000", "email": "halo@contoh.test",
		"website": "contoh.test", "npwp": "00.000.000.0-000.000",
		"bank":      []any{map[string]any{"bank": "Bank Contoh", "account": "000 000 0000", "holder": "PT Contoh Jasa Teknik"}},
		"signatory": map[string]any{"name": "Nama Penandatangan", "title": "Direktur"},
	}
	out := renderBody(t, `{{template "letterhead" .}}{{template "bank_accounts" .}}{{template "signature" .}}`, "A4",
		map[string]any{"name": "INV-1"}, func(in *Input) { in.Letterhead = lh })
	mustContain(t, out, "PT Contoh Jasa Teknik", "Inspeksi dan pengujian", "<div class=\"address\">Jl. Contoh No. 1\nJakarta 10110</div>",
		"<span>&#43;62 21 000 0000</span>", "halo@contoh.test", "contoh.test", "NPWP 00.000.000.0-000.000",
		"Bank Contoh · 000 000 0000 · a.n. PT Contoh Jasa Teknik", "Hormat kami,", "Nama Penandatangan", "Direktur")
	if strings.Contains(out, "<img") {
		t.Errorf("img without a logo:\n%s", out)
	}
}

func TestRenderNilMaps(t *testing.T) {
	out := renderBody(t, `{{template "letterhead" .}}{{template "bank_accounts" .}}{{template "signature" .}}[{{.Model.label}}|{{.Format.label}}|{{.Format.paper}}]`,
		"A4", map[string]any{"name": "INV-1"}, nil)
	mustContain(t, out, "[Faktur penjualan|Faktur|A4]")
}

func TestRenderTemplateOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "evil.html")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := Input{Format: &meta.Print{Name: "invoice", Paper: "A4", Template: outside, Root: root}, Model: testModel(), Doc: map[string]any{}}
	if _, err := Render(in); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("want outside error, got %v", err)
	}
	in.Format.Template = filepath.Join(root, "..", filepath.Base(filepath.Dir(outside)), "evil.html")
	if _, err := Render(in); err == nil {
		t.Fatal("want error for a template reached through ..")
	}
}

func TestRenderTemplateError(t *testing.T) {
	dir := t.TempDir()
	tpl := filepath.Join(dir, "bad.html")
	if err := os.WriteFile(tpl, []byte(`{{.Doc.name`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Render(Input{Format: &meta.Print{Template: tpl, Root: dir}, Model: testModel()}); err == nil {
		t.Fatal("want parse error")
	}
	if _, err := Render(Input{Format: &meta.Print{Template: filepath.Join(dir, "missing.html"), Root: dir}, Model: testModel()}); err == nil {
		t.Fatal("want read error")
	}
	if err := os.WriteFile(tpl, []byte(`x`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Render(Input{Format: &meta.Print{Template: tpl, Root: dir, Paper: "Letter"}, Model: testModel()}); err == nil {
		t.Fatal("want unknown paper error")
	}
}
