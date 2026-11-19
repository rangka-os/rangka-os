# KPI menu by business type

A starting menu, not a template: offer 4 to 6, keep what the owner says matters, and drop anything their installed
apps cannot compute. Model and field names are those of the rangka-apps catalogue at the time of writing; confirm
each in `rangka_meta` before using it. "Act" is what the owner does when the number moves.

Kind: **L** lagging (result), **D** leading (early signal), **Q** work queue, **R** risk.

## Outsourcing and project services (`jasa_proyek`)

| KPI (owner words) | Kind | Source | Act |
| --- | --- | --- | --- |
| Tagihan terbit (invoiced) | L | `sales_invoice` sum `grand_total`, `docstatus == 1`, period `posting_date` | compare with target |
| Siap ditagih | D | `billing_milestone` sum `amount`, `state == 'ready'` | issue the invoices |
| Piutang lewat jatuh tempo | R/Q | `sales_invoice` sum `outstanding`, overdue filter, `good_when: down`, plus list | chase the client |
| Penawaran menang vs kalah | D | `quotation` count by `outcome`, `docstatus == 1`, period `quotation_date` | fix pricing or follow-up |
| Pekerja aktif | snapshot | `worker` count, `active` | staffing vs contracts |
| Hari kerja tertagih | L | `timesheet` sum `billable_amount`, `docstatus == 1`, period `work_date` | unbilled work is lost money |

Avoid: average margin per project averaged across projects (use the sums on the project ledger, see
`apps/jasa_proyek/dashboards/project_margin.rk`).

## Equipment rental (`sewa_alat`)

| KPI | Kind | Source | Act |
| --- | --- | --- | --- |
| Nilai sewa dipesan | L | `rental_booking` sum `rent_amount`, `docstatus == 1`, period `booking_date` | |
| Uang masuk | L | `payment` sum `amount`, `docstatus == 1 && direction == 'receive'`, period `payment_date` (needs `faktur_piutang`) | |
| Unit di luar / di gudang | D | `rental_unit` donut by `state`, `active`; or `rental_schedule` sum `qty`, `state == 'out'` | idle stock: promote or sell |
| Telat kembali | Q | `rental_schedule` count and list, `state == 'out' && end_date < today()`, `good_when: down` | call the renter |
| Keluar hari ini | Q | `rental_schedule`, `state == 'booked' && start_date <= today()` | prepare the gear |
| Deposit dipegang | R | `rental_schedule` sum `deposit`, `state in ['booked', 'out']` | money owed back |
| Alat paling laku | L | `rental_booking` bar by `rental_item`, sum `rent_amount`, `sort: -value` | buy more of it |

## Courses and training (`kelas_kursus`)

| KPI | Kind | Source | Act |
| --- | --- | --- | --- |
| Peserta aktif | snapshot | `enrollment` count, `status == 'active' \|\| status == 'registered'` | |
| Pendaftar baru | D | `enrollment` count, `status != 'withdrawn'`, period `enrolled_on` | marketing working? |
| Peserta keluar | D | `enrollment` count, `status == 'withdrawn'`, period `left_on`, `good_when: down` | call them, fix the class |
| Kursi tersisa | D | `course_batch` bar by `batch_name`, sum `seats_left`, running batches | fill or merge batches |
| Tagihan kursus | L | `course_charge` sum `amount`, `docstatus == 1`, by `invoice_date` month | |
| Tunggakan | R/Q | `sales_invoice` sum `outstanding`, overdue filter, plus list | remind the payer |
| Kelas 7 hari ke depan | Q | `course_session` list, next-7-days filter | teacher and room ready |
| Ketidakhadiran | D | `course_session` sum `absent_count`, `attendance_taken`, period `session_date`, `good_when: down` | early dropout sign |

## Subscriptions and recurring services (`langganan_jasa`)

| KPI | Kind | Source | Act |
| --- | --- | --- | --- |
| MRR | L | `subscription` sum `monthly_net`, `status != 'stopped'` | |
| Langganan berjalan | snapshot | `subscription` count, `status != 'stopped'` | |
| Tingkat tertagih bulan ini | L | `subscription_month` `max` of `collection_rate`, `month == truncate(today(), 7)` | never `avg` across months |
| Tunggakan per umur | R | `sales_invoice` sum `outstanding`, aging buckets (see `langganan_ringkas`) | dunning |
| Ditangguhkan (isolir) | Q | `subscription` count and list, `status == 'suspended'`, `good_when: down` | collect or stop |
| Uang di penagih | R | `field_collection` sum `amount`, `deposit == ''`, `good_when: down` | collector deposits it |
| Langganan per status | snapshot | `subscription` donut by `status` (few options) | |

Churn ("berhenti bulan ini") needs a dated stop event: count `subscription_status_log` rows by `to_status` and
`changed_on` once you have confirmed the status values in the model; do not infer it from `subscription` alone.

## F&B outlets (`fnb_cabang`, `kasir`, `penjualan_harian`)

| KPI | Kind | Source | Act |
| --- | --- | --- | --- |
| Penjualan bersih hari ini / periode | L | `outlet_sales` sum `net_total`, `docstatus == 1`, `date == today()` or period `date` | |
| Laba kotor | L | `outlet_sales` sum `margin`, `docstatus == 1`, period `date` | price or recipe cost |
| Penjualan per outlet | L | `outlet_sales` bar by `outlet`, `sort: -value` | the weak outlet |
| Penjualan per hari | L | `outlet_sales` bar by `date`, `grain: day` | staffing per weekday |
| Penjualan belum diajukan | Q | `outlet_sales` list, `docstatus == 0` | outlet closes its day |
| Stok minus | Q/R | `stock_balance` count and list, `negative == true`, `good_when: down` | head office checks |
| Penjualan kasir | L | `pos_sale` sum `grand_total`, `docstatus == 1`, period `date` (with `kasir`) | |

Avoid: average ticket from `avg` of daily totals (that is an average of averages); use `pos_sale` `avg` of
`grand_total` over submitted sales, one row per receipt.

## Words owners use

| Owner says | Means |
| --- | --- |
| omzet, penjualan | submitted sales total (`docstatus == 1`), net of discounts when the model has a net field |
| uang masuk, pemasukan | payments received, not invoices issued |
| piutang, tagihan belum dibayar | `outstanding > 0` on submitted invoices |
| telat, nunggak | past due date and still open |
| paling laku | ranking bar, `sort: -value`, by item or outlet |
| minggu ini / bulan ini | ask or say: rolling 7/30 days (the period) or the calendar week/month (a filter) |
