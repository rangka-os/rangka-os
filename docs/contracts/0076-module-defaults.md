# 0076 Module defaults: master data a business has from day one

Status: proposed (25 Nov 2026, Dede: "data master kadang masih kosong kayak yang simple unit … langsung bikin") ·
Owner of the change: rangka-os (kernel) · Affects: rangka-apps (modules ship `defaults/`), rangka-cloud (none: its
`rangka migrate` / `rangka seed` run it) · Builds on: 0012 (imports), 0016 (units), 0032 (modules)

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
- Per org (`rk_orgs`), per model: **only when the org has no record of that model.** An org that already has a unit
  gets none; an owner who deletes a default does not get it back; running it again adds nothing.
- As the system actor, through the same path as fixtures (`Engine.Seed`): defaults, computed fields and naming apply.
- A defaults file that does not load, or a record the model refuses, is logged and that org's defaults are skipped;
  it never stops a start, a deploy or a change set.

### 3. First defaults (rangka-apps)

- `modules/catalogue/defaults/uom.rk`: Unit, Pcs, Buah, Set, Pak, Dus, Lusin, Kg, Gram, Liter, Meter, Porsi, Jam,
  Hari, Bulan. Coretax unit codes only where the model's help names them (Unit, Kg, Jam, Hari, Bulan).

## Later (not in this contract)

- **Starter data from the interview:** at build, Zafdoz drafts the master data the owner named (categories, the first
  items, the outlet with the business's name) for the owner to check and save in Mulai di sini (0053). Data only from
  what the owner said; never saved without the owner.
- **Create in place:** "Buat satuan baru" in a link picker opens a small form over the current one instead of
  leaving it.

## Tests

- Kernel `internal/engine/defaults_test.go`: own models only, an org with records gets nothing, a new org gets the
  defaults once, a broken file is an error for that module.
