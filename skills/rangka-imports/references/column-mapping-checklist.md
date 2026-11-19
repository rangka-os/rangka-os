# Column-mapping checklist

Go through it once per file, before the owner uploads. Each "ask" is a question for the owner, never a value you fill.

## A. Which import

- [ ] The model is clear (customers, items, stock, sales…). One file, one model. A sheet mixing customers and their
      invoices is two files.
- [ ] An import exists for it (`rangka_meta` → `imports`, or [imports-catalogue.md](imports-catalogue.md)). If not,
      stop and say so (SKILL.md section 1).
- [ ] Its app is installed. If not, the data waits for the app (`rangka-compose`).
- [ ] `per: outlet` import: one file per outlet, the outlet chosen at upload.

## B. Shape of the file

- [ ] CSV or XLSX, under 20 MB and 50 000 rows; unzipped.
- [ ] The data sits on the first sheet (or the import's `sheet`); other sheets are ignored.
- [ ] Exactly one header row on the import's `header_row`; report titles above it are fine, a second header is not.
- [ ] No merged cells, subtotal rows, "TOTAL" rows or notes below the data.
- [ ] One row per record (or per line, for imports with `group`/`lines`).

## C. Each column

| Column kind | Check | Accepted | Ask when |
| --- | --- | --- | --- |
| Name / text | trimmed, no two people in one cell | anything | "Toko Maju / Bu Rina" in one cell |
| Key (SKU, Nama pelanggan, Nomor karyawan) | filled on every row, unique in the file | text | a key repeats or is blank |
| Date | one format in the whole column | dd/mm/yyyy, d/m/yyyy, dd-mm-yyyy, dd.mm.yyyy, yy years, yyyy-mm-dd, Excel serial | month names ("27 Okt 2026"), month-first values, text like "awal Nov" |
| Rupiah | whole rupiah | 1250000, 1.250.000, 1,250,000, Rp 1.250.000, (15.000) | "1,25jt", "1.250rb", "-", "gratis", decimals on a currency |
| Quantity / decimal | unit is known | 12,5 or 12.5 | "1,250" (read as 1250), mixed units (pcs and dus) |
| Phone | one number per cell | as written; +62 form recommended | two numbers, letters, missing digits |
| NIK / NPWP / codes | stored as text | 16-digit NPWP, leading zeros kept | Excel shows 1,23E+15 or dropped a leading 0 |
| Yes/no | | Ya/Tidak, 1/0, true/false, y/n | "✓", "ada", "-" |
| Link (Satuan, Kategori, Pemasok, SKU in a document) | value exists as a record | exact name or code | the target is missing or spelled differently |
| Choice (Jenis, Metode) | value is one the import knows | the words in the import's comment | other words: the owner picks the closest |

## D. Against what already exists

- [ ] Customers, suppliers, items typed by hand earlier: `rangka_query` by name or SKU; the import does not match them.
- [ ] Units, categories, the supplier "Saldo awal" exist when the import links to them.
- [ ] A day of sales already imported: an overlapping file only adds new rows (keyed imports).

## E. Never in the file

- [ ] Passwords, PINs, logins, API keys, tokens: delete the column, do not quote it.
- [ ] Columns of personal data the import does not read (KTP photos, bank logins): leave them out.

## F. Before saying anything is done

- [ ] Small batch (10–20 rows) previewed with no errors, committed by the owner.
- [ ] `rangka_query` shows the records.
- [ ] Drafts (opening stock, opening balance, sales) checked and submitted by the owner.
