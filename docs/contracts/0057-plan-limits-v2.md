# 0057 Plan limits v2: apps, storage, PDF, and operator grants and caps

Status: implemented (14 Nov 2026, branch feat/0057-plan-limits-v2) · Owner of the change: rangka-os (kernel `plan`, `features.rk` schema, `rangka plan`,
`GET /api/v1/plan`) · Affects: rangka-cloud (Konsol, billing, provisioning, the Grup pool), rangka-apps (`features.rk`
plans of sites and templates) · Builds on: 0028 (plan limits), 0040 §9 (`limits.rate`), 0045 §2 (`features.rk` and
`config.plan` are not proposable by an assistant), 0048 (access mode)

## Context

- **The Cloud plan model changed (Dede, 14 Nov 2026).** rangka-cloud `docs/specs/2026-11-14-plans-billing-v2-design.md`:
  an owner account holds subscriptions, a subscription holds businesses, a business holds apps. Mulai, Juragan and Pro
  are one business per subscription; Juragan Grup is up to 3 businesses sharing one usage pool. Each plan limits apps
  per business (Mulai 1, Juragan 3, Pro 5, Grup 15), database and file size, and PDFs. Past a limit, the lower plans
  are offered the next plan; Grup buys capacity add-ons ("harga pecahan"). Zafdoz credits can be bought on every plan.
- **What the kernel enforces today (0028).** `outlets`, `users` and `ai_credits_month`. `records`, `storage_mb` and
  `api_calls_day` are only reported. `limits.rate` shapes traffic (0040 §9). There is no app count, no split between
  database and files, and no PDF count.
- **Extras have no place.** An operator can only switch the whole plan (`rangka plan set`). An add-on ("+1 GB") or a
  bought credit pack would need a new plan per customer.
- **One kernel serves one business.** A Grup pool spans 2 or 3 kernels, so the kernel cannot enforce the pool total by
  itself. Cloud can, if it can lower one kernel's limit below the plan's.

## Decision

### 1. New limits in `features.rk` (schema 0.5)

| Limit | Counts | Enforced how |
| --- | --- | --- |
| `apps` | installed apps of the tenant except `core`: `config.apps` in its order (later = newer), else every installed app | A change set that installs more apps than now and more than the limit is invalid at validate (error `plan_limit`). Keeping or lowering the count is always allowed. Seed and `rangka migrate` are not checked |
| `db_mb` | `pg_database_size` of the tenant's database, rounded up, measured by the plan snapshot (0028 §4, refreshed every 10 s at most) | From 100%: attachment uploads and imports (preview and commit) are refused (`plan_limit`). Creating, changing and deleting records is never refused for size, so a sale is never lost and the owner can clean up |
| `files_mb` | stored attachments, rounded up; a blob attached twice in one organisation counts once | From 100%: attachment uploads are refused (`plan_limit`) |
| `pdf_month` | PDF renders in the calendar month (site timezone) | Past the limit renders are slowed, not refused for the month: at most 10 an hour. Past that, 429 `pdf_slowed` with `Retry-After` until the next hour; the HTML print stays available |
| `requests_month` | requests in the month | The kernel neither counts nor enforces it: the hosting router reads it from `features.rk` and shapes traffic (with `limits.rate`) |

- `records`, `storage_mb` and `api_calls_day` stay reported-only keys (0028). The validator does not change: limit
  keys are free integers in schema 0.5, and grants live in the database, which the validator cannot see.
- A plan without one of these limits is not limited by it, as in 0028.
- **Downgrade.** Nothing is deleted. The apps past a lower `apps` limit (the newest) become read-only: records of the
  units only they use (their closure minus the closure of the kept apps and `core`) refuse writes with
  `plan_read_only`; reads, prints and exports are allowed, like outlets in 0028 §4. `db_mb` and `files_mb` past a lower
  limit behave as in the table.

### 2. Operator grants and caps

`rk_site_plan` gains `grants jsonb` (a list) and `caps jsonb` (a map). The effective limit is:

```
effective(key) = min( plan_limit(key) + Σ active grants(key), cap(key) )
```

- **Grant**: `rangka plan grant <limit> <+n> [--until YYYY-MM-DD] [--ref <text>] [--by] [--note]` adds capacity (a Grup
  add-on: `db_mb +1024`, `files_mb +10240`, `apps +1`; Zafdoz credits: `ai_credits_month +100 --until 2027-02-14`). It
  counts until the start of `--until` (UTC), or until `rangka plan revoke <grant id>`. A grant on a limit the plan
  does not set changes nothing (that limit stays unlimited).
- **Cap**: `rangka plan cap <limit> <value> [--reason <text>]` sets a ceiling below the plan, `rangka plan uncap
  <limit>` removes it. Cloud uses it for the Grup pool: when the pool's sum reaches its total, Cloud caps each
  business at what it uses now; when capacity frees up or is bought, Cloud uncaps.
- Grants and caps are operator-only: CLI, no REST endpoint, like `rangka plan set`. They stay forbidden to an
  assistant (0045 §2: `features.rk` and `config.plan`). Every change is a row in `rk_plan_changes` (0028 §6).
- A cap on a limit the plan does not set still limits it.
- A plan change (`rangka plan set`) keeps grants and caps. `rangka plan clear` removes them. `rangka plan show` lists
  them.

### 3. `GET /api/v1/plan`

- `usage` gains `apps`, `db_mb`, `files_mb` and `pdf_month` with `used`, `limit` (effective), `state` (`ok`, `near`
  from 80%, `full`, `over`; `db_mb`, `files_mb` and `pdf_month` stop at `full`, since nothing becomes read-only past
  them) and `message`.
- Each usage entry gains `plan_limit` (when it differs from nothing), the active `grants` and the `cap` when set.
- `read_only` gains `apps: [name]`.
- Messages name the next plan that has room ("Naik ke Juragan Pro untuk menambah app."). On the highest sellable plan
  they point to capacity instead ("Tambah kapasitas di Konsol."). A cap with reason `pool` says "Kolam Juragan Grup
  sudah penuh. Tambah kapasitas atau kurangi pemakaian di bisnis lain."

### 4. Refusal shape

Unchanged from 0028 §3: 403 `{"error": {"code": "plan_limit" | "plan_read_only", "message": …}}`. New: 429
`pdf_slowed` with `Retry-After` (seconds).

### 5. Scoped outlet count: `limit_scope` (amendment, 19 Nov 2026, rangka-apps#104)

- **Why.** The `outlet` record is the stock location everywhere (0021, 0039, 0010 scope), so sites already keep
  warehouses, canvas vans and a damaged-goods room as outlet rows. Under 0028 each of them took a paid outlet seat.
  The plans sell shops ("gudang tanpa batas"), not stock locations.
- **Declaration.** The module that declares model `outlet` may name, in its `module.rk`, a check field of `outlet`
  that the `outlets` limit counts by: `limit_scope: {outlets: is_store}`. Only `outlets` can be scoped. The field is
  usually a compute (rangka-apps outlets 1.7.0: `is_store: location_type == '' || location_type == 'store'`).
- **Count.** With the key, every place that counts outlets counts only rows where the field is true or null (a row
  written before the field existed and not yet backfilled by `rangka migrate` counts, so a missing backfill never
  lowers the count): the usage in `GET /api/v1/plan` and `rangka plan show`, the create check under the
  `rk_plan:outlets` advisory lock (0028 §2), and the read-only pick past a lower limit (0028 §4: the newest stores
  past the limit; rows outside the scope are never read-only). Without the key every row counts, as before.
- **Refusal.** Creating an outlet that counts past the limit is refused with 403 `plan_limit`, as in 0028 §2. Creating
  one that does not count is never refused. Changing an outlet so the field turns true (a warehouse retyped as a
  store) is checked like a creation; changing it so the field turns false frees a seat.
- **Validators.** Go and `rangka_validate.py`: `limit_scope_unknown_limit` (a key other than `outlets`),
  `limit_scope_not_owner` (declared by a unit that does not declare model `outlet`, which also makes it unique),
  `limit_scope_field` (not a check field of `outlet`). Schema: `module.json` `limit_scope`.
- **Not changed.** Stock (0021, 0039), row scope (0010) and device pairing (0058) keep treating every outlet row the
  same. Whether a cashier device may pair with a row outside the scope is a module rule, not a kernel one.

### 6. Phone-only staff: `staff` (amendment, 21 Nov 2026, rangka-os#261)

The `staff` limit counts people whose roles are all `desk: false` apart from `users` when a plan sets it; see
0074 §5.

## Consequences

- rangka-cloud adds subscriptions, the Grup pool sampler, add-on and credit grants, and the Konsol pages (its spec
  above). Hosting already runs `rangka plan set` through `P.RunTask`; grants and caps use the same path.
- rangka-apps: `features.rk` plans of the Cloud sites gain the new limits; templates without them stay unlimited.
- rangka-apps outlets 1.7.0 declares `limit_scope: {outlets: is_store}` (§5); rangka-cloud's Konsol plan page can
  say "N toko dari batas M, gudang tanpa batas".
- New tables and columns: `rk_site_plan.grants`, `rk_site_plan.caps`, `rk_pdf_usage` (installation-level, left out
  of org backups like `rk_ai_usage`).

## Open

- A `required` flag for catalogue apps that never count (today only `core`): decide in the catalogue (0036).
- Whether `requests_month` should ever be enforced in the kernel for self-hosted installs (today: no).
