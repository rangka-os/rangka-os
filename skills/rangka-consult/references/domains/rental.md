# Rental alat (`industry: rental`)

Businesses that lend equipment for a fee and get it back: cameras and lenses, sound systems, party equipment (tents,
chairs, lighting), construction tools and scaffolding, outdoor and camping gear, medical equipment. Money comes from
rent per day, deposits, and fines for late return or damage.

## How it usually runs

| Stage | What happens | Who | Documents the owner names |
| --- | --- | --- | --- |
| Enquiry | Customer asks over WA or Instagram: is it free on those dates, how much | Admin | Chat WA, katalog harga |
| Booking | Dates fixed, units reserved, DP or full rent paid | Admin | Nota sewa, bukti transfer |
| Guarantee | Deposit in money, or an ID card kept (KTP, SIM), sometimes both | Admin | Foto KTP, kuitansi deposit |
| Hand-over | Units checked and photographed, customer signs | Gudang, admin | Checklist, berita acara (BAST) |
| In use | Reminders to return; extensions asked by chat | Admin | Chat WA |
| Return | Units checked: late days, damage, missing parts | Gudang | Checklist kembali, foto |
| Settlement | Fines taken from the deposit, rest paid back, or extra billed | Admin, owner | Nota denda, transfer balik |
| Reporting | Which items earn, which sit idle, money held as deposits | Owner | Rekap sewa |

**Size bands:** mostly `staff: solo` or `to_5`, `scale: one_location`, `customers: to_200`. Event and construction
rentals reach `to_20` staff and business customers who want invoices with terms.

## Words owners use → Rangka

| Owner says | Rangka concept |
| --- | --- |
| alat, barang sewa, item | `rental_item` (with daily rate, `track_units`) |
| unit, nomor seri, kode stiker | `rental_unit` |
| booking, pesanan sewa, jadwal | `rental_booking`, `rental_schedule` (screen `jadwal_alat`) |
| deposit, jaminan uang | `deposit` on the booking |
| jaminan KTP | not a money field: a custom field or note (ask how they keep it) |
| alat keluar, serah terima | `rental_checkout` (screen `alat_keluar`) |
| alat kembali, pengembalian | `rental_return` (screen `alat_kembali`) with unit checklist |
| denda telat | `late_fee_per_day` on the booking, `late_fee` on the return |
| rusak, hilang, ganti rugi | `unit_damage`, `damage_fee` on the return |
| nota sewa, invoice | `sales_invoice` drafted from the booking |
| berita acara, BAST | printed from the checkout and the return |

## Common pains and the question that uncovers them

| Pain (owner's words) | 0061 code | Question to ask |
| --- | --- | --- |
| "Alat dobel dibooking di tanggal yang sama" | none | "Jadwal alat sekarang dicatat di mana: kalender, buku, atau grup WA?" |
| "Lupa alat mana yang belum kembali" | none | "Kalau ada yang telat balikin, kamu tahunya dari mana?" |
| "Ribut soal kerusakan karena tidak ada bukti kondisi awal" | none | "Waktu alat keluar, kondisinya dicatat atau difoto?" |
| "Deposit dan denda dihitung manual" | none | "Denda telat dan kerusakan biasanya dipotong dari deposit, atau ditagih terpisah?" |
| "Bikin nota atau invoice makan waktu" | `sales_invoices` | "Pelanggan perusahaan minta invoice dengan tempo, atau semua bayar di depan?" |
| "Pelanggan perusahaan telat bayar" | `sales_overdue` | "Biasanya berapa lama dari acara selesai sampai uangnya masuk?" |
| "Data pelanggan ada di HP admin" | `customer_records` | "Pelanggan langganan dicatat di mana sekarang?" |

## What covers it

- **App:** `sewa_alat` (modules `rental`, `invoicing`, `contacts`, `catalogue`, `users`): bookings that refuse double
  booking on the busiest day, hand-over per unit with checklist and BAST, deposit, late and damage fines, rent invoice.
  Demo site `demo-sewa` also installs `faktur_piutang` and `pembukuan`.
- **Add-ons:** `whatsapp_booking` (reminder the day before the rental starts), `whatsapp_reminder` (overdue invoices),
  `bank_statement` (match transfers), `payments` with an online payment connector for DP links.
- **Usually custom:** ID-card guarantee (record what is kept, returned on return); delivery and pick-up with a driver
  and a fee; packages of several items rented together; operator or crew hired with the equipment; maintenance and
  service schedule per unit; rent per hour or per event instead of per day.
- **Integrations:** WhatsApp is the main channel (connector `whatsapp_business`, or the Cloud-provided WhatsApp number connector listed in `rangka_catalogue`); payment links via
  `xendit`, `midtrans`, `doku`; Instagram has no connector.

## KPIs the owner watches

- Utilisation per item (days out / days owned), revenue per item, idle items.
- Units out now and overdue returns.
- Deposits held (a liability: money that belongs to customers).
- Damage and loss cost per month, fines collected.

## Money, stock and compliance hotspots

- **Deposit is not revenue.** The module books it as money held and pays the rest back on return. Never model it as a
  free-text note or fold it into the rent.
- **Fines and PKP.** For a non-PKP business the return keeps late and damage fines from the deposit; for a PKP business
  it does not (field `deposit_kept` follows `tax.pkp`) and the fines are billed instead. Whether fines carry PPN:
  verify with a tax consultant.
- **Units are stock-like.** A unit's position (out, returned) is written only by the module's flows; nobody edits it by
  hand except its condition after repair. Lost units come back as rows marked hilang and are charged as damage.
- **Refund of deposit** is a money movement the admin submits; it needs a recorded method (cash or bank).

## Red flags: not a fit yet

- Vehicle rental with GPS tracking, mileage and fuel rules, or car rental with drivers: no GPS connector, and mileage
  billing would be custom. Map what fits (booking, deposit, invoice) and keep tracking `open`.
- Long-term rentals billed monthly (kos, office, equipment leased for a year): that is a subscription; read
  [subscription.md](subscription.md).
- Rental of heavy equipment with operator timesheets is closer to [outsourcing.md](outsourcing.md) (`jasa_proyek` has
  business line `rental`): ask how they bill before choosing.
- Hourly rentals (studio, court, room booking by the hour): the booking counts days. Say plainly that per-hour booking
  needs a part designed for them.
