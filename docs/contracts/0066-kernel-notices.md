# 0066 Kernel notices: the inbox, the host, and what apps declare

Status: proposed (18 Nov 2026) · Owner of the change: rangka-os (schema, validators, kernel, web app) · Affects:
rangka-apps (`notices:` in modules, `notify` steps), rangka-cloud (private: the host side, its spec
`docs/specs/2026-11-18-notifikasi-design.md` §8), rangka-ai (Raka writes `notify` steps in site flows) · Builds on: 0007,
0010, 0016, 0017, 0020, 0033, 0040, 0043, 0056, 0063, 0065

## Context

- A kernel knows things its people must hear about. A purchase order waits for approval (0007). The payment ledger
  does not match the gateway statement (`gateway_mismatch`, 0063 §5). A parcel is stuck (`shipping_stuck`, the
  Pengiriman plan in rangka-cloud). Stock went negative.
- **What the kernel has today.**
  - Flows have a `notify` step (`notify: <who>`, `channel: email | push | whatsapp | in_app`, `template`). Apps use it:
    `invoicing/invoice_due_digest`, `pipeline/morning_follow_up`, `stock/stock_negative_alert`, the `payments`
    Xendit flows.
  - The step resolves recipients (`engine.recipients`) and delivers through the `log` driver only: one audit entry
    `notify.<channel>` and one log line. Nobody sees it.
  - `system.rk` `notify.email` (`driver: smtp | log`, `host`, `port`, `username`, `password`, `from`) is in the
    schema and reported in `/system/status` as `capabilities.notify.email`. No SMTP sending is built.
  - The top bar (0065) has a notifications button. Today it shows the toast "Belum ada notifikasi".
  - Approvals have an inbox (`#/approvals`, `GET /api/v1/approvals`) and an SSE event `approval`. A person learns about
    a request only by opening the app.
- **The host.** On Rangka Cloud, owners and their team read a feed and get email from Cloud. Cloud has no way to
  hear from a kernel. Kernels already talk to the host's services with a per-business bearer from deployment
  environment (`RANGKA_AI_URL` + `RANGKA_AI_SECRET`, 0020 and 0017 §2). Cloud already pushes the team to the kernel
  (0043), so it knows every member's role and outlets.

## Decision

### 1. Kinds and declarations

**Kinds** are namespaced. The kernel builds the name; nobody types it in full.

| Kind | Who declares it |
| --- | --- |
| `app.<module>.<notice>` | a `notices:` entry in `modules/<module>/module.rk` |
| `app.<module>.<flow>` | a `notify` step in a module flow with no `notice:` (implicit, §1.3) |
| `site.<flow>` | a `notify` step in a site flow (`flows/<flow>.rk`, 0016, 0033) |
| `kernel.<area>.<event>` | the kernel itself (§5) |

Each part is `[a-z][a-z0-9_]{0,39}`.

### 1.1 Declaration in `module.rk`

Schema `module.json`, new key `notices`:

```yaml
notices:
  - name: gateway_mismatch
    label: {id: Selisih saldo pembayaran, en: Payment balance mismatch}
    description: {id: Saldo di laporan DOKU beda dengan catatan pembayaran.}
    severity: high                 # info | warn | high; default info
    to: [owner, finance]           # roles; owner always allowed
    can: {model: gateway_mismatch, action: read}   # optional, see §3
    link: record                   # record | approvals | none; default record when the step names a record
```

- `name` is unique in the module. `to` lists roles; each must exist in the installed apps (`unknown_role`, existing
  code). `owner` is always a valid role.
- `can` narrows recipients to roles that hold that permission on that model (0005 / model `permissions`), evaluated
  at emit time. With `can` and no `to`, every role that holds it.
- Company overlays cannot change a module's `notices:`. A company changes who hears what by its own site flows (§1.4).

### 1.2 The `notify` step

The existing step keeps its shape and gains optional keys:

```yaml
- notify: owner                    # who, as today: a role, a variable, a field of records found, or an email
  notice: gateway_mismatch         # new: a notices: entry of this module
  title: {id: 'Selisih saldo {m.date}'}                           # new, label with {expr} parts; ≤ 80 runes
  template: {id: 'Laporan DOKU {m.closing}, catatan {m.ledger}.'}  # the text, as today; ≤ 300 runes
  record: m                        # new: the record the notice is about (link and read check)
  outlet: m.outlet                 # new, CEL: the outlet the notice is about
  id: "'gm:' + m.name"             # new, CEL: the dedupe id; default <flow run id>:<step index>
  channel: in_app                  # as today; see below
```

- With `notice:`, severity, `to`, `can` and the label come from the declaration. The step's own `notify:` target is
  added to the declaration's `to`.
- `title` falls back to the declaration's label, else the flow's label. `template` is the text, interpolated as today
  (`cel.Interpolate`).
- `channel` keeps its meaning for the legacy log driver. For notices it is a hint only: a notice always goes to the
  inbox, and mail and later channels are decided by the deliverer (§4) and the recipient's preferences. `channel:
  email` sets the declaration's mail default to on, `in_app` to off (self-hosted, §4.2).

### 1.3 Steps without `notice:` (every existing app)

So that the apps in use today start reaching people without edits:

- A `notify` step whose target is a role (or `owner`) emits a notice of kind `app.<module>.<flow>` (module flow) or
  `site.<flow>` (site flow), severity `info`, title = the flow's label, text = the template.
- A target that resolves to field values or a variable: values that are emails of members of the organisation become
  `to.emails` (§2); other values are dropped and counted in the audit entry.
- The audit entry `notify.<channel>` stays.

### 1.4 Raka and change sets

- Raka adds or changes notices only through **site flows** (`flows/<name>.rk`, 0033): a flow with a trigger and a
  `notify` step. That is how an owner asks "kabari kepala gudang kalau stok minus". The kind is `site.<flow>`.
- Risk follows 0033 for flows (medium). A step whose recipients include a role other than `owner` and whose `record`
  model that role cannot read is refused at validate (`notice_role_cannot_read`, error), so a change set cannot send a
  record's content to people who may not open it.
- Raka cannot edit `module.rk` (catalogue files). It never adds `emails` targets by hand: a person's address in a flow
  is refused for Raka-authored change sets (`notice_literal_email`, escalated to the owner as for other high-risk
  edits).
- Change-set preview shows notices in plain words: "Kirim kabar ke Kepala gudang: Stok {…} minus".

### 2. The event

What the kernel builds for every notice, stores in its inbox (§4), and posts to the host (§3):

```json
{
  "id": "kernel.approval.requested:apr_7Kx2",
  "org": "toko-budi",
  "kind": "kernel.approval.requested",
  "severity": "warn",
  "to": {"owner": true, "roles": ["purchasing_manager"], "emails": [], "outlet": "OUT-01"},
  "title": "PO-0012 menunggu persetujuanmu",
  "text": "Pesanan pembelian Rp 7.500.000 dari Sari.",
  "link": "/app/#/approvals",
  "data": {"model": "purchase_order", "name": "PO-0012", "approval": "apr_7Kx2"},
  "at": "2026-11-18T03:04:05Z"
}
```

| Field | Rules |
| --- | --- |
| `id` | 1–128 chars `[A-Za-z0-9._:-]`, stable for the same event: the kernel's dedupe key and the host's. Built from the step's `id` (prefixed with the kind), else `<kind>:<flow run id>:<step index>`. |
| `org` | the organisation. A host checks it against the business it holds the bearer for. |
| `kind` | §1. |
| `severity` | `info` \| `warn` \| `high`. |
| `to.owner` | true when `owner` is among the roles. |
| `to.roles` | role names, after `can` narrowing and after the read check (§6). At most 20. |
| `to.emails` | members of this organisation addressed one by one (the requester of an approval, §5). At most 20. Never an address outside the organisation. |
| `to.outlet` | optional outlet record name. Members limited to other outlets are left out (0010 §5). |
| `title` | 1–80 runes, plain text, Indonesian (the org's locale when others exist). |
| `text` | 0–300 runes, plain text. |
| `link` | a path of the kernel web app starting with `/app/` (`/app/#/r/<model>/<name>`, `/app/#/approvals`), or empty. |
| `data` | object ≤ 4 KB, for grouping and links; never shown in mail. |
| `at` | RFC 3339, when it happened. |

Recipients are **roles, not user ids**: the kernel's user ids mean nothing to a host, and a host that manages the
team (0043, 0056 `auth.team: host`) resolves roles itself. `emails` is only for a notice meant for one known member.

### 3. Transport to the host

Present only when whoever runs the kernel sets both variables (deployment config, never `config.rk` or `system.rk`,
0017 §2):

- `RANGKA_HOST_NOTICES_URL`: the host's endpoint, https, or http on a loopback or private address.
- `RANGKA_HOST_NOTICES_SECRET`: the bearer. Opaque to the kernel. On Rangka Cloud it is a per-business value derived
  by the host, valid only for this business. A company can read nothing it can use against another business, which is
  0063 §2's rule for anything in a tenant kernel.

**Call.** `POST <url>` with `Authorization: Bearer <secret>`, `Content-Type: application/json`, the event of §2 as the
body (≤ 16 KB), timeout 10 s. The kernel's notice client calls only this URL; it is not a connector and connector
egress rules (`RANGKA_CONNECTOR_EGRESS`) do not apply to it.

**Answers the kernel must handle.**

| Answer | Kernel does |
| --- | --- |
| 202 `{"status": "accepted"}`, 200 `{"status": "duplicate"}` | done |
| 429 with `Retry-After` | retry after that many seconds |
| 5xx, timeout, connection error | retry with backoff |
| 400 `bad_notice`, 401, 410 `business_gone`, 413 | stop; audit `notice.host_refused` with the code |

**Retries.** One kernel job per event (kind `notice_host`, the existing queue, args = the event). Backoff 30 s, 2 min,
10 min, 1 h, 6 h, then every 6 h, at most 8 attempts (about a day). After the last attempt: audit
`notice.host_failed`. Serverless kernels (0040) run the job in `tick` like any other. The inbox (§4) already has the
notice, so a host outage loses only the mail.

**Idempotency.** The host keeps `id` per business for at least 30 days and answers a repeat with `duplicate`. The kernel
never changes `id` on retry.

**Rate limits.** The kernel caps itself at 60 notices per minute per organisation (inbox and host together). Past the
cap it records `notice.dropped` with the kind and a count, once per minute. The host may cap lower or higher and
answers 429; Rangka Cloud: 60 per minute and 2 000 per day per business.

**Host behaviour** (Rangka Cloud, for reference; its spec §8): validates as §2, resolves `owner`, `roles`, `emails` and
`outlet` against its member list, writes its own feed, mails per its preferences and groups, and links to
`<business address><link>` through its own sign-in hand-over.

### 4. The kernel's own inbox (hosted and self-hosted)

Every kernel keeps an inbox, with or without a host. On a hosted kernel the host adds email and its own feed; the
inbox is still where people who work in the app see notices.

### 4.1 Storage, recipients, API, web app

**Storage.**

```sql
create table if not exists rk_notices (
  id        text primary key,               -- ntc_<random>
  org_id    text not null,
  event_id  text not null,                  -- §2 id
  user_id   bigint not null,                -- rk_users.id of the recipient
  kind      text not null,
  severity  text not null,
  title     text not null,
  text      text not null default '',
  link      text not null default '',
  data      jsonb not null default '{}',
  read_at   timestamptz,
  at        timestamptz not null default now(),
  unique (org_id, event_id, user_id)
);
create index if not exists rk_notices_user_idx on rk_notices (org_id, user_id, at desc);

create table if not exists rk_notice_prefs (
  org_id  text not null,
  user_id bigint not null,
  kind    text not null,                    -- a kind, or '*' for all
  mail    boolean not null,
  primary key (org_id, user_id, kind)
);
```

RLS like the model tables (0010 §4). Rows older than 90 days are pruned by the scheduler and by `tick`.

**Recipients in the kernel.** The kernel resolves `to` to members itself: the owner, members with those roles, the
members named by `emails`, minus members whose outlets exclude `to.outlet`, minus members who cannot read `record`
(row scope and field visibility, 0010 §5, 0027). Removed members get nothing; their rows go with their membership
(0043 §4 revocation also deletes their `rk_notices`).

**API** (session or token of the person; a `members:write` or `secrets:host` token gets `403 scope_limited`):

- `GET /api/v1/notices?limit=50&before=<cursor>&unread=1` → `{data: [{id, kind, severity, title, text, link, at,
  read}], unread, next}`.
- `POST /api/v1/notices/read` `{ids}`; no ids marks all.
- `GET /api/v1/notices/prefs` → `{mail: {available: bool, managed_by: "kernel" | "host"}, kinds: [{kind, label,
  mail}]}`. `PUT` with `{kinds: {<kind>: bool}}`. With a host (`RANGKA_HOST_NOTICES_URL` set), `managed_by` is `host`,
  `PUT` answers `409 managed_by_host`, and the page links to the host's settings.
- Realtime: SSE `event: notice` with the item on `GET /api/v1/events`, to that person only; the same event in
  `rk_event_log` for polling clients (0040 §4).
- MCP: `rangka_notices` (read-only, the caller's own inbox). No tool can create a notice.

**Web app (0065 slot).** The top bar's notifications button opens a panel: unread count on the button, the newest 50
grouped by day, "Tandai semua dibaca", each item a link to `link`. The 48px phone bar gets the same button. A `high`
notice that arrives while the app is open shows a toast with "Lihat". An empty panel says "Belum ada notifikasi". The
panel's settings link opens Pengaturan → Notifikasi (kernel prefs), or the host's page when `managed_by` is `host`.

### 4.2 Email without a host

- When `system.rk` `notify.email.driver` is `smtp` and no host is configured, the kernel mails notices itself: one mail
  per recipient with an address, plain text (title, text, the absolute link built from the site's public URL, a line
  "Atur notifikasi" with the prefs page). Job kind `notice_mail`, same backoff as §3.
- Default per kind: off for `info`, on for `warn` and `high`, overridden by the declaration (`channel: email` → on) and
  then by `rk_notice_prefs`.
- With a host configured, the kernel never mails notices: the host does, so nobody gets two mails.
- `/system/status` `capabilities.notify` gains `notices: {host: bool, pending: n, failed_24h: n, mail: "smtp" | "log" |
  "host"}`.

### 5. Kernel notices built in

| Kind | When | To | Severity | Link |
| --- | --- | --- | --- | --- |
| `kernel.approval.requested` | a request is created `pending` (0007 §2) | the policy's `approvers` roles + owner, narrowed to people who may read the record; never the requester | warn | `/app/#/approvals` |
| `kernel.approval.decided` | approved or rejected | the requester (`emails`) | info (approved), warn (rejected) | the record, or `/app/#/approvals` for a flow |

Title and text: "`<record>` menunggu persetujuanmu" / "`<label of the policy>` · diajukan oleh `<requester's
name>`"; "`<record>` disetujui" / "`<record>` ditolak: `<reason>`". `id`: `<kind>:<approval id>`. A request that
becomes `void` sends nothing; its notice stays and the inbox opens the void request.

Other modules' events (`gateway_mismatch`, `shipping_stuck`, KYB if it reaches the kernel) are `notices:` of their
modules, written by the rangka-apps owner.

### 6. Privacy

- `title` and `text` hold only what every recipient may read in the app: document numbers, amounts, outlet names,
  item names, a member's name. Never a customer's phone, email, address, national id (NIK) or bank account, and never
  free-text notes. The validator warns (`notice_personal_field`) when a template references a field the `users` or
  `contacts` modules mark as contact data, and errors on a `secret` field.
- The read check of §4 runs before the event leaves the kernel: `to.roles` holds only roles that may read the
  `record`, so the host never sends a record's text to someone who could not open it.
- `data` holds ids and names of records, never field values beyond those.
- Hosts keep events and texts no longer than 90 days, do not log `title`, `text` or `data`, and show their staff only
  kinds and counts.
- Mail leaves the system: the recipient's mailbox is outside the kernel's control. That is why texts are short and the
  details stay behind the link, where the app checks access again.

## Contract changes

**rangka-os**
- Schema 0.5: `module.json` `notices` (§1); `flow.json` `notify` step gains `notice`, `title`, `record`, `outlet`, `id`.
  Under `experimental:` until released (0000 §4).
- Validators (Go and Python, identical output): `unknown_notice` (a step names a notice its module does not declare),
  `duplicate_notice`, `notice_role_cannot_read` (error), `notice_literal_email` (Raka-authored change sets),
  `notice_personal_field` (warn), `notice_too_long` (a literal title over 80 or text over 300 runes). Testdata
  `kernel/testdata/notices` and `broken-notices`.
- Kernel: `rk_notices`, `rk_notice_prefs`; emit path in the `notify` step and in approvals; jobs `notice_host` and
  `notice_mail`; SMTP driver for `notify.email`; `RANGKA_HOST_NOTICES_URL`, `RANGKA_HOST_NOTICES_SECRET`; the API and
  SSE of §4; `rangka_notices` MCP tool; `/system/status` field; audit kinds `notice.host_refused`,
  `notice.host_failed`, `notice.dropped`.
- Web app: notifications panel in the top bar and the phone bar (replacing the "Belum ada notifikasi" toast), toast
  for `high`, Pengaturan → Notifikasi.

**rangka-apps** (owner of that repo)
- `notices:` for `payments` (`gateway_mismatch`), stock (`stock_negative`), and Pengiriman's `shipping_stuck` if the
  module raises it; `notice:` and `record:` on the existing `notify` steps.

**rangka-ai**
- Raka writes site flows with `notify` steps for "kabari …" requests and never puts a literal email in one.

**rangka-cloud** (private): `POST /api/host/v1/notices`, the per-business bearer at provisioning, feed, mail,
recipients (its spec §3 and §8).

## Order of work

1. Inbox, API, SSE and the top-bar panel; approvals emit `kernel.approval.*`; `notify` steps to roles emit implicit
   notices (§1.3). Gate: a submitted PO over the threshold shows in the approver's panel within seconds.
2. Transport to the host (§3) with the job and retries. Gate: against a stub host, a 500 is retried, a duplicate is
   accepted once, a 400 stops.
3. Declarations, step keys and validators (§1, §1.2, §1.4). Release.
4. SMTP driver and prefs (§4.2).
5. rangka-apps adds `notices:`; Rangka Cloud provisions the two variables for kernels from that release.

## Not in this contract

- How a host stores, groups, mails or shows notices (Rangka Cloud: its spec).
- Push and WhatsApp from the kernel itself. A self-hosted owner who wants them uses a flow `call:` to a connector.
- Read state shared between the kernel inbox and a host's feed.
- Digest mails.

## Open

1. Whether `kernel.changeset.proposed` (Raka proposed a change set that waits for the owner) is a built-in notice too.
   It fits §5; left out to keep the first release small.
