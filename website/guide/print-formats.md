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

For a statement of account, add `related:` to load other records for the template, for example every invoice of
the customer. They arrive as `.Related.invoices`.

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

Rangka picks `letterheads/<org>.rk` for the caller's organisation, then `letterhead.rk`, then builds one from the
organisation's settings (name and NPWP). The partials `letterhead`, `bank_accounts` and `signature` render it.

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
| `upper s` | | upper case |

Exact rules (rounding, negatives, limits) are in [contract 0008 §3](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0008-print-formats.md).

## Print and PDF

```text
GET /api/v1/r/sales_invoice/INV-2026-00047/print?format=sales_invoice&as=html
GET /api/v1/r/sales_invoice/INV-2026-00047/print?format=sales_invoice&as=pdf
```

HTML is always available. PDF needs Chromium or Chrome on the server: set `RANGKA_CHROMIUM` to its path, or
install it so it is found on `PATH`. `RANGKA_CHROMIUM=off` turns PDF off. Without a browser the PDF request
answers 501 `pdf_unavailable`, and the user can open the HTML version and use *Cetak, Simpan sebagai PDF* in the
browser. See the [REST API](/reference/rest-api) and the kernel README for the full list of codes.

Run `rangka validate` after editing: it reports a missing template, a template path that leaves its folder, a
template that does not parse and a missing logo.
