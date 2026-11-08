# 0016 Site company module

Status: accepted (8 Nov 2026, Dede; proposed 4 Nov 2026 as a follow-up of S11) · Owner of the change: rangka-os (kernel, schema, validator, web app) ·
Affects: nothing in rangka-apps

## Context

Contract 0015 §2 (as changed by #36) makes Ubah's "Tambah tabel" write the company's new model into a catalogue module,
`modules/<m>/models/<name>.rk`, and "Tambah otomasi" write its flow into `modules/<m>/flows/<name>.rk`. That breaks two
rules: company changes are overlays and never edit `apps/` or catalogue modules, and a module upgrade replaces the
module directory, so the company's table and automation would be lost (or block the upgrade). Every edit to an existing
module file is also high risk (it belongs to the app author), so the company could never change its own table again
without the owner.

A company needs a place for things it owns outright: its own tables and automations. Overlays (`custom/<model>.rk`)
stay the way to change a catalogue model.

## Decision

1. **Layout.** The site directory holds the company module directly, mirroring a module's folders:

   ```
   <site>/models/<name>.rk   company tables (kind model, schema 0.5 model.json)
   <site>/flows/<name>.rk    company automations (kind flow)
   ```

   No manifest. The loader turns these files into one unit named **`site`** with kind **`site`** when either folder holds
   a file (a site without them has no `site` unit, so nothing changes for it). Change-set paths are site-relative like
   `custom/` and `dashboards/`: `models/<name>.rk`, `flows/<name>.rk`. Both are **layer 4** (organisation).
   Backups already archive the site directory (minus `data/` and `backups/`), so company tables and flows are in them;
   module upgrades never touch them.

2. **Names.** A company model name starts with `x_` (`x_kunjungan_toko`), the same prefix as company fields, so a
   catalogue upgrade can never add a model of the same name. Its fields are ordinary columns of its own table
   (`r_x_kunjungan_toko`); only fields added to *catalogue* models live in the `x` JSONB column.
   Validator codes (Go and Python report them identically):
   - `company_model_name` (error): a `<site>/models` file whose name does not start with `x_`.
   - `duplicate_model` (error): a `<site>/models` file named like a model of a module or app; the site file is not
     loaded (the catalogue model stays).
   - `duplicate_unit` (error, existing code): a module or app named `site`; the company module is not loaded.
   A company flow named like a module flow replaces it (site files win, as for site dashboards and print formats).

3. **What it sees.** The company module sits on top of everything: its models may link to every model of every unit
   (no `requires`), may name every role, and use core hooks only. No catalogue unit sees company models (none
   requires `site`), so they can only be reached from the site: layouts, site dashboards, overlays, company flows. An
   overlay on a company model is allowed and sees every model too.

4. **Install and `/meta`.** The `site` unit is always installed when present, after every app (`app_installed('site')`
   is true). It is not an app: plans and `config.apps` cannot name it, and an app's `modules` cannot list it.
   `/api/v1/meta` lists it under `modules` with `kind: "site"`, `title: {id: "Perusahaan", en: "Company"}`; its models
   carry `app: "site"`, `module: "site"`. It has no menu of its own: the table appears where the layout's `nav.side`
   puts it (Ubah adds it to the group "Tabel baru"), whichever app is on screen.

5. **Risk.** A new `models/<name>.rk` is medium (high with money/stock fields; without `permissions` an extra reason
   says every role has full access), as a new module model was. Editing an existing company model is judged field by
   field like an overlay (adds a field: medium, changes a money/stock field or `permissions`: high, relabels: low),
   not as "edits a module file" (high). A new or changed `flows/<name>.rk` is medium. Deleting either is high.

6. **Ubah (replaces the contract 0015 §2 rows).**

   | Edit in Ubah | Change-set ops | Risk |
   | --- | --- | --- |
   | Add an automation | new `flows/<name>.rk` in the site with `on: <model>.updated`, for catalogue and company models alike | medium |
   | Add a table (model) | new `models/x_<name>.rk` in the site + a `nav.side` entry in the layout; `permissions` copied from a sibling model (the first model of the menu group the table joins, else the first model with `permissions` of the module of the model on screen, else of the app's first menu model), none when no sibling has them | medium (high with money/stock fields); without `permissions` an extra reason says every role has full access |

## Contract changes (rangka-os)

- Kernel loader (`meta.LoadOpts`): unit `site` from `<site>/models` and `<site>/flows`; registry docs keys
  `models/<m>.rk`, `flows/<f>.rk`. `meta.KindSite`; `App.IsApp()` and `App.IsModule()` are false for it.
- Change sets: `Layer()` and the `/files` layer give 4 for `models/` and `flows/`; `Assess` as in §5; preview counts
  affected records for `models/`.
- Validators: `tools/rangka_validate.py` and `rangka validate` load the same unit and report §2's codes in the same
  order; `make compare-python` runs `testdata/site-module` and `testdata/broken-site-module`.
- No schema change: company models and flows use `model.json` and `flow.json`.
- Web app: `RA.tableHome` answers `{unit: "site", kind: "site", near: <module>}`; `RA.opsAddModel` writes
  `models/x_<slug>.rk`; "Tambah otomasi" writes `flows/<name>.rk`.

## Open

- Company roles (`<site>/roles/`) and company dashboards that are not replacements (a site dashboard must name an
  installed dashboard today, contract 0015 §3).
- Turning a company table into a catalogue module ("Save as app"): move the files and drop the `x_` prefix with a rename
  migration.
- The Console's model editor still builds `apps/<app>/models/<m>.rk` paths; it should use the file path `/meta` reports.
