# UU PDP (Law 27/2022) and personal data in Rangka

Summary for design, **not legal advice**; as of 2026, verify with a lawyer for anything specific.

## The mechanism

- UU 27/2022 on Personal Data Protection applies to every business that processes personal data of people in
  Indonesia, small ones included. Its transition period has ended.
- Processing needs a lawful basis: consent is one; others include performing a contract with the person and a legal
  obligation (tax records, for example).
- Principles: collect for a clear purpose, collect only what that purpose needs, keep it accurate, secure it, keep it
  no longer than needed, and be able to show what you did.
- People have rights: to know, access, correct, and ask for deletion; to withdraw consent.
- Some data is **specific** (more protected): health data, biometric data, children's data, personal financial data,
  criminal records, among others.
- A breach must be notified to the people affected and the authority quickly (within 3 × 24 hours, as of 2026).

## What it means in Rangka

| Principle | Design in Rangka |
| --- | --- |
| Purpose | Every personal field has a `help` saying why it is kept ("Untuk faktur pajak pembeli orang pribadi") |
| Minimise | Prefer a check field ("KTP sudah dicek") over storing a KTP photo; store NIK only when a document needs it (e-Faktur for a buyer without NPWP) |
| Access by role | `hidden_for` on every role that does not need the field (contract 0027); row conditions (`permissions` with `if:`) so staff see only their own records, as `staff_attendance` does |
| No leaks in messages | Keep contact and identity fields out of notice texts (validator `notice_personal_field`); never repeat them in Raka's replies |
| Retention | Agree with the owner when the data is no longer needed and record it in the field's `help`; submitted tax documents are kept as the tax law requires, personal extras (photos) are removed when the purpose ends |
| Consent | When consent is the basis (marketing WhatsApp, for example), record it: `customer.wa_opt_out` exists for WhatsApp; a company `x_` check field with a date for others |
| Rights | A correction is an edit of a draft or master record; a submitted document is cancelled and amended. A deletion request on data tied to tax documents is a lawyer or consultant question |

Existing protections to point to rather than rebuild: `customer.npwp`/`nik`/`passport_no` hidden from outlet staff
and cashiers; employee `phone`/`email` visible to owner and head office only (contract 0056); payroll identity only
for finance; attendance selfies and positions readable only by the worker, their outlet supervisor and head office.

## Raka's own handling

- Never ask the owner to paste customers' or employees' NIK, NPWP, photos or salaries into chat. Data goes in through
  forms and imports.
- Raka's memory of the business needs separate consent (contract 0047); never store personal data of customers or
  employees in it.

## Owner phrasing

- "Foto KTP termasuk data pribadi. Kalau tujuannya cuma memastikan penyewa asli, cukup dicentang 'KTP sudah dicek'.
  Kalau tetap mau simpan fotonya, aku buat hanya bisa dilihat yang perlu, dan kita sepakati kapan dihapus."
- "NIK pelanggan cuma perlu kalau dia minta faktur pajak dan tidak punya NPWP."
- "Soal kewajiban hukumnya secara pasti, sebaiknya tanya konsultan hukum. Aku bantu dari sisi sistemnya."

## Escalate ("ini perlu konsultan hukum")

Whether a lawful basis other than consent applies; deletion requests on data kept for tax; health or children's
data; data leaving Indonesia; a suspected breach (tell the owner at once, then a lawyer).
