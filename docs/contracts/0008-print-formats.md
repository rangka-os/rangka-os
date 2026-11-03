# 0008: Print formats

Status: proposed (3 Nov 2026) · Owner: rangka-os (kernel, schema 0.5) · Stream S3 · Affects: rangka-apps (modules ship print formats), pilot bundles (letterheads), web app (print button)

## Context
- Pilot A must print a quotation, invoice, PO and statement of account (SOA) that look like the firm's own; pilot B prints POs to suppliers.
- Today a record can only be read as JSON. Firms retype it into Word.
- Layouts follow common public Indonesian conventions for *surat penawaran harga*, *faktur/invoice* (PPN, PPh 23 note), *purchase order* and *laporan tagihan / statement of account*. No real customer document is used anywhere.

## Decision

### 1. Kind `print` (`print/<name>.rk` in a module/app or in the site)
```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/print.json
name: sales_invoice          # = file stem
model: sales_invoice
label: {id: Faktur, en: Invoice}
paper: A4                    # A4 | A5 | 80mm   (default A4)
template: sales_invoice.html # relative to this file; must stay inside the module/app/site folder
default: true                # optional: the format the print button uses first
related:                     # optional: lists loaded for the template (SOA, delivery lists)
  invoices:
    model: sales_invoice
    match: {customer: name}  # related.customer == doc.name
    filter: docstatus == 1 && grand_total > paid_amount  # optional CEL on the related record (no `doc` access); open items only
    sort: posting_date       # field or -field
    limit: 1000              # default 200, max 1000; more matches → .Truncated.invoices
```
- A site `print/<name>.rk` with the same name replaces the module's one (a firm's own look without touching `apps/`).
- Formats of apps that are not installed are ignored.

### 2. Kind `letterhead` (site `letterhead.rk`, optional `letterheads/<org>.rk` per org)
```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/letterhead.json
legal_name: PT Contoh Jasa Teknik
tagline: Inspeksi dan pengujian
logo: brand/logo.png         # site-relative png/jpg/svg, inlined as a data: URI
address: |
  Jl. Contoh No. 1
  Jakarta 10110
phone: "+62 21 000 0000"
email: halo@contoh.test
website: contoh.test
npwp: "00.000.000.0-000.000"
bank:
  - {bank: Bank Contoh, account: "000 000 0000", holder: PT Contoh Jasa Teknik, branch: Jakarta}
signatory: {name: Nama Penandatangan, title: Direktur}
footer: {id: Terima kasih atas kepercayaan Anda}
```
- Lookup for an org not listed in `orgs:`: `letterheads/<org>.rk`, else `letterhead.rk`, else a letterhead built from the site's `org:` (legal name, NPWP). An org listed in `orgs:` is its own legal entity: `letterheads/<org>.rk`, else a letterhead built from its own data only. It never falls back to `letterhead.rk`, whose bank and signatory belong to another entity.
- Org data (contract 0010 §2): templates get `.Org`, `Registry.OrgIdentity(org)` (a copy). For an org listed in `orgs:` it holds only that org's own `config.org` (`legal_name`, `npwp`, `pkp`) plus `name` and `code` from `orgs.<id>`, and `legal_name` is `orgs.<id>.name` when its `config.org` has none; the site's `org:` is never merged in, so an org without its own `npwp` has none. For an unlisted org it is the site's `org:`. `.Org.pkp` is the org's own `org.pkp`, else the merged `OrgConfig(org, "tax.pkp")`. The same data fills a letterhead's `legal_name` (else the org's `name`) and `npwp` when the letterhead file leaves them out.

### 3. Templates: Go `html/template`
- The template holds the page body. The kernel wraps it in a full document: `<!doctype html>`, `lang="id"`, title = record name, `@page` size and margins for the paper, base CSS (Rangka tokens: ink text, stone rules, tabular numbers), and a `print-<paper>` class on `<body>`. A template may add CSS with `{{define "style"}}…{{end}}`.
- Data: `.Doc` (the record with child tables; every model field present, missing ones empty), `.Links` (each top-level link field's record, e.g. `.Links.customer.customer_name`), `.Related.<name>` (lists from `related:`), `.Truncated.<name>` (true when more rows matched than the list's `limit`, so the list was cut; templates should say so), `.Letterhead`, `.Org`, `.Tax` (the org's config `tax`, deep-merged over the site's: `ppn_rate`, default `12`; `dpp_ratio`, default `"11/12"`; `pkp`), `.Model` (`name`, `label`), `.Format` (`name`, `label`, `paper`), `.Printed` (print time, org time zone).
- Tax labels: a layout never hard-codes a rate or DPP factor. The rate shown is `taxrate .Doc .Tax`, printed with `number` (`PPN 11%`, `PPN 0%`); the DPP row is labelled `DPP nilai lain (<factor>)` with `dppfactor .Doc .Tax`, or `DPP` when that is empty (factor 1). The amounts stay the record's `dpp` and `ppn`: the layout computes no tax.
- Built-in partials, which a template may redefine: `letterhead` (logo, legal name, address, NPWP, contacts), `bank_accounts`, `signature`.
- Helpers (exact output, tested):

| Helper | Example | Output |
| --- | --- | --- |
| `rupiah n` | `rupiah 1250000` | `Rp 1.250.000` (negative `-Rp 1.250.000`; rounds half away from zero to whole rupiah) |
| `money n code` | `money "1234.5" "USD"` | `USD 1.234,50`; `IDR` → same as `rupiah` |
| `number n [decimals]` | `number 1.5` | `1,5` (Indonesian separators, trailing zeros dropped unless decimals given) |
| `date d ["long"]` | `date "2026-10-27"` | `27 Okt 2026`; `long` → `27 Oktober 2026` |
| `terbilang n` | `terbilang 1250000` | `satu juta dua ratus lima puluh ribu rupiah` (`seratus`, `seribu`, `sebelas`; up to triliun; `nol rupiah`; `minus …`) |
| `label "field"` | `label "due_date"` | the model field's Indonesian label |
| `option "field" v` | `option "status" .Doc.status` | the option's Indonesian label |
| `sub a b`, `sum list "field"` | `sub .Doc.grand_total .Doc.paid_amount` | exact decimal arithmetic for money columns |
| `overdue d` | `overdue .Doc.due_date` | whole days past `d` at print time, `0` when not due |
| `aging rows` | `aging .Related.invoices` | open amounts (`grand_total − paid_amount`) by age past `due_date`: `current`, `d30`, `d60`, `d90`, `over90`, `total` |
| `taxrate doc tax` | `number (taxrate .Doc .Tax)` | the record's `ppn_rate`, else its `tax_rate`, else `tax.ppn_rate`; a record value is set when present and not null or `""`, so a stored `0` prints `0` |
| `dppfactor doc tax` | `dppfactor .Doc .Tax` | the record's `dpp_ratio` (same rule), else `tax.dpp_ratio`, as written (`11/12`); `""` when it equals exactly 1 (`1`, `1/1`, `12/12`, `1.0`) |
| `upper s` | | upper case |
- Missing values print as empty. Templates cannot run script or load anything: the page carries `Content-Security-Policy: default-src 'none'; img-src data:; style-src 'unsafe-inline'` as a `<meta http-equiv>` first in `<head>` (so the PDF, rendered from a local file, is bound too) and, for `as=html`, as a response header.

### 4. REST
`GET /api/v1/r/{model}/{name}/print?format={print}&as=html|pdf`
- `format` defaults to the model's `default: true` format, else the first by name; none → 404 `no_print_format`.
- `as=html` (default): `text/html; charset=utf-8`.
- `as=pdf`: rendered by headless Chromium (`RANGKA_CHROMIUM` = path, or found on `PATH` / the macOS app path; `RANGKA_CHROMIUM=off` disables). Without it: 501 `pdf_unavailable` with message "PDF belum tersedia di server ini. Buka versi HTML lalu cetak dari browser (Ctrl+P → Simpan sebagai PDF)." and `hint` = the HTML URL. PDF is `inline; filename="<name>.pdf"`.
- `/api/v1/meta` lists each model's formats as `prints: [{name, label, paper, default}]`.
- `as` other than `html`/`pdf` → 400 `bad_request`. A failed conversion → 500 `pdf_failed` (detail in the server log). At most `RANGKA_PDF_CONCURRENCY` (default 2) Chromium processes run at once; further requests wait for a slot.
- Each print writes an audit entry `record.print` (`<model>/<name>`, format, as). A `HEAD` request gets the headers only: it starts no browser and writes no audit entry.

### 5. Permissions
- The route sits behind S1's record guard (`recordGuard("print")`): the record is read through the engine as the caller, then `Engine.Authorize(actor, model, "print", record)` decides.
- A record of another org, or one outside the caller's row scope (an outlet the caller is not assigned to, contract 0010), → 404 `not_found`, the same as `GET /r/{model}/{name}`, so a record's existence never leaks.
- A caller who can see the record but lacks the `print` permission → S1's 403 `permission_denied` with `{model, action: "print"}`:
  `{"error": {"code": "permission_denied", "message": "…", "model": "sales_invoice", "action": "print"}}`. The format is looked up only after this check, so a denied caller cannot probe which formats exist.
- Who holds `print` follows S1's rules (contract 0005): a role entry with `print: true` whose `if:` holds on the record, the `owner` role always, and models without `permissions:` open to signed-in members.

### 6. Reference layouts (rangka-os `templates/print/`)
Starting points that modules copy next to their `print/*.rk`:
- `quotation.html`: *Surat Penawaran Harga* with Nomor/Rev., Perihal, Kepada Yth., item table (No, Uraian, Qty, Satuan, Harga satuan, Diskon, Jumlah), subtotal, diskon, DPP (nilai lain per faktor), PPN (tarif dari record atau config), total, terbilang, syarat (masa berlaku, pembayaran, waktu pelaksanaan), Hormat kami + signatory.
- `sales_invoice.html`: *Faktur / Invoice* with no. faktur, tanggal, jatuh tempo, no. PO pelanggan, Kepada (NPWP), item table, subtotal, uang muka, DPP, PPN (only when `.Org.pkp`), total, PPh 23 note ("Bila pembayaran dipotong PPh Pasal 23, mohon kirimkan bukti potongnya kepada kami"), terbilang, bank accounts, note that the e-Faktur is issued separately.
- `purchase_order.html`: *Pesanan Pembelian* with vendor, kirim ke, item table, syarat pembayaran, tanggal kirim, disetujui oleh.
- `statement.html`: *Laporan Tagihan (Statement of Account)* on `customer`, using `related.invoices` (open invoices only, oldest first): per-invoice tagihan, dibayar, sisa, umur; aging summary (belum jatuh tempo, 1–30, 31–60, 61–90, > 90 hari); a note when `.Truncated.invoices`.
- The field names they use are listed in `templates/print/README.md` for S8 to match or adapt.

### 7. Validation (`rangka validate`, kernel checks)
`schema`, `name_mismatch`, `unknown_model` (model or related model), `unknown_field` (match fields), `template_missing`, `template_outside` (path escapes its folder), `template_error` (does not parse with the helpers), `logo_missing`.

### 8. Schema 0.5
`schema/0.5/print.json` and `schema/0.5/letterhead.json` are new files. The kernel embeds `schema/0.5/*.json` next to 0.4; a kind resolves to its 0.4 schema when one exists, else to 0.5. Other 0.5 files from other streams sit next to these.

## Consequences
- Any module gets printable documents by adding a `.rk` and an `.html`; firms override the look per site.
- PDF needs Chromium next to the binary (the release image should install `chromium`, S15); without it users print from the browser.
- `tools/rangka_validate.py` makes the same print and letterhead checks, except `template_error`: it has no Go template parser, so that check is kernel only and `compare-python`'s broken print input has no unparsable template.

## Open
- Per-document letterhead choice when one org has several brands (not needed for the pilots).
- QR code / e-Faktur fields on the invoice once the tax connector exists.
