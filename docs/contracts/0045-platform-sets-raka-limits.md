# 0045 The platform sets Raka's limits

Status: proposed (12 Nov 2026) · Owner of the change: rangka-os (kernel, skills) · Affects: rangka-ai (guardrails,
`/v1/plan`, agent and consult prompts), rangka-cloud (Raka's token, approval in the studio, build) · Amends: 0033
(accepted with the changes in §4), 0042 §8 (custom pieces), 0032 §5 (menu group for company tables), 0035 (skill
rule) · Builds on: 0016, 0017, 0020, 0028

## Context

- **Owner decision (Dede, 12 Nov 2026).** Raka may customise a business as far as Rangka OS can express it. What Raka
  can and cannot do comes from Rangka OS (schema, validator, the kernel's risk rules and approvals), not from choices
  written into the AI engine or into skills.
- **Today three places refuse before the platform is asked.**
  - rangka-ai `raka/agent/guardrails.py`: `AI_LAYERS = [4, 5, 6]`; every layer-3 path escalates as `connector_layer`;
    a money or stock change on a submittable model escalates as `posted_money_stock`; removing a field or select
    option escalates as `deletion`; `preflight` refuses any request that mentions login, 2FA, SSO, API keys, secrets or
    credentials. rangka-os/rangka-ai#26 adds `studio_approvable`, a copy of Cloud's allowlist inside the engine.
  - rangka-cloud `internal/kernel/overlay.go` `ApprovableOp`: overlay paths, plus a `create` of
    `apps|modules/<u>/(models|flows)/*.rk`. Site `models/`, `flows/`, `processes/` and `connectors/x_*` are refused
    with `not_overlay`, although 0016 put company pieces there.
  - rangka-ai `raka/onboarding/plan.py`: `/v1/plan` returns every `custom` piece as `deferred` "until 0033", and Cloud
    tells the owner "belum bisa aku bangun".
  - Skills follow the same pattern: `rangka-consult` uses custom only "when nothing covers it" and says "money and stock
    records never go in a custom piece"; agent `INSTRUCTIONS` say "a catalogue app that covers the request wins".
- **What the kernel has.**
  - `schema/0.5/changeset.json` accepts `models/`, `flows/`, `processes/` and `connectors/` paths.
  - `changeset.Layer` puts company paths in layer 4 (0016). `Assess` rates money and stock fields, deletions, roles,
    permissions and `system.rk` high. A company flow is always medium, and a new company model is high only with a
    money or stock field (`risk.go`).
  - `Approve` refuses a high-risk change set without the owner (`owner_required`). `Apply` refuses the assistant
    actor always. Low-risk auto-apply only with `config.ai.auto_apply: low_risk` (0020).
  - The assistant actor exists only for `ai: true` JWTs (`Via == "ai"`, 0020).
- **What the kernel lacks.** None of 0033's kernel parts exist: no list of paths an assistant may not propose
  (`ai_forbidden_path`), no company connectors (`connectors/x_*` still resolves into the shared directory), no
  connector test or confirm state, no `approvable_by`. That is the bulk of the work.
- **Raka in the Cloud is not seen as an assistant.** Cloud mints Raka's token with the owner's session
  (`projects.go` `CreateToken(ctx, sess, "raka")`), a plain API token. The kernel records its proposals as the owner,
  so a kernel check keyed on the assistant actor would never run there.
- **Money at run time.** `flow.json` has no step that calls a language model. It has `call`, which calls a connector,
  and a flow can write fields from a call's output.

## Decision

### 1. The rule

- Raka may propose **any change set the platform accepts**: the schema and validator accept its files, and the
  kernel's `Propose` accepts it from an assistant (§2).
- How hard it is to approve is decided by the kernel: `Assess` gives the risk (§2b), `approvable_by` gives who
  approves (§3). Raka never lowers or skips that.
- **Skills and engine prompts describe how, never whether.** They say how to design, order, explain and repair. They
  add no ban of their own. A skill line of the form "Raka never proposes X" is allowed only when the platform refuses
  X and the skill names the platform's code. Design advice ("keep payments in `payments`") is allowed and is worded
  as advice.
- **Every limit Raka states is traceable.** When Raka tells an owner that something cannot be done, it names the
  reason the platform gave: a validator code, a `Propose` refusal, or a capability that `rangka_catalogue` /
  `rangka_meta` does not list. "Aku tidak boleh" without one is a bug.

### 2. Hard limits, in the kernel

`changeset.Propose` refuses an assistant's change set with `403 ai_forbidden_path` and `{paths: [...]}` when any op:

| Op | Why it is a platform limit |
| --- | --- |
| touches `system.rk` | layer 2: database, login, queues, secrets; admin in the Console |
| touches `features.rk`, or `config.plan` (of the site or of an org) | plan entitlements and billing (0028); an operator changes them |
| touches `roles/` files, or changes `permissions` of an existing model | who may do what is decided by people |
| changes `config.ai`, `config.sso` or `config.connector_auth`, of the site or of an org (`orgs.<id>.config`) | decides who approves, how people sign in, and who sets connector secrets and replays jobs |
| any op under `apps/` or `modules/`, except a `create` of `apps/<name>/app.rk` whose name is not a catalogue app | catalogue files belong to their authors and are replaced on upgrade (0016); a company composes its own app from real modules (0032) |
| touches a connector not named `x_`, or edits or deletes an existing connector | catalogue connectors belong to rangka-connectors (0033 §2, §5) |
| deletes any file | deletion of a whole table, flow or file is a person's decision |

- `config.modules` and `config.apps` stay proposable. The validator's plan checks (`app_not_in_plan` and the module
  equivalent) decide whether the plan allows them.
- Removing a field or a select option from an overlay or company model is proposable and rated high (§2b); the data
  stays and only the owner approves.
- Secrets keep 0017 §4 unchanged: the validator's secret codes reject literal secrets; Raka never asks for or reads
  a secret value.
- This table is the only copy. rangka-ai and rangka-cloud read the kernel's answer (§3); they keep no list of their own.

### 2b. Risk rows added to `Assess`

| Change | Risk |
| --- | --- |
| new company model with `is_submittable` or `append_only`, or a `stock:` block | high |
| company flow that creates or updates a money or stock field (a child table's rows included), submits, cancels or amends, or runs a model action | high |
| company flow that creates a draft in a core module (`invoicing`, `payments`, `stock`) | high |
| company flow that writes any field from a `call` step's output into a money or stock field | high |
| other company flows | medium (as today) |
| removing a field or a select option (overlay or company model) | high |
| making a `set_once` field editable (dropping or switching off `set_once`, changing such a field), or a company flow that writes a `set_once` field | high |
| a company flow that writes a field limited by role (`read_only_for` or `hidden_for`, contract 0027; a child table's rows included), or changing who a field is limited for: a flow step writes such a field whatever those roles may do (0027 §6) | high |
| changing `config.connector_auth` (who sets connector secrets and replays jobs), like `config.tax` and `config.ai` | high |
| a key under an org's config (`orgs.<id>.config.<key>`): rated like the same site key, so an org's `tax`, `ai` and `connector_auth` are high (the org's value is merged over the site's, 0010 §2) | as the site key |
| adding or removing an org (a legal entity), or an org entry or config that is not a map | high |
| an org's `name` or `code` | medium |
| model (new, or a company model changed) that starts calling a kernel hook: `on_submit`, `on_cancel`, an automation's `do`, an action's `hook` (`stock.post_moves`, `ledger.post_receivable`, `crm.mark_won`, ...) | high |
| changing `stock:`, `is_submittable`, `on_submit`, `on_cancel`, `automations` or `actions` of a model that calls a kernel hook | high |
| a company flow's `set`, or an overlay's or company model's `compute`/`default`, that reads a field with `hidden_for` (directly or through a link) into a field those roles see | high |
| 0033 §2's company connector table | as written there |

A new flow file in an app or a module (`apps/<a>/flows/`, `modules/<m>/flows/`) is rated as a company flow. High is owner only (`owner_required`): approving it, and reverting it once applied.

A hook is Go code that writes stock, money or core records in the record's transaction, out of reach of the field
and flow rows above. When a flow or this table reads a model's fields it follows the loader: the model's own field
wins over an overlay field of the same name.

The risk is assessed again at apply, against the files as they are then. When it rose (the files changed since the
proposal, so a whole-file `replace` would now undo a later change), apply answers `409 risk_changed`, stores the
higher risk and sends the change set back to `validated`: preview and approve again. Preview may raise the risk too
(a removed unit still holds rows); every raised risk is stored, and approve reads the stored risk.

`revert` refuses with `409 stale_base` when a file changed since the apply (another change set, the operator): it
would put back an older text over that change. Make a new change set instead. Change sets applied before the
kernel kept the applied text are not checked.

### 3. Raka is an assistant everywhere; agents and the Cloud ask the platform

- **Assistant API tokens.** `POST /api/v1/tokens` accepts the scope `assistant`. Such a token resolves with
  `Via: "ai"`, so the kernel treats its holder as the assistant acting for the person who made it: §2 applies,
  proposals get source `ai`, `Apply` refuses it. Only a person can create one. The Cloud mints Raka's token this way;
  existing `raka` tokens are replaced on the next build or Buka sistem.
- **New field on every change set:** `approvable_by: [role]`, computed at validate and preview time (high:
  `["owner"]`; otherwise `config.ai.approvers`, always including `owner`). Returned by `GET /api/v1/changesets/{id}`
  and MCP `rangka_changeset_get`.
- **rangka-cloud, studio:** the approve action no longer calls `ApprovableOp`. It approves with the owner's session
  when `owner` is in `approvable_by` and the change set is `previewed`; the kernel's answer is final.
  `OverlayPath` stays, because the Cloud file editor uses it.
- **rangka-ai** drops its change-set policy: `AI_LAYERS`, the `connector_layer`, `posted_money_stock` and `deletion`
  escalations, and `studio_approvable`. It proposes, reads `invalid`, `ai_forbidden_path` and validator codes,
  repairs up to `max_repairs`, and explains what is left (§5). It keeps:
  - **typed-secret detection** in `preflight`: a password, 2FA code, API key or token pasted into the chat is not
    sent on and the person is told to set it in the Console. A *request about* login, SSO or keys is no longer
    refused by the engine; Raka answers it from skills and the platform (for example "Buka sistem", or `system.rk`
    being admin-only);
  - the claims check (never say done before the platform says applied);
  - `FORBIDDEN_TOOLS` (approve, apply, reject, revert).

### 4. Changes to 0033 (accepted with these changes)

- §1 "escalated to the owner as `posted_money_stock`, not proposed" is replaced: those drafts are **proposed** and
  rated by §2b. Company models may be `is_submittable`, `append_only` or carry money and stock fields when the
  validator accepts them; their numbers run through CEL, flows and the kernel like any module's.
- The sentence "Deposits, payments, invoices and stock moves are always records of the core modules" becomes design
  advice in `rangka-consult` (one ledger is easier to reconcile), not a refusal.
- §5 "What stays escalated" is replaced by §2 of this contract, enforced by the kernel.
- The rangka-ai section of 0033 is replaced by §3. Its repair codes (`company_model_name`, `company_connector_name`,
  `hosts_required`, `secret_literal`, `secret_misplaced`, `connector_host_private`) stay: Raka repairs them from the
  validator's answer.
- The rangka-cloud section of 0033 is replaced by §3. The test and confirm screens for company connectors (0033 §3,
  §4) stay.
- Everything else in 0033 (company connector location and `roots.file`, format, validator codes, test before wiring,
  second confirmation for money writes) is unchanged and is implemented with this contract.

### 5. Custom pieces are built (0042 §8)

- `/v1/plan` drafts each `custom` piece from its `design.records` and `design.steps`: new `models/x_<name>.rk` and any
  `flows/<name>.rk`, as **one change set per piece**, so the owner approves or rejects each one.
- **Where it shows (amends 0032 §5).** A site with apps gets no layout. The kernel's default navigation adds a group
  "Perusahaan" with every model of the `site` unit, after the apps' menus, the same way it adds a group for modules no
  app brings in. A site with a layout gets a `nav.side` entry in the same change set (0016 §6).
- `deferred` keeps only pieces the platform could not take, now as objects: `[{id, reason: {code, message}}]`, where
  the reason is the validator code still failing after `max_repairs` or `ai_forbidden_path`. This changes the
  `/v1/plan` response; rangka-ai and rangka-cloud ship it in the same release.
- **Real gaps** are only:
  1. a platform refusal or validator code Raka could not repair;
  2. a system with no usable API (no connector can be drafted);
  3. edge hardware with no edge module in `rangka_catalogue`;
  4. something no schema key can express.
- For a real gap Raka says it plainly, in the owner's words, and that the team will add support as soon as possible,
  with no date: *"Bagian ini belum bisa aku susun sekarang karena Rangka belum mendukungnya. Tim kami akan secepatnya
  menambah dukungannya, dan aku kabari begitu bisa."* Cloud records it through `internal/gaps` with the platform code
  as evidence and tells the owner when it closes.

### 6. Money at run time

- Raka may design and draft finance features: money fields, submittable documents, deposit and fee flows, drafts in
  `invoicing` and `payments`. They follow §1 and §2 and are rated by §2b (high, owner only).
- At run time, money and stock are computed and posted by CEL, flows and the kernel. Flows have no step that calls a
  language model, and this contract adds none. A flow that writes a money or stock field from a connector `call` is
  high (§2b), so the owner sees it before it runs; with 0033 §4 a money-touching connector also needs the second
  confirmation for writes.
- Raka moving money on its own (payouts, transfers) is out of scope and needs its own contract.

## Contract changes

**rangka-os (kernel, skills)**
- Auth: `assistant: true` on `POST /api/v1/tokens`, resolving to `Via: "ai"`; tests that such a token cannot apply.
- `changeset.Propose`: §2 for assistant actors, `403 ai_forbidden_path`. One test per row.
- `changeset.Assess`: §2b rows, with tests.
- `approvable_by` on change sets (service, REST, MCP `rangka_changeset_get`, REST reference).
- Default navigation: the "Perusahaan" group for `site` models (§5).
- 0033's kernel parts as written there: schema `hosts` and `money`, `roots.file` for `connectors/x_*`, validator codes
  with `make compare-python` testdata, connector test and confirm state and endpoints, send-time checks.
- Skills:
  - `rangka-concepts`: §1 in one paragraph; "what an agent never does" lists §2 with its code.
  - `rangka-consult`: §4–5 drop "only when nothing covers it"; custom pieces are designed and built; the money line
    becomes advice (§4); §5's gap list and wording replace the "two real gaps" text.
  - `rangka-compose`: the console row "draft company pieces only once 0033 is accepted" becomes "draft them".
  - `rangka-changesets`: read `approvable_by` when telling the owner who approves.
  - `skills/README.md` "Write or change one": add the "how, never whether" rule of §1.

**rangka-ai**
- Remove `AI_LAYERS`, the `connector_layer`, `posted_money_stock` and `deletion` escalations, `studio_approvable`
  (rangka-os/rangka-ai#26 is closed in favour of this contract), and the request-topic refusals in `preflight`; keep
  typed-secret detection.
- Agent `INSTRUCTIONS`: drop "a catalogue app that covers the request wins"; point at the skills for ordering.
- `/v1/plan`: §5, one change set per custom piece, `deferred` as objects with `reason`.
- Tests: a custom piece with a money field is proposed (not escalated); an `apps/` edit comes back
  `ai_forbidden_path` from a kernel stand-in and Raka explains it; a pasted API key is not sent on.

**rangka-cloud**
- Mint Raka's token with `assistant: true` (§3).
- Studio approve: from `approvable_by`, not `ApprovableOp`; keep `OverlayPath` for the file editor.
- Build: custom-piece change sets from `/v1/plan` are listed with the others; `deferred` objects become gaps with
  their reason.

## Open

- Console approval by team members (approving with their own kernel session, mapping Cloud members to kernel roles).
  Today only the studio approves, as the owner; this contract does not change that.
- A connector drafted for a language-model API: §2b makes money writes from it high, but the platform cannot tell such
  an API apart from any other. Whether to add a connector flag for it.
- Whether an owner may grant Raka edits to its own drafted company connector while it is still `untested`
  (0033 Open, unchanged).
- Company roles (`<site>/roles/`, 0016 Open): once they exist, whether §2's `roles/` row applies to them.
- The offer step (custom → modules and connectors → bundle, owner confirmation before build) and Raka's memory are
  separate contracts that build on this one.
