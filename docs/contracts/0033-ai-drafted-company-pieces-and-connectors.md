# 0033 AI-drafted company pieces and connectors

Status: proposed (8 Nov 2026) · Owner of the change: rangka-os (schema, validators, kernel) and rangka-ai
(guardrails) · Affects: rangka-cloud (`ApprovableOp`, test and confirm screens), rangka-connectors (nothing existing)
· Depends on: 0016 accepted · Builds on: 0002, 0017, 0020, 0032

## Context

- The Juragan interview (rangka-cloud `docs/specs/2026-11-08-raka-interview-design.md`, "Composition and new pieces",
  "Connector drafts") has Raka write the pieces no module covers. Examples are a rental booking table, a reminder
  flow, and a connector for a service with an open API that the owner gives documentation links for.
- rangka-ai's guardrails (`src/agent/guardrails.ts`, `src/rk/paths.ts`) do not know the site paths `models/` and
  `flows/` of 0016. They escalate every layer-3 path (`connector_layer`), so Raka can draft no connector at all.
- `connectors/<c>.rk` resolves into the shared connectors directory (rangka-connectors). A company's connector written
  there would be lost on the next catalogue upgrade, the problem 0016 solved for models.
- The kernel's `Assess` rates every `connectors/` change medium and every company flow medium, whatever they do.
- The Cloud approves only ops that pass `kernel.ApprovableOp` (rangka-cloud `internal/kernel/overlay.go`). That list
  still allows new model and flow files inside `apps/` and `modules/` (0015 §2) and has no site `models/` or `flows/`.

## Decision

### 1. Company models and flows (0016 paths)
- Raka may propose new `models/x_<name>.rk` and new or changed `flows/<name>.rk` in the site, judged by 0016 §5. Raka
  never proposes model or flow files under `apps/` or `modules/`.
- **Money and stock stay in the core modules.** These are escalated to the owner as `posted_money_stock`, not proposed:
  - a company model drafted by Raka with `is_submittable`, `append_only`, a `stock:` block, or a field with
    `stock: true`;
  - a company flow step that submits, cancels or amends, runs a model action, or `update`s a money or stock field on
    a submittable model.
- Money fields on a company model (an agreed rental price, for example) are allowed and make the change set high risk.
- A flow that `create`s a draft in a core module (a deposit invoice in `invoicing`) is allowed and rated high in the
  kernel when the draft sets money fields. Deposits, payments, invoices and stock moves are always records of the core
  modules.
- A new company model may carry `permissions` that name existing roles only (medium). Changing `permissions` of an
  existing model stays escalated.

### 2. Company connectors
- **Location.** A connector the company owns is named `x_<name>`. Its change-set path is `connectors/x_<name>.rk`,
  stored at `<site>/connectors/x_<name>/connector.rk`, so backups include it and upgrades of the connectors directory
  never touch it. It is layer 3.
- **Validator codes** (Go and Python print identical reports):
  - `company_connector_name` (error): a site connector whose name does not start with `x_`, or a connector in the
    connectors directory whose name does.
  - `duplicate_connector` (error): two connectors with the same name.
- **Format.** The schema is the same, with two keys added:
  - `hosts:` (0017 §5) is required for a company connector (`hosts_required`).
  - `money: true` marks a payment, bank or tax service (§4).
- **Rules.**
  - All of 0017 §5 applies: `secret_literal`, `secret_misplaced`, `secret_host_undeclared`, `secret_plain_http`.
  - New error `connector_host_private`: a host that is an IP literal, `localhost`, or a private, loopback or
    link-local name. The kernel checks it again on the resolved address at send time.
- **Raka may propose a new company connector.** Only a create, never an edit. Risk:

  | Draft | Risk |
  | --- | --- |
  | Only `GET` actions, no triggers, not money-touching | medium |
  | Any other method, any trigger, or money-touching (§4) | high (owner only) |
  | Edit or delete of any connector, a catalogue connector, a non-`x_` name | escalated (Raka); high (kernel) |

- **Secrets never pass.** The draft declares fields only (`secret: true`, no `default:`). 0017 §4 applies unchanged:
  - the change set lists the missing fields under `requires:`;
  - the connector stays `needs_auth` until a person sets them (Console or Cloud form, 0017 §3);
  - Raka never asks for a value and never calls the secrets API.

### 3. Test before wiring
- The kernel keeps a state per (org, company connector): `needs_auth` → `untested` → `tested`.
- `POST /api/v1/connectors/{c}/test {action, input}` runs one `GET` action once.
  - Who: a person with `connector_auth` (0017 §3). An `ai: true` token is refused (0020 §4).
  - Answer: the mapped output, scrubbed (0017 §6) and cut to 20 rows or 4 KB.
  - On success the state becomes `tested`. Audit entry `connector.test`, with the outcome and never the body.
- `GET /api/v1/connectors/{c}/test` returns the last result, so Raka can read it and explain it.
- **Apply-time check.** Applying a change set whose flows `call` or trigger on a company connector that is not
  `tested` in the change set's org is refused (`connector_untested`). That order is the interview's: draft, owner sets
  the secrets, owner runs the test, then the flows.
- **Reset.** Any change to the connector file (its content hash) sets the state back to `untested` and drops the
  confirmation of §4.

### 4. Money-touching connectors: a second confirmation for writes
- **Money-touching** means any of:
  - `money: true`;
  - a `provides` port under `payment.`, `bank.` or `tax.` (0002 §B);
  - a flow that triggers on or calls the connector and creates or updates a model with money or stock fields.
  The kernel and rangka-ai compute the same answer. A draft cannot lose the label by leaving out `money:`.
  Validator warning `money_unflagged`: the flow rule matches but `money: true` is missing.
- Reads work after the test.
- **Write actions** (any method other than `GET`) stay refused at send time with `connector_writes_unconfirmed`
  (no retry, dead-letter with the code) until the owner confirms:
  - `POST /api/v1/connectors/{c}/confirm-writes`, owner only, within 24 hours of a successful test;
  - the screen shows the test result and the list of write actions;
  - it is stored with the connector's content hash, and audited as `connector.writes_confirmed`.
- Catalogue connectors are not covered here (Open).

### 5. What stays escalated
- Editing or deleting any existing connector, including a company connector Raka drafted earlier.
- `system.rk`, including `connector_auth:` and `ports:`.
- Roles, secrets and config `ai`/`sso`.
- Edits to `apps/` and `modules/` files.

## Contract changes

**rangka-os (schema, validators, kernel)**
- `schema/0.5/changeset.json` ops path pattern: add `models/x_[a-z0-9_]+` and `flows/[a-z0-9_]+` (the 0016 paths, not
  yet in the pattern). `connectors/[a-z0-9_]+` stays and covers `x_` names.
- `schema/0.5/connector.json`: `hosts` and `money`. `hosts` moves out of `experimental:` (0017 §5).
- Validators: the §2 and §4 codes. Testdata `kernel/testdata/company-connector` and `broken-company-connector` in
  `make compare-python`.
- Change-set `roots.file`: `connectors/x_*` resolves into `<site>/connectors/`. The loader reads site connectors after
  the connectors directory.
- `changeset.Assess`:
  - a new `connectors/x_*` is rated per the §2 table; editing any existing connector is high;
  - a company flow that creates or updates a model with money or stock fields, or submits, cancels or amends, is high
    (today every company flow is medium).
- `changeset.Propose`: when `source: "ai"`, refuse with `403 ai_forbidden_path` any op on `system.rk`, `roles/`,
  `apps/` or `modules/` files that exist, an existing connector, a connector not named `x_`, or a delete. This is the
  kernel's copy of the guardrails, so a changed sidecar cannot get past it.
- Connectors: the state of §3, `rk_connector_checks` (`org_id, connector, hash, state, tested_at, tested_by,
  writes_confirmed_at, writes_confirmed_by`), the two endpoints, and the send-time checks
  `connector_writes_unconfirmed` and `connector_host_private`.

**rangka-ai (`src/rk/paths.ts`, `src/agent/guardrails.ts`)**
- `CHANGESET_PATH` follows the schema pattern above. `classify()` returns site `models/<m>.rk` as kind `model`, layer 4,
  scope `site`, and `flows/<f>.rk` as kind `flow`, layer 4, scope `site`. `connectors/x_<c>.rk` gets `scope: "site"`
  and resolves to `<site>/connectors/x_<c>/connector.rk`.
- `AI_LAYERS` becomes `[3, 4, 5, 6]`. Layer 3 is limited to a `create` of `connectors/x_*`. Every other layer-3 op
  keeps `connector_layer` (requires admin).
- New problems Raka repairs itself: `company_model_name` (site model not `x_`), `company_connector_name`,
  `hosts_required`, `secret_literal`, `secret_misplaced`, `connector_host_private`.
- `scanSecrets`: inside a connector draft, a field declaration (`{name, secret: true}` without `default`) is not a
  secret. `${VAR}`, `default:` on a secret field, and literal token-like values still escalate as `secrets`.
- `hasRoles`: a new site model's `permissions` that name only roles from `rangka_meta` are medium, not `permissions`.
- Risk table: §1 and §2, plus "money-touching connector" high. Escalation code `posted_money_stock` also covers §1's
  company model and flow cases.
- Header comment and `FORBIDDEN_TOOLS` unchanged. Raka never calls the test or confirm endpoints.

**rangka-cloud (`internal/kernel/overlay.go`)**
- `ApprovableOp` allows any op on `models/x_*.rk` and `flows/*.rk`, and a `create` of `connectors/x_*.rk`.
- It drops the `apps|modules/<u>/(models|flows)` create rule once Ubah writes to site paths (0016 §6).
- The Cloud shows the test result and the confirm button (§3, §4). The owner presses both.

## Open

- Whether catalogue connectors with money (`xendit`) should also need §4's confirmation per site.
- A `read_only: true` action flag for APIs that read with `POST` (search endpoints). Until then only `GET` can be tested.
- Promoting a company connector that recurs across tenants into rangka-connectors (drop `x_`, review, release).
- Whether Raka may propose edits to a company connector it drafted while the state is still `untested`, before
  anything uses it.
