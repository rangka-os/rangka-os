# 0043 Member API

Status: proposed (9 Nov 2026) · Owner of the change: rangka-os (kernel API, auth) · Affects: rangka-cloud (private: the host that pushes members) · Builds on: 0000, 0017, 0028, 0042

## Context

With upstream sign-in (0042) the kernel stops owning who may enter, but it must still enforce what they may do. The host that decides who is a member (Rangka Cloud for a hosted business) needs a way to tell the kernel: this person, identified by the provider's subject, is a member with this role and these outlets; or is not a member any more. There is no such API today: `rangka user add` is a local command, and the REST routes assume a person is signed in with a password.

This contract adds a small, token-only API for exactly that, keyed by the provider subject, so the host never has to know a kernel user id or set a password.

## Decision

### 1. Scope and token

- New API token scope `members:write`, beside `secrets:write` (0017 §3). `auth.TokenScopes` gains it.
- **API tokens only.** A browser session (a human) cannot use these routes even if the person is an owner: `403 forbidden`. A token without the scope is `403 forbidden`.
- **Who mints it.** An owner of the organisation, with their own session, through the existing `POST /api/v1/tokens` body `{ "name", "expires_in_days", "scopes": ["members:write"] }` (answers 201 with the token shown once). A token cannot mint a token. A non-owner gets `403 forbidden` (same rule and copy as `secrets:write`). Omilo helper and Rangka AI tokens never get the scope.
- **How a host gets the token (Rangka Cloud).** During provisioning the host creates the business's first owner, signs in once as that owner in the new kernel, mints one `members:write` token (and the `secrets:write` token it already mints), stores it with its other business secrets, and uses it for every push. A token acts in the organisation of the owner who minted it; one token serves one organisation. Rotating means minting a new one and deleting the old (`DELETE /api/v1/tokens/{id}`).
- The token authenticates as its minting owner but is limited to these routes by scope; it has no other API rights through this contract.

### 2. Routes

Both are under the token's organisation. `{upstream}` is an `id` from `auth.upstream` (0042); `{subject}` is the provider's `sub`, URL-encoded.

#### `PUT /api/v1/members/{upstream}/{subject}`

Creates or updates the member for that subject. Idempotent.

Request:

```json
{ "email": "sari@example.com", "name": "Sari", "role": "cashier", "outlets": ["OUT-01"] }
```

| Field | Rules |
| --- | --- |
| `email` | Required, a valid e-mail, stored lowercase. |
| `name` | Optional. Sets the user's name only when it is empty. |
| `role` | Required. A role that exists in the organisation's roles and is not `owner`. |
| `outlets` | Optional list of outlet record names the role is limited to. Omitted means no change for an existing member and none for a new one. Each must exist. |

Behaviour:

1. Resolves `{upstream}` to its issuer; unknown is `404 unknown_upstream`.
2. If `(issuer, subject)` is already linked, that user is the target; a different `email` in the body updates `rk_users.email` (`409 email_taken` when another account has that e-mail).
3. Else, if a user with `email` exists, that user is the target and the link is created; else a new user is created with no password.
4. The target's membership in the token's organisation is created or updated with `role` and `outlets`. Another organisation's membership of the same person is never touched.
5. A target that is already an `owner` in this organisation is never changed: `409 owner_protected`.
6. For a new person, plan limits apply (0028): past the `users` limit the answer is the existing `403 plan_limit` with its owner-facing message, and nothing is written. An existing member is not a new person.
7. When an existing member's `role` or `outlets` actually change, their sessions and API tokens in this organisation are revoked (§4). An unchanged push revokes nothing.

Response `200`: `{ "user_id": 12, "created": true }` (`created` is true only when the user account was created by this call).

#### `DELETE /api/v1/members/{upstream}/{subject}`

Removes the membership of that subject in the token's organisation. Idempotent: `204` also when the subject is unknown or has no membership here. The account (`rk_users`) and the identity link stay, so the same person can be pushed again and keeps their other organisations. An `owner` is `409 owner_protected`. Revokes the person's sessions and tokens in this organisation (§4).

Both routes answer errors as `{ "error": { "code", "message" } }`.

### 3. Guards and errors

| Status | Code | When |
| --- | --- | --- |
| 401 | (existing unauthenticated) | no valid token; also a revoked token or one of a removed member |
| 403 | `forbidden` | a session instead of a token, or a token without `members:write` |
| 404 | `unknown_upstream` | `{upstream}` not in `auth.upstream` |
| 409 | `owner_protected` | the target is already an owner in this organisation |
| 422 | `unknown_role` | `role` is missing from the organisation's roles, or is `owner`. `owner` is never created, changed or removed through this API |
| 422 | `unknown_outlet` | an outlet in `outlets` does not exist |
| 422 | `bad_request` | body is not valid JSON or `email` is missing or invalid |
| 409 | `email_taken` | e-mail change for a linked subject collides with another account |
| 403 | `plan_limit` | a new person would pass the plan's `users` limit (0028) |

Messages are Bahasa Indonesia, sentence case, plain (read by operators and logs); `plan_limit` keeps the 0028 owner-facing copy.

### 4. Revocation

Revoking a person in an organisation deletes their `rk_sessions` and `rk_tokens` for that organisation (a session or token for another organisation of the same person is kept). It happens in the same transaction as the membership change. Effect: the next request with the old cookie or token is `401`, and an API token the person minted stops working at once. It happens on DELETE and on any real change of role or outlets (so a lowered role cannot keep the old session's rights).

### 5. Audit

Each write is audited (`member.upsert`, `member.remove`) with actor = the token's owner and via `token`, target e-mail, role and outlets. Tokens, subjects of other systems and secrets are never put in the audit data beyond the subject string already stored in the link.

## Compatibility

- Additive: new scope, two new routes, no change to existing routes. `rangka user add` and REST member routes behave as before.
- A kernel without `auth.upstream` answers `404 unknown_upstream` to every call.
- Passwordless users are new in the data (`password_hash` null); password sign-in already refuses them (`bad_login`), so nothing else changes.
- Hosts that do not use this API lose nothing.

## Security notes

- Token only, owner-minted, one organisation: a leaked token can add and remove non-owner members of one organisation, never an owner, never read data, never reach another organisation.
- `owner` is excluded in every direction, so a compromised host or token cannot take an owner's place or lock the owner out.
- Role and outlet checks use the kernel's own registry; the host cannot invent a role.
- Removal revokes at once, so access ends when the host decides, not at session expiry.
- Plan limits are enforced here, so a host cannot exceed what the business bought.

## Contract changes

- **rangka-os:**
  - `auth.ScopeMembersWrite`, `auth.TokenScopes`, `RevokeMember`, minting guard in `POST /api/v1/tokens` (owner, human session, same as `secrets:write`);
  - `kernel/internal/api/members_api.go` with the two routes;
  - docs: the token mint example in the API reference.
- **rangka-cloud (private):** mints the token at provisioning, pushes on invite, role change and removal, treats `422`/`409`/`403 plan_limit` as owner-facing outcomes.
- **others:** none.
