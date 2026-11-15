# 0061 Pain picks in onboarding, and "Langsung mulai"

Status: proposed (16 Nov 2026) · Owner of the change: rangka-os (this contract, skill `rangka-consult`) · Affects:
rangka-ai (`/v1/interview` request, map ops, consult prompt), rangka-cloud (pick storage, studio card, quick start
build; private) · Amends: 0050 §4 (Raka's agenda for pains the owner picked), 0054 §4 (what follows a saved profile) ·
Builds on: 0042, 0046, 0050, 0053, 0054, 0054a, 0059 · Asked by: Dede, 16 Nov 2026

## Context

- **Dede, 16 Nov 2026:** "pain point usaha (ini boleh skema seperti informasi umum usaha masih tanya jawab), harus
  langsung diarahkan aja 3-5 kesulitan usaha dari tiap core modules, misal karyawan > data karyawan, rekrutmen
  karyawan, gaji, lainnya (free text tapi gk perlu diresponse). Langsung mulai (dengan checkbox optional sample data)
  gk perlu di studio lagi harusnya."
- **What exists.**
  - 0050 puts the owner's problems on the map as `pains` and links them to pieces with `solves`. Raka draws them out in
    an open interview ("Dari semua itu, mana yang paling bikin repot atau rugi?"). The offer card shows "Masalah yang
    terjawab"; `/v1/plan` writes them into stage `answers` for Mulai di sini (0053 §1).
  - 0054 asks the business profile on a card with fixed options. It feeds the plan and price only.
  - The only way to build is the offer: interview → offer → "Setuju, susun" → build → the onboarding frame
    ("Pratinjau usahamu"), from which the owner opens the full studio only by choice.
  - `POST /api/projects/{id}/host` takes `sample_data`; it copies the studio's `fixtures/start.rk` (one outlet and the
    owner). Real demo data is 0052 (Ruang coba), not built yet.
- **The problem.** An open question about problems costs turns and gets "ok" (0059). Owners who already know what
  hurts have no way to say it in one tap, and no way to start without the whole interview.

## Decision

### 1. The options

Four core modules, each with 3–4 fixed difficulties plus "Lainnya". Labels are short topics (Dede's style); the pain
text is what goes on the map, written as the owner would say it. Every option names the catalogue modules
(rangka-apps `modules/`) that answer it; an option with none stays honestly unanswered.

| Group (`module`) | Code | Chip label | Pain text on the map | Answered by |
| --- | --- | --- | --- | --- |
| `karyawan` Karyawan | `staff_records` | Data karyawan | Data karyawan tercatat di banyak tempat | `users` |
| | `staff_hiring` | Rekrutmen karyawan | Rekrutmen karyawan baru masih lewat chat dan berkas | none yet |
| | `staff_pay` | Gaji | Hitung gaji dan slip gaji masih manual | `payroll` |
| | `staff_advance` | Kasbon | Kasbon karyawan susah dilacak | `payroll` |
| `penjualan` Penjualan | `sales_orders` | Pesanan pelanggan | Pesanan dari WA atau telepon sering terlewat | `orders` |
| | `sales_daily` | Rekap penjualan harian | Rekap penjualan dan setoran tiap outlet masih manual | `outlet_sales` |
| | `sales_invoices` | Tagihan | Bikin tagihan atau faktur makan waktu | `invoicing` |
| | `sales_overdue` | Piutang telat | Pelanggan sering telat bayar dan lupa ditagih | `invoicing` |
| `produk` Produk | `product_prices` | Daftar produk dan harga | Daftar produk dan harga tersebar dan beda-beda | `catalogue` |
| | `product_stock` | Stok selisih | Stok di catatan sering beda dengan di rak | `stock` |
| | `product_transfer` | Stok antar cabang | Kiriman stok antar cabang tidak tercatat | `stock` |
| | `product_recipe` | Bahan baku | Pemakaian bahan baku tidak ketahuan | `stock` |
| `pelanggan` Pelanggan | `customer_records` | Data pelanggan | Data pelanggan ada di HP masing-masing | `contacts` |
| | `customer_followup` | Follow up calon pelanggan | Calon pelanggan lupa di-follow up | `pipeline` |
| | `customer_credit` | Batas utang pelanggan | Pelanggan berutang melebihi batas | `contacts` |

- Why these modules: `users` holds the employee record (0056); `payroll` has pay profiles, payslips and `cash_advance`
  (Kasbon); `orders` has customer orders; `outlet_sales` has daily sales and takings per outlet; `invoicing` has
  invoices and `invoice_reminder`; `catalogue` has items and price lists; `stock` has balances, counts, transfers and
  recipes; `contacts` has customers with `credit_guard`; `pipeline` has leads, deals and follow-up activities.
- **Rekrutmen has no module.** It is kept because Dede named it. On the map it stays "belum terjawab" until Raka
  answers it with a custom piece (0033) or a module exists. The build files it as a gap (the same filer as today).
- The module requirements (`payroll` needs `workforce`, `projects`, `invoicing`; `outlet_sales` needs `outlets` and
  `stock`) are added by the build's composition check, as for any map.
- The list lives in rangka-cloud code (`internal/painpicks`) and is served to the studio with the project
  (`pain_options`). rangka-ai validates against the same codes (§4). Changing an option is a change to this contract.

### 2. The stored picks

```json
{"karyawan":  {"picks": ["staff_pay", "staff_advance"], "other": "Karyawan sering pindah outlet tanpa catatan"},
 "penjualan": {"picks": ["sales_overdue"]},
 "produk":    {"picks": []},
 "pelanggan": {"picks": ["customer_records"]}}
```

| Key | Rule |
| --- | --- |
| group | one of the four `module` names of §1; a missing group is the same as `{"picks": []}` |
| `picks` | distinct codes of that group, in any order; stored in §1 order |
| `other` | the owner's words for "Lainnya", trimmed, 1–200 characters (UTF-16 units); absent when empty |

- **Saved** means the owner saved the card at least once with at least one pick or one `other` in total.
- Picks are personal business data like the profile (0054 §2): shown to the owner only, never sent to GitHub or the
  kernel. Kendali does not show them in this contract.
- Cloud stores them on the project (`pain_picks`) with the time they changed.

### 3. Picks on the map (feeds 0050 and 0053)

- Each pick is a map pain with id `n_pick_<code>` and the §1 pain text. Each `other` is a pain with id
  `n_pick_<module>_other` and the owner's words as text (cut to 300). No `stage`, `how_often` or `cost`.
- **Cloud owns these pains.** After every save of the picks and after every consult turn, Cloud puts them back
  exactly: a picked pain missing from the map is added, its text is restored, and an `n_pick_` pain that is no longer
  picked is removed (with its `solves` entries). Raka's own pains keep their place before them.
- At most 15 picks and 4 `other` fit the 0050 limit of 20 pains. When Raka's own pains and the picks together pass 20,
  Raka's newest own pains are dropped first; picks are never dropped.
- Everything downstream is unchanged and now sees the picks: the offer card's "Masalah yang terjawab" (0050 §4), the
  "Masalahmu" panel (0050 §5), and the stage `answers` that `/v1/plan` writes for Mulai di sini (0053 §1).
- The picks never change the plan or the price (0054 holds: a pain never changes the price).

### 4. The interview (rangka-ai)

`/v1/interview` gains one request field.

| Field | Rule |
| --- | --- |
| `pain_picks` | `[{module, code, label, pain}]` for each pick and `[{module, code: "other", text}]` for each `other`, in §1 order; absent = `[]` |

- **Raka's agenda (amends 0050 §4).**
  - With picks, Raka does not ask what is hard ("mana yang paling bikin repot") for a group the owner answered. It
    asks about the flow, and places pieces with `solves` on the `n_pick_` pains.
  - Raka may ask one concrete question about a picked pain only when the answer changes which piece answers it
    ("Kasbonnya dipotong dari gaji bulan itu?"), with `answers` (0059).
  - **"Lainnya" gets no reply.** Raka never answers, quotes or comments on an `other` text in chat. It is data: Raka may
    answer it with a piece, or leave it unanswered on the offer.
  - Raka never names a group's options in chat: the card asks them (the same rule as 0054a §2 for the profile).
- **Map ops.** rangka-ai drops a `put` or `remove` of a pain whose id starts with `n_pick_` (`dropped_ops` reason
  `picked_pain`). Cloud's restore (§3) is the backstop for an older rangka-ai.
- An older rangka-ai ignores `pain_picks`; the pains are still on the map.

### 5. The card (studio)

- **When.** With rangka-ai, in the interview: right after the profile card is saved complete and the picks were never
  saved, Cloud adds Raka's message with card kind `pains` (returned in the profile `PUT` reply). Raka's text: "Sekarang
  pilih yang paling bikin repot di usahamu. Boleh lebih dari satu." "Ubah" on "Masalahmu" in the peta panel adds the
  card again (`POST /api/projects/{id}/pain-picks/card`).
- **Layout.**
  - Title "Kesulitan usaha", sub "Pilih yang paling repot sekarang. Boleh lebih dari satu."
  - One group per module: the group label, its chips (multi-select, ✓ when chosen), and "+ Lainnya". "Lainnya" opens a
    text field: "Tulis singkat. Cukup dicatat, Raka tidak akan membahasnya."
  - Footer:
    - checkbox "Isi data contoh" (off by default), with the hint "bisa dihapus nanti";
    - primary "Langsung mulai" (§6);
    - secondary "Lanjut ngobrol dengan Raka".
  - Both buttons save first and are disabled until at least one chip or one "Lainnya" is filled. "Langsung mulai"
    also needs at least one pick answered by a module; otherwise it is disabled with "Pilih minimal satu yang bisa
    langsung dijawab sistem."
  - Under 600 px the groups stack; chips wrap; buttons at least 44 px high.
- **Saving.** `PUT /api/projects/{id}/pain-picks` with the §2 shape. 200 `{pain_picks, peta}`; 400 `bad_pains` for a
  broken §2 rule; 404 `not_found`. "Lanjut ngobrol" then posts the 0054a chat action `profile_saved` when the profile
  save has not been answered yet, so Raka answers the profile and the picks in one turn. A later edit posts nothing.
- The picks are optional for the offer path in this contract: an owner who skips the card can still talk with Raka,
  get an offer and build (open for Dede below).

### 6. "Langsung mulai" (quick start)

- **What.** The chat action `quick_start` (or `quick_start:sample` with "Isi data contoh") builds the business at once
  from the profile and the picks, with no offer and no more interview. The owner's line reads "Langsung mulai" (or
  "Langsung mulai dengan data contoh").
- **When it is refused.**
  - Outside the interview: 400 `bad_request`.
  - With rangka-ai and a profile that is not complete: 409 `profile_incomplete` with the profile card (0054 §4).
  - Picks not saved, or no pick answered by a module: 409 `pains_missing` with the pains card and "Pilih dulu
    kesulitan yang mau dijawab."
- **The map it builds** (Cloud code, deterministic, no LLM):
  - the §3 pains;
  - for each module that answers a pick, one `catalogue` piece (`p_pick_<module>`, `ref` the module, label from the
    catalogue, `solves` its picks). A catalogue piece already on the map with the same `ref` gets the `solves` instead;
  - for each group with a mapped pick, one stage `s_pick_<module>` labelled with the group ("Karyawan", …) listing its
    pieces, unless the map already has a stage that lists them;
  - the business kind from the profile's industry label when the map has none.
  - The result is validated like any map (0042 §1). A pending accepted offer is cleared: the picks decide.
- **The build.** The same build as "Setuju, susun": the composition from the map, `/v1/plan` with rangka-ai (so stage
  `hint` and `answers` exist for Mulai di sini), then the onboarding frame "Pratinjau usahamu", never the full studio.
- **Isi data contoh.** Stored on the project (`sample_data: true` in the project view). Today it pre-ticks "Pakai data
  contoh" in the hosting form, so `POST …/host` sends `sample_data: true`. When 0052 ships, the same flag fills the
  Ruang coba with the matching demo pack right after the build instead.
- **Price.** Unchanged: the plan card and hosting still use the 0054 §5 suggestion.

## Consequences

- **rangka-ai.** `pain_picks` in the request (validated against §1, unknown codes dropped), the prompt block of §4,
  `dropped_ops` reason `picked_pain`. Tests.
- **rangka-cloud.** `internal/painpicks` (options, parse, map restore, quick map) with tests; `pain_picks` and
  `pain_options` on the project view; `PUT …/pain-picks`, `POST …/pain-picks/card`; chat actions `quick_start` and
  `quick_start:sample`; the pains card after a complete profile save; the restore after each consult turn; the studio
  card, the receipt and the pre-ticked hosting checkbox; e2e in `tests/e2e/test_studio_consult.py`.
- **rangka-os.** This contract; skill `rangka-consult` §2 gains the picked-pain rule.
- **Board.** Figma "Rangka OS" › "Rangka Cloud" needs a board "Kesulitan usaha di onboarding · usulan" (card, narrow
  view, quick start). Not drawn yet.

## Open for Dede

1. Should the picks be required before any build, like the profile? This contract keeps them optional for the offer
   path and required only for "Langsung mulai".
2. Should "Langsung mulai" also host right away for a signed-in owner (subdomain from the business name, the suggested
   package), instead of landing in "Pratinjau usahamu"? Hosting creates a business and takes a seat, so this contract
   stops at the preview.
3. Rekrutmen has no module. Keep it as an honest "belum terjawab", or drop it until a module exists?
4. Sample data is thin until 0052 ships (one outlet and the owner). Build 0052's demo packs next, or copy rangka-apps
   `examples/fixtures/demo-*` into the build as a stopgap?

## Out of scope

- Weighing picks (most painful first) and per-pick frequency or cost.
- Showing picks to sales in Kendali.
- New modules for recruitment.
