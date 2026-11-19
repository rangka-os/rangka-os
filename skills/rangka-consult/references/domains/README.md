# Domain packs: how each business type runs

One file per business type Juragan names on the landing, in its order. The file names are the `industry` codes of the
business profile (contract 0054 §1), so the profile tells you which pack to read:

| `industry` | Owner's words | Pack | Main app in the catalogue today |
| --- | --- | --- | --- |
| `outsourcing` | Outsourcing, penyalur tenaga kerja, jasa manpower | [outsourcing.md](outsourcing.md) | `jasa_proyek` |
| `rental` | Rental alat, sewa kamera, sewa alat pesta | [rental.md](rental.md) | `sewa_alat` |
| `training` | Kursus, pelatihan, bimbel, les | [training.md](training.md) | `kelas_kursus` |
| `subscription` | Layanan langganan, RT/RW net, jasa bulanan, penyedia aplikasi | [subscription.md](subscription.md) | `langganan_jasa` |
| `fnb` | F&B, kedai, kafe, restoran, cabang makanan | [fnb.md](fnb.md) | `fnb_cabang`, `kasir` |

Demo sites named in the packs (`demo-sewa`, `demo-kursus`, …) live in rangka-apps `examples/sites/<name>`.

`retail`, `wholesale` and `production` have apps (`ritel_cabang`, `grosir_distribusi`, `produksi_pabrik`) but no pack
yet: work from their `rangka_catalogue` summaries.

## How to use a pack

- **It is background, not a script.** A pack tells you what usually happens in this kind of business so you can ask
  the right next question. The owner's story always wins: if they bill weekly and the pack says monthly, the map says
  weekly. Never put anything on the map because the pack says it is common (consult §1, §3: data comes only from what
  the owner said).
- **Read it once, early.** When the profile's `industry` is known, or the story makes it obvious ("aku sewain kamera"),
  read the pack, then keep following the owner's last answer. One question per turn still holds.
- **Map vocabulary.** Each pack has a table "Words owners use". Use the owner's word in the stage `label`, `actor` and
  `documents`; use the Rangka concept only for the piece (`ref`) and in your own reasoning.
- **Catalogue first, always.** The packs name apps and modules that existed in rangka-apps on 19 Nov 2026. The
  catalogue changes: call `rangka_catalogue` and place only what it lists today. If a pack names something the
  catalogue no longer has, trust the catalogue.
- **App skills beat packs.** When `rangka_skills` lists an app skill (`rangka-app-kasir`), load it with `rangka_skill`;
  it decides placement for that app.
- **Pains.** Each pack lists common pains and, where they exist, the matching pain-pick code of contract 0061
  (`staff_pay`, `sales_overdue`, …). Picked pains sit on the map as `n_pick_…` and are answered with `solves`, never
  asked again. A pain the pack lists but the owner never said is not a pain on the map.
- **Owner-facing words.** Replies never say module, connector, catalogue or an app's internal name (consult §7). The
  example sentences in the packs are already in the owner's words.
- **Numbers.** Tax, BPJS, THR and wage figures change by regulation and by region. The packs give none; the payroll
  and tax modules keep their own rates per period. When an owner asks a rate, say it is set per period in the system
  and that the exact figure is "perlu dicek dengan konsultan pajak". Never state a rate yourself.
- **Honest "not yet".** Each pack ends with red flags: businesses where Rangka is not a fit yet. Say so in the owner's
  words and keep the gap as `open` or `not_covered`, never as a promise.

## Shared patterns across the five

| Pattern | Where it lives | Notes |
| --- | --- | --- |
| Invoice and receivable | `invoicing`, `faktur_piutang` | every pack ends in an invoice; `invoice_reminder` and `whatsapp_reminder` chase late payers |
| Money in from the bank | `bank_statement` (`mutasi_bank`) | import the statement, match to invoices |
| Online payment | `payments` with `xendit`, `midtrans`, `doku` connectors, Cloud service `payment_gateway` | read `cloud_doc` before saying anything about fees |
| WhatsApp to customers | `whatsapp` + `whatsapp_reminder`, `whatsapp_booking`, `whatsapp_receipt`, `whatsapp_order_ready`; connectors `whatsapp_business` and the Cloud-provided WhatsApp number connector (check the exact name in `rangka_catalogue`) | if `connectors_run` is false, say messages are not sent automatically yet |
| Staff attendance | `attendance` (`absen_karyawan`) for outlet staff; `projects` field attendance for placed workers | different modules: outlet vs client site |
| Books and tax | `accounting` (`pembukuan`), `coretax` (`pajak`), `tax_id` | owner approves; figures verified by a tax consultant |

## Sources

Public knowledge of how Indonesian SMEs in these sectors operate (industry associations' public material, common
practice of service, rental, course, ISP and F&B businesses), mapped onto the rangka-apps catalogue of 19 Nov 2026 and
contracts 0042, 0050, 0054 and 0061. No text copied from any source.
