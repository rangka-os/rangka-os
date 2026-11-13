# 0048 Account access mode (unpaid bills)

Status: proposed (13 Nov 2026) · Owner of the change: rangka-os (kernel, web app) · Affects: rangka-cloud (billing,
dunning, router, Konsol), rangka-edge (`/edge/config`, POS banner) · Amends: 0006 §4 (config gains `account`) ·
Builds on: 0028 (plan limits, operator CLI, write gate), 0030 §7 (late writes with `_occurred_at`), 0043 (member API)

## Context

- **Owner decision (Dede, 13 Nov 2026).** Rangka Cloud bills a business for its package through DOKU. An unpaid bill
  gets 7 days of grace (the system runs fully), then 30 days read-only, then the business is suspended; its data is
  deleted 90 days after suspension, only after notice. Paying at any stage makes it active again. Design: Figma
  "Rangka OS" → page "Rangka Cloud" → "Pembayaran langganan · DOKU · usulan".
- **0028 leaves this to Cloud.** "Prices, invoices, trials, grace periods and suspending an unpaid tenant belong to
  Juragan Cloud." Cloud can already suspend (`provision.Env.Suspend`: the kernel is stopped, the router answers a 403
  page) and resume. It has no way to make a running kernel read-only.
- **The kernel has a write gate, but per outlet and per person.** `engine.planReadOnly` refuses writes by a person
  past the people limit and on records of an outlet past the outlet limit (`plan_read_only`), and the change-set
  service asks the same snapshot. Reading, printing and exporting are never refused; kernel actors are not checked.
  There is nothing for "the whole tenant".
- **The router cannot do it.** It sees HTTP, not meaning: `POST /api/v1/compute/{model}` writes nothing, MCP tools
  write through one endpoint, and connector webhooks (`/hooks/…`) carry money the business is owed. Only the kernel
  knows which request changes data.
- **Sales already made must never be lost.** 0006 §5.1: a sale recorded at the outlet is a fact. rangka-edge moves a
  `rejected` event to dead-letter and never retries it; a web counter replays queued presses with `_occurred_at`
  (0030 §7). A hold that refuses these would lose records of money that changed hands.

## Decision

### 1. Modes

One kernel serves one tenant (0028 §1). The tenant has one **access mode**:

| mode | meaning | writes |
| --- | --- | --- |
| `full` | default | as today |
| `notice` | grace: a bill is overdue, nothing is held yet | as today; every member sees the banner (§5) |
| `read_only` | held | refused, with the exceptions of §3 |

Suspension is not a kernel mode: Cloud stops the kernel (0028 "Not decided here" stays true). The modes are not tied
to billing: an operator may hold a tenant for any reason, and a self-hosted kernel is `full` unless its operator sets
otherwise.

### 2. Who sets it

- Only an operator, with the CLI, like `rangka plan set` (0028 §6):
  ```
  rangka access show [--json]
  rangka access set notice|read_only [--since <RFC 3339>] [--until <date>] [--message "..."] [--url <https url>]
                                     [--sales open|held] [--by <who>] [--note "..."]
  rangka access clear [--by <who>] [--note "..."]
  ```
  `--since` defaults to now and may not be in the future. `--until` is shown only ("sampai 8 Des 2026"); the kernel
  never changes mode by itself. `--message` is the owner-facing text (Indonesian), `--url` the place to act
  (Cloud's billing page), `--sales` is §3.3. `clear` is `full`.
- Stored in `rk_site_access` (one row: mode, since, until, message, url, sales, set_by, set_at, note); every change
  is appended to `rk_access_changes`. There is no REST endpoint and no file: a change set cannot touch it, so a
  business cannot lift its own hold.
- A running kernel sees a change within 10 seconds (the 0028 snapshot refresh, which now also reads
  `rk_site_access`). Unlike plan limits, the mode is read **before the kernel serves its first request** and on every
  reload, and a failed refresh keeps the last mode read; it never falls back to `full` because a read failed.

### 3. What `read_only` refuses

**3.1 Refused** with `403 account_read_only`, before any plan check, for every actor that is a person, an API token,
the assistant (0020) or an MCP client:

- record writes: create, write (PATCH), delete, and the lifecycle verbs submit, cancel, amend, also through
  `submit` many, actions and flows run by a person (`POST /api/v1/flows/{name}/run`);
- imports (`commit`; preview is allowed), attachment upload and delete;
- approval decisions; change sets: propose, approve, apply, revert; catalogue install and remove (0036);
- process edits (`processes/{name}/ops`, layout), connector secrets (`PUT|DELETE /connectors/{c}/auth/{f}`);
- members: adding a person or changing a role (`PUT /api/v1/members/…`, `rangka user add`);
- assistant chats (`POST /api/v1/ai/chat`); no credit is taken.

Body: `{"error": {"code": "account_read_only", "message": <message>, "url": <url>, "since": <since>}}`. Without
`--message` the kernel says "Sistem sedang hanya-baca. Data tetap bisa dilihat dan diunduh. Hubungi pemilik usaha."

**3.2 Never refused**, in any mode:

- reading, printing, export, dashboards, screens, `GET /api/v1/plan`, the event stream, `POST /api/v1/compute/{model}`
  (it writes nothing), change set and import previews;
- sign-in, sign-out, switching organisation, creating an API token (tokens read too);
- removing a member (`DELETE /api/v1/members/…`): taking access away from someone who left must always work;
- kernel actors: the scheduler, flows and hooks the kernel starts, recurring documents (0031), queue jobs;
- connector webhooks (`/hooks/…`) and what their flows write: a customer paying the business is money owed to it;
- writes that happened before the hold: a replayed web-counter write whose `_occurred_at` (0030 §7) is before `since`,
  and every edge event (3.3).

**3.3 Sales** (`--sales`, default `open`):

- **Edge events are always accepted**, in every mode and whatever `--sales` says (0006 §5.1: a sale is a fact). Under
  `held`, `/edge/config` tells the device to stop opening new sales (§4), so new events stop at the source, not by
  refusing them at the kernel.
- `open`: the counter keeps selling. Writes are allowed on the models a `counter` screen (0030) names: its
  `document.model` (lines and payment rows are its tables), its `session.model` and `session.close.model`, for actors
  that may open that screen. Everything else in 3.1 is refused.
- `held`: those writes are refused too, except replays from before `since` (3.2).

### 4. Edge (amends 0006 §4)

`GET /edge/config` gains `"account": {"mode": "full|notice|read_only", "sales": "open|held", "message": "...",
"since": "..."}`; a mode change changes the config `version`. The device shows the message on the POS. Under
`read_only` + `held` it closes the sale screen for new sales; a sale already in progress may finish. Events are never
refused for the mode (3.3), so no queued sale ends in dead-letter because of a bill.

### 5. Telling people (web app)

- `GET /api/v1/plan` gains `access: {mode, since, until, message, url, sales}` and, when the mode is not `full`, a
  notice `{resource: "account", state: "notice"|"read_only", message}`. `url` is sent to owners only (0028 §5 roles);
  others get the message with "Hubungi pemilik usaha".
- The web app shows the notice as a banner on every page, with a button to `url` for owners. Under `read_only` it hides
  or disables buttons that would write (§3.1), and a refused write shows the message, not a generic error.
- `/system/status` reports the mode, so the Console and Cloud can read it back.

### 6. Order of checks

`account_read_only` (this contract) → `plan_read_only` / `plan_limit` (0028) → permissions → model rules. A
refused write takes no number, no credit and no lock.

## What Cloud does (for reference, not decided here)

- Billing state per business: `active → grace (7 days) → read_only (30 days) → suspended → purged (90 days, with
  notices at 30, 7 and 1 day)`. On each move it runs `rangka access set notice|read_only --message … --url
  <Konsol billing page> --note <invoice>` the same way it runs `rangka plan set` (`provision/packages.go`), and the
  existing `Suspend` / `Resume` / `Delete`.
- A paid DOKU notification moves the business back: `rangka access clear`, or `Resume` for a suspension whose reason
  is billing (`SuspendedBy: billing`; a staff suspension is not lifted by a payment).
- The Konsol stays reachable in every state (it runs on Cloud), so the owner can always pay and download data.

## Kernel work

1. Tables `rk_site_access`, `rk_access_changes` (`store/system.sql`); `plan.Snapshot` carries the mode; boot reads it.
2. `rangka access` (show, set, clear) next to `rangka plan`.
3. One gate, `engine.accountReadOnly(actor, model, action, rec, occurredAt)`, called where `planReadOnly` is called
   today, plus the API handlers of 3.1 that do not go through the engine (imports commit, attachments, processes,
   connector auth, members, catalogue, change sets via `changeset.Service.ReadOnly`, AI chat). The counter model set
   of 3.3 is computed from the registry on load.
4. `GET /api/v1/plan` and `/system/status` fields; `/edge/config` `account` when the edge endpoints are built (0006
   kernel work §4).
5. Web app banner and disabled actions.
6. Tests: every route of 3.1 refused and every route of 3.2 allowed under `read_only`; `open` vs `held` on a counter
   model; a replay with `_occurred_at` before and after `since`; a webhook flow writing under `read_only`; boot with
   `read_only` set refuses the first write; a refresh failure keeps the mode.

## Not decided here

- Prices, the length of each stage and the notices: Cloud's (the numbers above are the 13 Nov design).
- Whether the counter keeps selling while held: `--sales open` is the default because stopping a shop's till over a
  late bill hurts its customers before it reaches the owner, and offline devices cannot be stopped at once anyway.
  Cloud passes `held` if Dede decides otherwise.
- Holding a single organisation of a tenant (each business is its own tenant on Cloud today).

## Consequences

- A held tenant keeps its data readable and exportable, and keeps receiving money (webhooks, edge sales, the counter
  under `open`). What stops is change: back-office edits, configuration, new people, Raka.
- Known gaps: a person mid-form when the hold starts loses that form's unsaved changes on save (the message says why);
  under `held`, a web counter opened before the hold and offline until after it has its presses refused when it
  reconnects (they are newer than `since`) and shown to the cashier per 0030 §7.
- Rollback: `rangka access clear`. Dropping the tables returns every tenant to `full`.
