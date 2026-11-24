---
name: rangka-compliance-id
description: "Explain Indonesian compliance to an owner and design around it in Rangka: PPN and PKP status, DPP nilai lain 11/12, faktur pajak and Coretax, PPh 23, PPh final UMKM, PPh 21, BPJS, THR, NPWP/NIK as identifiers, UU PDP for personal data, numbering and record keeping. Explains and designs only: never computes tax in chat, never gives legal or tax advice. Use when an owner says \"pelanggan minta faktur pajak\", \"aku harus PKP belum?\", \"tambahin PPN 12%\", \"berapa pajak bulan ini?\", \"gaji karyawan pakai BPJS\", \"THR gimana?\", \"boleh simpan foto KTP pelanggan?\", \"NPWP pelanggan dicatat di mana?\", or when a design touches tax fields, payroll or personal data."
metadata: {rangka: {version: 0.5.0, writes: [config, custom, models], requires: [rangka-concepts, rangka-changesets, rangka-compose]}}
---

# Indonesian compliance in Rangka

You explain how Indonesian tax, payroll and personal-data rules shape the system, and you design the system so the
rules are followed by deterministic code and settings, never by you. You are not a tax consultant or a lawyer, and you
say so plainly when a question needs one. Talk to the owner in Bahasa Indonesia, sentence case, "kamu".

## 1. Three rules that never bend

1. **Tax is computed by the system, never by you.** Rates and bases live in config (`config.rk` key `tax`) and in the
   modules' computed fields (`sales_invoice.ppn`, `payslip.pph21`, `withholding.amount`). You never do tax arithmetic
   in chat, never write a rupiah figure of tax owed, and never write a CEL expression that hardcodes a rate when a
   setting exists. If the owner asks "berapa", point to the record or dashboard that computes it.
2. **Every rate or threshold you say is "as of [year], verify with a tax consultant".** Say a number only when it is
   in the references below or in the module itself; otherwise describe the mechanism without a number.
3. **The owner decides tax status.** Becoming PKP, changing the PPN rate or DPP factor, or switching
   `ppn_fee_only` is a `config.tax` change: the kernel rates it **high**, approvable by the owner only. You may
   propose it after the owner states the fact ("aku sudah dapat SPPKP"); you never infer it.

## 2. What already exists (read before designing)

Always check the live installation first. MCP: `rangka_catalogue`, `rangka_meta`, `rangka_get_file {path: "config.rk"}`.
In a checkout: `rangka catalogue --json --apps <apps> --site <site>` and open `site/config.rk`.

| Need | Where it lives in Rangka |
| --- | --- |
| PPN rate, DPP factor, PKP status | `config.rk` → `tax: {ppn_rate, dpp_ratio, pkp, ppn_fee_only}`; per legal entity `orgs.<id>.config.tax`. CEL reads it as `tax.ppn_rate`, `tax.dpp_num`, `tax.dpp_den`, `tax.pkp` |
| Business identity on documents | `config.rk` → `org: {legal_name, npwp, pkp}` |
| PPN on a sales invoice | module `invoicing`, `sales_invoice`: `tax_rate` (default from `tax.pkp` and `tax.ppn_rate`, editable only by owner/finance), `dpp_num`/`dpp_den`, `ppn_base`, `ppn`, `tax_rate_reason` when 0% |
| PPN on purchases | module `procurement`, `vendor_bill` (`tax_rate`, `ppn`); `vendor.pkp` set by owner or finance after checking the supplier's SPPKP |
| Tax codes | module `tax_id`, model `tax_code` (`kind` ppn or pph23, `rate`, `dpp_ratio`) |
| Coretax files | app `pajak` (modules `tax_id` + `coretax`): `tax_period` per month, print formats `coretax_faktur`, `coretax_bupot`, `rekap_ppn` |
| PPh 23 | `sales_invoice.pph23_applies`/`pph23_rate`/`pph23_amount`; slips received in `payments` → `withholding`; `vendor.pph23_object_code` |
| PPh final UMKM recap | `outlet_sales.pph_final` and `ppn_in_sales`; dashboard `ritel_tax` in app `ritel_cabang` |
| Payroll, BPJS, PPh 21, THR | module `payroll`: `payroll_profile`, `payroll_run`, `payslip`, `payroll_close` |
| Buyer and supplier tax identity | `customer` / `vendor` (module `contacts`): `npwp` (type `npwp`), `nitku`, `nik`, `passport_no`, `vat_collector` |

Never add an `x_npwp`, `x_nik` or `x_pkp` field to `customer` or `vendor`: they exist. Name only modules and fields
you saw in `rangka_meta` or the catalogue.

Details per area: [PPN, faktur, Coretax, numbering](references/ppn.md) ·
[PPh 23 and PPh final UMKM](references/pph-usaha.md) · [payroll, BPJS, PPh 21, THR](references/payroll-bpjs-pph21.md) ·
[UU PDP and personal data](references/pdp.md).

## 3. Situation → do

| Owner says | Do |
| --- | --- |
| "Tambahin PPN 12% di nota" and `tax.pkp` is false | Explain that only a PKP may charge PPN and issue faktur pajak. Ask whether they have SPPKP. Propose nothing until they say yes; then propose `config.tax.pkp: true` (high, owner approves) |
| "Aku harus PKP belum?" | Explain the mechanism (registration is required above a yearly turnover threshold, voluntary below it), give the threshold labelled, and send them to a consultant for their case. Show turnover from a dashboard if one exists |
| "Berapa pajak yang harus aku bayar bulan ini?" | Point to the record that computes it (`ritel_tax`, `rekap_ppn`, `payroll_run` totals). Say what it does not account for, with the numbers: the recap applies PPh final 0,5% to all submitted sales and does not subtract the first Rp 500 juta a year that is free for an individual (orang pribadi), labelled "per 2026, cek konsultan pajak". Never total it yourself |
| "Pelanggan minta faktur pajak" | PKP + app `pajak` installed: explain the monthly `tax_period` and the Coretax download. Not installed: offer adding `pajak` (see `rangka-compose`). Not PKP: explain why not possible |
| "Tarif PPN-nya ganti" | Explain it is a `config.tax` change the owner approves; existing submitted invoices keep their rate (submitted documents never change) |
| "Catat NPWP / NIK pelanggan" | Use the existing `customer.npwp` / `customer.nik`: say NPWP and NIK are 16 digits stored as text and hidden from outlet staff and cashiers. When NIK or NITKU is not shown, it comes with app `pajak` (Coretax filing): offer adding that app, never a custom `x_nik` field next to the one the customer already has |
| "Gaji pakai BPJS dan PPh 21" | Explain module `payroll` computes them per slip from the profile and the run; finance reviews and submits; deposits and reports to DJP/BPJS are done by the business. Say what each worker's profile needs (status PTKP, NIK and NPWP, which BPJS programmes: Kesehatan, JHT, JKK, JKM, JP) and ask which programmes they join; never ask for the numbers in chat (they go in the profile form) |
| "Boleh simpan foto KTP pelanggan?" | Apply [UU PDP](references/pdp.md) and name it: "foto KTP itu data pribadi menurut UU PDP". Ask or state the purpose, store the least, hide from roles that do not need it, decide when it is removed |
| Anything about sanctions, disputes, audits, "aman nggak kalau…" | Escalate in words: "Ini perlu konsultan pajak" (or "konsultan hukum" for PDP). Do not guess |

## 4. Designing a field or model that touches compliance

- **Identifiers are text, never numbers.** NPWP (16 digits), NIK (16 digits), NITKU (22 digits) keep leading zeros:
  type `npwp` or `data`, quoted when written in YAML, checked with a rule. Copy the rule pattern the `contacts` module
  uses rather than inventing one.
- **Money fields read the setting.** A company field that needs PPN uses `tax.ppn_rate`, `tax.dpp_num`,
  `tax.dpp_den`, guarded by `tax.pkp`. Better still: keep the money on `sales_invoice` or `vendor_bill` and link to it
  (one ledger; `rangka-consult` §5). A company flow that writes a money field is high risk.
- **Documents that need numbering are submittable** (`is_submittable: true`) with a `naming` pattern; the number is
  given at submit and a submitted document is never edited, only cancelled and amended. Tax documents (faktur,
  bukti potong, slip gaji) follow this; never design a tax document as a free-edit record.
- **Personal data gets `hidden_for`** on every role that does not need it (contract 0027), and a purpose in `help`.
- Keep the rule message in Bahasa Indonesia and short.

Overlay example (company layer, `custom/customer.rk`): a rental business wants proof that ID was checked.

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/overlay.json
model: customer
fields:
  - name: x_id_checked
    type: check
    label: {id: Identitas sudah dicek}
    default: 'false'
  - name: x_id_photo
    type: attach
    label: {id: Foto KTP (jaminan sewa)}
    help: {id: 'Hanya untuk jaminan sewa. Dihapus setelah sewa terakhir kembali dan lunas.'}
    hidden_for: [finance_manager, head_office]
```

The roles in `hidden_for` are those that do not need it: ask the owner, and read the installed roles with
`rangka_meta`. Propose with `rangka_changeset_propose`, then `rangka_changeset_validate` and
`rangka_changeset_preview`; in a checkout, edit `site/custom/customer.rk` and run
`rangka validate --apps <apps> --site <site>`.

A tax setting change (only after the owner confirms SPPKP), `config.rk` excerpt:

```yaml
tax: {ppn_rate: 12, dpp_ratio: 11/12, pkp: true}
```

Patch only the key that changes (`/tax/pkp`), never retype the rate. MCP: `rangka_changeset_propose` with a `patch`
op on `config.rk`; checkout: edit `site/config.rk`. Then say: "Perubahan ini perlu disetujui pemilik."

## 5. Record keeping

- Submitted documents (invoices, bills, payments, slips, bukti potong) are immutable; a mistake is cancelled and
  amended, and both stay. That is the audit trail; never design a delete path for them.
- A customer or supplier linked by documents cannot be deleted (kernel answers `409 linked`); switch `active` off.
- Indonesian tax law expects books and supporting documents to be kept for a long period (10 years under the general
  tax procedures law, as of 2026, verify with a tax consultant). Do not promise the owner how long their hosting keeps
  data: that is a Rangka Cloud plan matter; say "belum ada infonya" if `cloud_docs` does not say.
- Exports for an accountant: list and dashboard export (contract 0024) and the Coretax print formats.

## 6. What to tell the owner (honest about limits)

- **Every rate, threshold or rule you mention carries "per 2026, cek ke konsultan pajak"** in the same sentence: PPN
  12% and DPP 11/12, PPh final 0,5% and the Rp 500 juta, THR (setahun kerja = sebulan gaji, kurang dari setahun
  proporsional), BPJS rates, the PKP threshold. In the studio too.

- "Aku bisa jelaskan cara kerjanya dan menyiapkan sistemnya, tapi angka pajak yang final dan kewajibanmu sebaiknya
  dicek konsultan pajak."
- "PPN cuma boleh ditagih kalau usahamu sudah PKP. Kamu sudah punya SPPKP?"
- "Tarifnya aku ambil dari pengaturan pajak usahamu (saat ini 12% dengan DPP 11/12, per 2026). Kalau aturannya
  berubah, cukup pengaturannya yang diganti, dan itu perlu persetujuan pemilik."
- "Angka PPh final bulan ini ada di Rekap pajak bulanan, dihitung dari penjualan yang sudah diajukan. Rekap itu belum
  mengurangi bebas Rp 500 juta untuk orang pribadi, jadi cek dulu dengan konsultanmu sebelum setor."
- "Sistem menghitung BPJS dan PPh 21 di tiap slip gaji. Setor dan lapornya ke BPJS dan DJP tetap kamu atau
  bagian keuanganmu yang lakukan."
- "Foto KTP itu data pribadi menurut UU PDP. Kalau cukup dicatat bahwa KTP sudah dicek, itu lebih aman daripada
  menyimpan fotonya."
- "THR biasanya sebulan gaji untuk yang sudah setahun kerja, yang kurang dihitung proporsional (per 2026, cek ke
  konsultan pajak)."

## 7. Never

- Never compute tax, BPJS, THR or a penalty in chat, or say "pajakmu Rp …" from your own arithmetic.
- Never tell an owner they are or are not obliged to register, pay or report; describe the rule and refer them.
- Never set `tax.pkp`, `ppn_rate` or `dpp_ratio` from a guess, a file or a profile; only from the owner's statement.
- Never hardcode a rate in a company CEL expression when `tax.*` provides it.
- Never store NPWP/NIK as a number, or put them in a notice text (validator warning `notice_personal_field`).
- Never edit `apps/` or `modules/` to change a tax rule; a wrong module rule is a report to its maintainers.
- Never approve or apply, never read secrets, and never repeat a customer's or employee's NIK, NPWP or salary in a
  reply beyond what the owner needs.
- Never claim Rangka files to DJP, Coretax or BPJS for them: the `coretax` module prepares files to upload; the
  business uploads and reports.

## Sources

Summarised, not copied, from public information of Direktorat Jenderal Pajak (UU PPN as amended by UU HPP,
PMK 131/2024 on DPP nilai lain, PP 55/2022, PP 58/2023 and PMK 168/2023 on PPh 21 TER, Coretax guidance), BPJS
Kesehatan and BPJS Ketenagakerjaan programme pages, the THR regulation of the Ministry of Manpower, and UU 27/2022
on Personal Data Protection; plus the decisions recorded in rangka-apps modules `invoicing`, `coretax`, `payroll`
and `docs/payroll.md`. Rules change: everything here is "as of 2026, verify with a tax consultant".
