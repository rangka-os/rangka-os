# 0007: Document approvals

Status: proposed (3 Nov 2026) · Owner: rangka-os · Stream S2 of the pilot program

## Context
Both pilots need a manager to approve documents before they take effect: a quotation before it goes out, a
purchase order before it reaches a vendor, an invoice above a threshold. Today `submit` makes a document final at
once, and the only approval in the kernel is for change sets (metadata), which is a different thing.

The approval must be deterministic (no LLM decides it), must bind to what the approver actually saw, and must use
the same lifecycle (draft → submitted → cancelled → amended) instead of adding a second one.

## Decision

### 1. Model key `approvals:` (schema 0.5)
```yaml
approvals:
  - name: manager_over_10m
    on: submit                 # submit | cancel | <action name of this model>
    when: grand_total > 10000000   # CEL over the record, optional (absent = always)
    approvers: [sales_manager]     # roles; owner may always approve
    label: {id: Persetujuan manajer, en: Manager approval}
```
- `name` is unique in the model. `on: submit|cancel` needs `is_submittable: true`; any other value must be an
  action of the model.
- Company overlays (`site/custom/<model>.rk`) may add `approvals:`; they are appended to the app's list, like `rules`.
  This is how a site sets its own thresholds without editing `apps/`.

### 2. What a matching transition does
When a person asks for a verb (`submit`, `cancel`, or an action) and one or more policies with that `on` match
(`when` true, evaluated on the stored record):
1. The verb's own checks run first (submit rules, required fields, warnings). A document that would fail submit
   never creates a request.
2. For each matching policy without an unused approved request (§6) for the same snapshot, the kernel inserts a row in
   `rk_approvals` with state `pending`, bound to the record's **snapshot hash** (§3).
3. The verb does **not** run. The record keeps `docstatus 0` (or 1 for cancel). REST answers **202**
   `{approval: <first pending item>, approvals: [<all items>], record: <record>}`.
4. When the caller holds one of the policy's approver roles (or `owner`) and is a person (§5), the request is
   created already `approved` by the caller (self-approval is recorded, not skipped). If every matching policy is
   approved this way, the verb runs at once and the call answers as before (200).

When the last pending request of a (record, verb) is approved, the kernel runs the verb in the same transaction,
**as the requester** (their id, user record and roles, stored on the request), with the request's arguments
(`force` for submit, `args` for actions). Audit entries record both requester and approver. If the verb now fails
(a rule fails because linked data changed, a warning needs `force` that was not given), the approval is rolled back
and the error is returned; the request stays pending. Once the verb has run, the other pending requests of the
same (record, verb), made for other arguments, become `void` (SSE `approval` event, audit `approval.void`).

Reject (`{reason}` required) sets the request `rejected`; other pending requests for the same (record, verb) become
`void`. The record stays a draft and can be edited and submitted again, which creates new requests.

### 3. Snapshot binding (the Review Focus rule)
- `snapshot` = SHA-256 (hex) of the canonical JSON of the stored record: every field of the model and `x_` fields,
  child tables as lists of their rows' fields (row `name`, `idx`, `parent`, `parent_field` left out), keys sorted,
  numbers as stored. `name`, `docstatus`, `status`, `created_*`, `modified_at` are left out.
- Any update or delete of a record with pending requests sets them to `void` in the same transaction (SSE
  `approval` event, audit `approval.void`).
- Approve recomputes the snapshot from the stored record; if it differs from the request's, the request becomes
  `void` and approve returns **409** `approval_void` ("Dokumen berubah setelah diajukan; ajukan ulang"). This guards
  against writes that bypass the engine.
- A submitted document cannot be edited (cancel + amend), so "edited after approval" can only mean "edited while
  pending", which voids the request.

### 4. Flow step `approve`
```yaml
- approve: {approvers: [purchasing_manager], when: po.grand_total > 5000000, label: {id: Setujui PO}}
  as: po_approval
```
- If the step's `if:` or the map's `when` is false, the step is skipped.
- Otherwise a request is created (model/name empty, `flow` set, snapshot = hash of the flow variables) and the
  flow stops, like `wait`. Approve runs the remaining top-level steps in the approve transaction with the saved
  variables, as the flow's original actor; `as:` holds `{state: "approved", by, at}`. Reject ends the flow
  (audit `flow.rejected`). `approve` is only allowed at the top level of a flow (as `wait`).

### 5. Who may decide
- Approve and reject need a person: a browser session (`Principal.Human()`), as for change sets. API tokens and
  OIDC (AI) tokens get **403** `human_required`; MCP's `rangka_approve` follows the same rule, so an agent can list
  approvals but never decide one.
- The caller must hold one of the request's `approvers` roles or `owner`; otherwise **403** `permission_denied`
  with S1's body `{code, message, model, action}`, `model` the request's model and `action` `approve`. Approving
  is governed by the policy's approvers, not by model `permissions:`.
- For a request on a record the approver must also be allowed to **read** that record (contract 0005); otherwise
  **403** `permission_denied` with `action: read`.
- S1's permission check for submit, cancel and actions (`write`) runs **before** the hold: a caller without the
  permission gets 403 and no request is created. When the last approval lands, the verb runs as the requester and
  that check is made again for the requester at approve time.
- Only `pending` requests can be decided (**409** `not_pending`).

### 6. Storage
```sql
create table if not exists rk_approvals (
  id           text primary key,           -- apr_<random>
  org_id       text not null,
  model        text not null default '',   -- '' for flow approvals
  name         text not null default '',   -- record name; follows the rename at submit (DRAFT-… → number)
  verb         text not null,              -- submit, cancel, <action>, or flow:<flow>
  policy       text not null,              -- approvals[].name, or the flow step label
  label        jsonb,
  approvers    text[] not null,
  state        text not null,              -- pending, approved, rejected, void
  snapshot     text not null,              -- sha256 hex
  args         jsonb not null default '{}', -- {force} / action args / flow resume state
  requested_by text not null,
  requester    jsonb not null,             -- {id, user, roles, human, system} the verb runs as
  requested_at timestamptz not null default now(),
  decided_by   text,
  decided_at   timestamptz,
  reason       text not null default '',
  used_at      timestamptz                 -- set when the verb ran with this approval
);
create index if not exists rk_approvals_org_state_idx on rk_approvals (org_id, state, requested_at desc);
create index if not exists rk_approvals_record_idx on rk_approvals (org_id, model, name);
```
- `used_at` makes an approval single-use: when the verb runs (the approve that completes the requests, or a call
  whose requests were all self-approved), every approved request of that (record, verb, snapshot, args) gets
  `used_at`. A used approval never satisfies a policy again; the next run of the verb asks again.
- `requester.human` records whether the requester was a person; the verb then runs as a person or not, so a
  token-made request never self-approves a policy that starts to match at approve time. Rows without the key are
  read as a person.

### 7. Interfaces
- REST (`/api/v1`): `GET /approvals?state=pending|approved|rejected|void&model=&name=` → `{data, total}` (another
  `state` → **400** `bad_request`, as for MCP's list);
  `GET /approvals/{id}`; `POST /approvals/{id}/approve` `{}`; `POST /approvals/{id}/reject` `{reason}`. Item:
  `{id, model, name, policy, label, verb, approvers, state, requested_by, requested_at, decided_by, decided_at,
  reason, can_decide}`; `can_decide` is true when the caller may decide it now. Approve/reject return 200 with the
  item, plus `record` when the verb ran.
- `POST /r/{model}/{name}/submit|cancel` and `/actions/{action}` answer 202 as in §2 when held.
- Visibility (contract 0005): the list holds requests on models the caller may read (model level: a conditional
  read grant counts, as in `/meta`) and flow requests (model `''`) when the caller is `owner` or holds one of their
  approver roles; owners see everything. `GET /approvals/{id}` needs read on the record (403 `permission_denied`)
  and returns 404 for a flow request the caller may not see. MCP's list applies the same rule.
- SSE: `event: approval` with `{id, model, name, state}` on create and on every state change, to callers who may
  see the request (as for `GET /approvals/{id}`).
- MCP: `rangka_approvals_list` (read-only; `state`, `model`, `name`) and `rangka_approve` (`id`,
  `decision: approve|reject`, `reason`), the latter refused for non-person callers (§5).
- `GET /api/v1/meta` carries each model's `approvals` unchanged.

### 8. Validation (Go and Python identical)
- `approvals/<i>/on`: `unknown_verb` (error) when not submit/cancel/an action of the model; `not_submittable`
  (error) for submit/cancel on a non-submittable model.
- `approvals/<i>/when`: the usual expression checks.
- `approvals/<i>/approvers/<j>`: `unknown_role` (warn), as for `permissions`.
- `approvals/<i>/name`: `duplicate_approval` (error).
- Flow `approve` steps: schema, and parentheses in `when`.

## Rulings made while writing this (check these)
- The verb runs as the **requester**, not the approver: the approver need not hold submit rights, and the submitted
  document's `created_by`/hooks see the person who prepared it.
- **Self-approval is allowed** and recorded: an owner-led firm's owner submitting their own quotation should not
  wait for themself. A site that needs four-eyes can list approver roles the preparer does not hold.
- `owner` may always approve, matching change sets.
- Multiple matching policies all need approval (e.g. manager above Rp 10.000.000 and director above
  Rp 100.000.000); there is no ordering between them.
- Flow approvals bind to the flow's variables, not to a record; a flow that needs record binding should use a
  model `approvals:` policy on the verb instead.
- Approvals are **bound to the verb's arguments and single-use**: a request matches only the same policy, snapshot
  and arguments (`{force}` for submit, the action's args), and earlier approvals let the verb run only inside the
  approve that completes them. Once the verb has run, its approvals are marked `used_at` (§6) and never count
  again. Running an action again on an unchanged record asks again.
- A `when` that cannot be evaluated **requires approval** (fail closed), unlike rules, which let a broken check pass.
- The snapshot includes every declared field, including a real `status` field (only the derived status is left out).
- **Flow approvals never self-approve**; they always wait. Flow requests remember whether the starter was a person.
- Reject voids the other pending requests of the same verb only; requests for other verbs on the record stay.
- Schema: the keys live in `schema/0.5/` (model `approvals`, flow step `approve`, overlay `approvals`).
- Concurrent work on one record is **serialised** with a transaction-scoped advisory lock
  (`pg_advisory_xact_lock(hashtext(org/model/name))`) taken by hold, approve and reject, so two approvers cannot
  each see the other pending and a double submit makes one request per policy.
- **Attachments are not part of the snapshot** for the pilots: uploading a file to a held draft does not void its
  requests.
- Known limits: editing a flow definition while one of its approvals is pending resumes the flow at the stored
  step index of the new definition; action args reach an approved hook after a JSON round trip (numbers as
  `float64`), as for queued actions.

## Consequences
- Apps can require approval with metadata only; sites can tighten it through overlays.
- The web app (S0) gets an inbox from `GET /approvals` and a "Menunggu persetujuan" banner from `?model=&name=`.
- The inbox follows S1's read permissions (§7); outlet row scope follows S5 when it lands.
