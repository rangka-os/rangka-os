# REST API

JSON over HTTP at `/api/v1`. Authenticate with the `rk_session` cookie (from `/auth/login`) or
`Authorization: Bearer rk_…` (an API token) or an OIDC access token.

## Records

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/meta` | Apps, models (with `app`, merged overlays and `can`), dashboards, flows, roles, connectors, theme tokens (light + dark), layout, `site` (config, features, redacted system), `installed`, `user`, `today` |
| GET | `/r/{model}` | `?filter=<CEL>&sort=-field&limit=50&offset=0&q=text` → `{data, total}` |
| POST | `/r/{model}` | Create → 201. Defaults and computed fields applied, save rules run |
| GET · PATCH · DELETE | `/r/{model}/{name}` | PATCH merges. Submitted documents return 409 |
| POST | `/r/{model}/{name}/submit` · `cancel` · `amend` | Document lifecycle; `submit` takes `{"force": true}` to accept warnings |
| POST | `/r/{model}/{name}/actions/{action}` | Runs the hook; 202 `{job_id}` when the action has `queue:` |
| GET | `/dashboards/{name}?period=30` | Every widget computed; `X-Cache: hit\|miss` when cached |
| POST | `/flows/{name}/run` | Run a flow now with `{event: {…}}` |

## Permissions

Model `permissions:` are enforced on every route below, for people, API tokens and MCP alike. Mapping:

| Request | Action |
| --- | --- |
| `GET` record, list, `/events` | `read` |
| `POST /r/{model}` | `create` |
| `PATCH`, `actions/{action}`, any other lifecycle verb | `write` |
| `DELETE` | `delete` |
| `submit`, `cancel`, `amend` | the same name |
| `GET /r/{model}/{name}/print` | `print` |
| `GET` attachments of a record | `read` |
| `POST` attachments of a record | `write` |

Child models use their parent's permissions. A flow that a person starts runs as that person, so each step is
checked with their roles. A dashboard widget over a model the caller cannot read returns the 403 body in place of
its data. `export` and `import` are checked by the code that serves them; there is no export endpoint yet.

A refusal is `403`:

```json
{"error": {"code": "permission_denied", "message": "permission denied: cancel on voucher", "model": "voucher", "action": "cancel"}}
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

## System and organisation

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/system/status` | Version, uptime, deploy target, schema version, every capability's driver and health, connectors health, OIDC clients |
| GET | `/system/jobs?state=` · `/system/jobs/{id}` | Queue jobs |
| GET | `/system/audit?kind=&actor=&org=&limit=` | Audit log |
| GET | `/orgs` · `/orgs/{id}/members` | Organisations (with plan and usage) and members |
| GET | `/files` · `/files/{path}` | Every metadata file with kind and layer; one file's text and parsed form |
| POST | `/tokens` | Create an API token |

## Auth

`POST /auth/login`, `POST /auth/logout`, `GET /auth/me`, and the OIDC endpoints in [Rangka ID](/concepts/rangka-id).

The CRM app's own endpoints, generated from its models with examples, are in the
[CRM API reference](/api/crm/index.html).
