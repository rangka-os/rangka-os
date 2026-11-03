# 0001: Modules and apps

Status: accepted (3 Nov 2026, S8b) · Owner of the change: rangka-os (schema, validator, kernel) · Affects: rangka-apps

## Context
- Today an "app" (`rangka-apps/apps/crm`: lead, deal, activity) is really a reusable business unit.
- What a customer installs and buys, "Rangka CRM", is a "template" (`templates/crm`).
- The names are inverted from how people think, and nothing stops logic piling up where it can't be reused.
  For example, payment status lives inside `sales` although POS will need it too.

## Decision
Two levels, both in `rangka-apps` (no new repo while one person owns both):

| Level | Folder | Contains | Must not contain |
| --- | --- | --- | --- |
| **Module** | `modules/<name>/module.rk` | models, rules, core flows, its own roles; `requires` other modules | menus, home page, onboarding, connector choice |
| **App** | `apps/<name>/app.rk` | the modules it uses, connectors, roles, menu, dashboards, home, onboarding, per-industry defaults | models (make a module instead) |

Target layout:
```
modules/  contacts  catalogue  outlets  invoicing  payments  stock  pipeline  cashier
apps/     crm  (contacts, pipeline, invoicing · whatsapp_business)
          pos  (contacts, catalogue, outlets, stock, invoicing, payments, cashier · payment.qris · print)
```

Migration from today:
- `apps/core` splits into `modules/contacts`, `catalogue` and `outlets` (`user` stays in the kernel).
- `apps/sales` becomes `modules/invoicing`. `payments` is split out when POS starts.
- `apps/crm` becomes `modules/pipeline`.
- `templates/crm` becomes `apps/crm`.

## Contract changes (rangka-os)
1. Schemas:
   - `schema/0.5/module.json`, from today's `app.json`. Kind `module`.
   - `schema/0.5/app.json`, from today's `template.json`. Kind `app`, with `modules: {name: range}`.
2. `config.rk` keeps `apps: [...]`, now meaning apps. The kernel installs the modules each app needs (deduplicated, versions resolved).
   `features.rk` (plan) limits apps.
3. Validator: an app defining models is an error (`app_defines_model`). A module referencing a module it doesn't `require` is an error (existing `unknown_model` rule).
4. `/meta` reports `apps` and `modules` separately. Every model carries `module`. Change-set paths use `modules/...`.
5. Keep reading 0.4 `app.rk` and `template.rk` for one release, with a deprecation warning.

## Open
- Can an app ship small glue flows? Proposed: yes, flows and dashboards are allowed in apps; models are not.
- Version ranges between modules: caret ranges as today (`^1.0`).

## As built (rangka-os, S8b)
- Schemas: `schema/0.5/module.json` (0.4 `app.json` without `menu`, `dashboards`) and `schema/0.5/app.json`
  (0.4 `app.json` + `template.json` fields + required `modules: {name: range}`, optional `rangka`, no `requires`).
  An `app.rk` without `modules` is a 0.4 app and is checked against `schema/0.4/app.json` (embedded for that rule).
- Roots: `--modules` / `RANGKA_MODULES`, default the `modules` dir next to the apps dir. Docs and change-set paths
  `modules/<m>/...` and `apps/<a>/...`; `modules/...` is layer 5 like `apps/...`.
- Install: `config.apps` (else the plan's apps) names apps and 0.4 units; the kernel adds each app's modules and every
  unit's `requires`, transitively, dependencies first. Plans limit apps only.
- Validator codes: `duplicate_unit`, `unknown_module`, `version_mismatch` (`^a.b` same major and at least a.b, `>=a.b`),
  `app_defines_model`, warnings `deprecated_layout` (0.4 `app.rk`) and `deprecated_template` (`templates/*/template.rk`
  next to the apps dir). `missing_dependency` stays for 0.4 units. An app's menu and dashboards see its modules, their
  requires and its optional.
- `/api/v1/meta`: `apps` (apps and 0.4 units, each with `kind` and `modules`), `modules` (modules and 0.4 units, with
  `requires`), and `module` on every model.
- Kernel tests use `kernel/testdata/catalogue-v0` (rangka-apps as of v0) plus `testdata/modules-apps` and
  `testdata/broken-modules-apps`.
