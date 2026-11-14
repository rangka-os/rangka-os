# 0054 Business profile in onboarding: the plan comes from the profile

Status: proposed (14 Nov 2026) · Owner of the change: rangka-os (this contract) · Affects: rangka-ai (`/v1/interview`
request and reply, consult prompt), rangka-cloud (profile storage, studio profile card, plan and offer-kind
suggestion, offer card, build gate, Kendali "Calon pelanggan"; private) · Builds on: 0042 (consultant interview),
0046 (offer), 0050 (pains), 0041 (prices from Cloud docs) · Implemented together with: 0050

## Context

- **Two tracks (Dede, 14 Nov 2026).** Raka works for two things.
  - **The system it builds (rangka-os).** It starts from the owner's problems and answers them with pieces on the
    map. That is 0042, 0046 and 0050.
  - **The plan, the price and the sales team (rangka-cloud).** It uses a short business profile with fixed options.
    That is this contract.
  - The profile never puts a piece on the map. A pain never changes the price.
- **Design.** Figma "Rangka OS" › page "Rangka Cloud" › board "Profil usaha di onboarding · usulan" (node
  47:143366): profile card in the interview (A), "Lainnya" with Raka digging (B), plan and offer card (C), Kendali
  "Calon pelanggan" (D), mapping (E), rules (F).
- **Decisions by Dede (14 Nov 2026)** that replace the board's proposals:
  1. The plan is **not** chosen by scale alone. Scale, number of outlets, staff, customers, products and sales
     channels all count. The price stays per business: outlets and staff are package **limits** in
     `config/tiers.yaml`, so they can call for a bigger package but never add a charge per person or per head.
  2. The profile is **required**. Every question must have an answer the owner confirmed before the build starts.
     There is no "Lewati dulu". The chat itself is never blocked.
  3. The plan and the offer kind are computed by Cloud code, never by the LLM.
  4. 0050 (pains and `solves`) ships in the same rangka-ai and rangka-cloud PRs as this contract.
  5. "Jumlah outlet" is its own question, next to scale.
  6. "Perkiraan penjualan per bulan" is a question too. It counts for the plan and ranks leads for sales.
- **What exists today.**
  - `store.Project.Profile` holds legacy interview answers and `consultBody.profile` is the example profile name
    ("default"). Neither is this profile, so the new field is named `business_profile` everywhere.
  - `config/tiers.yaml` has, per package: `outlets` and `users` limits (0 = no limit), `businesses`, the monthly Raka
    turns, and `price.per_month`. The ordered packages for new businesses are `mulai`, `juragan`, `pro`, `grup`;
    `enterprise` is sized per contract.
  - Staff accounts (`internal/staff`) have no roles yet.

## Decision

### 1. The questions

Eight questions. Each has fixed options plus "Lainnya", except `outlets`, which is a number.

| Field | Question (card copy) | Hint | Codes → labels | Kind |
| --- | --- | --- | --- | --- |
| `industry` | Jenis usaha | | `outsourcing` Outsourcing · `rental` Rental alat · `training` Kursus & pelatihan · `subscription` Layanan langganan · `fnb` F&B · `retail` Toko ritel · `wholesale` Grosir · `production` Produksi | one |
| `scale` | Skala usaha | | `starting` Baru mulai · `one_location` 1 lokasi · `branches_2_5` 2–5 cabang · `branches_over_5` Lebih dari 5 cabang · `multi_business` Beberapa usaha | one |
| `outlets` | Jumlah outlet | cabang atau lokasi tempat kamu melayani | integer 1–999 | number |
| `sales_channels` | Cara jualan | boleh lebih dari satu | `offline_outlet` Offline · toko/outlet · `offline_visit` Offline · datang ke klien · `offline_sales` Offline · tim sales · `online_social` Online · WhatsApp/medsos · `online_marketplace` Online · marketplace · `online_website` Online · website | many |
| `customers` | Pelanggan | aktif per bulan | `under_50` Kurang dari 50 · `to_200` 50–200 · `to_1000` 200–1.000 · `over_1000` Lebih dari 1.000 | one |
| `staff` | Staf | termasuk petugas lapangan | `solo` Sendiri · `to_5` 2–5 · `to_20` 6–20 · `to_100` 21–100 · `over_100` Lebih dari 100 | one |
| `products` | Produk/layanan | yang kamu jual | `to_10` 1–10 · `to_50` 11–50 · `to_500` 51–500 · `over_500` Lebih dari 500 | one |
| `revenue` | Perkiraan penjualan | per bulan | `under_10m` Kurang dari Rp 10 juta · `to_50m` Rp 10–50 juta · `to_250m` Rp 50–250 juta · `to_1b` Rp 250 juta–1 miliar · `over_1b` Lebih dari Rp 1 miliar | one |

- The `industry` codes follow the landing's business types, in the same order, and add the three the board adds.
- Every field but `outlets` accepts `other` ("Lainnya") with the owner's own text.
- `outlets` is prefilled from `scale` when empty: `starting` and `one_location` give 1. The owner still confirms it.

### 2. The stored profile

```json
{
  "industry":       {"value": "outsourcing", "source": "raka_guess", "confirmed": false},
  "scale":          {"value": "one_location", "source": "owner", "confirmed": true},
  "outlets":        {"value": 1, "source": "owner", "confirmed": true},
  "sales_channels": {"value": ["offline_visit", "other"], "text": "Tender dan referensi pengelola gedung",
                     "note": "Klien minta surat penawaran sebelum kontrak", "source": "owner", "confirmed": true},
  "customers":      {"value": "under_50", "source": "raka_guess", "confirmed": false}
}
```

| Key | Rule |
| --- | --- |
| field | one of the eight names; a missing field is unanswered |
| `value` | a code of the field; for `sales_channels` a list of 1–7 distinct codes; for `outlets` an integer 1–999 |
| `text` | the owner's words for `other`, kept as written; required when `value` is or contains `other`, else absent; 1–200 characters |
| `note` | Raka's one-sentence summary after digging into `other`; at most 300 characters; optional |
| `source` | `owner` (the owner clicked or typed) or `raka_guess` (Raka read it from the story or a file) |
| `confirmed` | `true` only after the owner clicked a chip or saved the card; a `raka_guess` is never confirmed by itself |

- **Complete** means all eight fields are present and confirmed.
- A guess never replaces a confirmed field. A new guess replaces an older unconfirmed guess.
- Changing a confirmed answer is allowed at any time (§5). The paid package does not change by itself.
- The profile is personal business data under UU PDP. It is shown only to the owner and to staff with the `sales`
  role (§7), and it is never sent to GitHub or to the kernel.

### 3. The interview (rangka-ai)

`/v1/interview` (0042 §3) gains one request field and two reply fields.

**Request**

| Field | Rule |
| --- | --- |
| `business_profile` | the stored profile (§2), `{}` when there is none; absent = `{}` |
| `profile_dig` | field names whose answer is `other`, confirmed, without a `note`, and dug into fewer than 2 times; absent = `[]` |

**Reply**

| Field | Rule |
| --- | --- |
| `profile_guesses` | `{field: {value, text?}}`: Raka's reading of the story and files, for fields that are not confirmed; always emitted, `{}` when none |
| `profile_notes` | `{field: note}`: a summary of an `other` answer Raka dug into this turn; always emitted, `{}` when none |

- rangka-ai validates both against §1 and §2. A guess with an unknown field or code, a bad number, or `other`
  without `text` is dropped silently. So is a guess for a field that is confirmed in the request. A note for a field
  that is not `other` is dropped.
- **Raka's agenda.**
  - After the owner's first story, Raka fills `profile_guesses` with what the story says. It does not ask the
    profile questions one by one: the card asks them.
  - The profile is context for the next questions, never a source of pieces. Online sales point Raka at marketplace
    problems; 21 staff or more points it at attendance and payroll. Nothing goes on the map because of the profile.
  - For a field in `profile_dig`, Raka asks one question about it, offers two short answers when it can, and returns
    a `note` once it understands. At most two questions per answer (Cloud counts them).
  - Raka never names a package or a price from the profile. Prices come from `cloud_docs` only (0041), and the
    plan card (§6) is the place for them.
- The reply and map rules of 0042, 0046 and 0050 are unchanged.

### 4. The profile card (studio)

- **When.** Cloud adds a message with card kind `profile` after the first consult turn of a project, and again
  whenever the owner opens "Ubah" on the profile in the peta panel. Raka's text above it: "Biar paket dan harganya
  pas, aku isi dulu profil usahamu dari cerita tadi. Cek ya: yang bergaris putus-putus itu tebakanku."
- **Layout** (board A and B).
  - Title "Profil usaha", status line, and "Pilih yang paling dekat. Tidak ada yang pas? Pilih Lainnya dan
    ceritakan."
  - Status line: "<n> tebakan · <m> belum diisi" while incomplete; "Semua terisi" when complete, with
    " · <k> isian sendiri" when some answers are `other`.
  - One row per question: label, hint, chips, and "+ Lainnya". `outlets` is a number field with − and + buttons.
  - A guessed chip has a dashed border until the owner clicks it; a chosen chip shows ✓.
  - "Lainnya" opens a text field in its row: "Pakai kata-katamu sendiri. Enter untuk simpan, Raka akan tanya lanjut."
  - Footer: primary "Simpan profil", disabled until every row has an answer; and the note "Bisa diubah lagi di Peta
    alur". No "Lewati dulu".
  - Under 600 px: one question per screen, chips wrap downwards, buttons at least 44 px high.
- **Saving.** `PUT` on the project's profile in the studio API with the whole profile. Every field the owner sent
  becomes `source: "owner"`, `confirmed: true`. Fields the owner did not touch keep their guess unconfirmed.
  Errors are `bad_profile` (a §2 rule broken) and `not_found`.
- **Peta panel.** Below the stages: "Profil usaha" with "Saran: <package>" when complete, one line per field (label
  and answer, " · tebakan" for an unconfirmed guess, "Belum diisi" for a missing one), and "Ubah".
- **Build gate.** Accepting an offer ("Setuju, susun", 0046 §3) and every other way to start a build is refused while
  the profile is not complete. The error code is `profile_incomplete` and the studio says "Lengkapi profil usaha dulu,
  supaya paket dan harganya pas." with the profile card. The interview, the map and offers keep working.

### 5. The suggestion (Cloud)

Cloud computes the suggestion from the confirmed fields, the package values in `config/tiers.yaml` and the current
offer. It is computed on read and never stored. Unconfirmed guesses are not used.

```json
{"plan": "pro", "label": "Juragan Pro", "price_per_month": 249000, "offer": "modules_custom",
 "reasons": [{"field": "staff", "text": "21–100 staf: Juragan Pro memuat sampai 30 pengguna."}], "needs_review": false}
```

**Plan.** The packages in order are `mulai` < `juragan` < `pro` < `grup` < `enterprise`. Each answer gives a minimum
package; the plan is the highest minimum.

| Field | Minimum package |
| --- | --- |
| `scale` | `starting` → mulai · `one_location` → juragan · `branches_2_5` → pro · `multi_business` → grup · `branches_over_5` → enterprise |
| `outlets` | the first of mulai, juragan, pro, enterprise whose `outlets` limit is at least the number (0 or absent = no limit) |
| `staff` | the first of mulai, juragan, pro, enterprise whose `users` limit is at least the lowest count in the range: `solo` 1, `to_5` 2, `to_20` 6, `to_100` 21, `over_100` 101 |
| `customers` | `under_50` → mulai · `to_200` → juragan · `to_1000` → pro · `over_1000` → pro |
| `products` | `to_10`, `to_50` → mulai · `to_500` → juragan · `over_500` → pro, or enterprise when `sales_channels` has `online_marketplace` or `online_website` |
| `sales_channels` | `online_marketplace` or `online_website` → juragan; otherwise mulai |
| `revenue` | `under_10m` → mulai · `to_50m` → juragan · `to_250m` → juragan · `to_1b` → pro · `over_1b` → enterprise |
| any `other` | no minimum from that field; `needs_review: true` |

- `grup` is reached only through `scale: multi_business`; the limit searches skip it because its extra is more
  businesses, not more outlets or users.
- With today's `config/tiers.yaml` the limit rows give: outlets 1 → mulai, 2–5 → pro, more → enterprise; staff
  `solo`, `to_5` → mulai, `to_20` → juragan, `to_100` → pro, `over_100` → enterprise. When the file changes, the rows
  follow it.
- **Reasons.** One reason per field whose minimum equals the plan, in the order of §1, plus the sentence "Harga
  dihitung per usaha, bukan per orang." when `staff` is `to_20` or bigger and did not raise the plan. When
  `customers` is `over_1000`: "Kalau jatah Raka kurang, bisa tambah paket kredit." At most four reasons.
- **Price.** `price_per_month` is the package's `price.per_month`; `enterprise` has none and the card says "Harga
  setelah konsultasi".

**Offer kind** (the board's "jenis penawaran"), from the current offer (0046) of the project:

| `offer` | When |
| --- | --- |
| `custom` ("Full custom") | the plan is `enterprise`, or the recommended option has more than 3 `custom` pieces |
| `modules_custom` ("Modul + khusus") | the recommended option has 1 to 3 `custom` pieces |
| `app` ("Paket aplikasi") | the recommended option has no `custom` piece |
| `""` | there is no offer yet (and the plan is not `enterprise`) |

The profile never changes the offer itself. It only names the kind for sales.

### 6. The plan card (studio)

The offer card of 0046 gains a "PAKETMU · dari profil usaha" block below the options, built from §5 when the profile
is complete (board C). Above it, the 0050 block "Masalah yang terjawab" is the card's "SISTEMMU".

- Normal package: "Paket yang cocok", the label, "Rp 249.000 / bulan", "per usaha, sudah termasuk PPN", "Kenapa
  <label>" with the reasons, the actions "Lihat rancangan" and "Bandingkan paket", and the source line "Harga dari
  dokumen paket Rangka Cloud".
- `enterprise`: "Harga setelah konsultasi", "dihitung per outlet, bukan per orang", the reasons, and the actions
  "Jadwalkan ngobrol dengan tim" (marks the prospect stage `consult_requested`, §7) and "Tetap susun sendiri".
- A changed answer recomputes the block on the next read. A paid package never changes by itself.

### 7. Kendali "Calon pelanggan"

- **Who.** Staff with the role `sales` ("Penjualan"). Roles are a list on the staff account, granted with
  `rangka-cloud staff role <email> sales` (and removed with `--remove`). Without the role the API answers 403
  `forbidden` and the menu item is hidden.
- **API.** `GET /api/staf/prospects` with optional `q` (business or owner name), `offer`, `plan`, `industry`,
  `page`. One row per studio project that has a profile:

  | Column | Value |
  | --- | --- |
  | Usaha | project title; owner's first name and when the profile last changed ("hari ini", "kemarin", "<n> hari lalu") |
  | Jenis, Skala, Jualan, Pelanggan, Staf, Produk, Penjualan | short labels of the answers; an `other` answer shows "Lainnya: <text>" |
  | Outlet | the number |
  | Saran paket | §5 label |
  | Penawaran | §5 offer kind as an uppercase lozenge: PAKET APLIKASI, MODUL + KHUSUS, FULL CUSTOM |
  | Tahap | `interview` Wawancara · `preview` Pratinjau · `consult_requested` Minta konsultasi · `active` "Aktif · <package>" |
  | Profil | "<confirmed>/8", plus " + <k> sendiri" for `other` answers |

  The reply also carries the counts per offer kind and per plan for the filter chips ("Semua 42", "Paket aplikasi
  19", …). Rows are sorted by `revenue` (biggest first), then by when the profile changed. Opening a row shows the owner's words and Raka's `note` for every `other` answer.
- `GET /api/staf/prospects.csv` exports the same rows for the same filters. Every export is written to the staff audit
  log.
- **Read only.** Staff never change the owner's answers. Sales notes are out of scope here and will be stored apart
  from the profile.

## Consequences

- **rangka-ai.** `business_profile` and `profile_dig` in, `profile_guesses` and `profile_notes` out, validated in
  code; the consult prompt gains the profile agenda; 0050 map changes.
- **rangka-cloud.** `business_profile` column on projects (file and Postgres stores), merge rules, studio profile
  card and peta panel block, build gate `profile_incomplete`, the §5 suggestion in Go with tests over the tiers file,
  the plan block on the offer card, staff roles and Kendali "Calon pelanggan" with CSV; 0050 map storage, "Masalahmu"
  and "Masalah yang terjawab".
- **Board.** Sections C, E and F still show the "skala only" rule and "Lewati dulu"; they are out of date on those two
  points and this contract wins.

## Open for Dede

- The minimum package per `revenue` band (§5) is a first proposal; change the table when you set the real
  bands.
- The `customers`, `products` and `sales_channels` minimums are first proposals too; the `outlets` and `staff`
  rows follow `config/tiers.yaml`.

## Out of scope

- Sales notes, lead assignment and follow-up tasks in Kendali.
- Using the profile in the console (after the build) or for billing changes.
- Prices for Enterprise and paid connectors.
