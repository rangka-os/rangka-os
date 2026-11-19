# PPN, PKP, faktur pajak, Coretax and numbering

Every number here is **as of 2026, verify with a tax consultant**. In Rangka the numbers live in settings and module
fields; this page is for explaining them.

## The mechanism

- **PPN** (Pajak Pertambahan Nilai) is VAT. A business that is a **PKP** (Pengusaha Kena Pajak) charges PPN on its
  sales (PPN keluaran), may credit the PPN on its purchases from other PKPs (PPN masukan), and pays or reports the
  difference every month (masa pajak).
- **Who must be PKP:** registration is required once turnover passes a yearly threshold (Rp 4,8 miliar a year as of
  2026); a smaller business may register voluntarily. Whether a given business should register is a consultant
  question: explain the mechanism, never decide it.
- **A non-PKP may not charge PPN and may not issue a faktur pajak.** In Rangka `tax.pkp: false` makes the default
  `tax_rate` on `sales_invoice` 0.
- **Rate and DPP.** The general rate is 12% (from 2025). For most goods and services the base is "DPP nilai lain"
  of 11/12 of the price, so the PPN works out like the old 11% (PMK 131/2024). Luxury goods use the full price
  (factor 1/1). In Rangka: `config.tax.ppn_rate` and `config.tax.dpp_ratio` ("11/12"), read in CEL as
  `tax.ppn_rate`, `tax.dpp_num`, `tax.dpp_den`. `sales_invoice.ppn` = round(ppn_base × dpp_num / dpp_den ×
  tax_rate / 100). The kernel computes it; you explain it.
- **0% on a PKP invoice** needs a legal ground: `sales_invoice.tax_rate_reason` (exempt, not collected, not taxable).
  The facility codes on the faktur follow from it (`coretax_facility`, `efaktur_code` 07/08). Choosing the ground is
  finance's or the consultant's call.
- **PPN on the fee only** (outsourcing: wages billed on their own line): `config.tax.ppn_fee_only`, off by default;
  invoice lines marked `ppn_exclude` stay out of the PPN base. Switching it is a consultant decision.
- **Retail sales** of a PKP shop to end consumers are reported as a monthly total (digunggung); `outlet_sales.ppn_in_sales`
  shows the PPN inside sales prices.

## Faktur pajak, e-Faktur, Coretax

- A faktur pajak is the tax invoice a PKP issues for each taxable sale. Since 2025 it is made in **Coretax**, DJP's
  core tax system, which replaced the old e-Faktur desktop app.
- Rangka does not file. App `pajak` (modules `tax_id`, `coretax`) prepares the files: per month a `tax_period`
  (masa pajak) with the business NPWP and NITKU, and print formats `coretax_faktur` (sales faktur XML for import),
  `coretax_bupot` (withholding slips) and `rekap_ppn` (a recap). Finance downloads them and uploads to Coretax.
- The buyer's identity on the faktur comes from `customer`: NPWP 16 digits + NITKU 22 digits (head office = NPWP +
  000000); without NPWP, NIK; a foreigner, passport. It is frozen on the invoice at submit (`buyer_*` fields).
- Transaction code (`efaktur_code`): 01 normal at 1/1, 04 with DPP nilai lain, 02 and 03 when the buyer is a VAT
  collector (`customer.vat_collector`), 07 and 08 for facilities.
- Correcting an invoice of an earlier period is a **faktur pengganti** made in Coretax; the Coretax export refuses an
  amended invoice of an earlier period for that reason.
- The `tax_period.month` help text carries the monthly deadlines the module uses; read it there rather than quoting
  dates from memory.

## Invoice and receipt basics

- A sales invoice (`sales_invoice`) is submittable: numbered at submit from `config.numbering` (series `invoice`,
  for example `INV-{YYYY}-{#####}`), then immutable. Mistakes: cancel and amend; both stay.
- Show on a printed invoice what the buyer needs: seller legal name and NPWP (`config.org`), buyer, date, lines,
  DPP, PPN, total. The print format reads `org.legal_name` and `org.npwp`.
- A non-PKP's nota or receipt shows no PPN line.
- Multiple legal entities (two PTs) each have their own `orgs.<id>.config.tax`, `org` and numbering; adding an org is
  high risk.

## Owner phrasing

- "Usahamu belum PKP di pengaturan, jadi nota belum bisa pakai PPN. Kalau kamu sudah dapat SPPKP, aku siapkan
  perubahannya dan kamu yang menyetujui."
- "PPN-nya 12% dari DPP 11/12, jadi efeknya sekitar 11% dari harga (per 2026). Sistem yang menghitung di tiap faktur."
- "File untuk Coretax bisa diunduh per masa pajak. Unggah dan lapornya tetap dari akun Coretax usahamu."

## Escalate ("ini perlu konsultan pajak")

Whether to register as PKP; which 0% ground applies; fee-only PPN; faktur pengganti or late faktur; luxury goods;
exports and bonded zones; any question about penalties or an audit.
