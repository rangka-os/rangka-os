# 0029 XML file formats

Status: proposed (6 Nov 2026) · Owner of the change: rangka-os (kernel, schema 0.5, web app) · Affects: rangka-apps (modules may ship XML formats, first user: `tax_id` Coretax export)

## Context

Indonesian firms report tax through Coretax DJP (2025+), which imports XML only: e-Faktur keluaran as
`TaxInvoiceBulk`, unified withholding slips (BPPU) as `BpuBulk`, one file per tax period. Juragan already holds every
number those files need (invoices, PPN, PPh 23), but contract 0008 only renders HTML and PDF, and contract 0024 only
writes CSV. Owners retype hundreds of invoices into Excel templates and run DJP's Windows converter.

The tax rules (which invoices, which transaction code, how a line's DPP is split) belong to the apps. The kernel only
needs a safe way to turn a document and its related lists into a well-formed XML file.

## Decision

1. **`output: xml` on a print format** (contract 0008 §1). `template` then names a `.xml` file; `paper` is ignored.
   The format is listed in `/api/v1/meta` `prints` with `output: "xml"` (HTML formats say `output: "html"`), so the
   web app offers it as a download, not in the Cetak menu.
2. **Rendering.** Go `text/template` with the print helpers of contract 0008 §3 plus `add`, `mul`, `div`
   (exact rationals, `div` by zero is 0), `round x n` (half away from zero to n decimals, default 0), `dec x n` (a
   plain number with `.` as decimal point, at most n decimals, trailing zeros dropped: `2.5`, `15000`), `digits`
   (only 0-9 of a value) and `isodate` (`YYYY-MM-DD`, "" when empty). In an XML format the number helpers (`add`,
   `sub`, `mul`, `div`, `round`, `dec`) fail the render (`500`, nothing sent, the value in the server log) on an operand
   that is set but is not a number; an empty or missing operand still counts as 0 (`dec` of it is ""). HTML formats
   keep the lenient helpers. Every action that writes output is escaped as
   XML text (`&`, `<`, `>`, `'`, `"`; characters XML forbids become U+FFFD): the kernel appends the escaper to each
   output action, the way `html/template` does, so a template cannot emit markup from data. Data is the print data
   (`.Doc`, `.Links`, `.Related`, `.Org`, `.Tax`, `.Printed`, ...), related rows filled like `.Doc` (every field
   present, missing ones `""`).
3. **Well-formed or nothing.** The result must parse as XML (`encoding/xml`) with exactly one root element and no
   `<!DOCTYPE>` or other directive; otherwise `500 xml_invalid` (detail in the server log) and nothing is sent.
4. **Related lists** (contract 0008 §1) gain, for any format:
   - `range: {<related field>: [<doc field>, <doc field>]}`: rows whose field lies between the two document values,
     inclusive, compared as text (dates are stored `YYYY-MM-DD`). A list needs `match`, `range` or both; an empty
     bound matches no row. A tax period selects its month's invoices with `range: {posting_date: [period_start, period_end]}`.
   - `children: true`: rows come with their child tables (`.Related.invoices` rows have `items`).
   - `require: [{check: <CEL>, message: {id: ...}}]`: every row of the list must satisfy each check, evaluated on the
     row like a list filter, with the printed document as `doc` (a check that errors or is not `true` fails). Any failure: `422 export_invalid` with
     `problems: [{list, name, message}]` (at most 100, in list order), nothing rendered and no audit entry.
   - For an XML format the rows are those the caller may export (contract 0024 grants), and a list cut at its `limit` is refused with `422 export_too_large` (a tax file must hold every
     row); HTML formats keep `.Truncated`. A list that lost rows to the caller's row scope or an `if:` of their read
     or export grant is refused with `422 export_partial` (the count of missing rows, nothing of them), so a partial
     file never looks complete.
   - In `require`, a field of the related model named `doc` wins over the printed document; `rangka validate` warns
     (`require_doc_shadowed`).
5. **Endpoint.** The same `GET /api/v1/r/{model}/{name}/print?format=<xml format>`; `as` is omitted or `xml` (`html`
   or `pdf` on an XML format, or `xml` on an HTML format: `400 bad_request`). The caller needs the model's `print`
   and `export` grants (a tax file is an export). Response: `application/xml; charset=utf-8`,
   `Content-Disposition: attachment; filename="<format>-<record name>.xml"` (`/` becomes `-`), `Cache-Control:
   no-store`. A `HEAD` checks grants and `require` but renders and audits nothing. Each download writes the audit
   entry `record.print` with `as: xml`.
6. **Validation** (`rangka validate`, `tools/rangka_validate.py` via the schema): an XML format's template parses as a
   `text/template` with the helpers above; `require` checks compile as CEL.
7. **Web app (follow-up change).** XML formats get an "Unduh <label>" button next to Cetak; a `422` shows its
   problems as a list the owner can act on. The button lands after its design is reviewed.

## Not in this change

- XSD validation: DJP publishes sample XML and Excel templates, not schemas; apps test their files against the samples.
- Other file types (CSV per document, JSON); signed or encrypted files.
- A lock that stops a period's documents from changing after its file was downloaded (apps can model it).
