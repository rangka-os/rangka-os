# S8b Modules and Apps in the Kernel (contract 0001) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The kernel, the Go validator and `tools/rangka_validate.py` read contract 0001's two levels, `modules/<m>/module.rk` and `apps/<a>/app.rk` (`modules: {name: range}`), while still reading 0.4 `app.rk`/`template.rk` with a deprecation warning.

**Architecture:** A module and an app both load into `meta.App`, which gains `Kind` (`module`, `app`, `legacy`). The modules root defaults to the `modules` sibling of the apps dir. The installed set is `config.apps` plus the transitive closure of each app's `modules` and each unit's `requires`. Visibility rules are unchanged: a unit sees its own models plus its `requires`/`optional`, and for apps also its `modules`.

**Tech Stack:** Go 1.24 kernel, JSON Schema 2020-12, Python 3 reference validator.

**Spec:** `docs/contracts/0001-modules-and-apps.md` ("Contract changes (rangka-os)" 1–5), program plan `docs/superpowers/plans/2026-11-03-pilot-program.md` (S8).

## Global Constraints
- Scope is exactly contract 0001's list. Schema 0.5 bootstrap (dir, `$id`s, embed, `schema.Base`, Python paths) belongs to S5 (`pilot/s05a-schema-05`). This branch adds only `schema/0.5/module.json` and `schema/0.5/app.json`. Any embed or lookup wiring it needs before S5a lands goes in ONE commit titled `temp: minimal 0.5 lookup until S5a` that is dropped on rebase.
- `make -C kernel test` and `make -C kernel compare-python` stay green. Go and Python reports stay byte-identical.
- Every existing 0.4 catalogue (rangka-apps main today) loads and behaves as before. The only difference is new warnings.
- Code and comments in English; messages as today.

## Decisions
1. **Schemas.** `module.json` = 0.4 `app.json` minus `menu` and `dashboards` (`name, version, title, requires, optional, hooks`), `$id` …/0.5/module.json. 0.5 `app.json` = 0.4 `app.json` (`name, version, title, menu, dashboards, hooks, optional`) + 0.4 `template.json` fields (`label, summary, recommends, connectors, fixtures, roles, home, onboarding`) + required `modules: {name: range}`. It has no `requires` (an app needs modules, not apps) except `rangka: '>=x'` under a new optional `rangka` string.
2. **Which schema an `app.rk` uses:** a document with a `modules` key is a 0.5 app; without one it is a legacy 0.4 app (a module and an app in one). Kind lookup: `module` → 0.5; `app` → 0.5 when `modules` is present, else 0.4.
3. **Roots.** `Options.ModulesDir`; CLI `--modules` (env `RANGKA_MODULES`). When empty, use `filepath.Join(filepath.Dir(AppsDir), "modules")` if it exists. Docs keys: `modules/<m>/...` for modules and `apps/<a>/...` for apps and legacy units. Change-set paths `modules/...` resolve to ModulesDir and are copied into the dry-run temp dir like `apps/`.
4. **Names.** Module and app names share one namespace: a clash is an error `duplicate_unit` on the second file.
5. **Install.** `config.apps` lists apps (or legacy units). Installed = those plus, transitively, each app's `modules` and every unit's `requires`. A missing module → error `unknown_module` (on the app's `modules/<name>`). A version that does not satisfy the caret/`>=` range → error `version_mismatch` (reuse the existing range check if there is one, else `^a.b` = same major, `>=x.y`). `missing_dependency` stays only for legacy units. `features.rk` plan `apps` limit apps only, and modules pulled in by an allowed app are allowed.
6. **Validation codes (new):** `app_defines_model` (error, a 0.5 app with a non-empty `models/` dir; `where` = `models`), `module_has_menu` is already covered by the schema, `deprecated_layout` (warn, once per legacy `app.rk`: "0.4 app.rk: split into modules/<name>/module.rk and an app (contract 0001)"), `deprecated_template` (warn, each `templates/*/template.rk` found in the apps dir's parent: "template.rk becomes apps/<name>/app.rk (contract 0001)"). For an app, `unknown_model` in menus and dashboards checks models of its modules, their requires and its optional.
7. **/meta.** `apps: [{name, title, version, modules: [..]}]` (apps and legacy units), `modules: [{name, title, version, requires}]` (modules and legacy units), and every model carries `module` (its unit's name). Keep the existing keys; this only adds keys.
8. **Kernel tests keep working on rangka-apps main**, and gain fixtures for the new layout: `kernel/testdata/modules-apps/{modules,apps,site}` (two modules, one app, a site) plus `testdata/broken-modules-apps` for the error codes. Both go in compare-python's loop.
9. **Decouple kernel tests from rangka-apps' layout** (needed so rangka-apps S8 can move to the new layout): copy today's `rangka-apps/{apps,fixtures,sites/kopi-senja}` to `kernel/testdata/catalogue-v0/` and point the tests and Makefile defaults (`APPS`, `SITE`, test consts, `run-demo`) there, in one commit. `make run-demo APPS=… SITE=…` still overrides.

## Tasks
### Task 1: Schemas + Python validator
Files: `schema/0.5/module.json`, `schema/0.5/app.json`, `tools/rangka_validate.py` (+`rkfile.py` if needed). Tests: run the validator on `kernel/testdata/modules-apps` → 0 errors; on `testdata/broken-modules-apps` → exactly the codes in Decisions 4–6, in an expected file `kernel/testdata/broken-modules-apps.expected.txt`. Commit.

### Task 2: Go loader, validator, install closure
Files: `kernel/internal/meta/{registry.go,types.go,validate.go,validate_ext.go}`, `kernel/internal/schema` (temp commit only), `kernel/cmd/rangka/common.go` (`--modules`). Tests: `validate_test.go` cases for both testdata trees; Go output equals Python (`compare-python` with the two new arg sets added to the Makefile loop). Commit.

### Task 3: /meta, change sets, engine
Files: `kernel/internal/api/meta.go`, `kernel/internal/changeset/service.go`, `actions.go`. Tests: `/api/v1/meta` on the modules-apps fixture lists `apps` and `modules` separately and `module` on each model; a change set editing `modules/<m>/models/x.rk` validates, previews and applies. Commit.

### Task 4: catalogue-v0 testdata + contract status
Decision 9 plus `docs/contracts/0001-modules-and-apps.md` status `accepted (3 Nov 2026, S8b)` with a short "As built" list. `make -C kernel test` and `compare-python` green. Commit, push, PR to `main` (not merged).
