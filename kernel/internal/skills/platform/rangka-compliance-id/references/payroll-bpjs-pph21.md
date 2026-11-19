# Payroll: BPJS, PPh 21, THR

Every number here is **as of 2026, verify with a tax consultant** (and with BPJS for the programmes). The module
`payroll` holds the rules; its decisions are recorded in rangka-apps `docs/payroll.md`. You explain, you do not
compute.

## Where things live

| Thing | Record | Who fills it |
| --- | --- | --- |
| Per worker: NIK, NPWP, PTKP status (TER category), pay basis, salary, allowances, which BPJS programmes, BPJS numbers, bank | `payroll_profile` (import `payroll_profile_list`) | finance |
| Per period: tax year and month, pay date, UMK of the site, JKK rate, JP and BPJS Kesehatan wage caps | `payroll_run` | finance |
| Per worker per month: pay, overtime, THR months, deductions, BPJS shares, PPh 21, net pay | `payslip` (submittable) | made from attendance or by hand, submitted by finance |
| Closing the period (and the client invoice for outsourcing) | `payroll_close` | finance |

Payroll profiles are read by finance (and, read-only, project admins). Salaries and identity numbers never go into a reply, a notice or a dashboard
visible to other roles.

## BPJS (mechanism)

- **BPJS Kesehatan** (health): the employer pays the larger share and the worker a smaller share of the wage, up to a
  wage cap. In the module: 4% + 1% up to the run's `kes_wage_cap`.
- **BPJS Ketenagakerjaan** (employment): JKK (work accident, rate by risk class, set per run as `jkk_rate`), JKM
  (death), JHT (old-age savings, employer and worker shares), JP (pension, employer and worker shares up to
  `jp_wage_cap`). The caps change yearly: they are run fields so finance updates them, not the module.
- The BPJS wage is basic pay plus fixed allowance, never below the UMK (`payroll_run.umk`).
- Programme shares other than JKK and the caps are written in the module's computed fields; a change in law is a
  module update by its maintainers, not a company overlay.

## PPh 21 (mechanism)

- The employer withholds income tax from employees' pay monthly. Since 2024 the monthly withholding uses effective
  rates (TER, PP 58/2023) by PTKP category A/B/C on gross pay; December recomputes the year with the normal
  progressive rates and settles the difference. Workers without a valid tax ID are withheld at a higher rate.
- Daily or casual workers use the daily TER (PMK 168/2023).
- In the module: `payslip.pph21`, from `payroll_profile.ptkp_status`, `has_tax_id` and the year to date. Slips are
  submitted in month order so December sees every month.
- Rangka prepares the numbers. Depositing and reporting (bukti potong PPh 21, SPT Masa) are done by the business in
  Coretax.

## THR

- Religious holiday allowance: a worker with 12 months' service or more gets one month's wage; fewer months (at least
  one), pro rata. It must be paid ahead of the holiday (at the latest 7 days before, as of 2026).
- In the module: `payslip.thr_tenure_months` → `thr_pay`; it is part of gross pay, so PPh 21 applies to it.
- Which holiday applies to which worker, and when, is the owner's to set on the slip.

## Designing around payroll

- Payroll is money: new pay components belong in the module (ask maintainers) or as `other_income` / deductions on
  the slip, not as company money fields that bypass PPh 21 and BPJS.
- Employee identity (NIK, NPWP) stays in `payroll_profile` (contract 0056), not on the `user` record.
- Contact data of employees (phone, email on `user`) is hidden from every role except owner and `head_office`; the
  validator warns `contact_field_visible` when a role is missing from that list.

## Owner phrasing

- "Sistem menghitung BPJS dan PPh 21 di tiap slip dari data gaji karyawan. Bagian keuanganmu tinggal cek lalu ajukan
  slipnya. Setor dan lapornya tetap lewat BPJS dan Coretax."
- "Batas upah BPJS dan tarif JKK diisi di periode gaji, jadi kalau berubah tahun depan, cukup diganti di sana."
- "THR untuk yang sudah kerja 12 bulan atau lebih satu bulan gaji, yang kurang dihitung proporsional (aturan per
  2026, cek lagi dengan konsultanmu)."

## Escalate

Status of a worker (permanent, contract, casual, freelancer); PTKP disputes; severance (pesangon); gross-up; tax for
foreign workers; any BPJS arrears or sanctions.
