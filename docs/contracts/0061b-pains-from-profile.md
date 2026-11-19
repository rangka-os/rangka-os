# 0061b The "Kesulitan usaha" card follows the business profile

Status: proposed (19 Nov 2026; rules accepted by Dede the same day) · Amends: 0061 §1 (the options), §2 (stored picks),
§4 (the request field), §5 (the card); withdraws 0061 §6 ("Langsung mulai") · Builds on: 0050, 0054, 0054a, 0061 ·
Affects: rangka-ai (`pain_picks` validation and prompt), rangka-cloud (option catalogue and selection, studio card;
private) · Owner of the change: rangka-os (this contract) · Asked by: Dede, 19 Nov 2026

## Context

- **Dede, 19 Nov 2026:** the pain card is too predefined. It does not follow the answers the owner gave on the profile
  card just before it. An owner with no staff, one place, or only online sales gets the same fifteen options as
  everyone; a rental business sees nothing about rental.
- **What exists.** Cloud sends one fixed list (0061 §1, `pain_options`) with every project. rangka-ai keeps a copy of
  the codes and drops a code it does not know. The domain packs (skill `rangka-consult`, `references/domains/`) list
  the common pains of each business type, most of them without a 0061 code.
- **"Langsung mulai" is gone.** rangka-cloud #166 (17 Nov 2026) removed the quick start: the owner talks with Raka
  before anything is built, and the card has one button, "Lanjut ngobrol dengan Raka". This amendment brings the
  contract in line.

## Decision

### 1. One catalogue, a card per profile

- The options are one **catalogue**: the four core groups of 0061 §1 with their codes unchanged, and one **business
  group** per `industry` code of 0054 §1 (§3). Changing an option is still a change to this contract.
- Cloud selects the card from the catalogue on every read of the project, from the profile's **confirmed** answers
  (0054 §2), in code, with no LLM. A field that is not confirmed filters nothing. `industry: other` counts as no
  industry.
- `pain_options` keeps its shape (`[{module, label, options: [{code, label, pain, modules, custom?}]}]`), now per
  project.
- **A pick is never hidden.** An option the owner picked is shown even when the rules of §2 would hide it, in its
  group, at its catalogue place; a hidden group comes back with only its picked options. A profile change never drops
  a pick without the owner seeing it.
- **Size.** A card has at most five groups (the business group and the four core groups) and fifteen options. When
  the rules leave more than fifteen, Cloud drops options from the end of the card until fifteen are left: the last
  unpicked option of the last group with more than three options, then of the last group with more than two. For
  example a wholesale card loses "Titip jual" and "Kasbon". Picked options are never dropped (they may take a card
  past fifteen).

### 2. The rules

Terms, from confirmed answers:

| Term | Means |
| --- | --- |
| *goods* | `industry` is `fnb`, `retail`, `wholesale`, `production`, or no industry |
| *service* | `industry` is `outsourcing`, `rental`, `training` or `subscription` |
| *solo* | `staff` is `solo` |
| *one place* | `scale` is `starting` or `one_location`, or `outlets` is 1 |
| *channel X* | `sales_channels` is not confirmed, or contains X |

| Option | Shown when | Wording |
| --- | --- | --- |
| Karyawan: `staff_records`, `staff_pay`, `staff_advance` | not *solo* | |
| `staff_hiring` | not *solo*, and `staff` is not `to_5` or `industry` is `outsourcing` | |
| `sales_orders` | `industry` is not `outsourcing`, `training`, `subscription`; and `sales_channels` is not confirmed as only `offline_outlet` | |
| `sales_daily` | `industry` is `fnb`, `retail` or no industry, and *channel* `offline_outlet` | *one place*: "Rekap penjualan dan setoran harian masih manual"; else "Rekap penjualan dan setoran tiap cabang masih manual" |
| `sales_invoices`, `sales_overdue` | `industry` is not `fnb`, or *channel* `offline_sales` or `offline_visit` | |
| `product_prices` | always | *service*: label "Daftar harga", pain "Daftar harga layanan tersebar dan beda-beda" |
| `product_stock` | *goods* | |
| `product_transfer` | *goods*, and not *one place* | |
| `product_recipe` | `industry` is `fnb` or no industry | |
| `customer_records` | always | |
| `customer_followup` | `industry` is not `fnb` or `retail` | |
| `customer_credit` | `industry` is `wholesale`, `production` or no industry | |

- The Produk group is labelled "Produk & layanan" for *service*.
- The business group of the profile's `industry` comes first. The other groups follow in this order, a group with no
  option left is not shown:

  | `industry` | Order after the business group |
  | --- | --- |
  | `outsourcing` | Karyawan, Penjualan, Pelanggan, Produk |
  | `fnb`, `retail`, `production` | Produk, Penjualan, Karyawan, Pelanggan |
  | `rental`, `training`, `subscription` | Penjualan, Pelanggan, Karyawan, Produk |
  | `wholesale` | Penjualan, Produk, Pelanggan, Karyawan |
  | no industry | Karyawan, Penjualan, Produk, Pelanggan (0061 §1) |

- Business groups of another industry come back only with their picked options, after the core groups.
- The `sales_daily` pain text changes from "tiap outlet" to "tiap cabang" (0054a §5 "cabang" in owner copy).

### 3. The business groups

The group's `module` is the `industry` code. Pains come from the domain packs; retail, wholesale and production have no
pack yet and their groups are a first draft.

| Group | Code | Chip | Pain on the map | Answered by | Shown when |
| --- | --- | --- | --- | --- | --- |
| `rental` Sewa alat | `rental_schedule` | Jadwal alat bentrok | Alat dobel dibooking di tanggal yang sama | `rental` | |
| | `rental_returns` | Alat belum kembali | Lupa alat mana yang belum kembali | `rental` | |
| | `rental_condition` | Kondisi alat | Ribut soal kerusakan karena tidak ada bukti kondisi awal | `rental` | |
| | `rental_deposit` | Deposit dan denda | Deposit dan denda dihitung manual | `rental` | |
| `training` Kelas & siswa | `training_quota` | Kuota kelas | Kelas kepenuhan atau kosong, tidak kelihatan | `courses` | |
| | `training_attendance` | Absen kelas | Absen kelas masih di kertas | `courses` | |
| | `training_teacher_pay` | Honor pengajar | Rekap honor pengajar per pertemuan masih manual | custom (below) | |
| `subscription` Langganan | `sub_status` | Status nunggak dan isolir | Pelanggan nunggak dan isolir tidak tercatat | `subscriptions` | |
| | `sub_collector` | Setoran penagih | Setoran penagih tidak cocok | `subscriptions` | *channel* `offline_visit` |
| | `sub_transfer` | Cocokkan transfer | Cocokin transfer ke pelanggan makan waktu | `bank_statement` | |
| | `sub_churn` | Pelanggan berhenti | Tidak tahu berapa pelanggan yang berhenti tiap bulan | `subscriptions` | |
| `outsourcing` Penempatan & klien | `os_site_attendance` | Absen di lokasi | Absen di lokasi bisa dititipkan | `projects` | |
| | `os_client_margin` | Untung per klien | Tidak tahu untung per klien | `projects` | |
| | `os_contract_end` | Kontrak karyawan habis | Kontrak kerja karyawan habis tanpa ketahuan | custom (below) | |
| `fnb` Outlet & dapur | `fnb_attendance` | Absen karyawan | Karyawan titip absen | `attendance` | not *solo* |
| | `fnb_cash` | Selisih kas | Uang di kasir sering selisih saat tutup | `pos` | |
| | `fnb_waste` | Bahan terbuang | Bahan rusak atau terbuang tidak dicatat | `stock` | |
| | `fnb_delivery` | Pesanan ojol | Pesanan ojol tidak masuk ke laporan | `outlet_sales` | *channel* `online_social`, `online_marketplace` or `online_website` |
| `retail` Toko | `retail_cash` | Selisih kas | Uang di kasir sering selisih saat tutup | `pos` | |
| | `retail_promo` | Promo dan diskon | Promo dan diskon dihitung manual di kasir | `promotions` | |
| | `retail_reorder` | Stok habis telat dipesan | Barang laris sering habis karena telat dipesan | `stock` | |
| `wholesale` Grosir | `ws_price_tiers` | Harga bertingkat | Harga grosir bertingkat dihitung manual | `catalogue` | |
| | `ws_delivery` | Pengiriman barang | Pengiriman ke pelanggan tidak tercatat rapi | `delivery` | |
| | `ws_giro` | Giro mundur | Giro mundur dari pelanggan susah dilacak | `distribution` | |
| | `ws_consign` | Titip jual | Barang titip jual dan setorannya tidak tercatat | `consignment` | |
| `production` Produksi | `prod_plan` | Rencana produksi | Rencana produksi masih di kepala atau di kertas | `production` | |
| | `prod_cost` | HPP produk | HPP per produk tidak ketahuan | `production` | |
| | `prod_material` | Pemakaian bahan | Pemakaian bahan produksi tidak tercatat | `production` | |

- **Custom designs** (like Rekrutmen in 0061 §1; Raka designs the piece from the owner's flow, 0033, 0051):
  - `training_teacher_pay`: records "Honor pengajar"; steps "Hitung pertemuan tiap pengajar dari absen kelas", "Buat
    rekap honor tiap bulan".
  - `os_contract_end`: records "Kontrak kerja"; steps "Catat tanggal akhir kontrak tiap karyawan", "Ingatkan sebelum
    kontrak habis".
- The domain packs' "0061 code" column gains these codes (a skill change in rangka-os).

### 4. Stored picks (amends 0061 §2)

- A group is any catalogue group; a code is any option of that group, shown on the card or not.
- At most 15 picks and 5 `other` texts in total (the 0050 limit of 20 pains); more is `bad_pains`.
- Picks are stored in catalogue order: groups as in §3 after the four core groups, codes in option order.

### 5. The request (amends 0061 §4)

| Field | Rule |
| --- | --- |
| `pain_picks` | as 0061 §4, each entry with one more field `group`: the group label as shown on the card |

- rangka-ai no longer keeps a copy of the codes. It accepts an entry whose `module` and `code` are ids
  (`^[a-z][a-z0-9_]{0,39}$`) and keeps `label`, `pain` and `group` as text (trimmed, at most 300 characters). An entry
  that is not an id, a repeat, or an `other` without text is left out. At most 20 entries, in the order Cloud sent.
- The prompt names the group by `group`, or by the 0061 core label when `group` is absent.
- **Order.** rangka-ai ships first: an older rangka-ai drops the business-group codes from the prompt (their pains are
  still on the map). An older Cloud sends no `group`.

### 6. The card (amends 0061 §5)

- The sub line reads "Pilih yang paling repot sekarang, boleh lebih dari satu. Pilihannya mengikuti profil usahamu."
- One button, "Lanjut ngobrol dengan Raka", which saves first (rangka-cloud #166). No "Isi data contoh", no "Langsung
  mulai".

### 7. "Langsung mulai" is withdrawn (0061 §6)

The chat actions `quick_start` and `quick_start:sample` are refused (400 `bad_request`). Nothing is built from the
profile and the picks alone. The 0052 demo packs stay for a build that asks for sample data.

## Before and after

Before: every owner sees Karyawan (4), Penjualan (4), Produk (4), Pelanggan (3).

| Profile | Card after |
| --- | --- |
| Rental alat, Sendiri, 1 lokasi, Online · WhatsApp/medsos | Sewa alat (Jadwal alat bentrok, Alat belum kembali, Kondisi alat, Deposit dan denda) · Penjualan (Pesanan pelanggan, Tagihan, Piutang telat) · Pelanggan (Data pelanggan, Follow up calon pelanggan) · Produk & layanan (Daftar harga). 10 options, no Karyawan, no stock |
| F&B, 2–5 cabang (3), 6–20 staf, Offline · toko/cabang + Online · marketplace | Outlet & dapur (Absen karyawan, Selisih kas, Bahan terbuang, Pesanan ojol) · Produk (Daftar produk dan harga, Stok selisih, Stok antar cabang, Bahan baku) · Penjualan (Pesanan pelanggan, Rekap penjualan harian … tiap cabang) · Karyawan (4) · Pelanggan (Data pelanggan). 15 options |
| Outsourcing, 1 lokasi, 21–100 staf, Offline · datang ke klien + tim sales | Penempatan & klien (Absen di lokasi, Untung per klien, Kontrak karyawan habis) · Karyawan (4) · Penjualan (Tagihan, Piutang telat) · Pelanggan (Data pelanggan, Follow up calon pelanggan) · Produk & layanan (Daftar harga). 12 options |

## Consequences

- **rangka-ai.** `raka/interview/pain_picks.py`: id-shaped codes, `group`, at most 20 entries; tests.
- **rangka-cloud.** `internal/painpicks`: the catalogue, `For(profile, picks)`, Parse over the catalogue and the §4
  limits; `pain_options` per project; the studio sub line; tests.
- **rangka-os.** This contract; the domain packs' code column (after the packs land).

## Out of scope

- Ordering options inside a group by how often owners pick them (needs pilot data).
- Business groups from a pack written later: each is a change to this contract.
