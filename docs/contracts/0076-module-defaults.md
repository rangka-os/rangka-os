# 0076 Module defaults: master data a business has from day one

Status: proposed (25 Nov 2026, Dede: "data master kadang masih kosong kayak yang simple unit … langsung bikin") ·
Owner of the change: rangka-os (kernel, validators, web app) · Affects: rangka-apps (modules ship `defaults/`),
rangka-ai (`/v1/plan` writes `starter`), rangka-cloud (none: its `rangka migrate` / `rangka seed` run it) · Builds on: 0012 (imports), 0016 (units), 0032 (modules)

## Context

- A business Cloud builds from an accepted offer starts with empty master tables. Tested locally on 24 Nov 2026 (a
  rental business): the first item could not be saved because no unit existed. The unit picker offers "Buat satuan
  baru", which leaves the item form and loses what was typed.
- Units are the same for every business. The data already exists in rangka-apps, but only in the demo fixtures
  (`examples/fixtures/demo-*/demo.rk`), which a real business never gets.
- Master data that depends on the business (its item categories, its first items, its outlet's name) is not in this
  contract: it comes from the owner (see "Later").

## Decision

### 1. `defaults/` in a module

- A module (or app) may ship `<unit>/defaults/*.rk`, in the fixtures format: `model: [records]`.
- A unit fills **only its own models**. Records for a model another unit owns are left out (the kernel names them as
  `<unit>/<model>` and seeds nothing for them), so one module never writes into another's tables.
- Records usually carry no `name`: the model's `naming` numbers them, so the owner's next record continues the same
  series.
- Only master data every business of that module needs belongs here (units, not categories). No money, no stock, no
  documents, no personal data.

### 2. When the kernel applies them

- After every migrate: `rangka seed` (a new business, after its fixtures), `rangka migrate` (Cloud's deploys),
  `rangka serve` at start, and the reload after a change set is applied (a module installed later).
- Per org (`rk_orgs`) that has been seeded (it has a member), per model: **only when the org has no record of that
  model.** An org `rangka migrate` has synced from config.rk but not yet seeded waits for its `rangka seed`, which
  loads its fixtures first: the fixtures keep their numbers (UOM-001…) and the defaults only fill what they left empty
  (25 Nov 2026: migrate-time defaults took demo-fnb's unit numbers and its fixture units were skipped). An org that already has a unit
  gets none; an owner who deletes a default does not get it back; running it again adds nothing.
- As the system actor, through the same path as fixtures (`Engine.Seed`): defaults, computed fields and naming apply.
- A defaults file that does not load, or a record the model refuses, is logged and that org's defaults are skipped;
  it never stops a start, a deploy or a change set.

### 3. First defaults (rangka-apps)

- `modules/catalogue/defaults/uom.rk`: Unit, Pcs, Buah, Set, Pak, Dus, Lusin, Kg, Gram, Liter, Meter, Porsi, Jam,
  Hari, Bulan. Coretax unit codes only where the model's help names them (Unit, Kg, Jam, Hari, Bulan).

### 4. Starter data from the interview (process `starter`)

Units are the same for every business; the tents and chairs are not. The owner names them in the interview, and the
first steps of Mulai di sini (0053) ask for exactly those records.

- **Shape.** A process file may carry `starter: {<model>: [<row>]}`: at most 10 models, 20 rows each, a row being 1
  to 4 fields of plain text (at most 120 characters each). Written by `/v1/plan` (rangka-ai); never by hand-written
  app processes.
- **Validators (Go and the Python reference, same report).** `process_starter_model`: the key is not a model.
  `process_starter_document`: the model is submittable (starter is master data only). `process_starter_field`: not a
  field of the model, or not a plain text field (`data`, `small_text`, `text`) that is writable, not computed and not
  hidden from a role.
- **Which models (rangka-ai).** Each stage's model when it is master data, and the master data reached from it
  through required links, up to two links away. The interview's stages are the flow (booking, payment, return): their
  models are documents, and the master data they need are the "Isi dulu" prerequisites. A rental item's title is
  computed from the item it links to, so the owner's tents are items.
- **Data only from what the owner said.** One model call lists the names per model from the owner's words on the map
  (business, stage labels and actors, documents, systems, settings, problems, open questions). Code keeps a name only
  when every word of it with three letters or more is in those words; at most 20 per model. An outlet model with no
  name gets the business's name. A provider failure gives no starter; the build goes on.
- **Older kernels.** A kernel before this contract refuses `starter`: the process is drafted again without it (then
  without 0053's guidance, as before).
- **Mulai di sini.** `GET /api/v1/onboarding` returns `starter` by model: the models the caller can read, minus the
  rows whose title is already a record (trimmed, case-blind). A step shows "Dari ceritamu: + <title> …" for its own
  model and for the empty models it needs first (`locks[model].needs`); each button opens that model's form
  prefilled. The owner completes it (unit, price) and saves. Nothing is saved for them, and a saved row stops being
  offered.

Measured locally on 25 Nov 2026 (the PR stack): a rental story naming "tenda 3x3, kursi futura dan meja bulat" built a
business whose process carried `starter.item` with the three names; "Isi dulu Alat sewa" offered them, "+ Tenda 3x3"
opened the Barang form with the name filled, and the unit picker offered Unit (UOM-001) from §1.

## Later (not in this contract)

- **Create in place:** "Buat satuan baru" in a link picker opens a small form over the current one instead of
  leaving it.
- **A refreshed Mulai di sini** after a record is saved elsewhere (it reads onboarding once per load today).

## Tests

- Kernel `internal/engine/defaults_test.go`: own models only, an org with records gets nothing, a new org gets the
  defaults once, a broken file is an error for that module. `internal/server/seed_test.go`: an unseeded org gets
  nothing at migrate; seeded with fixtures it keeps only the fixtures' records, without fixtures it gets the defaults.
- `internal/meta/process_test.go` and the broken-canvas golden report (Go = Python): the three starter codes.
- `internal/server/onboarding_test.go`: starter by model, saved rows left out.
- web app node tests: `starterRows` (own model and needed models), `starterTitle`, `newValues` (prefill).
- rangka-ai `tests/test_starter.py`: candidates through required links, names the owner never said dropped, outlet
  fallback, failures give none, the process carries `starter`.
