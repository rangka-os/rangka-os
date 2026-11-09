# 0042 Upstream sign-in

Status: proposed (9 Nov 2026) · Owner of the change: rangka-os (kernel auth, schema 0.5, web app sign-in) · Affects: rangka-cloud (private: acts as the OpenID Provider for hosted businesses, pushes members through 0043), self-hosters (any OIDC IdP) · Builds on: 0000, 0017, 0028, 0043

## Context

A kernel already has Rangka ID: password, sessions, API tokens and its own OIDC provider for Omilo and other apps (`system.rk auth.clients`). `auth.upstream` exists in schema 0.5 (`id`, `label`, `issuer`, `client_id`, `client_secret`) but nothing reads it.

Two needs meet here:

1. **Hosted businesses.** Rangka Cloud knows who the owner is and who the owner invited. The owner should sign in to the business's kernel with the one Cloud account, and Cloud, not the kernel's password table, should decide who enters.
2. **Self-hosters.** A company that runs Rangka itself wants its staff to sign in with the company's own IdP (Keycloak, Entra, Google Workspace, Authentik) instead of a second password.

Both are the same thing: the kernel is the relying party of an external OpenID Provider. This contract defines that relying party, the rule that ties an upstream identity to a kernel user, and the `auth.password` policy that lets a business turn password sign-in off for everyone but its owners. Who is a member, with which role and outlets, is pushed by the host through the member API (0043); the kernel still enforces it.

## Decision

### 1. Configuration

`system.rk auth.upstream` is an array. Each entry:

| Key | Meaning |
| --- | --- |
| `id` | Name used in routes, `[a-z][a-z0-9_]*`. Unique. Required. |
| `label` | Shown to people ("Masuk dengan <label>"). Default: the `id`. |
| `issuer` | The provider's issuer URL. Required. `https`, except `http` on `localhost`, `127.0.0.1` or `::1` (development). Compared to the `iss` claim as an exact string (no trailing-slash or case tolerance). |
| `client_id` | A literal or `${ENV}`. |
| `client_secret` | `${ENV}` only (the schema already refuses a literal secret). Absent means a public client: PKCE only, no secret. |

`auth.upstream` is read and checked when the kernel starts: a missing or malformed `id`, a duplicate `id`, a missing or non-https `issuer`, a missing `client_id` or a literal `client_secret` stops the start with an error that names the entry, never a secret. `${ENV}` values are resolved then too; a missing variable stops the start with an error that names the variable, never a value. A change to `auth.upstream` (or to the variables it reads) takes effect at the next start, not on a configuration reload. Secrets, tokens and codes never appear in logs, errors, audit entries or responses.

`auth.password` accepts three values (§6): `true` (default), `false`, `"owners"`.

No other key is added. Which organisation a person enters is not configured here: it is the membership (§4).

### 2. Discovery and keys

- The kernel fetches `{issuer}/.well-known/openid-configuration` and the `jwks_uri` it names. The document's `issuer` must equal the configured `issuer` exactly, or the upstream is unusable (sign-in answers `502 upstream_unavailable`).
- Discovery and the JWKS are cached for 1 hour per issuer.
- An id_token whose `kid` is not in the cached JWKS makes the kernel refetch the JWKS once, at most once per minute per issuer, then decide. A key rotation therefore heals itself; a flood of bad `kid`s cannot make the kernel hammer the provider.
- Algorithms: RS256 and ES256. `none`, HS* and every other algorithm are refused, whatever the header says.
- The discovery document's `authorization_endpoint`, `token_endpoint` and `jwks_uri` must be `https` (the same loopback exception as `issuer`), or the upstream is unusable (`502 upstream_unavailable`): a plain-http JWKS would let the network substitute keys.
- Outgoing requests to the provider have a 10 s timeout and read at most 1 MiB. A redirect is followed only to the same host as the request being followed, with the same scheme or http to https (never a downgrade), at most 5 times. The JWKS and token endpoint may live on another host than the issuer.

### 3. Routes

| Route | Auth | Purpose |
| --- | --- | --- |
| `GET /auth/providers` | public | `{ "password": true \| false \| "owners", "upstream": [{ "id", "label" }] }`. The sign-in screen reads it. It reveals no issuer, client id or secret. |
| `GET /auth/upstream/{id}/start?to=<path>` | public | Begins sign-in. Unknown `id` is `404 unknown_upstream`. |
| `GET /auth/upstream/{id}/callback` | public | The redirect URI registered at the provider: `{kernel origin}/auth/upstream/{id}/callback`. |

**Start.** `to` must be a same-site path (starts with `/`, not `//`, no scheme, no backslash); anything else is replaced by `/app/`. The kernel creates `state`, `nonce`, a PKCE verifier and a browser-binding value (random, at least 256 bits each), stores a server-side row (`rk_upstream_states`: state hash, upstream id, binding hash, nonce hash, verifier, `to`, expiry) valid for 10 minutes and usable once, and redirects (302) to the provider's authorization endpoint with `response_type=code`, `scope=openid email profile`, `code_challenge_method=S256`, `state`, `nonce`, `redirect_uri`. PKCE and `state` are on every flow, also for confidential clients. The binding value goes to the browser as the cookie `rk_upstream` (HttpOnly, SameSite=Lax, `Path=/auth/upstream/{id}/`, Max-Age 600, Secure when the kernel's issuer is https or the request came over TLS), so a callback is accepted only in the browser that started the flow. The browser holds the redirect and that cookie, nothing else.

**Callback.** The kernel:

1. looks up the row by `state`, upstream id and the hash of the `rk_upstream` cookie, and deletes it in the same statement (one use), then clears the cookie. A missing, expired, already used or other-upstream `state`, or a missing or different cookie, is `400 invalid_state`; no session, no token request is made. A replayed callback is therefore refused the second time, and a callback URL carried to another browser (login CSRF) is refused there without consuming the state of the browser that started it;
2. if the provider returned `error`, shows the generic copy `Masuk dengan <label> belum berhasil. Coba lagi.` The provider's `error_description` is never rendered;
3. exchanges `code` at the token endpoint with the PKCE verifier and `client_secret_basic` (or no secret for a public client);
4. verifies the id_token (§4);
5. resolves the person (§5), checks the membership, opens a session (§7) and redirects (302) to the stored `to`.

Errors render the existing sign-in page with the error copy (§8), never a raw JSON body, because the user is in a browser.

### 4. id_token checks

All must hold, else `401 upstream_invalid` and no session:

- the signature verifies against a JWKS key of the configured issuer (§2);
- `iss` equals the configured `issuer` exactly;
- `aud` contains `client_id`. When `aud` has several values, `azp` must be present and equal `client_id`; an `azp` naming another client is refused even if `aud` contains ours;
- `exp` is in the future (60 s skew allowed);
- `iat` is not more than 5 minutes ahead of the kernel's clock;
- `nonce` equals the one stored for this `state`;
- `sub` is present and non-empty.

`email` and `email_verified` are read only after these pass. The id_token's `name` fills the user's name when it is empty.

### 5. Identity link

An upstream identity is the pair `(issuer, sub)`, stored in `rk_identities(issuer, subject, user_id, created_at)`, primary key `(issuer, subject)`, `user_id` references `rk_users` and is removed with the user. The e-mail is not the identity: it can change at the provider.

Resolution at the callback:

1. **Known pair.** `(issuer, sub)` exists: that user. The e-mail claim is ignored.
2. **Unknown pair, first sign-in.** The kernel links by e-mail only when `email_verified` is `true` AND a user with that e-mail already has a membership in the organisation AND that user has no identity yet at this issuer. It then stores the link and continues. A user already linked at the issuer under another `sub` is not linked again by e-mail: an address the provider reassigned, or a second account claiming it, must not become a second way in. Such a sign-in gets rule 3. E-mails are compared trimmed and lower-cased (every writer of `rk_users.email` stores them so).
3. **Otherwise** it refuses: `403 membership_pending`, "Aksesmu sedang disiapkan. Coba lagi sebentar." Sign-in never creates a user, a membership or a role. A provider account with the victim's e-mail and `email_verified: false` gets no link.

`(issuer, sub)` already linked to another user is never re-linked at sign-in.

After resolution the person must have a membership in the organisation being entered (the kernel's organisation, or the one the user last used when the kernel serves several). A known identity whose membership was removed gets `403 no_membership` and no session. Members arrive through the member API (0043), which also creates the link by subject, so a person who is pushed before they sign in never depends on rule 2.

Why `membership_pending` exists: a host may let someone through the provider's front door a moment before its push reaches the kernel. The message says to try again; it is not an error to log at warn.

### 6. `auth.password`

| Value | Password sign-in |
| --- | --- |
| `true` (default) | Everyone, as today. |
| `"owners"` | Only members whose role in the organisation is `owner`. |
| `false` | Nobody. |

The rule applies everywhere a password is checked: `POST /auth/login`, and the sign-in form of the kernel's own OAuth provider (`POST /oauth/authorize`, used by Omilo and other `auth.clients`). Passkeys and upstream sign-in are not affected. A refused password sign-in is `403 password_disabled`, message `Masuk dengan <label>.` where label is the first upstream's; with no upstream, `Masuk lewat penyedia akun organisasimu.` The check happens after the password verifies, so the answer does not tell a stranger whether an e-mail exists; a wrong password is still `401 bad_login`. Under `owners`, a user with no membership in the organisation who gives the right password gets the existing `403 no_membership` (after the password verifies), not `password_disabled`; `password_disabled` is for members whose role is not `owner`.

The schema (0.5 `system.json`) changes `auth.password` from `boolean` to `oneOf [boolean, const "owners"]`, default `true`. Nothing else in the schema changes.

### 7. Sessions

An upstream session lasts 12 hours, stored in `rk_sessions` and carried by the same cookie as today (`rk_session`, HttpOnly, SameSite=Lax). It is shorter than a password session's default (24 h) on purpose: the host can end access (0043) but the kernel cannot learn about a provider-side sign-out. When the session ends, the person goes through the provider again, which is silent while they are signed in there.

`auth.session_ttl` still governs password sessions and is not applied to upstream sessions.

### 8. Errors

Browser routes show these on the sign-in page; JSON clients (`/auth/providers` aside, which has no error) get `{ "error": { "code", "message" } }`.

| Status | Code | Message (Bahasa Indonesia) | When |
| --- | --- | --- | --- |
| 404 | `unknown_upstream` | Penyedia akun tidak ditemukan. | `{id}` not configured |
| 400 | `invalid_state` | Sesi masuk sudah habis. Mulai lagi. | state missing, expired, used or for another upstream |
| 401 | `upstream_invalid` | Masuk dengan <label> belum berhasil. Coba lagi. | any §4 check, provider `error`, token exchange refused |
| 502 | `upstream_unavailable` | <label> sedang tidak bisa dihubungi. Coba lagi sebentar. | discovery, JWKS or token endpoint unreachable or malformed |
| 403 | `membership_pending` | Aksesmu sedang disiapkan. Coba lagi sebentar. | §5 rule 3 |
| 403 | `no_membership` | (existing copy) | known identity, no membership |
| 403 | `password_disabled` | Masuk dengan <label>. | §6 |
| 500 | `internal` | Terjadi kesalahan. Coba lagi. | the kernel's own failure (database) during start or callback |

No message echoes provider text, a token, a code or a claim.

## Compatibility

- A kernel with no `auth.upstream` and `auth.password` unset or `true` behaves exactly as today. `GET /auth/providers` answers `{ "password": true, "upstream": [] }`; the app's sign-in screen then shows the password form as before.
- `auth.password: false` with no upstream leaves only passkeys and tokens; the schema allows it, the validator may warn (not part of this contract).
- Self-hosters: any OIDC provider that publishes discovery, a JWKS with RS256 or ES256, and the authorization-code flow with PKCE works. Register `{kernel origin}/auth/upstream/{id}/callback`. The kernel is the relying party; its own OIDC provider (`/oauth/*`) is unchanged and can run beside it.
- Existing users keep their passwords. A user who signs in upstream first and later wants a password has none (member API users are passwordless, 0043); a password is set only through the existing flows.
- New tables are additive (`rk_identities`, `rk_upstream_states`), created like the other system tables; no migration of existing data.

## Security notes

- The provider is trusted for who a person is, never for what they may do. Role and outlets come only from the kernel's membership, set by an owner or by the member API.
- `aud`/`azp` and `iss` checks stop a token issued by the right provider for another client, or by another provider, from signing anyone in.
- One-use `state`, stored nonce and PKCE verifier stop replay: a callback is accepted once, only for the upstream it was started for, and a stolen `code` is useless without the verifier held by the kernel. The `rk_upstream` binding cookie stops login CSRF: a callback URL from the attacker's own flow, opened in the victim's browser, lacks the attacker's cookie and is refused.
- Provider URLs are https (loopback excepted), and redirects never leave the host or downgrade the scheme, so the network cannot substitute keys or read codes.
- Linking by e-mail requires a verified e-mail and an existing membership, so a provider account with someone else's address cannot take their place, and the kernel never creates access from a sign-in.
- `to` is restricted to same-site paths, so the callback is not an open redirect.
- JWKS refetch is rate limited per issuer (§2); outgoing calls are bounded in time and size.
- Secrets, codes, tokens and `state` values are never written to logs, audit data or responses.

## Contract changes

- **rangka-os:**
  - `kernel/internal/auth`: relying party (discovery, JWKS cache, code flow with PKCE, id_token checks), `rk_identities`, `rk_upstream_states`, passwordless user upsert, password policy in `Login` and in `Authorize`;
  - routes `GET /auth/providers`, `GET /auth/upstream/{id}/start`, `GET /auth/upstream/{id}/callback`;
  - schema 0.5 `auth.password` accepts `"owners"` (`make sync-schemas`, `make compare-python`);
  - `web/app` sign-in: upstream buttons first, password form only when allowed, "Masuk sebagai pemilik" link under `owners`.
- **rangka-cloud (private):** an OpenID Provider for hosted businesses (issuer, client per business, redirect URI registration), and the member push (0043).
- **rangka-apps, rangka-connectors, rangka-ai, rangka-edge:** none.

## Open

- A provider-side logout does not end a kernel session before its 12 hours. Back-channel logout is not in this contract.
- Several organisations in one kernel and one upstream: the person enters the organisation they last used, else the first membership. A picker is not part of this contract.
