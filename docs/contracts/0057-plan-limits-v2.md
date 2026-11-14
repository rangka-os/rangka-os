# 0057 Plan limits v2: apps, storage, PDF, and operator grants and caps

Status: proposed (14 Nov 2026) · Owner of the change: rangka-os (kernel `plan`, `features.rk` schema, `rangka plan`,
`GET /api/v1/plan`) · Affects: rangka-cloud (Konsol, billing, provisioning, the Grup pool), rangka-apps (`features.rk`
plans of sites and templates) · Builds on: 0028 (plan limits), 0040 §9 (`limits.rate`), 0045 §2 (`features.rk` and
`config.plan` are not proposable by an assistant), 0048 (access mode)

## Context

- **The Cloud plan model changed (Dede, 14 Nov 2026).** rangka-cloud `docs/specs/2026-11-14-plans-billing-v2-design.md`:
  an owner account holds subscriptions, a subscription holds businesses, a business holds apps. Mulai, Juragan and Pro
  are one business per subscription; Juragan Grup is up to 3 businesses sharing one usage pool. Each plan limits apps
  per business (Mulai 1, Juragan 3, Pro 5, Grup 15), database and file size, and PDFs. Past a limit, the lower plans
  are offered the next plan; Grup buys capacity add-ons ("harga pecahan"). Raka credits can be bought on every plan.
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
| `apps` | installed apps of the tenant, except `core` and apps the catalogue marks `required` | Installing one more (a change set adding to `config.apps`, `rangka` apply, seed excluded) past the limit is refused with 403 `plan_limit`. The validator reports `app_not_in_plan` as today plus `apps_over_limit` (warning) for a site file that lists more |
| `db_mb` | `pg_database_size` of the tenant's database, measured by the plan snapshot (0028 §4, every 10 s at most) | From 100%: imports and new attachments are refused (`plan_limit`). From 120%: creating records is refused too, except records that arrive from outlet devices (edge sync, 0006), so a sale is never lost. Updates and deletes stay allowed, so the owner can clean up |
| `files_mb` | total size of stored attachments and generated files | An upload that would pass the limit is refused (`plan_limit`) |
| `pdf_month` | PDF renders in the calendar month (site timezone) | Past the limit renders are not refused: they queue and run at most 10 an hour per tenant. `GET /api/v1/plan` reports the queue |
| `requests_month` | API requests in the month | Reported only. Shaping stays with `limits.rate` and the hosting router |

- `storage_mb` stays readable for one release and is reported as `db_mb + files_mb`; the validator warns
  `storage_mb_deprecated`.
- A plan without one of these limits is not limited by it, as in 0028.
- **Downgrade.** Nothing is deleted. Apps past a lower `apps` limit, newest installed first, become read-only
  (`plan_read_only`: reads, prints and exports allowed; writes refused), like outlets in 0028 §4. `db_mb` and
  `files_mb` past a lower limit behave as in the table.

### 2. Operator grants and caps

`rk_site_plan` gains `grants jsonb` (a list) and `caps jsonb` (a map). The effective limit is:

```
effective(key) = min( plan_limit(key) + Σ active grants(key), cap(key) )
```

- **Grant**: `rangka plan grant <limit> <+n> [--until <date>] [--ref <text>] [--by] [--note]` adds capacity (a Grup
  add-on: `db_mb +1024`, `files_mb +10240`, `apps +1`; Raka credits: `ai_credits_month +100 --until 2027-02-14`).
  `rangka plan revoke <grant id>` ends one early. `--until` passes → the grant stops counting; nothing else changes.
- **Cap**: `rangka plan cap <limit> <value> [--reason <text>]` sets a ceiling below the plan, `rangka plan uncap
  <limit>` removes it. Cloud uses it for the Grup pool: when the pool's sum reaches its total, Cloud caps each
  business at what it uses now; when capacity frees up or is bought, Cloud uncaps.
- Grants and caps are operator-only: CLI, no REST endpoint, like `rangka plan set`. They stay forbidden to an
  assistant (0045 §2: `features.rk` and `config.plan`). Every change is a row in `rk_plan_changes` (0028 §6).
- A plan change (`rangka plan set`) keeps grants and caps. `rangka plan clear` removes them.

### 3. `GET /api/v1/plan`

- `usage` gains `apps`, `db_mb`, `files_mb`, `pdf_month` and `requests_month` with `used`, `limit` (effective),
  `state` (`ok`, `near` from 80%, `full`, `over`) and `message`.
- Each usage entry gains `plan_limit`, `grants: [{id, amount, until, ref}]` and `cap: {value, reason}` when set.
- `read_only` gains `apps: [name]`. `pdf_month` reports `queued`.
- Messages name the next plan that has room ("Naik ke Juragan Pro untuk menambah app."). On the highest sellable plan
  they point to capacity instead ("Tambah kapasitas di Konsol."). A cap with reason `pool` says "Kolam Juragan Grup
  sudah penuh. Tambah kapasitas atau kurangi pemakaian di bisnis lain."

### 4. Refusal shape

Unchanged from 0028 §3: `{"error": {"code": "plan_limit" | "plan_read_only", "resource": "<limit>", "message": …}}`.

## Consequences

- rangka-cloud adds subscriptions, the Grup pool sampler, add-on and credit grants, and the Konsol pages (its spec
  above). Hosting already runs `rangka plan set` through `P.RunTask`; grants and caps use the same path.
- rangka-apps: `features.rk` plans of the Cloud sites gain the new limits; templates without them stay unlimited.
- One PR per limit (`apps`, `db_mb`, `files_mb`, `pdf_month`), then grants and caps, each with tests and a
  Python validator change where it reads `features.rk`.

## Open

- The `required` flag for catalogue apps that never count (today only `core`): decide in the catalogue (0036).
- Whether `requests_month` should ever be enforced in the kernel for self-hosted installs (today: no).
