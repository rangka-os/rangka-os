package docprint

// The page shell, CSS and built-in partials wrapped around a format's template (contract 0008 §3).

// CSP is the Content-Security-Policy of every printed page: no script, inline styles, data: images only.
const CSP = "default-src 'none'; img-src data:; style-src 'unsafe-inline'"

// paper maps a format's paper to its @page rule and the body class suffix.
var paper = map[string]struct{ page, class string }{
	"A4":   {"@page{size:A4;margin:15mm}", "a4"},
	"A5":   {"@page{size:A5;margin:10mm}", "a5"},
	"80mm": {"@page{size:80mm auto;margin:4mm}", "80mm"},
}

// baseCSS uses the Rangka tokens: ink text, stone rules, muted stone for secondary text.
const baseCSS = `*{box-sizing:border-box}` +
	`body{margin:0;color:#1C1917;font-family:system-ui,-apple-system,"Segoe UI",Roboto,"Helvetica Neue",Arial,sans-serif;font-size:10pt;line-height:1.4}` +
	`body.print-80mm{font-size:8pt}` +
	`h1,h2,h3{margin:0 0 3mm;font-weight:600}` +
	`table{width:100%;border-collapse:collapse}` +
	`th,td{padding:1.5mm 2mm;border-bottom:1px solid #D6D3D1;text-align:left;vertical-align:top}` +
	`th{font-weight:600}` +
	`.num{text-align:right;font-variant-numeric: tabular-nums}` +
	`.muted{color:#57534E}` +
	`.letterhead{display:flex;gap:4mm;align-items:flex-start;padding-bottom:3mm;margin-bottom:5mm;border-bottom:1px solid #D6D3D1}` +
	`.letterhead .logo{max-height:18mm;max-width:40mm}` +
	`.letterhead .legal-name{font-size:1.3em;font-weight:600}` +
	`.letterhead .address{white-space:pre-line}` +
	`.contacts span+span::before{content:" · "}` +
	`.bank-accounts{margin-top:4mm}` +
	`.signature{margin-top:8mm;width:60mm}` +
	`.signature .space{height:18mm}`

// shell builds the page template text; the format's template is executed as bodyName.
func shell(page, class string) string {
	// The CSP is also a meta tag, first after charset: a PDF is rendered from a file:// page with no HTTP header.
	return "<!doctype html>\n<html lang=\"id\">\n<head>\n<meta charset=\"utf-8\">\n" +
		"<meta http-equiv=\"Content-Security-Policy\" content=\"" + CSP + "\">\n<title>{{.Doc.name}}</title>\n" +
		"<style>" + page + baseCSS + "{{template \"style\" .}}</style>\n</head>\n" +
		"<body class=\"print-" + class + "\">\n{{template \"" + bodyName + "\" .}}\n</body>\n</html>\n"
}

const bodyName = "rangka_format_body"

// partials are parsed before the format's template, so its {{define}}s replace them.
const partials = `{{define "style"}}{{end}}` +
	`{{define "letterhead"}}{{with .Letterhead}}<header class="letterhead">` +
	`{{with .logo_src}}<img class="logo" src="{{.}}" alt="">{{end}}` +
	`<div>` +
	`<div class="legal-name">{{.legal_name}}</div>` +
	`{{with .tagline}}<div class="muted">{{.}}</div>{{end}}` +
	`{{with .address}}<div class="address">{{.}}</div>{{end}}` +
	`{{if or .phone .email .website}}<div class="contacts">` +
	`{{with .phone}}<span>{{.}}</span>{{end}}{{with .email}}<span>{{.}}</span>{{end}}{{with .website}}<span>{{.}}</span>{{end}}` +
	`</div>{{end}}` +
	`{{with .npwp}}<div>NPWP {{.}}</div>{{end}}` +
	`</div></header>{{end}}{{end}}` +
	`{{define "bank_accounts"}}{{with .Letterhead.bank}}<div class="bank-accounts"><div class="muted">Pembayaran ke rekening:</div>` +
	`{{range .}}<div>{{.bank}} · {{.account}}{{with .holder}} · a.n. {{.}}{{end}}</div>{{end}}` +
	`</div>{{end}}{{end}}` +
	`{{define "signature"}}<div class="signature"><div>Hormat kami,</div><div class="space"></div>` +
	`{{with .Letterhead.signatory}}<div>{{.name}}</div>{{with .title}}<div class="muted">{{.}}</div>{{end}}` +
	`{{else}}<div>{{.Letterhead.legal_name}}</div>{{end}}</div>{{end}}`
