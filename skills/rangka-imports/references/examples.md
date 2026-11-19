# Three common sources, worked

Made-up businesses and data. Each shows the owner's file, the profile, the mapping and what Raka says.

## 1. A messy customer sheet (Toko Bangunan Sinar, Excel)

Owner's sheet `Pelanggan` (first rows):

| No | Nama Toko | Pemilik | No HP | Kota | Limit | NPWP | Ket |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | TB Maju Jaya | Pak Anton | 0812-3456-7890 | Bekasi | 5jt | 1,23457E+15 | langganan |
| 2 | tb maju jaya | | 812 3456 7890 | bekasi | | | |
| 3 | CV Karya Abadi | Bu Sri | 0813xxxx / 0857xxxx | Depok | 10.000.000 | 0012345678901234 | tempo 30 |
| … | | | | | | | |
| 141 | TOTAL | | | | | | |

Profile:

- Header on row 1, 140 data rows plus a TOTAL row (delete it).
- "TB Maju Jaya" and "tb maju jaya" (rows 2 and 3 in Excel) are the same shop by phone: the key `Nama pelanggan`
  would treat them as two customers. Ask which name to keep.
- No HP: three forms (0812-…, 812 …, two numbers in one cell). Recommend +62 form, one number per cell.
- NPWP: Excel shows 1,23457E+15, the digits are lost. Format the column as Text and type again.
- Limit: "5jt". `customer_list` does not read a credit limit anyway; it is set per customer by the sales or finance
  manager.
- Pemilik, Ket ("tempo 30"): not read by the import. "tempo 30" belongs in `Tempo (hari)`: ask whether it applies to
  all customers or only this one.

Mapping to `customer_list`:

| Owner column | Import column | Note |
| --- | --- | --- |
| Nama Toko | Nama pelanggan | key; resolve the repeats first |
| No HP | WhatsApp | one number, +62… |
| Kota | Kota | |
| NPWP | NPWP | text, 16 digits, may be empty |
| (Ket "tempo 30") | Tempo (hari) | only where the owner confirms |
| No, Pemilik, Limit | not imported | Pemilik can become a company field if the owner wants it, filled by hand |

Raka says:

> Aku cek daftarnya: 140 pelanggan. Sebelum diunggah, ada empat hal: hapus baris TOTAL di bawah; "TB Maju Jaya"
> muncul dua kali (baris 2 dan 3), mau pakai nama yang mana?; kolom NPWP berubah jadi 1,23457E+15 di Excel, ubah
> formatnya jadi Teks lalu ketik ulang; nomor HP tulis satu nomor per sel, sebaiknya +62812…. Lalu ganti judul kolom
> "Nama Toko" jadi "Nama pelanggan" dan "No HP" jadi "WhatsApp". Coba unggah 20 baris dulu di Impor data, nanti
> kirim ke aku kalau ada baris yang ditolak.

## 2. An item and price sheet (Kedai Kopi Lereng, exported from an old POS)

Owner's file `produk.csv`:

```
Kode,Produk,Kategori,Unit,Harga,HPP,Stok
KP-01,Kopi susu gula aren,Minuman,cup,"Rp 22.000",8.500,
KP-02,Americano,Minuman,Cup,18rb,6000,
BB-01,Susu UHT 1L,Bahan,liter,"19.500","17.250",24
BB-02,Gula aren cair,Bahan,btl,,32000,"1,5"
```

Profile:

- `Harga` "18rb" (row 3): fails; ask for 18000. `Harga` blank on BB-02: it is a material, not sold; fine.
- Units "cup", "Cup", "liter", "btl": items link `Satuan` by unit name, so the units must exist and be spelled the
  same. Check with `rangka_query {model: "uom"}`; the owner creates missing units first.
- `Kategori` "Bahan" vs "Minuman": `item_list` has no category column but reads `Jenis`; `item_master` reads
  `Kategori` by name (categories must exist). Pick one import; ask which matters to the owner.
- `Stok` is not an item field. It goes in a separate opening stock file (`opening_stock`), with a date, per outlet,
  after the items exist. "1,5" btl is 1.5: confirm the unit.

Mapping to `item_list` (Jenis derived by the owner, one word per row):

| Owner column | Import column |
| --- | --- |
| Kode | SKU (key) |
| Produk | Nama barang |
| Kategori → Bahan / Minuman | Jenis → Bahan baku / Produk jadi |
| Unit | Satuan |
| Harga | Harga jual |
| HPP | Harga pokok |
| Stok | not here: `opening_stock` file (Tanggal, SKU, Qty, Harga pokok) |

Order: units → items → opening stock (draft goods receipt from "Saldo awal", owner submits).

Raka says:

> Daftar barangnya bisa langsung dipakai, dengan tiga perbaikan: harga Americano ditulis "18rb", tulis 18000;
> satuan "cup" dan "Cup" dianggap beda, samakan jadi satu dan pastikan satuannya sudah ada; kolom Stok jangan ikut di
> file ini. Stok awal masuk lewat file terpisah per outlet, dengan tanggal saldo awal. Mau pakai tanggal berapa?

## 3. A POS export (majoo daily sales, F&B with two outlets)

Owner's file: majoo "Penjualan per produk" export for both outlets, one week, headers in Indonesian:

```
Tanggal,Outlet,No. Transaksi,Produk,SKU,Jumlah,Penjualan Kotor,Diskon,Refund,Penjualan Bersih
03/11/2026,Lereng - Dago,TRX-0001,Kopi susu gula aren,KP-01,2,"44.000",0,0,"44.000"
03/11/2026,Lereng - Braga,TRX-0002,Americano,KP-02,1,"18.000","2.000",0,"16.000"
```

Profile:

- Two outlets in one file: `pos_sales_file` is per outlet. Split the file by Outlet, one upload per outlet.
- The outlet's Sumber penjualan must be "File POS", or the day is counted from two sources.
- Every SKU must exist as an item (link by SKU). Unknown SKUs show as row errors; import the items first.
- Refunds: `pos_sales_file` expects a return as its own row with Jenis "Retur", negative Qty, Penjualan kotor 0 and
  the value in Retur. Ask how majoo writes a refund before mapping it; do not guess.
- Penjualan Bersih is not read (the kernel computes net from gross, discount and refund).
- Dates dd/mm/yyyy: fine.

Mapping to `pos_sales_file`:

| majoo column | Import column |
| --- | --- |
| Tanggal | Tanggal (group: one draft per day) |
| No. Transaksi | No struk |
| (add) | Jenis = Jual or Retur |
| SKU | SKU |
| Jumlah | Qty |
| Penjualan Kotor | Penjualan kotor |
| Diskon | Diskon |
| Refund | Retur |
| Outlet, Produk, Penjualan Bersih | not imported (Outlet is chosen at upload) |

Re-uploads: the key is Tanggal, No struk, Jenis, SKU. The same file twice is refused; next week's export that
overlaps one day adds only the new rows. Each day lands as a draft; head office checks and submits it, which is when
stock moves.

Raka says:

> File majoo ini bisa masuk lewat template penjualan dari file POS, tapi harus dipisah per outlet: satu file Dago,
> satu file Braga. Tambahkan kolom "Jenis" isi "Jual", dan ganti judul kolom sesuai template (No. Transaksi jadi
> "No struk", Jumlah jadi "Qty", Refund jadi "Retur"). Satu hal yang aku perlu tahu: kalau ada refund, majoo
> menulisnya sebagai baris sendiri atau dikurangi di baris yang sama? Setelah diunggah, tiap hari jadi draf
> penjualan; stok baru berkurang setelah kamu ajukan drafnya.
