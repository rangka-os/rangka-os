# 0010: Multi-org and row scope

Status: proposed (3 Nov 2026) · Owner: rangka-os (stream S5) · Needed by: both pilots

## Context
Pilot A's business units are separate PTs with their own NPWP, letterhead, bank account, number series and
users. One owner belongs to several of them and switches. Pilot B's outlet staff log in to count stock and
receive goods and must see only their own outlets. Contract 0005 §4 asks for row scope by company and branch
through Postgres RLS.

The kernel already keys every record table by `org_id` and every store query filters on it, and `rk_members`
already allows one row per (org, user). What is missing: switching orgs, listing a user's orgs, per-org
settings, a database-level guard, and outlet scope.

## Decision

### 1. Orgs and membership
- An **org** is a tenant: one PT. Rows of every model table carry `org_id`; a record is addressed by name
  inside the caller's current org only.
- `rk_orgs` gains `code text not null default ''`, the org's short code (`{ORG}` in number schemes, contract
  0009). Empty means `upper(id)`.
- A user may be a member of many orgs. `rk_members` keeps one role per (org, user) and gains
  `outlets text[] not null default '{}'`: the outlet names this member works at in that org.
- The API reports roles as a list (`roles: [role]`) so a later change to several roles per org does not
  change the shape.

### 2. Per-org settings (`config.rk` key `orgs:`, schema 0.5)
```yaml
# site config.rk
org: {legal_name: PT Contoh, pkp: false}      # site default (program plan's fixed key)
orgs:
  pt_alpha:
    name: PT Alpha Inspeksi
    code: ALP
    config:            # any config.rk key, overriding the site value for this org
      org: {legal_name: PT Alpha Inspeksi, npwp: "00.000.000.0-000.000", pkp: true}
      numbering: {invoice: {pattern: "INV/{ORG}/{YY}/{####}", reset: yearly}}
```
- No new kind: the program plan fixes `org:` as a `config.rk` key, and one site file holding every org keeps
  them reviewable together. An org's `config` may not itself contain `orgs` (error `nested_orgs`).
- `rangka migrate`, `seed` and `serve` upsert `rk_orgs` (id, name, code) from `orgs:`. Orgs created
  only in the database (seed, CLI) keep working with the site config.
- Kernel code reads org-dependent settings with `Registry.OrgConfig(org, key)`: `orgs.<org>.config[key]`
  when present, else `config.rk`'s value. S3 (letterhead), S4 (numbering, `{ORG}`) and S8
  (`org.pkp`) use it instead of `SiteConfig`.
- An org's value replaces the site's top-level key wholesale; there is no deep merge. An org that sets
  `org:` must repeat every subkey it needs (`legal_name`, `npwp`, `pkp`, ...): `org: {legal_name: X}` alone
  leaves that org's `org.pkp` unset, not the site's value.

### 3. Signing in and switching (REST, fixed in the program plan)
- `GET /auth/me` adds `orgs: [{id, name, code, roles}]` (every membership, sorted by id) and
  `org: {id, name, code, roles}` (the current one). `user.outlets` lists the member's outlets in the
  current org.
- `POST /auth/org` `{"org": "<id>"}` switches a **browser session** to another org the user belongs to:
  it updates the session row, sets cookie `rk_org=<id>` (Path=/, SameSite=Lax, 1 year, readable by
  the web app) and answers like `GET /auth/me`. Not a member → 403 `permission_denied`. API tokens and
  OIDC access tokens carry a fixed org → 400 `org_fixed`.
- `POST /auth/login` picks the org from the body's `org`, else the `rk_org` cookie, else the user's first
  membership by org id. A user with no membership gets 403 `no_membership`.
- Membership is checked on every request: a session, API token or OIDC token whose org the user no longer
  belongs to is rejected with 401 `unauthenticated`.
- `GET /api/v1/orgs` lists only the caller's orgs. `GET /api/v1/orgs/{id}/members` for an org the caller
  does not belong to → 403 `permission_denied`.
- Records of another org are never addressable: a name that exists only in another org is 404 like any
  missing record (names repeat across orgs, so 403 would be wrong and would leak existence). Entering
  another org (switch, members, tokens) is 403.

### 4. Database guard: row-level security keyed by org
- Every model table (parents and child tables) gets `enable` and `force row level security` and policy
  `rk_org` `using (rk_org_ok(org_id)) with check (rk_org_ok(org_id))`, where
  `rk_org_ok(o) = coalesce(nullif(current_setting('rangka.org', true), ''), o) = o`.
- The kernel sets `rangka.org` per pooled connection from the request context (`pg.WithOrg(ctx, org)`),
  so every query made while serving a request is limited to the caller's org even if a `where org_id`
  filter is missing. Jobs set the job's org. Work with no org in context (migrations, seed, scheduler
  scans) runs with the setting empty, which the policy allows; that code filters by org itself.
- Superusers and roles with `BYPASSRLS` skip RLS. `rangka serve` logs a warning when connected as one;
  hosted installs (S15) connect as a plain role that owns the tables.
- A pgx-based pool (contract 0005 §1) sets the same setting in its `BeforeAcquire` hook.

### 5. Row scope by outlet (role key `scope`, schema 0.5)
```yaml
# apps/fnb_cabang/roles/outlet_staff.rk
name: outlet_staff
label: {id: Staf outlet}
scope: {field: outlet, from: user.outlets}
```
- `field` is a field name or a list of names (`[from_outlet, to_outlet]`). `from` is CEL over `user`
  returning a list of record names; `user.outlets` is the member's outlets in the current org.
- For a member whose role has `scope`:
  - a record of a model that has one of the fields is visible when any of those fields holds a value from
    `from`;
  - a record of the model those fields link to (`outlet`) is visible when its name is in `from`;
  - other models are not limited by scope (model `permissions:` from S1 still apply);
  - an empty `from` shows nothing of the scoped models (fail closed).
- Only the member's own role counts; `inherits` does not pass a scope on. Roles without `scope` see the
  whole org.
- Enforced in the engine, so every interface built on it follows:
  - lists, dashboards and reports leave out-of-scope records out (totals included); the dashboard cache key
    includes the scope's field and values;
  - reading, updating, deleting, submitting, cancelling, amending or running an action on an out-of-scope
    record → 403 `permission_denied` `{model, action}`;
  - creating a record, or changing a scope field, to a value outside `from` → 403 `permission_denied`;
  - SSE `record` events of scoped models reach a scoped subscriber only for visible records. The filter is
    fixed per connection: role or outlet changes apply when the client reconnects. An update that moves a
    record out of a subscriber's scope sends that subscriber nothing (no delete-like event), so an open view
    keeps the stale row until it reloads;
  - a queued action is guarded when it is enqueued, not again in the worker;
  - MCP tools go through the same engine calls.
- Automations, hooks and flow steps run inside an allowed operation are not scoped (an outlet's goods
  receipt may write a stock move that head office sees). Because flow steps are not scoped, a member with a
  row scope may not start a flow directly: `POST /api/v1/flows/{name}/run` → 403 `permission_denied`
  `{model: flow, action: run}`. Flows started by automations, the scheduler or jobs are not affected.
- System lists: `GET /api/v1/system/jobs` and `/jobs/{id}` show the current org's jobs and installation-wide
  ones (another org's job id is 404); a member with a row scope sees only jobs they queued
  (`args.actor.id`). `GET /api/v1/system/audit` shows the current org (`?org=` must be a membership, else
  403 `permission_denied` `{model: org, action: read}`) and leaves `record.*` entries of scoped models out
  for a member with a row scope.
- Print, import and attachment endpoints (S3, S6, S7) load records through `Engine.Get` or call
  `Engine.Guard(ctx, actor, model, record, action)` before acting.

### 6. Error shape
`403 {"error": {"code": "permission_denied", "message": "...", "model": "<model>", "action": "<action>"}}`,
shared with S1's model permissions (`engine.PermissionError`).

### 7. CLI
- `rangka user add <email> --org <id> --role <role> [--outlets a,b] [--record USR-001] [--name "..."]`:
  creates the user (password from `RANGKA_USER_PASSWORD`, required for a new user) or updates the membership.
- `rangka user remove <email> --org <id>` ends a membership; it fails when the user is not a member.

## Consequences
- The web app (S0) shows an org switcher from `/auth/me` `orgs` and calls `POST /auth/org`.
- S3, S4 and S8 read org settings with `OrgConfig`; S4's `{ORG}` is `rk_orgs.code`.
- S9's pilot bundles declare `orgs:` in `config.rk` and create members with `rangka user add`.
- Several roles per org, business units inside an org, and scope on fields other than outlet-like links
  wait for a later contract.
- `config.rk`, including `orgs:`, is a site-level file: a change set that edits it (any org's settings) is
  approved by a site approver regardless of org. Per-org approval of org settings waits for a later contract.
- Scheduled flows (`schedule:`) currently register only for the default org (`Cfg.Org`); other orgs do not
  get them. Per-org schedules are a follow-up in the scheduler work, not S5.
