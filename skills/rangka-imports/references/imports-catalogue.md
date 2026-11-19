# Imports that ship with the modules

Read from rangka-apps `modules/*/imports/*.rk` (main, 19 Nov 2026). The live list is `rangka_meta` → `imports`
(only imports the caller may run, of installed apps); read one with `rangka_get_file {path: "<path below>"}`. When
the two differ, the live one wins. Column names are the import's `map`, `key` and `group` values, exactly.

## Master data

| Import (path) | Model | Columns | Key | Needs first |
| --- | --- | --- | --- | --- |
| `customer_list` (`modules/contacts/imports/customer_list.rk`) | customer | Nama pelanggan, Kota, WhatsApp, Email, Tempo (hari), NPWP | Nama pelanggan | nothing; customers typed by hand are not matched |
| `vendor_list` (`modules/contacts/imports/vendor_list.rk`) | vendor | Nama pemasok, Kota, WhatsApp, Tempo (hari), NPWP | Nama pemasok | nothing |
| `item_list` (`modules/catalogue/imports/item_list.rk`) | item | Nama barang, SKU, Jenis (Bahan baku, Produk jadi, Jasa, else Barang), Satuan, Harga jual, Harga pokok | SKU | units (Satuan) by name |
| `item_master` (`modules/catalogue/imports/item_master.rk`) | item | SKU, Barcode, Nama barang, Kategori, Grup varian, Varian, Satuan, Harga jual, Harga pokok, Penitip, Harga setor | SKU | units, categories, consignors (suppliers) by name |
| `worker_list` (`modules/workforce/imports/worker_list.rk`) | worker | Nama, Nomor karyawan, Jabatan, WhatsApp, Tarif harian; optional Upah harian, Tarif lembur per jam, Upah lembur per jam, Email karyawan | Nama, Nomor karyawan | nothing; Email karyawan links an existing employee |
| `payroll_profile_list` (`modules/payroll/imports/payroll_profile_list.rk`) | payroll_profile | see the file | Nomor karyawan | `worker_list` first; NIK and bank account are personal data |

## Opening data

| Import | Model | Columns | Key | Notes |
| --- | --- | --- | --- | --- |
| `opening_stock` (`modules/procurement/imports/opening_stock.rk`) | goods_receipt (draft, per outlet) | Tanggal, SKU, Qty, Harga pokok | SKU per outlet; grouped by Tanggal | supplier "Saldo awal" must exist; submit posts the stock at that cost |
| `opening_balance_sheet` (`modules/accounting/imports/opening_balance_sheet.rk`) | opening_balance (draft) | Tanggal, Kas, Bank, Piutang, Persediaan, Aset tetap, Akumulasi penyusutan, Utang usaha, Utang bank, Utang lain, Modal | Tanggal | totals only; submit posts the journal |

## Daily and periodic data

| Import | Model | Columns | Notes |
| --- | --- | --- | --- |
| `moka_item_details` | outlet_sales (per outlet) | Moka "Export Item Details" English headers (Date, Time, Receipt Number, Items, Variant, Modifier Applied, SKU, Quantity, Gross Sales, Discounts, Refunds, Outlet, …) | one outlet per file; the Outlet column must equal the outlet's "Nama outlet di Moka"; dates dd-mm-yyyy |
| `loyverse_receipts` | outlet_sales (per outlet) | Loyverse "Receipts by item": Date, Receipt number, Receipt type, SKU, Variant, Quantity, Gross sales, Discounts, Net sales | headers not yet checked against a real export; Date without time |
| `pos_sales_file` | outlet_sales (per outlet) | Tanggal, No struk, Jenis (Jual/Retur), SKU, Qty, Penjualan kotor, Diskon, Retur | Juragan's template for any other POS (majoo, Pawoon, Kasir Pintar, Olsera); outlet "Sumber penjualan" = File POS |
| `daily_form` | outlet_sales (per outlet) | Tanggal, SKU, Jumlah, Penjualan kotor, Diskon | no key: only the same file is refused |
| `takings_file` | outlet_takings (per outlet) | Tanggal, Metode, Menurut POS, Disetor, Potongan, Referensi | Metode: Tunai, QRIS, Kartu debit, Kartu kredit, Transfer bank, Dompet digital, Lainnya |
| `bank_statement_csv` | bank_transaction | Rekening, Tanggal, Urutan, Keterangan, Masuk, Keluar, Saldo | the whole row is the key |
| `attendance_recap` | timesheet | Proyek, No. pegawai, Dari tanggal, Sampai tanggal, Hari hadir, Jam lembur | workers by employee number |
| `billing_milestone` | billing_milestone | Proyek, Urutan, Judul, Jenis, Nominal, Jatuh tempo, Periode dari, Periode sampai | |

## No import (say so, offer what exists)

- Unpaid invoices or bills per customer or supplier from before Rangka: the totals go in Saldo awal.
- Purchase history, old quotations, old orders.
- Users, roles, passwords: people are invited (Tim); never from a file.
- Updates to existing records (new prices for existing SKUs): imports only create. Prices are changed on the item.
- Accurate, Jurnal, majoo, Tokopedia, Shopee exports in their own format: map them onto the templates above.
