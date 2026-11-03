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
org: {legal_name: PT Contoh}                  # site default (program plan's fixed key)
tax: {pkp: false}                             # PKP status lives in tax: (§8)
orgs:
  pt_alpha:
    name: PT Alpha Inspeksi
    code: ALP
    config:            # any config.rk key, overriding the site value for this org
      org: {legal_name: PT Alpha Inspeksi, npwp: "00.000.000.0-000.000"}
      tax: {pkp: true}
      numbering: {invoice: {pattern: "INV/{ORG}/{YY}/{####}", reset: yearly}}
```
- No new kind: the program plan fixes `org:` as a `config.rk` key, and one site file holding every org keeps
  them reviewable together. An org's `config` may not itself contain `orgs` (error `nested_orgs`).
- `rangka migrate`, `seed` and `serve` upsert `rk_orgs` (id, name, code) from `orgs:`. Orgs created
  only in the database (seed, CLI) keep working with the site config.
- Kernel code reads org-dependent settings with `Registry.OrgConfig(org, key)`: `config.rk`'s value with
  `orgs.<org>.config[key]` deep-merged over it (below). S3 (tax), S4 (numbering, `{ORG}`) and S8 use it
  instead of `SiteConfig`.
- Identity is read with `Registry.OrgIdentity(org)`, a deep copy holding the org's `org:` keys (`legal_name`,
  `npwp`, ...) plus `name`, `code` and `pkp` (rules below). Print's `.Org` and letterheads use it. PKP status
  has one source, the merged `tax.pkp` (`Registry.TaxSettings`, §8): `OrgIdentity(org).pkp` is that value,
  and an `org:` `pkp` key is ignored.
- An org's config is deep-merged over the site's, except `org` and `numbering`. Where both hold a map at a
  key, the maps merge key by key (recursively) and the org wins; for any other pair of types the org's value
  replaces the site's. An org that sets `tax: {ppn_rate: 11}` keeps the site's `tax.pkp` and `tax.dpp_ratio`.
  `OrgConfig` returns deep copies, so callers cannot change the site config.
- `org` and `numbering` replace: when an org sets them, its value wins whole and nothing is inherited. Orgs
  are separate legal entities, so identity is never inherited: for an org listed in `orgs:`,
  `OrgConfig(org, "org")` and `OrgConfig(org, "org.<key>")` give only its own `config.org` (nothing when it
  sets none), never the site's `org:`. `OrgIdentity` then fills in: `legal_name` is its
  `config.org.legal_name`, else `orgs.<id>.name`; `npwp` is only its own `config.org.npwp`; `pkp` is always the
  merged `tax.pkp`. The site's `org:` applies only to orgs not listed in `orgs:`.
  A `numbering` map is one scheme set: an org's map is used whole, so site series are not visible to an org
  that defines its own.
- A site subkey cannot be unset from an org: the schema rejects a null value (`tax: null` is "not of type
  object"), so an org sets another value instead.

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
  - **reads need any, writes need every:** reading (list, get, print, attachments list, import preview)
    needs one scope field in `from`; writing (create, update before and after the patch, delete, submit,
    cancel, amend, actions, attachment upload) needs every scope field that holds a value to be in `from`,
    and at least one filled. So outlet staff at Dago read a Braga→Dago `stock_transfer` but cannot create,
    change, submit, cancel or delete it; cross-outlet transfers are made by head office or by a member
    scoped to both outlets;
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
- Approval requests of a flow's approve step are scoped by the record the flow started on (the request's
  `model`/`name`, else the flow event's `model`/`name`): for a scoped caller an out-of-scope record makes the
  request invisible in lists, totals and SSE `approval` events and 403 `permission_denied` `{model, action}` on
  GET, approve and reject; a flow request that names no record is hidden from scoped callers (404).
- System lists: `GET /api/v1/system/jobs` and `/jobs/{id}` show the current org's jobs and installation-wide
  ones (another org's job id is 404); a member with a row scope sees only jobs they queued
  (`args.actor.id`). `GET /api/v1/system/audit` shows the current org (`?org=` must be a membership, else
  403 `permission_denied` `{model: org, action: read}`) and leaves `record.*` entries of scoped models out
  for a member with a row scope.
- Print, import and attachment endpoints (S3, S6, S7) load records through `Engine.Get` or call
  `Engine.Guard(ctx, actor, model, record, action)` before acting.

### 6. Error shape
`403 {"error": {"code": "permission_denied", "message": "Kamu tidak punya izin untuk melihat faktur penjualan", "model": "sales_invoice", "action": "read"}}`,
shared with S1's model permissions (`engine.PermissionError`). `code`, `model` and `action` are for code; `message` is
for people: an Indonesian verb per action (melihat, membuat, mengubah, mengajukan, membatalkan, merevisi, menghapus,
mencetak, mengekspor, mengimpor, menyetujui, menolak, menjalankan) and the model's label, lower-cased mid-sentence.

### 7. CLI
One command, shared with S0's set-up (which spells the email `--email`):
- `rangka user add <email> --org <id> --role <role> [--outlets a,b] [--record USR-001] [--name "..."]
  [--reset-password] [--create-org] [--db <url>]`: creates the user or updates the membership. The email may
  also be given as `--email <e>`. The password comes from `RANGKA_USER_PASSWORD` (preferred; `--password <p>`
  also works but lands in shell history) and is required only for a new account or with `--reset-password`;
  otherwise an existing account's password is never changed. `--outlets` replaces the member's outlets and
  `--outlets ""` clears them; without it they are left alone. An unknown org is refused unless `--create-org`
  is given (no silent auto-create). `--db` and `--org` default to `$DATABASE_URL` and `$RANGKA_ORG`.
- `rangka user remove <email> --org <id> [--db <url>]` ends a membership; for a non-member it exits 1 with
  `<email> is not a member of <org>`.
- Missing or wrong flags exit 2.

### 8. CEL globals `org` and `tax` (S8d)
Every expression that sees `user` (model defaults, computes, rules and action `if:`, flow steps, dashboard
widget `if:`, role `scope.from`) also reads two read-only globals, resolved per operation from the caller's
org (`Actor.Org`):

| Global | Members | Source |
| --- | --- | --- |
| `org` | `id`, `code`, `name`, `legal_name`, `npwp` | Identity only: `Registry.OrgIdentity(org)` (§2) cut to these members, each a string (`""` when missing), plus `id`, the org id. A listed org gets its `orgs:` `name`/`code` and only its own `config.org` (`legal_name` falling back to its `name`); an unlisted org gets the site's `org:` and empty `name`/`code` |
| `tax` | `ppn_rate`, `dpp_num`, `dpp_den`, `pkp` | `Registry.TaxSettings(org)`: `OrgConfig(org, "tax")`, the site's `tax:` with the org's `config.tax` deep-merged over it. `ppn_rate` is a percent (default 12; a whole number arrives as an int, a fractional one such as 11.5 as a float); `dpp_ratio` such as `"11/12"` becomes `dpp_num` 11 and `dpp_den` 12 (default 11/12); `pkp` is always a bool, default `false` |

```yaml
- {name: tax_rate, type: int, default: "tax.pkp ? tax.ppn_rate : 0"}
```
- One source for print and CEL: `TaxSettings` also fills print's `.Tax` (`ppn_rate`, `dpp_ratio`, `pkp`, the
  same normalised values) and `OrgIdentity(org).pkp` (print's `.Org.pkp`), so a template and a model default
  cannot disagree. PKP status is `tax.pkp` only: an `org:` `pkp` key is ignored. PPN is charged only for an
  org declared PKP.
- An `org:` `pkp` key (site `org/pkp` or an org's `config.org.pkp`) is read by nothing: both validators warn
  `org_pkp_ignored` ("pkp pindah ke tax.pkp; org.pkp diabaikan (contract 0010 §8)") so a site that set only
  `org: {pkp: true}` does not silently stop charging PPN.
- `tax:` deep-merges (§2): an org with `config: {tax: {pkp: false}}` keeps the site's `ppn_rate` and
  `dpp_ratio`.
- A model field named `org` or `tax` shadows the global inside that model's expressions, as for `user`, and
  the validators check it as that field (`org.nope` on a model with a link field `org` is `unknown_field`).
- Otherwise both validators accept `org` and `tax` as globals and do not check their members (as for `user`).
  A `dpp_ratio` that is not two positive whole numbers around `/` (site `tax:` or an org's `config.tax`) is
  error `bad_dpp_ratio`; at run time it falls back to 11/12, in CEL and in print alike.
- Imports' `cel:` mappings (contract 0012) see only `row`, not these globals.

## Consequences
- The web app (S0) shows an org switcher from `/auth/me` `orgs` and calls `POST /auth/org`.
- S3, S4 and S8 read org settings with `OrgConfig` and identity with `OrgIdentity`; S4's `{ORG}` is
  `rk_orgs.code`.
- S9's pilot bundles declare `orgs:` in `config.rk` and create members with `rangka user add`.
- Several roles per org, business units inside an org, and scope on fields other than outlet-like links
  wait for a later contract.
- `config.rk`, including `orgs:`, is a site-level file: a change set that edits it (any org's settings) is
  approved by a site approver regardless of org. Per-org approval of org settings waits for a later contract.
- Scheduled flows (`schedule:`) currently register only for the default org (`Cfg.Org`); other orgs do not
  get them. Per-org schedules are a follow-up in the scheduler work, not S5.
