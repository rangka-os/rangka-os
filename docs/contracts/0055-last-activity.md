# 0055 Last activity per organisation, and system sessions

Status: proposed (14 Nov 2026) · Owner of the change: rangka-os (kernel auth, `GET /api/v1/orgs`,
`GET /api/v1/orgs/{id}/members`) · Affects: rangka-cloud (the usage sampler signs in as a system session, the idle job
reads `last_activity_at`) · Builds on: 0043 (member API)

## Context

Rangka Cloud archives a free business that nobody has used for 14 days, with reminders from day 7 (rangka-cloud
`docs/specs/2026-11-14-beta-seats-idle-archive-design.md`, decided 14 Nov 2026). Cloud needs to know when a person
last used a business. The kernel cannot tell it today:

- `last_seen_at` on a member is the newest of "a session was **created**" and "an API token was used". Someone who
  signs in once and works for a month on the same cookie looks unseen after day one.
- Logging out or an expired session deletes the row, and with it the only trace of that person's activity.
- Cloud's hourly usage sampler signs in **as the owner** with a password. That creates a session every time Cloud
  restarts and makes the owner look active forever. Cloud works around it by skipping the owner, so a business used
  only by its owner looks idle.

## Decision

### 1. Activity is stored on the membership

- New column `rk_members.last_active_at timestamptz` (null = never active since this contract shipped).
- The kernel sets it to now when it authenticates a request for that member in that organisation, by any means:
  session cookie, API token, OIDC access token, assistant token (a person talking to Raka is a person using the
  business). Signing in counts too.
- **Not activity:** a system session (§2), and dev-open requests with no real principal.
- **Write throttle:** the row is written only when the stored value is older than 5 minutes, so a busy user causes at
  most one write per 5 minutes. Readers must treat the value as accurate to 5 minutes.
- Logging out, an expired session, or a revoked token never clears it. Removing the member removes the row.

### 2. System sessions

- `POST /api/v1/auth/login` accepts `"system": true` in its body. The session works exactly like any password session
  (same roles, same password policy, same audit `auth.login` entry, with `{"system": true}` in its data), except that
  nothing it does counts as activity.
- Stored as `rk_sessions.system boolean not null default false`.
- Any caller may ask for it: marking your own session as system can only hide your own activity. It grants nothing.
- Rangka Cloud must sign in with `system: true` for every call it makes on its own (usage sampler, health and status
  reads, provisioning). Calls that carry out something the owner just did in the Konsol may use either.

### 3. API

`GET /api/v1/orgs`: every organisation gains

| Field | Type | Meaning |
| --- | --- | --- |
| `last_activity_at` | RFC 3339 UTC string or null | Newest `last_active_at` among the organisation's members (§1). Null when no member has been active since this contract shipped |

`GET /api/v1/orgs/{id}/members`: `last_seen_at` becomes the newest of the member's `last_active_at`, their newest
non-system session's creation, and their newest API token use. It no longer jumps on a system session, so Cloud can
stop skipping the owner when it counts active users.

Both fields are additive; old clients ignore them.

## Consequences

- rangka-cloud: the owner login used by `withOwnerSession` sends `system: true`; the idle job reads
  `last_activity_at` from `GET /api/v1/orgs` (newest over all organisations of the business). A business whose kernel
  predates this contract has no field; Cloud then falls back to the newest non-owner `last_seen_at`.
- One extra conditional `update` per authenticated request, writing at most once per 5 minutes per member.
- Self-hosters get the same field; nothing archives on its own outside Cloud.
