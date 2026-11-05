# 0012: Imports

Status: proposed (3 Nov 2026) · Owner: rangka-os · Stream S7 of the first-pilots program

## Context
The F&B pilot keeps its Moka cashier. Each outlet's daily sales reach Rangka as Moka's sales export, uploaded by
head office per outlet per day; opening data (customers, items, stock) for both pilots also arrives as
spreadsheets. v0 has no upload path except `.rk` files and no way to turn rows into records.

What the public Moka material says (help.mokapos.com, GoBiz developer portal; no real export was available):
Backoffice → Transactions offers "Export Transactions" (one row per receipt) and "Export Item Details" (one row
per item per receipt; the same item with the same variant and modifiers on one receipt is one row); the Sales
report's Item Sales gives quantity, gross sales, discounts, refunds and net sales per variant. Exact column
headers are not published, so the kernel knows nothing about Moka: an import `.rk` names the columns, and the
pilot fixes them when its first real export arrives.

The risk that matters (program plan, Review Focus): the same export uploaded twice, or two exports with
overlapping days, must not create sales or stock moves twice.

## Decision

### 1. Kind `import`
`imports/<name>.rk` in an app (later a module, contract 0001) or in the site. Schema `schema/0.5/import.json`.

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/import.json
name: moka_item_details
label: {id: Penjualan Moka (detail item)}
model: outlet_sales
source: csv                 # csv | xlsx: the default the wizard offers; both readers accept either file
header_row: 1               # 1-based row holding the column names; rows above it are ignored
sheet: Item Details         # xlsx only; default the first sheet
per: outlet                 # outlet | org (default org)
key: [Date, Receipt Number, Item, Variant, Modifiers]
group: [Date]               # rows with equal values become one record; without group, one row = one record
map:
  date: Date
  note: "='Moka ' + row['Date']"
lines:
  field: lines              # a `table` field of model
  map:
    item: {value: SKU, match: sku}
    qty: Quantity
    gross: Gross Sales
    discount: Discounts
    refund: Refunds
    net: Net Sales            # Moka's net already has discounts and refunds taken off
```

- **`map` values.** A plain string is a column header. A string starting with `=` is a CEL expression with `row`
  (the row as `{header: cell text}`) in scope; `int()`, `double()` and `string()` are available. An object
  `{value: <column or =CEL>, match: <field>}` sets a link field by finding the one target record whose `match`
  field equals the value (e.g. an item by SKU); without `match` a link value is the target's `name`.
- **`per: outlet`** requires the upload's `outlet` form value, which must name an `outlet` record of the caller's
  org; it is written to the model's `outlet` link field (the model must have one) and scopes duplicate detection.
  `per: org` scopes duplicates to the org.
- **`group`** collects rows into one parent: parent fields come from the group's first row; every row becomes a
  child line of `lines.field`. `lines` without `group` gives each record one line.

### 2. Reading files
- CSV: UTF-8 (BOM stripped), delimiter `,` or `;` detected from the header row, RFC 4180 quoting.
- XLSX: the named or first sheet, read with `github.com/xuri/excelize/v2` (the module proxy was reachable).
  Cells are read raw: numbers as numbers, dates as Excel serials, so Excel display formats never change values.
  A ZIP from Moka must be unzipped first.
- Limits: 20 MB per file, 50 000 data rows. Empty rows are skipped.
- **Cell conversion follows the target field's type.** Numbers accept `1250000`, `1.250.000`, `1,250,000`,
  `Rp 1.250.000`, `12,5` and `12.5`; `currency` fields must be whole rupiah. Dates accept `dd/mm/yyyy`,
  `d/m/yyyy`, `dd-mm-yyyy` (Moka exports), `dd.mm.yyyy`, the same with a two-digit year (`31-12-23`, Excel's window:
  `00`-`29` is 20xx, `30`-`99` is 19xx), `yyyy-mm-dd` and Excel serials (day first: Indonesian exports). Times accept
  `HH:MM[:SS]` and `HH.MM[.SS]`. Check fields accept
  `1/0/ya/tidak/true/false`. Money is never parsed as float: integers and decimal strings only.

### 3. REST (`/api/v1`, same error shape as today)
- `POST /imports/{import}` (multipart `file`, form `outlet` when `per: outlet`) reads, maps and checks every row,
  then runs the creates as a dry run in a transaction that is rolled back (so model rules and required fields are
  reported now, not at commit), stores the upload as an import log entry, and returns
  `{id, import, rows, preview[], errors[], skipped, duplicate}`:
  - `rows`: data rows read; `preview`: the first 20 records as they would be created (parent with lines), keys in
    the model's field order (lines in the child model's);
  - `errors`: `[{row, column, message}]`, `row` the spreadsheet row number (header row included, so it matches
    Excel), `message` in Bahasa Indonesia, e.g. `Kolom "Quantity" harus angka, isinya "dua"`,
    `Item dengan sku "TH-XX" tidak ditemukan`, `Kolom "Gross Sales" tidak ada di file`;
  - `skipped`: rows whose key was already imported (§4);
  - `duplicate: true` when this file (SHA-256) was already committed for the same import and scope, or when every
    row was already imported. Its message is "sudah diimpor".
- `POST /imports/{import}/{id}/commit` creates every record in **one transaction** through the engine (rules,
  naming, computed fields, automations, audit, events as for any create), skipping already-imported rows. Refused
  with 422 `import_errors` while the preview has errors, 409 `already_imported` when `duplicate` is true or the
  entry is already committed. Returns the entry with `records` (the names created).
- `GET /meta` lists the imports of installed apps and the site under `imports: {<name>: {name, model, label, source,
  per, app}}` (site imports have `app: ""`), so the web app can offer an import wizard.
- `GET /imports/{import}/{id}` returns the entry. `GET /imports/{import}` returns `{data, total}` (as `/r/{model}`
  and `/approvals` do): `data` holds the org's 50 most recent entries without `preview`, `total` counts all of them. For a member
  with a row scope (contract 0010) both leave out entries it may not read, so `total` counts only visible entries.
- A file with no data rows previews with `rows: 0`; its commit is refused with 422 `import_empty`.
- An upload over 20 MB gets 413 `too_large`. A commit whose rows were imported by another commit after its preview
  gets 409 `already_imported` ("Sebagian baris sudah diimpor, unggah ulang file").
- In `errors`, `column` is the file's column header when the failing field is mapped from a column, else the field
  name.
- Commit needs a person signed in (a session); tokens (AI and CLI) may preview but get 403 `permission_denied` on
  commit (data that moves money and stock is applied by people). Every import endpoint needs the model permission `import`
  on `model` (contract 0005 §4), and each created record is checked for `create` (403 `permission_denied`).
  `GET /meta` lists only imports the caller may run (`import` and `create`).

### 4. Duplicates
Two layers, both scoped by org, import name and (for `per: outlet`) outlet:
1. **File:** the SHA-256 of the uploaded bytes. The same file committed twice → `duplicate: true`, commit 409.
2. **Rows:** the values of `key` columns are hashed per row (joined with U+001F; a key column mapped to a typed field
  is hashed as converted, e.g. a date as `2026-11-02`, so the CSV and XLSX copies of one export match; other columns
  as trimmed text); committed keys are kept
  in `rk_import_keys` with a unique index. A later upload (an overlapping export, a re-export of the same day)
  skips rows whose key exists and creates only the new ones. Two rows with the same key in one file are an error.
  The unique index makes concurrent commits of overlapping files safe: the second transaction fails and nothing
  from it is written.

`per: outlet` imports should set `key` (the validator warns otherwise); without `key`, only the file check applies.
An overlapping export for a day already imported creates a second record for that day with only the new rows, so
reports sum per date.

### 5. Import log
`rk_imports (id, org_id, import, scope, file_name, file_sha, status previewed|committed, rows, skipped, errors
jsonb, preview jsonb, records jsonb, created_by, created_at, committed_at)` plus the parsed rows (jsonb) so commit
does not need the file again. Previewed entries older than 7 days may be purged; committed entries stay.

### 6. Validator (Go and Python, identical reports)
Schema check, `name_mismatch`, `unknown_model` (with suggestion), `lines.field` must be a `table` field,
`map` keys must be fields of the model (or of the child model for `lines.map`) (`unknown_field`), `match` must be
a field of the link target, `per: outlet` needs an `outlet` link field (`import_per_outlet`), CEL parentheses.
Columns cannot be checked without a file.

## Schema
New kind, so it lives in `schema/0.5/import.json`; the kernel and the Python validator embed `schema/0.5/*.json`
next to 0.4 and look a schema up by kind (0.4 first, then 0.5). Only the `$schema` kind is read, as before.

## Not in this contract
Moka API, ZIP uploads, updating existing records from a file (imports only create), submitting in the import
transaction (records arrive as drafts; head office submits them, which is when stock moves), scheduled pulls, MCP
tools for imports (later; preview only, never commit).

## Consequences
Any app can ship a spreadsheet import as metadata; Moka is one `.rk` file, not kernel code. Re-uploads and
overlapping exports are safe by construction. S0 builds the import wizard on these endpoints; S8 ships the Moka
and daily-form imports; S9 uses imports for opening data.
