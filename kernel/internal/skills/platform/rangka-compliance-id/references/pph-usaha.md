# PPh 23 and PPh final UMKM

Every number here is **as of 2026, verify with a tax consultant**.

## PPh 23 (services, rent and similar)

- When a business pays another business for certain services or rent, the payer **withholds** PPh 23 and gives the
  supplier a bukti potong. The common rate for services is 2% of the base; it is higher when the recipient has no
  NPWP. The object codes (kode objek pajak) decide which payments are in scope.
- **As the seller** (your customer withholds): `sales_invoice.pph23_applies`, `pph23_rate`, `pph23_amount` show what
  the customer will hold back, so `amount_due` is lower. When the slip arrives, finance records it in
  `withholding` (module `payments`); its `difference` shows a gap with the invoice. Lines marked `pph23_exclude`
  (for example workers' wages billed on their own line) stay out of the base.
- **As the buyer** (you withhold): `vendor_bill` carries `pph23_amount` and the vendor's `pph23_object_code`; the
  `coretax` module prepares the unified withholding file (`coretax_bupot`).
- Rates are in `tax_code` records (`kind: pph23`) and on the documents' default; finance changes them, not you.

## PPh final UMKM

- A small business (turnover up to Rp 4,8 miliar a year) may pay a final 0,5% of gross turnover each month (PP
  55/2022) instead of the normal income tax. An individual's first Rp 500 juta of yearly turnover is exempt. The
  scheme can be used only for a limited number of years that depends on the legal form: ask a consultant, do not
  quote years.
- Rangka shows it, it does not decide it: `outlet_sales.pph_final` and `pos_sale` compute 0,5% of sales without PPN;
  dashboard `ritel_tax` (app `ritel_cabang`, role `head_office`) sums it per period. The recap does **not** subtract
  the Rp 500 juta exemption and the 0,5% is written in the module, not a setting. Say both when you point to it.
- A business above the threshold, or one that chose normal bookkeeping, ignores this figure; that choice is the
  consultant's.

## Owner phrasing

- "Pelangganmu yang memotong PPh 23, jadi uang yang masuk lebih kecil dari total faktur. Simpan bukti potongnya di
  sistem supaya selisihnya kelihatan."
- "Angka PPh final 0,5% ada di Rekap pajak bulanan. Itu belum dikurangi bebas Rp 500 juta, jadi cek dulu sebelum
  setor."

## Escalate

Which object code applies; whether a payment is in scope of PPh 23; whether the business may still use PPh final;
switching from PPh final to normal bookkeeping; year-end income tax (SPT Tahunan).
