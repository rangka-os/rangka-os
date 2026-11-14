# 0032 Free module composition

Status: implemented (rangka-os) (9 Nov 2026; proposed 8 Nov 2026) · Owner of the change: rangka-os (schema, validators, kernel, scaffold) · Affects:
rangka-cloud (scaffold and interview build), rangka-ai (guardrails for `config.modules`), nothing existing in
rangka-apps · Builds on: 0001, 0015, 0016, 0028

## Context

- Contract 0001 makes a site install **apps**: `config.apps` names apps, and each `app.rk` lists its modules. A site
  cannot name a module on its own.
- The Juragan interview (rangka-cloud `docs/specs/2026-11-08-raka-interview-design.md`, "Free composition from
  modules") composes a system from individual modules: a rental gets `contacts`, `catalogue`, `invoicing`,
  `payments` and nothing from CRM. Today the Cloud scaffold copies one `catalogue/apps/<app>`, so the composition
  has nowhere to go.
- Module dependencies (`requires`) must be resolved by the validators, never by the LLM.
- Later, adding or removing a module has to be an ordinary change set, and the interview's learning loop reads
  "modules kept, removed or added after a week" from the site's module list.

## Decision

1. **`config.modules`.** `config.rk` gains `modules: {<module>: <range>}`, the same shape as `modules` in `app.rk`.

   ```yaml
   apps: []                       # may be empty or absent
   modules: {contacts: "^1.0", catalogue: "^1.0", invoicing: "^1.2", payments: "^1.0"}
   ```

   - It sits next to `config.apps`. A site may use both; existing sites with only `config.apps` mean the same as
     before.
   - Once either key is present, the plan's `apps` are no longer the fallback. A site with neither key still installs
     the plan's apps, or else every app (0001, unchanged).
   - Per-org `orgs.<id>.config.modules` is refused (validator error `org_config_key`, new): one kernel has one schema.

2. **Resolution in the validators, not the LLM.**
   - The installed set is the closure of `config.apps` (each app's modules and their `requires`) plus the closure of
     `config.modules` (each module and its `requires`), dependencies first, names sorted at each level, as
     `resolveInstalled` does today. The `site` unit (0016) still comes last.
   - Both validators report these codes, in the same order (`make compare-python`):
     - `unknown_module` (error, existing code): a key that is not a module. For an app or a 0.4 unit, the message says
       "list it under apps". `site` cannot be named.
     - `version_mismatch` (error, existing code): the module's version is outside the range.
     - `module_not_in_plan` (error, new): see §6.
     - `uses_uninstalled_model` (error, new): a site file names a model of a module that is not installed. That covers
       overlays, company models and flows (0016), layouts, site dashboards and `numbering`. A company model that links
       to such a model counts too.
   - The scaffold and the Cloud never compute closures. They write `config.modules` and run `rangka validate`.

3. **Changing the set is a change set.** The kernel judges a `config.rk` edit by the **difference in installed
   modules**, not by which key changed. The same rule covers `config.apps`, so removing an app that drops modules is
   judged the same way.

   | Change | Risk |
   | --- | --- |
   | Adds modules (preview lists the whole closure added) | medium |
   | Changes a range | medium |
   | Removes modules whose models hold no rows in any org | medium |
   | Removes modules whose models hold rows | **high** (owner only) |

   - `Assess` marks every removal medium. Preview counts rows per removed model and raises the change set to high when
     any count is above zero. Approval is only from previewed, so it always sees the raised risk.
   - A module that leaves `config.modules` but is still required by another installed unit stays installed. Preview
     says so ("tetap terpasang: dibutuhkan oleh invoicing"), and the change makes no difference.
   - Removing a module never drops a table. Its models leave `/meta`, REST, MCP and flows. Rows stay and come back when
     the module is added again. Purging data is an operator command, not part of this contract (Open).

4. **Install order and `/meta`.**
   - Migrations run in install order. A module added by a change set migrates on apply. Apply calls the same migrator
     as `rangka migrate` and creates tables only, so revert hides them again without dropping rows.
   - `/api/v1/meta` `modules[]` gains `installed_by`, a list naming `config` and/or the apps that bring the module in.
     `site.config.modules` is reported as written.

5. **Menu and home.** A module carries no menu (0001). For a site that names modules directly:
   - **Menu.** If the site layout sets `nav.side`, it is the menu, as today. Otherwise the kernel appends one side group
     per installed module that no installed app's menu covers. Groups follow `config.modules` file order, then
     required-only modules by name. Each group is titled with the module's `title` and lists its models that are not
     `is_child`, in file-name order. The web app reads it from `/meta` like an app menu.
   - **Home.** The layout's `home`, else the first dashboard of the first module in `config.modules` that has one,
     else the list of the first menu item.
   - The Cloud writes a site layout (`layouts/<name>.rk`, `nav.side` and `home`) at build time. It is generated by code
     from `/meta`, not by the LLM, and goes into the initial change set (low risk). Owners change it later in Ubah.

6. **Plans (0028).**
   - `features.rk` plans gain an optional `modules: [...]`. The modules a plan allows are its `modules` plus the
     closure of its `apps`, following `extends`.
   - A plan with neither key allows every module and every app. Juragan's per-outlet plans list neither.
   - `module_not_in_plan` works like `app_not_in_plan`: it is checked when `config.rk` changes, not on
     `rangka plan set`.
   - The enforced limits of 0028 (`outlets`, `users`, `ai_credits_month`) do not depend on the composition. The
     `outlets` limit applies only while the `outlet` model is installed.

7. **Onboarding.** The kernel never reads an app's `onboarding`; only the scaffold and the Cloud do. A composed site has
   no app onboarding. The interview's answers become the initial change set instead: overlay defaults, seeded
   records, and existing `config.rk` keys (design "Module settings").

8. **Upgrades.**
   - A catalogue upgrade replaces module directories. Ranges in `config.modules` guard it as app ranges do: a major
     bump fails `version_mismatch`, so migrate refuses until a change set moves the range.
   - New `requires` in an upgraded module join the closure, and the migrate preview lists them.
   - New models appear in the kernel's default menu. When the layout sets `nav.side`, they stay out of it until a
     layout change set adds them. The Cloud proposes that change set; it is low risk.

### Rejected alternative: a site-owned app (`<site>/app.rk`) synthesised by the Cloud

It would reuse app machinery (menu, home, roles, onboarding), but:
- The menu would be frozen at generation time, so every module upgrade would need regenerating and merging the file.
- The validators and the kernel treat app files as app-author files, which are high risk and never AI-edited. A site
  app breaks that rule, and it would clash with the `site` unit of 0016, which is "not an app".
- Only the closed Cloud would hold the generator. A self-hosted site should get the same result from one list in
  `config.rk`.

## Contract changes (rangka-os)

- Schema `schema/0.5/config.json`: `modules`, an object of module name → range (pattern of `app.json` `modules`).
  Schema `features.json`: plan `modules`.
- Validators (`tools/rangka_validate.py`, `rangka validate`): §2 resolution and codes. Testdata
  `kernel/testdata/config-modules` and `broken-config-modules` in `make compare-python`.
- Kernel: `meta.resolveInstalled` and `Registry.Closure` take `config.modules`. `changeset.assessConfig` judges by
  installed-set difference, with the row count at preview (§3). `/meta` `installed_by` and the default menu (§5).
- Scaffold: `npm create @rangka-os -- --modules contacts,invoicing` writes `config.modules` (ranges from the copied
  modules) and no `apps`.
- rangka-ai guardrails: `config.modules` is no longer escalated or treated as "apps". Adding is medium. Removing is
  marked high before drafting, because preview may find data.
- rangka-cloud (not this repo): the build writes the composition into `config.modules` plus the generated layout,
  instead of copying `catalogue/apps/<app>`.

## As built in rangka-os (9 Nov 2026)

- `uses_uninstalled_model` is reported on sites that set `config.modules`. App-only sites keep the codes they had
  (`optional_model` in layouts, `not_installed` warning in `numbering`), so their reports do not change.
- The modules a plan allows include what its listed modules require, not only the closure of its apps.
- `features.rk` plan `modules` naming no module is `unknown_module`.
- The default menu is `/api/v1/meta` `menu`: `[{group, module, items, dashboards}]`, empty when the site names no
  modules or the layout sets `nav.side`. The web app appends these groups after the app menus and takes home from
  them when no app gives one. A module counts as covered when an installed app brings it in.
- Preview of a `config.rk` change carries `preview.modules`: `added` (the whole closure), `removed`, `rows` (per
  removed model, all organisations), `kept` (`{name, required_by}`) and, when a removed model holds submitted
  documents, `submitted` (per model, all organisations; the installed apps no longer see them, so their checks against
  them stop), with notes in Bahasa Indonesia ("tetap terpasang: dibutuhkan oleh …", "dokumen yang sudah diajukan di
  app yang dilepas: …"). The raised risk is written to the change set at preview.
- `GET /api/v1/catalogue` and MCP `rangka_catalogue` read `installed_by` and `in_plan` from the same resolution. A
  module outside the plan is `not_in_plan` in the MCP catalogue.

## Open

- Work screens (0030) are offered only when an app binds them with `work:`, so a composed site has none. One option is
  a layout `work:` key.
- Module-level roles are installed with the module. Roles that cross modules (as an app's `roles:` block does) have no
  home in a composed site until company roles exist (0016 Open).
- An operator purge for the rows of a removed module (`rangka purge-module <m>`), with a backup first.
- Whether a composed site should be able to "save as app" into rangka-apps, for compositions that keep recurring.
