# REST API

JSON over HTTP at `/api/v1`. Authenticate with the `rk_session` cookie (from `/auth/login`) or
`Authorization: Bearer rk_…` (an API token) or an OIDC access token.

## Records

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/meta` | Apps, models (with `app` and merged overlays), dashboards, flows, roles, connectors, theme tokens (light + dark), layout, `site` (config, features, redacted system), `installed`, `user`, `today` |
| GET | `/r/{model}` | `?filter=<CEL>&sort=-field&limit=50&offset=0&q=text` → `{data, total}` |
| POST | `/r/{model}` | Create → 201. Defaults and computed fields applied, save rules run |
| GET · PATCH · DELETE | `/r/{model}/{name}` | PATCH merges. Submitted documents return 409 |
| POST | `/r/{model}/{name}/submit` · `cancel` · `amend` | Document lifecycle; `submit` takes `{"force": true}` to accept warnings |
| POST | `/r/{model}/{name}/actions/{action}` | Runs the hook; 202 `{job_id}` when the action has `queue:` |
| GET | `/dashboards/{name}?period=30` | Every widget computed; `X-Cache: hit\|miss` when cached |
| POST | `/flows/{name}/run` | Run a flow now with `{event: {…}}` |

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
