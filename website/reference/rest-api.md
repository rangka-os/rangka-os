# REST API

JSON over HTTP at `/api/v1`. Authenticate with the `rk_session` cookie (from `/auth/login`) or
`Authorization: Bearer rk_…` (an API token) or an OIDC access token.

## Records

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/meta` | Apps, models (with `app`, merged overlays and `can`), dashboards, flows, roles, `screens` and `work` (the work screens the caller may open, in order, `home` marking theirs; contract 0030), connectors, theme tokens (light + dark), layout, `site` (config, features, redacted system), `installed`, `user`, `today` |
| GET | `/r/{model}` | `?filter=<CEL>&sort=-field&limit=50&offset=0&q=text` → `{data, total}` |
| GET | `/aggregate/{model}` | `?filter=<CEL>&q=text&group=<field>&fn=count,sum:<field>` → `{count, values, groups, other?}` over the same rows as the list, no paging ([below](#list-aggregates)) |
| POST | `/r/{model}` | Create → 201. Defaults and computed fields applied, save rules run |
| GET · PATCH · DELETE | `/r/{model}/{name}` | PATCH merges. Submitted documents return 409 |
| POST | `/r/{model}/{name}/submit` · `cancel` · `amend` | Document lifecycle; `submit` takes `{"force": true}` to accept warnings; `amend` → 201. 202 `{approval, approvals, record}` when the verb waits for [approval](#approvals) |
| POST | `/r/{model}/{name}/actions/{action}` | Runs the hook; 202 `{job_id}` when the action has `queue:`, 202 as above when it waits for approval |
| GET | `/r/{model}/{name}/print` | `?format=<print>&as=html\|pdf`: the record as a document ([Print formats](/guide/print-formats)); 501 `pdf_unavailable` without Chromium |
| GET | `/dashboards/{name}?period=30` | Every widget computed; `X-Cache: hit\|miss` when cached |
| POST | `/compute/{model}` | The record as the model would save it (defaults, computes, child totals), without saving or numbering ([contract 0034](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0034-counter-compute-and-summary.md)) |
| GET | `/export/r/{model}` | `?filter=&q=&sort=&columns=a,b&sep=semicolon`: the list as CSV (UTF-8 with BOM, plain numbers, select labels, Ya/Tidak). Needs the model's `export` permission on top of read ([contract 0024](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0024-list-and-dashboard-export.md)) |
| GET | `/export/dashboards/{name}` | `?period=`: every widget the caller may export, one CSV block per widget |
| (header) | `Idempotency-Key` on `POST /r/{model}`, `/submit`, `/cancel`, `/amend`, `/actions/{action}` | A retry with the same key answers the stored result (`Idempotency-Replayed: true`) and writes nothing; another body is 409 `idempotency_conflict`, the same request still running 409 `idempotency_in_progress`. Body `_occurred_at` (RFC 3339) goes to the audit ([contract 0030](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0030-work-screens.md) §6, §7) |
| GET | `/screens/{name}` | A work screen the caller may open: `{name, screen, set}`, `set` = the screen's `set:` evaluated for the caller now; 404 otherwise ([contract 0030](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0030-work-screens.md)) |
| POST | `/flows/{name}/run` | Run a flow now with `{event: {…}}` |

## Attachments

Files on records (contract 0011). Not to be confused with `/files`, which serves the `.rk` files.

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/r/{model}/{name}/attachments` | `{data: [{id, filename, content_type, size, sha256, created_by, created_at, url}]}`, newest first |
| POST | `/r/{model}/{name}/attachments` | Multipart, part `file` → 201 with the item. 413 `too_large` over `files.max_upload_mb` (default 20) |
| GET | `/attachments/{id}` | The bytes; inline for images and PDF, a download otherwise |
| DELETE | `/attachments/{id}` | 204. 409 `submitted` once a submittable record is submitted |

`files: {enabled: false}` in `system.rk` answers 501 `files_disabled`.

## Approvals

Document approvals (contract 0007). A `submit`, `cancel` or action that matches an approval policy does not run; it
answers 202 and creates a request. When the last pending request is approved, the verb runs as the requester.

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/approvals` | `?state=pending\|approved\|rejected\|void&model=&name=&limit=50&offset=0` → `{data, total}`; each item has `can_decide` |
| GET | `/approvals/{id}` | One request |
| POST | `/approvals/{id}/approve` · `reject` | `{}` or `{reason}`; reject needs a reason. 200 with `approval`, plus `record` (or `action`, `flow`) when the verb ran. People only |

SSE sends `event: approval` with `{id, model, name, state}` on every change.

## Imports

Spreadsheet imports declared in `imports/<name>.rk` (contract 0012). `GET /meta` lists them under `imports`.

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/imports/{import}` | Multipart `file` (CSV or XLSX), form `outlet` when the import is per outlet. Checks every row in a dry run → `{id, import, rows, preview, errors, skipped, duplicate}`. 413 `too_large` over 20 MB |
| POST | `/imports/{import}/{id}/commit` | Creates every record in one transaction. People only. 422 `import_errors` / `import_empty`, 409 `already_imported` |
| GET | `/imports/{import}` · `/imports/{import}/{id}` | The 50 most recent entries as `{data, total}`; one entry |

## List aggregates

`GET /aggregate/{model}` answers totals and group summaries for a list view in one request (contract 0018). It
matches exactly the rows `GET /r/{model}` returns for the same `q` and `filter`, under the same read grant and row
scope, so `count` always equals the list's `total`.

- `fn` is a comma-separated list of `count`, `sum:<field>`, `avg:<field>`, `min:<field>` and `max:<field>`
  (default `count`). Values are keyed `count` and `<kind>:<field>`.
- `group` names a field of the model or a system column such as `status` or `docstatus`. The 50 largest groups by
  count are returned, ties broken by key; the rest are summed into `other`, which is left out when nothing
  overflowed. Empty values share one group with `key: null, label: "(kosong)"`. For a link field, `label` is the
  linked record's title.

```json
{
  "count": 142,
  "values": {"count": 142, "sum:grand_total": 51250000},
  "groups": [
    {"key": "CUST-0012", "label": "CV Maju Jaya", "count": 4, "values": {"count": 4, "sum:grand_total": 3900000}}
  ]
}
```

Errors: 400 for an unknown or table `group`, a `sum|avg|min|max` over a field that is not currency, int, float or percent, a
bad `fn`, a `filter` that does not compile, or a child model; 404 for an unknown model; 403 without a read grant.
There is no preview variant.

## Permissions

Model `permissions:` are enforced on every route below, for people, API tokens and MCP alike. Mapping:

| Request | Action |
| --- | --- |
| `GET` record, list, `/aggregate/{model}`, `/events` | `read` |
| `POST /r/{model}` | `create` |
| `PATCH`, `actions/{action}`, any other lifecycle verb | `write` |
| `DELETE` | `delete` |
| `submit`, `cancel`, `amend` | the same name |
| `GET /r/{model}/{name}/print` | `print` |
| `GET` attachments of a record | `read` |
| `POST` attachments of a record, `DELETE /attachments/{id}` | `write` |
| `/imports/{import}` (upload, commit, read) | `import` on its model; each created record also needs `create` |

Child models use their parent's permissions. A flow that a person starts runs as that person, so each step is
checked with their roles. A dashboard widget over a model the caller cannot read returns the 403 body in place of
its data. Exports need the `export` permission as well as read.

A refusal is `403`:

```json
{"error": {"code": "permission_denied", "message": "Kamu tidak punya izin untuk cancel voucher", "model": "voucher", "action": "cancel"}}
```

A single record the caller may not read answers 403 too, never 404; the print and attachments routes answer
`action: "read"` when the caller cannot read the record. List rows removed by an `if:` condition are left out and
are not counted in `total`.

`GET /meta` adds `models.<name>.can`, an object with a boolean per action (`read`, `create`, `write`, `delete`,
`submit`, `cancel`, `amend`, `export`, `import`, `print`, `customize`) for the caller. A conditional grant reads as
`true`; the record check still applies. Clients such as the web app and the Console can use it to hide actions the caller may not take.

Errors: 422 `{errors:[{field?, rule?, code, message}], warnings?, needs_force?}`; everything else
`{error:{code, message}}`. Successful writes may carry `_warnings`.

## Realtime

`GET /events` is a Server-Sent Events stream (`?models=deal,lead` to filter), with a heartbeat every 15 s:

```text
event: record
data: {"model":"deal","name":"DEAL-00012","op":"updated","docstatus":0}

event: changeset
data: {"id":"cs_rzlzncahuf","status":"applied"}
```

## Change sets

| Method | Path | Notes |
| --- | --- | --- |
| GET · POST | `/changesets` | List; propose → 201 with `layers`, `risk`, `status: proposed` |
| GET | `/changesets/{id}` | Full change set with validation, preview (`migrations`, `affected`, `files[{path, before, after}]`) and history |
| POST | `/changesets/{id}/validate` · `preview` · `approve` · `reject` · `apply` · `revert` | Approve only from `previewed`, by a signed-in approver; high risk owner only (403 `owner_required`) |

### Live preview

`/preview` shows the installation as it would look with a change set applied, read against the live database without
migrating it (contract 0015). Only callers who may propose change sets can use it. Responses carry
`X-Rangka-Preview: <id>`.

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/preview?changeset=<id>` | `{changeset: {id, title, status, risk, layers}, meta}` |
| GET | `/preview/{id}/meta` · `/preview/{id}/r/{model}` · `/preview/{id}/r/{model}/{name}` · `/preview/{id}/dashboards/{name}` | Like the live routes |

Any other method answers 405 `preview_read_only`. A change set that changes permissions has no preview (409
`preview_permissions`).

## Process canvas

A process is a declared `processes/<name>.rk` file or one inferred from an app's menu (contract 0014). The graph shows only stages whose model the caller may read.

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/processes` | `{data: [{name, label, inferred, stages, source}]}`, only processes with a stage the caller may read |
| GET | `/processes/{name}` | `{name, label, inferred, source, groups, nodes, edges, layout}`; 404 `not_found` for an unknown process or one with no visible stage |
| POST | `/processes/{name}/ops` | `{edits: [{node, key, value}]}` → `{title, ops}`; compute only. 400 `bad_edit` for an unknown or hidden node or key. Send the result to `POST /changesets` |
| PUT | `/processes/{name}/layout` | `{groups: {id: {collapsed?, x?, y?}}, nodes: {id: {x, y}}}` → 200 layout, written to `site/canvas/<name>.json`. Owner only (403 `permission_denied`); 400 `bad_layout` for other keys |

## System and organisation

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/system/status` | Version, uptime, deploy target, schema version, every capability's driver and health, connectors health, OIDC clients |
| GET | `/system/jobs?state=` · `/system/jobs/{id}` | Queue jobs |
| GET | `/system/audit?kind=&actor=&org=&limit=` | Audit log |
| GET | `/orgs` · `/orgs/{id}/members` | Organisations (with plan and usage) and members |
| GET | `/files` · `/files/{path}` | Every metadata file with kind and layer; one file's text and parsed form (`/files/meta/…` is an older alias) |
| POST | `/tokens` | Create an API token |
| GET | `/plan` | The tenant's plan, every plan of `features.rk`, usage against the limits (`ok`, `near` from 80%, `full`, `over`), notices and what is read-only after a downgrade ([contract 0028](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0028-plan-limits.md)) |
| GET | `/catalogue` | Every module, app and connector this kernel can install, with its state for the site; for people who may propose change sets. Read only for now ([contract 0036](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0036-catalogue-in-the-platform.md)) |

## AI chat

The embedded assistant Raka (contract 0020). The kernel relays the chat to the rangka-ai sidecar set with
`--ai-url`, giving it a 10-minute token that acts as the signed-in person over MCP. Raka reads and proposes change
sets; it never approves, applies or writes records.

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/ai/chat` | `{message, history: [{role: user\|assistant, text}], context: {route, title, model, record}}` → `text/event-stream` |

- History keeps the last 20 turns and at most 8,000 characters (oldest dropped first). Another role → 400 `bad_request`.
- `404 not_found` when no sidecar is configured, `403 ai_disabled` when `config.ai.enabled` is false, `403 ai_forbidden`
  for an assistant token.
- Per person: one chat at a time (`429 ai_busy`), 30 chats per 10 minutes (`429 ai_rate_limited`).
- The stream passes the sidecar's events through: `text {delta}`, `tool {name, summary}`, `changeset {id, title,
  risk, status}`, `escalate {reason}`, then `done {}` or `error {code, message}`. The kernel itself ends the stream
  with one `error` event when the sidecar is down or refuses the request (`ai_unavailable`, or `bad_request` when it
  rejects the body) and after 120 seconds (`ai_timeout`).
- `GET /meta` has `features.ai: true` when the sidecar answered `GET /healthz` within the last 30 seconds and
  `config.ai.enabled` is not false.
- Each chat writes one audit entry of kind `ai.chat` with the tools used, change set ids, duration and outcome; the
  message text is not stored.

```text
event: tool
data: {"name":"rangka_query","summary":"Membaca 20 faktur"}

event: text
data: {"delta":"Ada 20 faktur bulan ini."}

event: done
data: {}
```

## Auth

`POST /auth/login`, `POST /auth/logout`, `GET /auth/me` (user with outlets, every org membership, the current org),
`POST /auth/org {"org": "<id>"}` (switch a browser session to another org of the user; tokens have a fixed org: 400
`org_fixed`), and the OIDC endpoints in [Rangka ID](/concepts/rangka-id).

The CRM app's own endpoints, generated from its models with examples, are in the
[CRM API reference](/api/crm/index.html).
