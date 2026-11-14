# 0053 Mulai di sini, locked menus with a reason, and Pengaturan sistem

Status: proposed (14 Nov 2026) · Owner of the change: rangka-os (kernel, schema 0.5 `process.json`, validators, web
app) · Affects: rangka-cloud (`/v1/plan` writes stage hints and answers, private), rangka-ai (`/v1/plan`) · Amends:
0014 §1–2 (two optional stage keys), 0042 §8 (the plan fills them) · Builds on: 0014, 0020, 0042, 0050

## Context

Dede tested a business that Rangka Cloud built (14 Nov 2026). He found four problems.

- **The owner feels lost.** The system exists, but nothing says what to do first. The order of the business (client,
  then contract, then staff, then attendance, then invoice) lives in the process file Cloud wrote at build (0042 §8),
  which only the canvas reads.
- **A menu that cannot be used yet gives no reason.** A list whose model needs another model's records (attendance
  needs staff) shows an empty page with a disabled "Tambah". Nothing tells the owner to add staff first.
- **Raka's button is missing on Cloud businesses.**
  - The web app shows it only when `meta.features.ai` is true (0020).
  - On AWS the kernel never gets `RANGKA_AI_URL`: the central Raka service for running businesses is not built
    (rangka-cloud runbook `aws-hosting.md`).
  - The fix is infrastructure in rangka-cloud. This contract only makes the reason visible.
- **System settings sit among business menus.** "Modul dan sambungan" and "Impor data" are entries in the sidebar
  group "Lainnya", next to the business's own work.

Design: Figma "Rangka OS" › Rangka Cloud › "Setelah dibangun · usulan".

## Decision

### 1. Two optional stage keys in `process.json` (amends 0014 §1–2)

```yaml
stages:
  - {name: petugas, label: {id: Daftarkan petugas}, model: staff,
     hint: {id: Masukkan petugas yang ditempatkan di gedung klien.},
     answers: [{id: "Absensi 60 petugas lewat grup WA, direkap manual"}]}
```

| Key | Type | Max | Meaning |
| --- | --- | --- | --- |
| `hint` | i18n text (`{id, en?}`) | 300 characters per language | One sentence on what to do in this stage, shown in Mulai di sini |
| `answers` | array of i18n text | 5 items, 300 characters each | The owner's problems this stage answers, in their words (0050 `pains` whose `solves` pieces back the stage) |

- Both keys change no behaviour, like the rest of a process (0014 §1).
- **Validation, identical in the kernel and `tools/rangka_validate.py`.** Each limit is checked by the schema
  (`maxItems`, `maxLength`), and a breach is reported as code `schema`.
- **0042 §8 amended.**
  - `/v1/plan` writes `hint` for every stage it puts in `processes/<name>.rk`.
  - `/v1/plan` writes `answers` from the map's pains: a pain goes to a stage when one of its `solves` pieces is on
    that stage. A pain that matches no stage is not written.

### 2. `GET /api/v1/onboarding`

```json
{"process": "bersih_jaya", "hidden": false,
 "steps": [{"stage": "klien", "label": {"id": "Tambah klien pertama"}, "hint": {"id": "…"}, "model": "client",
            "state": "done", "done_at": "2026-11-13T09:40:00Z", "needs": [], "answers": []},
           {"stage": "petugas", "label": {"id": "Daftarkan petugas"}, "model": "staff", "state": "open", "needs": [],
            "answers": []},
           {"stage": "absensi", "label": {"id": "Catat absensi hari ini"}, "model": "attendance",
            "state": "locked", "needs": ["staff"], "answers": [{"id": "Absensi 60 petugas lewat grup WA, direkap manual"}]}],
 "current": "petugas",
 "locks": {"attendance": {"reason": "needs_data", "needs": ["staff"]}}}
```

- **Which process.** In this order:
  1. the site process named in `config.onboarding.process`;
  2. the only site process, when the site has exactly one;
  3. the first declared process of an installed app, by name;
  4. the inferred process of the first installed app in `config.apps` order (0014 §3).

  With none, `steps` is `[]` and the web app shows no Mulai di sini.
- **Steps.** One step per stage the caller may read (0014 §7), in stage order. `label` is the stage label, `hint` and
  `answers` come from §1, and both are omitted or empty when the stage has none.
- **State.**
  - `done`: at least one record of the stage `model` matching the stage `when` exists in the caller's org. The count
    uses the org's data, not the caller's row scope, and only the boolean leaves the kernel. `done_at` is the
    `created` time of the oldest matching record.
  - `locked`: not done, and `needs` is non-empty.
  - `open`: otherwise.
- **`needs`.** The models named by a required `link` field of the stage model that have no record in the org,
  without duplicates, in field order. A required link to the model itself is ignored.
- **`current`.** The first step that is `open`, or `null`.
- **`locks`.** For every model in the caller's menu that has non-empty `needs` (computed as above), whether or not it
  is a stage: `{reason: "needs_data", needs}`.
- **Hiding.**
  - `hidden` is the org's flag (§3).
  - The answer carries steps even when `hidden`: the web app decides what to show.
- **Errors and cost.**
  - A caller with no readable stage gets `steps: []` and no 404: the endpoint itself never leaks.
  - The answer is computed on every request; it is not cached across records.
  - At most one `EXISTS` query per stage model and per link target.

### 3. `POST /api/v1/onboarding/hide`

- Body: `{hidden: true|false}`. Answers `{hidden}`.
- Only the owner may call it; anyone else gets `403 permission_denied` (model `onboarding`, action `hide`).
- The flag is stored per org in the kernel's own store. It is not metadata and not a change set, like the canvas
  layout (0014 §6). Raka never calls it.

### 4. `meta.features.ai_off`

- When `meta.features.ai` is false, meta also carries `features.ai_off`, one of:
  - `not_configured`: the kernel runs without `--ai-url`;
  - `disabled`: `config.ai.enabled` is false;
  - `unreachable`: the sidecar did not answer `/healthz`.
- When `meta.features.ai` is true, the key is absent.
- It carries no URL and no host.

### 5. Web app

- **Mulai di sini.**
  - Beranda shows the steps above the dashboard while `hidden` is false and at least one step is not done.
  - The sidebar shows the entry "Mulai di sini" with "<done>/<total>" in the first group.
  - Content of the Beranda card:
    - the steps;
    - progress;
    - for each done step, a ★ with its label;
    - each step's `answers` as "Menjawab: …";
    - the `current` step's actions: "Tambah <model label>" and, when the model has an import, "Impor dari file".
  - When a step turns done, the next screen shows a short note: "<done> dari <total> selesai", the next step, and
    "Lanjut". The note appears once per step, for 6 seconds, at the bottom left. No confetti and no points.
  - When every step is done, the card says so, lists the answered problems, and offers "Tutup Mulai di sini", which
    calls §3.
  - "Sembunyikan" is always available, and "Tampilkan Mulai di sini" on Beranda brings it back.
- **Locked menus.** A menu entry whose model is in `locks`:
  - shows a lock icon and the tooltip "Butuh <needs labels> dulu" with "Tambah <first needs label> →";
  - its list page shows a locked state instead of an empty table: title "<label> belum bisa dipakai", what the model
    records, the list of what it needs with each one's state, and a primary "Tambah <first missing>";
  - the entry stays clickable, and reading existing records stays possible.
- **One reason per locked thing.** Other locked or disabled states keep their current checks but say why, with these
  texts:

  | Reason | Text |
  | --- | --- |
  | `needs_data` | "Butuh <label> dulu" |
  | `role` | "Hanya <roles> yang bisa <action>" |
  | `submitted` | "Sudah diajukan dan terkunci. Batalkan lalu ubah." |
  | `connector` | "<sambungan> belum dipasang" (owner: link to Pengaturan sistem › Sambungan) |
  | `plan` | the kernel's 0045 refusal text |

  A disabled control without one of these reasons is a bug.
- **Pengaturan sistem.**
  - A gear button next to the business switcher opens `#/pengaturan/<section>`. The settings sidebar replaces the
    business sidebar, with "Kembali ke usaha" at the top.
  - Sections:
    - Usaha: Profil usaha.
    - Sistem: Modul, Sambungan (the 0036 catalogue split in two), Pengguna & peran, Tampilan, Impor data, Raka.
    - Riwayat: Perubahan (Ubah), Audit.
  - A section shows only when the caller may use it, by the same checks as today: catalogue `200`, `features.imports`,
    owner for users and theme.
  - The gear shows when at least one section does.
  - The business sidebar no longer has "Modul dan sambungan" or "Impor data". "Proses bisnis" and "Persetujuan" stay:
    they are business work.
  - Section Raka shows on or off. When off, it shows the reason from `features.ai_off`:
    - `not_configured`: "Raka belum dipasang untuk usaha ini";
    - `disabled`: "Raka dimatikan di pengaturan";
    - `unreachable`: "Raka sedang tidak bisa dihubungi".

    The floating Raka button stays hidden while Raka is off.

### 6. Raka

- Raka reads `GET /api/v1/onboarding` through MCP as `rangka_onboarding` (read only).
- Raka may suggest the `current` step in chat and prepare data for it: a draft, or an import preview. Saving stays the
  owner's action.
- Raka never calls §3.

## Consequences

- **rangka-os.**
  - `process.json` and both validators gain `hint` and `answers`.
  - New endpoints §2–3, the meta key §4, and MCP tool `rangka_onboarding`.
  - Web app changes in §5. `features.catalogue` and `features.imports` move into the settings routes.
- **rangka-cloud.**
  - `/v1/plan` (through rangka-ai) writes `hint` and `answers`.
  - Hosted businesses get Raka once the central Raka service exists. That is infrastructure, tracked in rangka-cloud,
    not here.
- **rangka-apps.** App process files may add `hint` to their stages; no other change.

## Out of scope

- Badges with their own names ("Tim lengkap"). A done step shows its own label with a ★.
- Points, streaks and leaderboards.
- Steps beyond the process stages, such as inviting staff or connecting WhatsApp.
- Per-user onboarding. The flag and the steps are per org.
