# Outsourcing (`industry: outsourcing`)

Juragan's default business type. Companies that place workers at a client's site (security, cleaning service,
drivers, SPG, admin, manpower for factories and projects) and bill the client for them. Also inspection, training and
consulting firms that bill people's days: `jasa_proyek` asks the business line (manpower, inspeksi, rental, training,
konsultan).

## How it usually runs

| Stage | What happens | Who | Documents the owner names |
| --- | --- | --- | --- |
| Lead | Client asks for N workers of a role, often through a tender or a contact | Owner, marketing | RFQ, email, WA |
| Offer | Price per worker per month or per day, plus management fee | Owner, admin | Penawaran harga |
| Contract | Signed agreement or PO for a period; renewals are common | Owner | SPK, PKS, PO klien |
| Recruit and place | Find workers, check documents, assign to the client site | HRD, koordinator | CV, KTP, kontrak kerja (PKWT) |
| Attendance | Workers clock in at the site; supervisor checks | Worker, supervisor lapangan | Absen, timesheet, lembur |
| Payroll | Monthly (or daily for casual workers): wage, allowances, overtime, BPJS, PPh 21, kasbon | Admin payroll, finance | Slip gaji, rekap absen |
| Client invoice | Bill the client from the same attendance and payroll, often with PPN | Finance | Invoice, faktur pajak, BAST |
| Collection | Client pays on terms (30–60 days is common); some withhold PPh 23 | Finance | Bukti potong, mutasi bank |
| Reporting | Margin per client or project, receivables, tax period | Owner | Laporan laba per proyek |

The money squeeze: workers are paid every month on a fixed date, the client pays weeks later. Cash gap and late client
payment are the owner's main worry.

**Size bands** (profile `staff`, contract 0054): `to_20` and `to_100` are typical first customers (a few clients,
dozens of placed workers); `over_100` happens fast because placed workers count as staff. Usually one office,
`scale: one_location` or `multi_business` (several PTs, one per client segment or for tax reasons).

## Words owners use → Rangka

| Owner says | Rangka concept |
| --- | --- |
| klien, user, principal | `customer` (contacts) |
| penawaran, quotation | `quotation` |
| kontrak, SPK, PKS, PO klien | `project` with `contract_kind` and `billing_model` (borongan, termin, retainer, per jam/hari) |
| penempatan, plotting, penugasan | `assignment` (projects) |
| karyawan outsourcing, TKO, tenaga kerja | `worker` (workforce) with a `payroll_profile` |
| lokasi kerja, site, proyek | `work_site` |
| absen, timesheet, lembur | `attendance_log`, `timesheet` (projects; screen `absen`) |
| gajian, payroll, slip gaji | `payroll_run`, `payslip`, `payroll_close` |
| kasbon, pinjaman karyawan | `cash_advance` |
| tagihan ke klien, invoice | `sales_invoice` (invoicing), termin `billing_milestone` |
| BAST, berita acara | `acceptance` (projects) |
| potong PPh 23 oleh klien | `withholding` (payments) |
| beberapa PT | several orgs (`jasa_proyek` asks "Berapa PT yang kamu kelola?") |

## Common pains and the question that uncovers them

| Pain (owner's words) | 0061 code | Question to ask |
| --- | --- | --- |
| "Hitung gaji tiap bulan masih di Excel, sering salah lembur" | `staff_pay` | "Gaji dihitung dari mana, absen dikirim supervisor atau direkap sendiri?" |
| "Kasbon karyawan susah dilacak" | `staff_advance` | "Kasbon dipotong dari gaji bulan berikutnya, atau dicicil?" |
| "Data karyawan tersebar di banyak file" | `staff_records` | "Data karyawan sekarang disimpan di mana?" |
| "Cari orang baru masih lewat grup WA" | `staff_hiring` | "Lamaran masuk lewat mana, dan siapa yang memutuskan diterima?" |
| "Tagihan ke klien diketik ulang dari rekap absen" | `sales_invoices` | "Tagihan ke klien dihitung dari absen, atau angka tetap per bulan?" |
| "Klien telat bayar, gaji harus tetap keluar" | `sales_overdue` | "Biasanya berapa lama dari tagihan dikirim sampai uangnya masuk?" |
| "Tidak tahu untung per klien" | none | "Kalau satu klien berhenti, kamu tahu berapa untung yang hilang?" |
| "Absen di lokasi bisa dititipkan" | none | "Absen di lokasi pakai apa sekarang: kertas, mesin, atau foto?" |

## What covers it

- **Bundle:** `jasa_proyek` covers the whole flow: quotation, order, project with assignment, field attendance
  (screen `absen` for `field_worker`), timesheet, project expense, acceptance (BAST), payroll with BPJS, PPh 21 and
  THR fields, kasbon, invoices with milestones, payments with PPh 23 withholding, bank statement matching, books and
  the Coretax tax period. One app, so in the studio it is usually one `catalogue` piece per role stage, `ref: jasa_proyek`.
- **Modules, when the owner wants less:** `workforce` + `payroll` (needs `projects` and `invoicing`, contract 0061 §1)
  for payroll only; `invoicing` + `payments` + `bank_statement` for client billing only.
- **Client invoice from payroll.** The payroll module tags an invoice with its payroll run and lets it be submitted
  once, after the run is closed (flow `invoice_payroll_once`). How the management fee is added on top is not written in
  the app summary: read the app skill or the module before promising a formula; if the owner's fee rule does not fit,
  design it as a custom rule.
- **Usually custom:** recruitment (0061 design: records Lowongan, Pelamar); contract renewal reminders per worker
  (PKWT end dates); uniform and equipment issued to workers; client-specific shift rosters.
- **Integrations:** bank statement import exists; WhatsApp reminders for overdue client invoices (`whatsapp_reminder`).
  No connector exists for fingerprint or face attendance machines, BPJS online reporting, bank bulk-transfer payroll
  files or job portals: keep them `open`, or draft a company connector only when the system has a usable API
  (consult §5a).

## KPIs the owner watches

- Margin per client and per project (`project_margin` dashboard), revenue per placed worker.
- Receivables by age (`ar_aging`), days from invoice to cash.
- Payroll cost vs. billed amount per period; overtime hours billed vs. paid.
- Headcount placed, attendance rate, turnover.

## Money, staff and compliance hotspots

- **Payroll is high risk.** Wage, allowances, overtime, BPJS (Ketenagakerjaan, Pensiun, Kesehatan), PPh 21 (TER, PTKP
  status) and THR are computed by the payroll module from rates stored on each payroll run (UMK, JKK rate, wage caps).
  Raka never sets or guesses a rate: the owner or their consultant enters it. Verify with a tax consultant.
- **Daily vs. monthly workers** are taxed differently (`pay_basis`, `employment` on the payroll profile). Ask which
  the owner has before mapping payroll.
- **PPN on the client invoice and PPh 23 withheld by the client** depend on PKP status and the service type.
  `jasa_proyek` asks PKP at onboarding. Exact treatment: verify with a tax consultant.
- **Kasbon** is a money record: cut from payslips through `cash_advance`, never by editing a payslip total.
- **Personal data** (NIK, NPWP, BPJS numbers, bank accounts) sits in `payroll_profile`, visible to finance only. Never
  ask for it in chat; the owner fills it in the system.

## Red flags: not a fit yet

- Hundreds of workers on rotating shifts with complex roster rules and fingerprint machines at every site: there is no
  machine integration and no roster engine. Say: "Absen lewat mesin sidik jari belum bisa masuk otomatis; untuk
  sekarang karyawan absen dari HP di lokasi."
- Owners who need BPJS or PPh 21 reporting files sent to the authorities automatically: the tax period exports Coretax
  files for PPN and PPh 23 (per the `pajak` summary); confirm anything else with `rangka_catalogue` before mapping it,
  otherwise it stays `open`.
- Payroll paid straight from the system into workers' bank accounts: not supported; payment stays a recorded transfer.
- Labour-law questions (PKWT limits, severance): Raka does not advise; suggest a labour consultant.
