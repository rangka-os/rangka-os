# 0075 Integration keys, Open API and outbound webhooks

Status: Proposed (22 Nov 2026) · Owner of the change: rangka-os (kernel API, auth, plan) · Affects: rangka-cloud (plan limits), docs site · Builds on: 0017, 0028, 0040, 0043, 0057 · Issue: rangka-os#266

Slice A (this contract's first implementation): integration keys, `api:` scopes, the guards, the plan limit key and `GET /api/v1/openapi.json`. Slice B (design only here): outbound webhooks. The management screen and the public docs are later slices.

## 1. Purpose

An owner wants another system (an accounting tool, a marketplace bridge, a script) to read and write some of the business's records without lending it a person's login. Today the only machine credential is an API token that acts as the person who minted it, with all of that person's rights. This contract adds a narrower credential, an integration key, that is limited by role and by explicit scopes, cannot reach anything administrative, and has its own rate bucket and its own plan limit.

## 2. Integration keys

- An integration key is an API token whose holder is a service member: a row in `rk_users` with `service = true` (no password, no upstream identity, never signs in through a browser) and one membership in the organisation with an ordinary role chosen by the owner when the key is created (never `owner`) and optional outlets.
- Because the key acts through that role, every permission, field hiding, row scope and approval rule of the engine applies to it exactly as to a person with that role. The scopes below only narrow further; they never grant what the role does not.
- A service member has no core user record (`record` is empty), is not listed as a person, and is not counted under the plan's `users` or `staff` limits. It is counted under `integration_keys` (section 7).
- One key, one service member. Revoking a key deletes the service member, its membership and its token in one transaction.
- Created, listed and revoked by an owner with a browser session only:
  - `POST /api/v1/integration-keys` body `{ "name", "role", "outlets"?, "scopes": ["api:read:<model>", ...], "expires_in_days"? }`, answers `201 { "id", "name", "role", "scopes", "token", "note" }`. The token is shown once. Errors: `403 forbidden` (not an owner with a session, or a token caller), `403 plan_limit` (section 7), `422 unknown_role` (missing or `owner`), `422 unknown_scope` (not in section 3 or a model that does not exist), `422 unknown_outlet`, `422 bad_request`.
  - `GET /api/v1/integration-keys` answers `{ "data": [{ "id", "name", "role", "outlets", "scopes", "created_at", "last_used_at", "requests_today" }], "limit": n|null, "used": n }`. `requests_today` is reported only, never enforced in v1.
  - `DELETE /api/v1/integration-keys/{id}` answers `204`, also for a key that is already gone.
- `POST /api/v1/tokens` refuses every `api:` scope (`403 forbidden`, "use integration keys"): a key can only be minted through the routes above, so it always has a service member.

## 3. Scopes

Grammar: `api:read:<model>`, `api:write:<model>` and `api:read:*`. `<model>` is a model name of the installed registry. There is no `api:write:*`: write access is always listed model by model.

- `api:read:<model>` allows `GET` on that model's record routes (list, get, aggregate).
- `api:write:<model>` allows create, patch, delete, lifecycle verbs and actions on that model. It does not imply read; list both when both are wanted.
- A key never carries `secrets:write`, `secrets:host`, `members:write` or `assistant`, nor any scope outside this section. Scopes are fixed when the key is created; to change them, create a new key and revoke the old.
- Scope and role must both allow a request. The scope check runs first (`403 scope_limited`), then the engine's own permission check (`403 permission_denied`).

## 4. Refused routes

The guard sits where the kernel resolves the caller (`principal()`, the same place as the 0043 `members:write` guard), so a new route cannot miss it. It is an allow list, not a deny list: a request authenticated by an integration key may reach only

- `GET /api/v1/openapi.json`,
- `GET /api/v1/r/{model}`, `GET /api/v1/r/{model}/{name}`, `GET /api/v1/aggregate/{model}` (read scope),
- `POST /api/v1/r/{model}`, `PATCH` and `DELETE /api/v1/r/{model}/{name}`, `POST /api/v1/r/{model}/{name}/{verb}`, `POST /api/v1/r/{model}/{name}/actions/{action}`, `POST /api/v1/r/{model}/submit` (write scope).

Everything else answers `403 scope_limited`. In particular `/api/v1/tokens`, `/api/v1/integration-keys`, secrets, members, change sets, `/auth/*` and the dev/system routes. `/mcp` answers `403` as well (JSON `{ "error": "scope_limited", ... }`), not the 401 that other scoped tokens get, so a client can tell "wrong credential kind" from "no credential".

## 5. Rate limits

Integration keys take the tenant's bucket (0040 §9) and, nested inside it, their own bucket per key: half the tenant's rate (at least 1 request a second, burst at least 1), same maximum wait. A key over its bucket gets `429 rate_limited` with `Retry-After` and does not spend the tenant's tokens, so one noisy integration cannot starve the staff. The number of requests per key per day is counted and reported by the list route; it is not enforced in v1.

## 6. OpenAPI

`GET /api/v1/openapi.json` returns an OpenAPI 3.1 document generated from the loaded models, for any authenticated caller (a session or a token), filtered by what the caller may do:

- a model appears only when the caller's role may read it; write operations (create, patch, delete) only when the role may write it;
- for an integration key, additionally only what its scopes allow;
- fields hidden from the caller (0027) are left out of the schemas; read-only fields are marked `readOnly`;
- security scheme: `bearerAuth` (HTTP bearer, the `rk_` token).

The document is deterministic (sorted keys, no timestamps). Lifecycle verbs and actions are listed as `POST` operations with `operationId` `<model>.<verb>`. Filtering is by the caller, so the document is never cached publicly.

## 7. Plan limits

New limit key `integration_keys` in `features.rk` limits (additional to 0057). Proposed defaults, set by the host's plan files and not by the kernel: Mulai 0, Juragan 2, Pro 5, Grup 20. A plan without the key is unlimited, like every other limit. Creating a key past the limit answers `403 plan_limit` with an owner-facing message. Existing keys are never disabled by a downgrade; they keep working and the owner is told to remove some. Requests per day are reported, not enforced (a later slice may add `api_calls_day`, already reported only in 0057).

## 8. Webhooks (slice B, design only)

- An owner registers an endpoint (HTTPS URL, event list, secret generated and shown once). Events are the record events already emitted by the engine (`<model>.created`, `.updated`, `.deleted`, lifecycle verbs), delivered through the job queue with retries and backoff, at least once, with a stable `Rangka-Event-Id` for de-duplication.
- Signature header: `Rangka-Signature: t=<unix>,v1=<hex hmac-sha256 of "<t>.<body>">` (no `X-` prefix, RFC 6648). `docs/site/docs.js` still documents the older header name; it must be aligned when slice B ships.
- Delivery respects the same role filtering as the OpenAPI document: the endpoint is bound to a service member and only receives records that member may read.
- Disabled automatically after repeated failures; the owner is notified (0066).

## 9. Security

- The token is hashed at rest like every API token; shown once.
- A key cannot mint, list or revoke keys, cannot read secrets, members or change sets, and cannot reach MCP.
- A key is revoked by deleting its service member; sessions and tokens of that member go with it (0043 §4 pattern).
- Role changes by the owner apply immediately, because the engine reads the role per request.
- The owner chooses the role: a narrow role (for example one that only reads orders) is the recommended default; the UI slice should say so.

## 10. Open questions for Dede

1. Package limits: are 0 / 2 / 5 / 20 keys per plan right, and should Mulai really have none?
2. Listed partners vs raw keys: should the first release offer only raw keys for the owner to paste, or also a short list of vetted partners (with their scope presets)?
3. Should `api:write:*` stay absent, or be allowed for the Grup plan?
4. Should write imply read of the same model?
5. Daily request cap: report only, or enforce a plan-level `api_calls_day`?
