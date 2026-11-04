# 0014: Process canvas

Status: proposed (4 Nov 2026) · Owner: rangka-os (kernel, schema 0.5, web) · Stream S10 · Affects: rangka-apps (apps ship process files), Console and web app (canvas view), rangka-ai (reads the graph)

## Context
- A business is described in `.rk` as many small files: models, rules, approvals, automations, flows. Nobody can see "from lead to paid" in one place, and an owner cannot change one rule without opening a file.
- The process canvas draws those files as one picture: stages left to right, the triggers, rules, approvals, actions and lifecycle steps of each stage as nodes, and edits to a node's text values go through a change set (contract 0007's apply path), never straight into a file.
- `.rk` stays the only source of truth. The canvas keeps only layout (collapsed stages, positions).
- Inference alone cannot name "Lead masuk" or "Lunas", or say that two stages share `sales_invoice`, so a small kind `process` names the stages. An app without one still gets a canvas by inference.

## Decision

### 1. Kind `process` (`processes/<name>.rk` in a module/app or in the site)
```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/process.json
name: lead_to_cash          # = file stem
label: {id: Dari lead sampai lunas, en: Lead to cash}
stages:
  - {name: lead_in, label: {id: Lead masuk, en: New lead}, model: lead}
  - {name: deal, label: {id: Peluang, en: Deal}, model: deal}
  - {name: invoice, label: {id: Faktur, en: Invoice}, model: sales_invoice}
  - {name: paid, label: {id: Lunas, en: Paid}, model: sales_invoice, when: "status == 'paid'", models: [payment]}
```
- Required: `name`, `stages` (at least one). A stage requires `name` and `model`.
- A process changes no behaviour. It is read by the canvas only.
- A site `processes/<name>.rk` with the same name replaces the unit's one (like a site print format).
- The registry key (and the `Registry.Docs` key) is `<unit prefix>/processes/<name>.rk` (`modules/<m>` or `apps/<a>`) or `processes/<name>.rk` for the site. A node's source file is such a key.
- A process of a unit that is not installed is ignored. A stage's `models:` entry whose module is not installed is skipped at run time without a message, so a process may span optional modules.
- Stage position in the list is the stage's identity for edits: JSON pointer `/stages/<index>`.

### 2. Stage keys
`{name, label, model, when?, models?}`.
- `model`: the stage's main model. Every node of that model belongs to the stage.
- `when`: a CEL condition on `model` (`status == 'paid'`). It narrows the stage to documents in that state; two stages may share a model and tell themselves apart by `when`. A stage-level `when` is itself a node (a step) of the stage and is editable.
- `models`: more models whose nodes join the stage.

### 3. Inference (ruling 1: declared, then inferred)
Every installed app (kind app or legacy) with a `menu:` and no declared process named like the app also gets an inferred process `<app>`: one stage per non-child menu model, in menu order, named after the model and labelled by the model's label. `GET /processes` marks it `inferred: true`. So any bundle has a canvas without authoring. A declared process of the same name wins.

### 4. Graph REST (`/api/v1`)
`GET /processes` → `{data: [{name, label, inferred, stages, source}]}`, only processes with at least one stage the caller may read.

`GET /processes/{name}`:
```json
{"name": "lead_to_cash", "label": {"id": "Dari lead sampai lunas"}, "inferred": false,
 "source": {"file": "apps/crm/processes/lead_to_cash.rk"},
 "groups": [{"id": "lead_in", "label": {"id": "Lead masuk"}, "model": "lead", "models": ["lead"], "when": "",
             "source": {"file": "…", "path": "/stages/0", "line": 5, "col": 5},
             "nodes": ["<node id>", "…"], "counts": {"trigger": 1, "rule": 1, "action": 1, "approval": 0, "step": 4, "condition": 1}}],
 "nodes": [{"id": "modules/pipeline/models/deal.rk#/automations/0", "group": "deal", "kind": "trigger|condition|rule|action|approval|step",
            "label": {"id": "…"}, "detail": "stage changes to 'won'", "model": "deal",
            "source": {"file": "modules/pipeline/models/deal.rk", "path": "/automations/0", "line": 120, "col": 5},
            "editable": {"when": "stage changes to 'won'", "if": "app_installed('invoicing')"}}],
 "edges": [{"from": "<id>", "to": "<id>", "kind": "flow|lifecycle|handoff", "label": {"id": "…"}}],
 "layout": null}
```
- Node id = `<file>#<JSON pointer>`. A guard condition: the guarded item's pointer plus `/if` or `/when`. A lifecycle step: the option's pointer (`…/fields/3/options/4`). A derived-status step: `<model file>#status/<value>`. A stage-level `when` step: `<process file>#/stages/<i>/when`.
- A node added by a company overlay (`site/custom/<model>.rk`: a rule, approval or field) has the overlay file and pointer as its source, not the app file.
- `editable` maps key paths (dot-separated, relative to the node's `source.path`, for example `when`, `message.id`, `set.status`, `approvers`) to current values.
- Edge kinds: `flow` (inside a flow or automation), `lifecycle` (status steps of a model), `handoff` (from the last node of a stage to the first of the next).

### 5. Edits: change sets only
`POST /processes/{name}/ops` `{edits: [{node, key, value}]}` → `{title, ops}`. Compute only, nothing is written. `400 bad_edit` for an unknown node or key. `ops` is empty when every value equals the current one. The client then `POST /api/v1/changesets {title, ops}`; the change set goes through the normal states (proposed, validated, previewed, approved, applied). Nothing on the canvas writes metadata directly, and AI never approves or applies.

- **Byte-stable edits (ruling 3).** A scalar edit splices the re-quoted scalar into the original text at its exact span (comments and formatting kept) and emits op `replace` with the full new text. A non-scalar or multi-line target falls back to op `patch` (JSON Patch, re-dumped by `rk.Dump`).

### 6. Layout (ruling 5: layout is not metadata)
`PUT /processes/{name}/layout` `{groups: {<id>: {collapsed?: bool, x?: number, y?: number}}, nodes: {<id>: {x, y}}}` → 200 with the stored layout.
- Written to `<site>/canvas/<name>.json`: 2-space JSON, sorted keys, trailing newline. It is stored directly, never through a change set, and never changes behaviour.
- Owner (or system) only: 403 `permission_denied` otherwise. 400 `bad_layout` for other keys.
- A layout file that is missing, empty or not valid JSON is not an error: the graph answers with `layout: null`.

### 7. Permissions (ruling 4)
- A stage (and its nodes) shows only when the caller has model-level `read` on its model. Nodes of `models:` entries likewise. A flow joins a stage when one of its steps creates, updates or finds the stage's model.
- A hidden stage disappears; the handoff edge joins its neighbours; no node of the hidden model leaks.
- A process with no visible stage is 404 `not_found`: its existence does not leak.

### 8. Validation (`rangka validate`, kernel and `tools/rangka_validate.py`, identical text)
- `schema`: the file does not match `schema/0.5/process.json` (no stages, unknown key, bad name).
- `process_name_mismatch`: `name` differs from the file stem.
- `process_duplicate_stage`: a stage name appears twice in one process.
- `process_unknown_model`: a stage `model` or a `models` entry names no model in any loaded unit (installed or not); the message suggests the closest model name.
- `process_when`: `when` is not a valid condition over the stage model (unbalanced parentheses, unknown function or field, `option()` on a non-select field). The message is that of the same check applied to a rule's `check`. It is not checked when the stage's model is unknown (that is reported once, as `process_unknown_model`).

### 9. Web
`window.RangkaCanvas = {Canvas, mount(el, opts), client(baseUrl)}` from `web/canvas/canvas.js` and `web/canvas/canvas.css`, using design-system tokens and the React UMD already in use. On a narrow screen the canvas scrolls horizontally inside its own container; the page never does.

### 10. Files proposed for rangka-apps (ruling 6)
- `apps/crm/processes/lead_to_cash.rk`: the example in §1.
- `apps/jasa_proyek/processes/quote_to_cash.rk`: Penawaran (`quotation`) → Pesanan (`customer_order`) → Proyek (`project`, models `assignment`, `timesheet`, `acceptance`) → Faktur (`sales_invoice`) → Lunas (`sales_invoice`, `when: "status == 'paid'"`, models `payment`, `payment_allocation`, `withholding`).
- The kernel's tests use a snapshot of rangka-apps under `kernel/testdata/canvas/` with these two files added. Adding them to rangka-apps is a follow-up PR there.

## Consequences
- Any bundle shows its flow on a canvas without authoring; an author who wants real stage names adds one small file.
- An owner changes a rule's text or an approver from the canvas, and the edit lands as a reviewable change set with a diff that touches only that value.
- The validator checks process files in both implementations; they print identical reports (`compare-python` covers the canvas snapshot and a broken set).

## Open
- Editing structure (adding or moving nodes, adding a stage) is not part of this contract; only text values of existing nodes.
- Per-user layouts (today one layout per site).
