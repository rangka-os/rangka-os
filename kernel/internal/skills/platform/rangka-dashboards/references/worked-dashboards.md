# Worked dashboards

Two site dashboards that replace an app's home dashboard. Both validate with `rangka validate` (0 errors, 0 warnings)
against the rangka-apps example sites `examples/sites/demo-sewa` (apps `sewa_alat`, `faktur_piutang`, `pembukuan`)
and `examples/sites/demo-kursus` (apps `kelas_kursus`, `faktur_piutang`, `pembukuan`), saved as
`<site>/dashboards/<name>.rk`. Change-set path: `dashboards/<name>.rk`, risk low.

## 1. Rental: `dashboards/sewa_ringkas.rk`

Owner (made up): "Aku sewain alat pesta. Tiap pagi aku mau lihat uang masuk, alat yang di luar, dan siapa yang telat
balikin."

Choices:

- Two lagging money numbers with a period (nilai sewa dipesan, uang masuk), both `docstatus == 1`: what was booked and
  what actually came in are different facts on different models, so they are two widgets, not one.
- Two "now" numbers without a period (unit di luar, telat kembali); late has `good_when: down`.
- One trend (`line`, weekly grain) and one ranking (`bar`, top 8 items).
- Utilisation as a donut of `rental_unit.state`: two options, so a donut is fine.
- Work queue (late list) and risk (deposit held, overdue invoices). 10 widgets, each row adds up to 12.
- `payment` comes from `faktur_piutang`; on a site without it, drop `cash_in` (a site dashboard cannot name a model
  that is not installed).

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/dashboard.json
name: sewa_ringkas
label: {id: Sewa hari ini, en: Rentals today}
description: {id: 'Uang sewa, uang masuk, alat di luar, yang telat, dan deposit yang dipegang'}
default_period: 30
cache: 5m
widgets:
  # Row 1: four numbers. Money from submitted documents only (docstatus == 1).
  - name: rent_booked
    type: number
    label: {id: Nilai sewa dipesan}
    description: {id: Pesanan sewa yang sudah diajukan pada periode ini}
    model: rental_booking
    aggregate: sum
    field: rent_amount
    filter: docstatus == 1
    period: booking_date
    compact: true
    span: 3
  - name: cash_in
    type: number
    label: {id: Uang masuk}
    model: payment
    aggregate: sum
    field: amount
    filter: docstatus == 1 && direction == 'receive'
    period: payment_date
    compact: true
    span: 3
  - name: units_out
    type: number
    label: {id: Unit sedang di luar}
    model: rental_schedule
    aggregate: sum
    field: qty
    filter: state == 'out'
    span: 3
  - name: late
    type: number
    label: {id: Telat kembali}
    model: rental_schedule
    filter: state == 'out' && end_date < today()
    good_when: down
    span: 3
  # Row 2: the trend and the mix.
  - name: rent_by_week
    type: chart
    chart: line
    label: {id: Nilai sewa per minggu}
    model: rental_booking
    group_by: booking_date
    grain: week
    aggregate: sum
    field: rent_amount
    filter: docstatus == 1
    period: booking_date
    span: 6
  - name: rent_by_item
    type: chart
    chart: bar
    label: {id: Alat paling laku}
    model: rental_booking
    group_by: rental_item
    aggregate: sum
    field: rent_amount
    filter: docstatus == 1
    period: booking_date
    sort: -value
    limit: 8
    span: 6
  # Row 3: utilisation now, and what to chase.
  - name: units_by_state
    type: chart
    chart: donut
    label: {id: Unit di gudang dan di luar}
    model: rental_unit
    group_by: state
    filter: active
    span: 4
  - name: late_list
    type: list
    label: {id: 'Telat kembali, hubungi penyewa'}
    model: rental_schedule
    filter: state == 'out' && end_date < today()
    sort: end_date
    limit: 20
    columns: [booking, customer, rental_item, qty, end_date]
    span: 8
  - name: deposit_held
    type: number
    label: {id: Deposit dipegang}
    description: {id: Uang penyewa yang harus kembali kalau alat aman}
    model: rental_schedule
    aggregate: sum
    field: deposit
    filter: state in ['booked', 'out']
    compact: true
    span: 4
  - name: overdue_invoices
    type: list
    label: {id: Faktur sewa lewat jatuh tempo}
    model: sales_invoice
    filter: docstatus == 1 && outstanding > 0 && due_date < today()
    sort: due_date
    limit: 10
    columns: [name, customer, due_date, outstanding]
    span: 8
```

## 2. Course: `dashboards/kursus_ringkas.rk`

Owner (made up): "Kursus bahasa Inggris, tiga angkatan jalan. Aku mau tahu murid baru, yang keluar, sama yang belum
bayar."

Choices:

- Keeps the original's `roles` (course_admin, finance_manager) and `cache`: replacing a dashboard must not widen who
  opens it.
- Pendaftar baru and peserta keluar are counts on `enrollment` with a period on their own dates (`enrolled_on`,
  `left_on`), so each compares with the previous period. Keluar is `good_when: down`.
- Tagihan per bulan uses a 180-day filter, not `period`: a 30-day period would show one month bar.
- Kursi tersisa sorts ascending (`sort: value`): the fullest batches first.
- 8 widgets.

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/dashboard.json
name: kursus_ringkas
label: {id: Ringkasan kelas, en: Classes overview}
description: {id: 'Peserta, pendaftar baru, yang keluar, tunggakan, tagihan per bulan dan kursi tersisa'}
default_period: 30
roles: [course_admin, finance_manager]
cache: 5m
widgets:
  - name: active_students
    type: number
    label: {id: Peserta aktif}
    model: enrollment
    filter: status == 'active' || status == 'registered'
    span: 3
  - name: new_enrollments
    type: number
    label: {id: Pendaftar baru}
    model: enrollment
    filter: status != 'withdrawn'
    period: enrolled_on
    span: 3
  - name: withdrawn
    type: number
    label: {id: Peserta keluar}
    model: enrollment
    filter: status == 'withdrawn'
    period: left_on
    good_when: down
    span: 3
  - name: overdue_total
    type: number
    label: {id: Tunggakan}
    model: sales_invoice
    aggregate: sum
    field: outstanding
    filter: docstatus == 1 && outstanding > 0 && due_date < today()
    good_when: down
    compact: true
    span: 3
  - name: billed_by_month
    type: chart
    chart: bar
    label: {id: Tagihan kursus per bulan}
    description: {id: Enam bulan terakhir}
    model: course_charge
    group_by: invoice_date
    grain: month
    aggregate: sum
    field: amount
    filter: docstatus == 1 && invoice_date >= today() - duration(180)
    span: 6
  - name: seats_left
    type: chart
    chart: bar
    label: {id: Kursi tersisa per angkatan}
    model: course_batch
    group_by: batch_name
    aggregate: sum
    field: seats_left
    filter: end_date == '' || end_date >= today()
    sort: value
    limit: 10
    span: 6
  - name: week_sessions
    type: list
    label: {id: Jadwal kelas 7 hari ke depan}
    model: course_session
    filter: session_date >= today() && session_date < today() + duration(7)
    sort: session_date
    limit: 20
    columns: [session_date, start_time, batch, session_no, topic]
    span: 12
  - name: overdue_invoices
    type: list
    label: {id: Tagihan lewat jatuh tempo}
    model: sales_invoice
    filter: docstatus == 1 && outstanding > 0 && due_date < today()
    sort: due_date
    limit: 20
    columns: [name, customer, due_date, outstanding]
    span: 12
```

## Proposing the first one

```json
{"title": "Dashboard sewa: uang masuk dan yang telat",
 "reason": "Pemilik mau lihat uang masuk, alat di luar dan yang telat tiap pagi",
 "ops": [{"op": "create", "path": "dashboards/sewa_ringkas.rk", "content": "<the file above>"}]}
```

A later change patches the same file, for example adding a fifth number (then also patch the spans of that row so it adds up to 12 again):

```json
{"op": "patch", "path": "dashboards/sewa_ringkas.rk",
 "patch": [{"op": "add", "path": "/widgets/4", "value": {"name": "going_out_today", "type": "number",
   "label": {"id": "Keluar hari ini"}, "model": "rental_schedule",
   "filter": "state == 'booked' && start_date <= today()", "span": 3}}]}
```
