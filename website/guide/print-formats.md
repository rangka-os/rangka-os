# Print formats

A print format turns one record into a document: a quotation, an invoice, a purchase order or a statement of
account. A format is a small `.rk` file and an HTML template. The full rules are in
[contract 0008](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0008-print-formats.md); this page is the short version.

## Add a format to a module

Put two files in the module (or app):

```text
sales/
  print/
    sales_invoice.rk
    sales_invoice.html
```

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/print.json
name: sales_invoice          # same as the file name
model: sales_invoice
label: {id: Faktur, en: Invoice}
paper: A4                    # A4, A5 or 80mm
template: sales_invoice.html
default: true                # the format the print button uses first
```

The template is Go `html/template` and holds only the page body. The kernel adds the document, the page size and
the base styles.

```html
<h1>Faktur {{.Doc.name}}</h1>
<p>Kepada Yth. {{with .Links.customer}}{{.customer_name}}{{else}}{{.Doc.customer}}{{end}}</p>
<p>Jatuh tempo: {{date .Doc.due_date}}</p>
<p>Total: {{rupiah .Doc.grand_total}}</p>
<p><em>{{terbilang .Doc.grand_total}}</em></p>
{{template "bank_accounts" .}}
{{template "signature" .}}
```

Start from a reference layout in [`templates/print/`](https://github.com/rangka-os/rangka-os/tree/main/templates/print)
(quotation, invoice, purchase order, statement) and adapt it. That folder lists the field names each layout reads.
Guard nested values with `{{with}}`: a field that is present but empty stops the render when you reach into it.

For a statement of account, add `related:` to load other records for the template, for example the customer's
open invoices (`filter: docstatus == 1 && grand_total > paid_amount`). They arrive as `.Related.invoices`, cut at
the list's `limit`. When more records matched than the limit, `.Truncated.invoices` is true; say so on the page,
as the reference statement does.

## Organisation data

`.Org` holds the caller's organisation: its `org:` settings in `config.rk` (`legal_name`, `npwp`) plus `name`
and `code` from `orgs.<id>`. `.Org.pkp` is the organisation's merged `tax.pkp` (an `org:` `pkp` key is ignored),
the same value as `tax.pkp` in expressions; `.Tax` holds the same `ppn_rate` and `dpp_ratio` as the `tax` global. The reference invoice
and quotation print the DPP and PPN rows only when `.Org.pkp` is true.

Per-organisation settings are deep-merged over the site's, with two exceptions. `tax: {ppn_rate: 11}` keeps the
site's `tax.pkp`. But `org:` and `numbering` replace whole: each organisation listed in `orgs:` is its own legal
entity and never inherits the site's `legal_name` or `npwp`. Its legal name is its own `org.legal_name`, else its
`name`; its `pkp` is its merged `tax.pkp`. A listed organisation without its own
`letterheads/<org>.rk` does not use `letterhead.rk` (that holds another entity's bank and signatory); its
letterhead is built from its own data only.

## Override per site

A firm changes the look without touching `apps/`. Add a `print/<name>.rk` with the same name in the site:

```text
site/
  print/
    sales_invoice.rk
    sales_invoice.html
```

The site file replaces the module's format of the same name. The template path must stay inside the site folder.

## Letterhead

The letterhead holds the firm's name, logo, address, NPWP, bank accounts and signatory. It is not part of each
template, so it changes in one place.

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/letterhead.json
legal_name: PT Contoh Jasa Teknik
logo: brand/logo.png
address: |
  Jl. Contoh No. 1
  Jakarta 10110
npwp: "00.000.000.0-000.000"
bank:
  - {bank: Bank Contoh, account: "000 000 0000", holder: PT Contoh Jasa Teknik}
signatory: {name: Nama Penandatangan, title: Direktur}
footer: {id: Terima kasih atas kepercayaan Anda}
```

Rangka picks `letterheads/<org>.rk` for the caller's organisation, then (only for an organisation not listed in
`orgs:`) `letterhead.rk`, then builds one from the organisation's settings (`org.legal_name`, else the org's
`name`, and `org.npwp`), which also fill a letterhead file that leaves them out. The partials `letterhead`,
`bank_accounts` and `signature` render it. `rangka validate` warns (`letterhead_missing`) for each
organisation listed in `orgs:` that has no `letterheads/<id>.rk`.

## Helpers

| Helper | Example | Output |
| --- | --- | --- |
| `rupiah n` | `rupiah 1250000` | `Rp 1.250.000` |
| `money n code` | `money "1234.5" "USD"` | `USD 1.234,50` |
| `number n [decimals]` | `number 1.5` | `1,5` |
| `date d ["long"]` | `date "2026-10-27"` | `27 Okt 2026` |
| `terbilang n` | `terbilang 1250000` | `satu juta dua ratus lima puluh ribu rupiah` |
| `label "field"`, `option "field" v` | `label "due_date"` | the Indonesian field or option label |
| `sub a b`, `sum list "field"` | `sub .Doc.grand_total .Doc.paid_amount` | exact money arithmetic |
| `overdue d`, `aging rows` | `overdue .Doc.due_date` | days past due; open amounts by age |
| `taxrate doc tax` | `number (taxrate .Doc .Tax)` | the PPN rate: the record's `ppn_rate`, else `tax_rate`, else the org's config (a stored 0 counts) |
| `dppfactor doc tax` | `dppfactor .Doc .Tax` | the DPP factor (`11/12`), empty when it is exactly 1 |
| `upper s` | | upper case |

Exact rules (rounding, negatives, limits) are in [contract 0008 §3](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0008-print-formats.md).

## Print and PDF

```text
GET /api/v1/r/sales_invoice/INV-2026-00047/print?format=sales_invoice&as=html
GET /api/v1/r/sales_invoice/INV-2026-00047/print?format=sales_invoice&as=pdf
```

HTML is always available. PDF needs Chromium or Chrome on the server: set `RANGKA_CHROMIUM` to its path, or
install it so it is found on `PATH`. `RANGKA_CHROMIUM=off`, or a path that does not exist, turns PDF off. Without a
browser the PDF request answers 501 `pdf_unavailable`, and the user can open the HTML version and use *Cetak,
Simpan sebagai PDF* in the browser.

Answers besides 200: 400 `bad_request` (`as` is not `html` or `pdf`), 403 `permission_denied` (the caller can
read the record but may not print it), 404 `not_found` (no such record for the caller, including another org's),
404 `no_print_format`, 500 `pdf_failed` (Chromium failed) and 501 `pdf_unavailable`. See also the
[REST API](/reference/rest-api).

Run `rangka validate` after editing: it reports a missing template, a template path that leaves its folder, a
template that does not parse and a missing logo.
