# Layanan langganan (`industry: subscription`)

Businesses that bill the same customer every period for an ongoing service: RT/RW net and small ISPs, water or gas
refill subscriptions, laundry and cleaning contracts, security patrol, waste pick-up, maintenance retainers (AC,
IT support), agency retainers, gyms with monthly members, and software or SaaS-like providers selling plans to
businesses. What they share: a plan, a cycle, a bill every period, and what happens when the customer does not pay.

## How it usually runs

| Stage | What happens | Who | Documents the owner names |
| --- | --- | --- | --- |
| Lead | Customer asks; for ISPs a coverage check at the address | Sales, teknisi | Chat WA, survei lokasi |
| Sign-up | Plan chosen, start date, installation or set-up fee | Admin | Formulir, kontrak |
| Activation | Service switched on (installation, account created) | Teknisi, admin | BA pemasangan |
| Billing | Bill every month (or 3, 12 months, or prepaid) on a fixed day | Admin, system | Tagihan, invoice |
| Collection | Transfer, VA, QRIS, or a collector visits door to door | Penagih, keuangan | Bukti bayar, setoran penagih |
| Dunning | Reminders, then suspend (isolir), then stop | Admin, teknisi | Pengingat, daftar isolir |
| Changes | Upgrade, downgrade, pause, move address, stop | Admin | Permintaan pelanggan |
| Reporting | MRR, collection rate, churn, per area | Owner | Rekap bulanan |

**Size bands:** RT/RW net and local services: `customers: to_1000` or `over_1000`, `staff: to_5` or `to_20`,
`sales_channels` include `offline_visit` (collectors). Agencies and software providers: `customers: under_50` or
`to_200`, bigger invoices, bank transfer or payment links.

## Words owners use → Rangka

| Owner says | Rangka concept |
| --- | --- |
| paket, plan, tier | `subscription_plan` (monthly price, default cycle, due days, dunning ladder) |
| pelanggan, member, klien, ID pelanggan | `customer` + `subscription` (with `service_id`) |
| bulanan, per 3 bulan, tahunan, bayar di muka | `cycle`, `prepaid_months`, `cycle_months` |
| tanggal tagih, tanggal 1, sesuai tanggal pasang | anchor: start date or month start (first bill pro-rated) |
| biaya pasang, add-on, tambahan | `subscription_extra` (once or every period) |
| tagihan bulan ini | `subscription_bill` + draft `sales_invoice` |
| telat, isolir, putus | status late / suspended (Ditangguhkan) / stopped, set by the dunning flow |
| berhenti langganan | `end_date` on the subscription (history kept) |
| wilayah, area, RT/RW | `service_area` |
| penagih, kolektor, setoran | `collector`, `field_collection`, `collection_visit`, `collector_deposit` (screens `rute_tagih`, `catat_tagih`) |

## Common pains and the question that uncovers them

| Pain (owner's words) | 0061 code | Question to ask |
| --- | --- | --- |
| "Bikin tagihan ratusan pelanggan tiap bulan" | `sales_invoices` | "Tagihan dibuat tanggal berapa, dan semua pelanggan tanggalnya sama?" |
| "Banyak yang nunggak, lupa siapa" | `sales_overdue` | "Kalau pelanggan belum bayar, kapan biasanya diisolir?" |
| "Setoran penagih tidak cocok" | none | "Penagih setor uangnya ke siapa, dan dicocokkan pakai apa?" |
| "Cocokin transfer ke pelanggan makan waktu" | none | "Pelanggan bayar lewat transfer biasa, VA, atau QRIS?" |
| "Tidak tahu berapa yang berhenti tiap bulan" | none | "Bulan lalu ada berapa pelanggan yang berhenti, kamu tahu dari mana?" |
| "Data pelanggan di Excel dan HP teknisi" | `customer_records` | "Daftar pelanggan sekarang disimpan di mana?" |
| "Calon pelanggan yang survei lupa dihubungi" | `customer_followup` | "Yang sudah disurvei tapi belum pasang, ada yang menghubungi lagi?" |

## What covers it

- **App:** `langganan_jasa` (modules `subscriptions`, `invoicing`, `payments`, `bank_statement`, `contacts`,
  `catalogue`, `procurement`, `users`): plans, per-customer cycles, automatic bills each period, statuses with suspend
  and stop, areas, collectors with routes and deposits, MRR and collection-rate dashboards. Demo site
  `demo-langganan` (an RT/RW net) adds `faktur_piutang` and `pembukuan`.
- **Payment in:** `payments` has flows for payments from Xendit (VA, QR, invoice), Midtrans and DOKU, with connectors
  `xendit`, `midtrans`, `doku` and the Cloud service `payment_gateway`; `bank_statement` matches plain transfers.
  Read `cloud_doc` before mentioning fees.
- **Reminders:** `whatsapp_reminder` for overdue invoices.
- **Usually custom:** installation jobs and technician tickets (complaints, gangguan); equipment lent to the customer
  (router, galon) and its return; pause or holiday freeze; referral rewards.
- **SaaS-like providers:** plans, cycles, prepaid years and extras fit. Usage-based billing (per user, per
  transaction) needs the usage figure on an extra: typed by the admin, or pulled by a company connector from the
  provider's own API (consult §5a, `rangka-connectors`).

## KPIs the owner watches

- MRR and its change; active subscriptions per plan and area.
- Collection rate (billed vs. paid) per month and per collector.
- Churn: subscriptions stopped per month; suspended count.
- Days from bill to payment; collector deposits outstanding.

## Money and compliance hotspots

- **One bill per period.** Bills are made only by the billing flow, unique per subscription and period; nobody types
  them. A wrong invoice is cancelled and amended; the bill stays.
- **Proration is limited.** The first bill is pro-rated when the anchor is the 1st and the start is another day; the
  last bill is pro-rated to `end_date`. A plan or price change applies from the next bill: there is no mid-period
  upgrade credit. If the owner credits upgrades mid-period, that is a custom rule.
- **Status is computed, never set by hand** (late, suspended, stopped). Suspending the actual service (router, account)
  is not done by Rangka: no connector exists for routers or ISP billing systems. Say: "Status isolir tercatat
  otomatis, tapi memutus internetnya masih dilakukan teknisi."
- **Collector cash** is money in a person's hands until deposited: collections, visits and deposits are recorded
  apart, and a deposit is checked against the collections it covers.
- **PPN** on the service depends on PKP status and the service: verify with a tax consultant.

## Red flags: not a fit yet

- ISPs that need RADIUS/MikroTik automation (auto-isolate, bandwidth profiles): keep their network tool as `existing`;
  Rangka covers customers, bills and money.
- Pure consumer app subscriptions billed by app stores: revenue arrives as payouts, not per-customer bills.
- Metered utilities with meter readings per customer per month (water, electricity): readings would be custom and the
  volume may be high; ask how many customers before mapping.
- Gyms that need door access control or class booking by the hour: membership billing fits; access hardware has no
  edge module (`edge` piece without `ref`, "belum tersedia").
