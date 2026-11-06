# 0024 List and dashboard export

Status: proposed (6 Nov 2026) · Owner of the change: rangka-os (kernel, web app) · Affects: nothing in rangka-apps

## Context

Owners keep asking for "the list in Excel": receivables to send to an accountant, sales per outlet for a bank, stock
for an audit. Documents already print to PDF (`GET /api/v1/r/{model}/{name}/print?as=pdf`, contract 0008), but a
list or a dashboard has no download. The `export` permission action has existed since contract 0005 and is granted in
rangka-apps (49 grants), yet no endpoint checks it.

## Decision

1. **Endpoints.**
   - `GET /api/v1/export/r/{model}?q=&filter=&sort=&columns=&sep=` the rows `GET /api/v1/r/{model}` matches for the
     same `q`, `filter` and `sort`, without paging, at most 50 000 rows (`X-Export-Truncated: <n>` when cut).
   - `GET /api/v1/export/dashboards/{name}?period=&sep=` every widget of the dashboard for the period.
2. **Permission.** Both need the model's `export` grant on top of `read`; a conditional grant (`if:`) is evaluated
   per row, before the 50 000 cut. A dashboard widget counts rows under `read`, so it is exported only when the
   caller may export every record of its model (an export grant without `if:`); otherwise the block is one line
   saying so in the owner's words, naming the data by its model label: with a conditional export grant it points
   at the list's download (the rows the caller may export), with none it says the caller may not download that data. Child models are refused (`400`); their rows come with the parent's print.
3. **File.** `text/csv; charset=utf-8`, `Content-Disposition: attachment; filename="<model or dashboard>-<today>.csv"`,
   `Cache-Control: no-store`, UTF-8 byte-order mark first (Excel reads UTF-8 only with it). `sep=semicolon` writes `;`
   for an Excel whose list separator is `;` (Windows with Indonesian regional settings); the default is `,`.
4. **Columns.** `columns=a,b,c` (what the user chose under Kolom); unknown names and table fields are skipped.
   Without it: `name` ("Nomor"), the `in_list` fields in declaration order (every scalar field when none is marked),
   then the computed `status`.
5. **Cells.** Numbers plain (`12500000`, so a spreadsheet can sum them), select values as their label, yes/no as
   `Ya`/`Tidak`, dates ISO, a link as the linked record's title, the same one the list shows
   (contract 0018 §3): its code when the record is missing, outside the caller's row scope or not readable, and a
   title field that is itself a link is followed only into a record the caller could read (else that hop's code), the
   computed status in the list's words (Draf, Diajukan, Dibayar sebagian, Jatuh tempo, Lunas, Dibatalkan). A text cell that starts with `=`, `+`, `-`, `@`, tab or CR and is not a number gets a
   leading `'`, so a spreadsheet shows it instead of running it (CSV injection).
6. **Dashboard layout.** The dashboard label and period, then one block per widget separated by an empty line:
   the widget label, then `Nilai` (and `Periode sebelumnya`, `Jumlah dokumen`) for a number widget, `Label, Nilai,
   Jumlah` per point for a chart, the widget's columns for a list.
7. **Web app (follow-up change).** The list toolbar gets "Unduh" (Excel with `;`, or CSV with `,`) carrying the search, quick filters,
   sort and visible columns; dashboards get an "Unduh" button. Neither is shown in a live preview, and the list's
   only when `/meta` says `can.export`. The endpoints land first; the buttons follow once their design is reviewed.

## Not in this change

- `.xlsx`: CSV opens in Excel and Google Sheets; a native workbook (formats, several sheets) can follow.
- An audit entry per export, and a row limit per plan.
- Telling the user in the web app that a file was cut at 50 000 rows (the header is there; a direct `<a download>`
  cannot read it).
- Phone numbers and NPWP keep a leading `0`/`+` in the file, but Excel reads them as numbers; open the file with
  Data → From Text to keep them as text.
