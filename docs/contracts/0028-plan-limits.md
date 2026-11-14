# 0028 Plan limits

Status: accepted (6 Nov 2026) · Owner of the change: rangka-os (kernel) · Affects: Juragan Cloud (provisioning,
billing), rangka-apps sites with `features.rk` plans, the web app (a plan banner later)

## Context

`features.rk` names plans with `limits:` (for example `mulai`: 1 outlet, 3 users, 200 AI credits a month;
`tumbuh`: 5 outlets, 15 users, 2,000 credits), and `config.rk` names the site's `plan`. Until now the kernel only
counted usage (`GET /api/v1/orgs` → `usage`) and never refused anything, so a tenant on the cheapest plan could add
outlets, staff and assistant chats without end, and the plans sold on the landing page meant nothing. Changing a
tenant's plan meant editing `config.rk` and restarting the kernel.

## Decision

1. **The tenant's plan.** One kernel serves one tenant (one business, possibly several organisations). Its plan is
   the one an operator set with `rangka plan set <plan>` (stored in `rk_site_plan`, source `operator`), else
   `config.rk` `plan` (source `config`). Limits come from that plan in `features.rk`, `extends` resolved (the
   parent's limits, overridden by the child's). A site without a plan, a plan not in `features.rk`, or a plan without
   a given limit is not limited by it. Limits count over all organisations of the tenant.
2. **Enforced limits.** Three limits are enforced; the others (`records`, `storage_mb`, `api_calls_day`) are only
   reported.
   - `outlets`: creating a record of model `outlet` (REST, import, a flow's `create`) past the limit is refused with
     403 `plan_limit`. Creations take an advisory lock so two at once cannot both take the last place. Seed is not
     checked (operator data).
   - `users`: people, each counted once however many organisations they belong to. `rangka user add` refuses a new
     person past the limit (`plan_limit`, exit 1); an existing member is not a new person. Any future member API
     calls `plan.CheckNewPerson` the same way.
   - `ai_credits_month`: one credit is one chat (`POST /api/v1/ai/chat`), taken atomically before the chat is
     relayed and given back when the chat never reached the assistant (sidecar down or refusing). Past the limit the
     chat is refused with 403 `plan_limit`. The month is `YYYY-MM` in the site timezone (`config.timezone`); credits
     are kept in `rk_ai_usage` and start again each month. A site without the limit still counts.
3. **Refusal.** `{"error": {"code": "plan_limit", "message": …}}` with a message for the owner, in Indonesian, that
   names the plan, the limit, and the next plan that has room ("Naik ke paket Tumbuh untuk menambah.").
4. **Downgrade.** Changing the plan deletes nothing. What is past a lower limit becomes read-only, code
   `plan_read_only` (403):
   - outlets: the newest outlets past the limit (by creation, then name). Changing such an outlet, or creating or
     changing (write, submit, cancel, amend, delete) a record whose link field to `outlet` names it, is refused.
   - people: the newest members past the limit; owners come first and the first person is never read-only. Every
     change by such a person is refused.
   The same holds for adding or removing attachments of such a record, and for approving or rejecting a request: a
   read-only person decides nothing (change sets included: proposing, approving, rejecting, applying or reverting one
   is refused with `plan_read_only`; a proposal would otherwise reach `config.ai.auto_apply: low_risk`), and nobody
   decides on a record of a read-only outlet. Reading, printing and exporting are
   never refused. Kernel actors (seed, flows and hooks the kernel starts) are not checked.
   The check reads a snapshot cached in memory and never takes a database connection inside a write: a stale
   snapshot (older than 10 seconds, or after an outlet was created) is served while one background refresh measures
   again. So a running kernel sees a plan change within about 10 seconds; an upgrade lifts the read-only marks.
   Before the first measurement, or while measuring fails, writes are not held back (the creation limits still hold).
5. **Usage endpoint.** `GET /api/v1/plan` (any signed-in member) answers
   `{plan: {name, label, source, known}, plans: [{name, label, limits}], usage: {outlets|users|ai_credits_month:
   {used, limit, state, message}}, notices: [{resource, state, message}], read_only: {outlets: ["org/OUT-02"],
   users: [email]}, period}`. `state` is `ok`, `near` (from 80% of the limit), `full` (at the limit) or `over` (past
   it, after a downgrade); `limit` is null when not limited. `notices` are ready for an owner banner.
   `read_only.outlets` lists only outlets of the caller's organisations; `read_only.users` lists every read-only
   person to an owner of an organisation they belong to, and to anyone else only themselves.
6. **Who changes the plan.** Only an operator, with `rangka plan set <plan> [--by] [--note]` or `rangka plan clear`
   (back to `config.rk`); each change is kept in `rk_plan_changes`. There is no REST endpoint: payment is manual
   (concierge, QRIS), so Juragan Cloud changes the plan after it marks the invoice paid. The serving kernel copies
   the plan's limits to `rk_site_plan` at start and on reload, so `rangka user add` (which reads no metadata) sees
   them.
   While an operator set the plan, the plans in `features.rk` are the operator's too: a change set that touches
   `features.rk` is refused with `403 plan_managed` when proposed, applied or reverted, so a business cannot raise its own
   limits. The operator edits the file and runs `rangka plan set` again. A change set that touches `features.rk` is
   pinned to the file's text when proposed (the op's `base`, contract 0014 §5), so if the operator writes the file
   before the apply, the apply answers `409 stale_base` instead of writing over the operator's plans. A `features.rk` op
   without `base` (proposed before plans were pinned) is refused with `409 stale_base` at apply while the file exists,
   so it is proposed again (apply-rows audit note 4). On the company's own plan (`config.rk`, no
   operator) a change set may edit `features.rk` as before.

## Not decided here

Prices, invoices, trials, grace periods and suspending an unpaid tenant belong to Juragan Cloud. Apps and
capabilities still follow `config.rk` (`plan`, `apps`): a plan change through `rangka plan set` changes limits only;
installing other apps needs `config.rk` and `rangka migrate`.

## Consequences

- Known limits: records tied to a read-only outlet only indirectly (a payment of an invoice of that outlet) are not
  locked; outlets created at the same moment are ordered by name as text; a chat cut off before the assistant's
  first answer gives its credit back even if the assistant had started; two `rangka user add` at once can pass the
  people limit by one.

- Rollback: `rangka plan clear`, or remove `limits:` from `features.rk` (nothing limited).
- The tables are new (`rk_site_plan`, `rk_plan_changes`, `rk_ai_usage`) plus `rk_members.added_at` (existing members
  get the migration time, so their order is by user id).
