# Upstream sign-in and member API (contracts 0042, 0043)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A kernel can let people sign in through an external OpenID Provider configured in `system.rk
auth.upstream` (Rangka Cloud for hosted businesses, any OIDC IdP for self-hosters), and a host holding a
`members:write` token can create, change and remove members by the provider's subject, so Cloud can decide who
enters while the kernel enforces it.

**Architecture:** A relying-party module in `kernel/internal/auth` (discovery + JWKS cache, code flow with PKCE,
id_token verification, identity links in a new `rk_identities` table) plus routes `/auth/upstream/{id}/start` and
`/callback`. A member API under `/api/v1/members/{upstream}/{subject}` guarded by the new `members:write` scope.
`auth.password` gains the value `owners`. Self-hosted kernels with no `auth.upstream` behave exactly as today.

**Tech Stack:** Go (kernel, `net/http`, in-tree pg layer), JSON Schema 0.5, plain JS app (`web/app`).

**Spec:** rangka-cloud `docs/specs/2026-11-09-cloud-sso-design.md` §2 and §3 (on rangka-cloud main), with the calls
recorded there: member API keyed by the provider subject; no membership claim in the id_token; `membership_pending`
when a member signs in before the push lands; `owner` is never pushed; unknown role/outlet is 422.

## Global Constraints

- No behaviour change for a kernel without `auth.upstream` and with `auth.password` unset or `true`.
- id_token checks: signature against the issuer's JWKS (RS256 and ES256), `iss` equals the configured issuer exactly,
  `aud` contains `client_id`, `exp` in the future and `iat` not more than 5 minutes ahead (60 s skew), `nonce` equals
  the one sent. PKCE S256 and `state` on every flow; one-time `state`, valid 10 minutes.
- Discovery and JWKS cached 1 hour; an unknown `kid` refetches the JWKS once (at most once per minute per issuer).
- Upstream sessions last 12 hours (`rk_sessions`, same cookie as today).
- Identity link: by `(issuer, subject)`; first sign-in links by e-mail only when `email_verified` is true AND a
  membership for that e-mail exists in the org; otherwise 403 `membership_pending` "Aksesmu sedang disiapkan. Coba
  lagi sebentar." (no account is created at sign-in).
- `client_secret` comes from `${ENV}` (schema already enforces it) and is never logged; tokens, codes and secrets
  never appear in logs, errors or responses.
- Member API: scope `members:write`, API tokens only (a human session cannot use it); `owner` is never created,
  changed or removed through it (409 `owner_protected`); role must exist and not be `owner` (422 `unknown_role`);
  outlets must exist (422 `unknown_outlet`); plan limits apply (contract 0028, existing error); delete or role/outlet
  change revokes the person's sessions and tokens in that org.
- `auth.password`: `true` (default) | `false` | `"owners"`; with `owners` only members with role `owner` may sign in
  with a password; with `false` nobody may. Refused password sign-in: 403 `password_disabled` "Masuk dengan
  <label upstream pertama>." (falls back to "Masuk lewat penyedia akun organisasimu." when no upstream).
- Schema 0.5 changes go through `make sync-schemas`, and `make compare-python` stays identical.
- Copy: Bahasa Indonesia, sentence case, "kamu". Code, comments and docs in English.
- Branch `kernel/upstream-signin`, worktree `.worktrees/rangka-os-upstream`; commit only; never push.

## Review Focus

1. **An id_token from the right issuer but for another client** (`aud` mismatch, or `azp` naming another client when
   `aud` has several): refused. Test in Task 4.
2. **Replayed callback** (same `code`/`state` twice, or `state` from another browser): the second is refused, no
   session. Test in Task 4.
3. **An attacker-controlled IdP account with the victim's e-mail but `email_verified:false`**: no link, 403. Test in
   Task 4.
4. **Removing a member while they are signed in**: their next request is 401 and their API tokens stop working.
   Test in Task 6.
5. **`auth.password: owners` and the OAuth provider's own login form** (`/oauth/authorize`, used by Omilo): the same
   rule applies there, not only on `/auth/login`. Test in Task 5.

---

### Task 1: Contracts 0042 and 0043

**Files:** Create `docs/contracts/0042-upstream-sign-in.md`, `docs/contracts/0043-member-api.md` (status `proposed`,
format of 0035/0041).

- [ ] **Step 1:** Write 0042 from the Architecture, Global Constraints and Task 3–5 interfaces: config keys, routes,
  checks, identity link rule, session TTL, `auth.password` values, errors, self-host compatibility, security notes.
- [ ] **Step 2:** Write 0043 from Task 6: routes, scope, request/response shapes, errors, revocation, plan limits,
  how a host gets the token (Task 6 Produces).
- [ ] **Step 3:** Commit `Contracts 0042 (upstream sign-in) and 0043 (member API), proposed`.

### Task 2: Schema: `auth.password` accepts `owners`

**Files:** Modify `schema/0.5/system.json` (`auth.password`: `oneOf: [{type: boolean}, {const: "owners"}]`, default
`true`), then `make sync-schemas`; `tools/` only if the Python validator needs it.
- Test: schema fixtures/tests the repo already has for system.rk (find them), plus `make compare-python`.

- [ ] **Step 1:** Failing test: a system.rk with `auth: {password: owners}` validates; `password: owner` (typo) fails.
- [ ] **Step 2:** Change, `make sync-schemas`, run the kernel schema tests and `make compare-python` → identical.
- [ ] **Step 3:** Commit.

### Task 3: Identities and passwordless members in the store

**Files:** `kernel/internal/store/system.sql` (new `rk_identities`), `kernel/internal/auth/identities.go` (create),
`kernel/internal/auth/password.go` (passwordless member upsert), tests next to them (`make test` uses a throwaway
Postgres).

**Interfaces (Produces):**
- Table `rk_identities(issuer text, subject text, user_id bigint references rk_users on delete cascade, created_at
  timestamptz default now(), primary key (issuer, subject))`, index on `user_id`.
- `func UserByIdentity(ctx, q pg.Querier, issuer, subject string) (userID int64, err error)` (`ErrNotFound`).
- `func LinkIdentity(ctx, q pg.Querier, issuer, subject string, userID int64) error` (idempotent for the same user;
  `ErrConflict` when the pair belongs to another user).
- `func UpsertMember(ctx, q pg.Querier, email, fullName, org, role string, outlets []string) (userID int64, created
  bool, err error)`: creates the user with `password_hash` NULL when new, sets name only if empty, upserts the
  membership's role and outlets; never touches an existing password.

- [ ] **Step 1:** Failing tests: link/lookup round trip; conflict; cascade on user delete; UpsertMember creates a
  passwordless user, updates role/outlets, keeps an existing password, never changes another org's membership.
- [ ] **Step 2:** Implement; `make test` (or the package tests with the throwaway DB) → PASS.
- [ ] **Step 3:** Commit.

### Task 4: Upstream relying party and routes

**Files:** Create `kernel/internal/auth/upstream.go`, `upstream_test.go`; modify `kernel/internal/server/server.go`
(parse `auth.upstream` like `clients`), `kernel/internal/api/server.go` (routes), session TTL plumbing in
`service.go`.

**Interfaces (Produces):**
- `type Upstream struct { ID, Label, Issuer, ClientID, ClientSecret string }` parsed from `system.rk auth.upstream`
  (`${ENV}` resolved at load; missing env → startup error naming the variable, never its value).
- `func (s *Service) UpstreamStart(w, r)` → `GET /auth/upstream/{id}/start?to=<path>`; `to` must be a same-site path
  (reuse the app's safe-redirect rule); stores `state`, `nonce`, PKCE verifier and `to` in a new
  `rk_upstream_states(state_hash, upstream, nonce, verifier, to_path, expires_at)` row (10 min, one use) and
  redirects to the provider's authorization endpoint with `scope=openid email profile`.
- `func (s *Service) UpstreamCallback(w, r)` → `GET /auth/upstream/{id}/callback`: consumes the state row, exchanges
  the code at the token endpoint (`client_secret_basic`), verifies the id_token per Global Constraints, resolves the
  user (identity or e-mail link rule), checks membership in the org, creates the 12 h session, redirects to `to`
  (default `/app/`). Errors render the existing sign-in page with the error copy, never the provider's text.
- `GET /auth/providers` (public): `{"password": true|false|"owners", "upstream": [{"id","label"}]}`.

- [ ] **Step 1:** Failing tests with a fake OIDC provider (`httptest`, RSA and EC keys, discovery, JWKS, token
  endpoint): happy path (session cookie, `/auth/me` shows the member); `aud` mismatch; `azp` mismatch; wrong `iss`;
  expired; bad nonce; replayed `state`; `state` from another upstream id; unknown `kid` refetch then success; JWKS
  rotation; `email_verified:false` with a matching e-mail → 403 `membership_pending`; known identity whose
  membership was removed → 403; provider error page text not reflected; secrets absent from logs.
- [ ] **Step 2:** Implement until green.
- [ ] **Step 3:** Commit.

### Task 5: Password policy and the sign-in UI

**Files:** `kernel/internal/auth/service.go` (`Login`), `kernel/internal/auth/oidc.go` (Authorize's login form),
`web/app` sign-in screen (the code behind `api.login` in `web/app/api.js`), app tests (`web/app/test_app.py` or the
JS tests the app has).

- [ ] **Step 1:** Failing tests: `password: owners` → owner signs in with password, a cashier gets 403
  `password_disabled` with the upstream label in the copy, on `/auth/login` AND on `POST /oauth/authorize`;
  `password: false` refuses everyone; default unchanged.
- [ ] **Step 2:** Implement; the app's sign-in reads `/auth/providers`: upstream buttons first ("Masuk dengan
  <label>"), the password form only when allowed; with `owners` the form is behind a link "Masuk sebagai pemilik".
- [ ] **Step 3:** Run Go tests and the app tests the repo runs in CI for `web/app` → PASS. Commit.

### Task 6: Member API

**Files:** Create `kernel/internal/api/members_api.go`, tests; modify `kernel/internal/auth/service.go`
(`ScopeMembersWrite = "members:write"`, revocation helper), the CLI that mints scoped API tokens today (find where
`secrets:write` tokens are created for Cloud provisioning and add `members:write` the same way).

**Interfaces (Produces):**
- `PUT /api/v1/members/{upstream}/{subject}` body `{email, name, role, outlets?}` → 200 `{user_id, created}`; links
  `(issuer of {upstream}, subject)` to the user (UpsertMember + LinkIdentity); 404 `unknown_upstream`.
- `DELETE /api/v1/members/{upstream}/{subject}` → 204 (idempotent: 204 when absent); removes the membership in the
  token's org only (the account and identity stay).
- Guards per Global Constraints; e-mail change for a linked subject updates `rk_users.email` (409 `email_taken` if
  another account has it).
- `func (s *Service) RevokeMember(ctx, q, userID int64, org string) error`: deletes the person's `rk_sessions` and
  `rk_tokens` in that org.
- Token minting: `rangka token create --scope members:write` (or the existing equivalent) documented in 0043.

- [ ] **Step 1:** Failing tests: create new member (passwordless, linked); update role/outlets revokes sessions;
  delete revokes and the next request with the old cookie/token is 401; owner protected; unknown role/outlet 422;
  plan limit error passes through; a human session or a token without the scope → 403; another org untouched;
  e-mail change and its conflict.
- [ ] **Step 2:** Implement until green. Commit.

### Task 7: End to end over HTTP

**Files:** `kernel/internal/server/upstream_e2e_test.go` (or the package that boots a full server in tests).

- [ ] **Step 1:** Boot a kernel with `auth.upstream` pointing at a fake provider and `auth.password: owners`;
  mint a `members:write` token; PUT a cashier; sign in through the upstream (follow redirects with a cookie jar);
  `/auth/me` shows role and outlets; DELETE the member; the same cookie gets 401; the cashier's password sign-in is
  refused; the owner's password sign-in works.
- [ ] **Step 2:** `make test` → PASS. Commit.
