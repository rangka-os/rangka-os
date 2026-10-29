# rangka-os kernel (v0)

The Rangka OS server: one Go binary that validates `.rk` metadata, migrates Postgres from it, runs the
document lifecycle, serves the REST API, realtime events, the queue and scheduler, Rangka ID (sessions,
API tokens, an OpenID Connect provider), change sets and the MCP endpoint. The binding contract is
[`../../CONTRACTS.md`](../../CONTRACTS.md); the reference semantics are the browser prototype
(`../web/prototype/engine.js`) and `../tools/rangka_validate.py`.

```
make test       # all tests against a throwaway Postgres (started and removed by scripts/pgtemp.sh)
make run-demo   # temp Postgres → migrate → seed → serve :8080 with --dev-open; Ctrl-C cleans up
make validate   # rangka validate on rangka-apps/apps + sites/kopi-senja + rangka-connectors
make compare-python   # Go vs rangka_validate.py on the real metadata and two broken copies (must be identical)
```

Then open http://localhost:8080/prototype/, http://localhost:8080/api/v1/meta, or point an MCP client at
`POST http://localhost:8080/mcp`. Without `--dev-open`, sign in with `ceo@saga.co.id` / `rangka-demo`.

## Standard library only (for now)

The decided stack was chi, pgx v5, cel-go, River, santhosh-tekuri/jsonschema and yaml.v3. When this was
built the sandbox could not reach the Go module proxy (proxy.golang.org answered 403), so the kernel uses
only the Go standard library. Each library has a small in-tree stand-in behind the same seam, so swapping
the real one in is local to one package:

| Decided | In-tree stand-in | Seam |
| --- | --- | --- |
| chi | `net/http` `ServeMux` patterns (Go 1.22+) | `internal/api/server.go` `Handler()` |
| pgx v5 | `internal/pg`: wire protocol v3, SCRAM-SHA-256/MD5/cleartext, TLS, extended protocol, pool, tx, LISTEN/NOTIFY | `pg.Querier` (`Query`, `Exec`) |
| cel-go | `internal/cel`: CEL grammar + macros, heterogeneous numeric equality, CONTRACTS functions | `cel.Compile`, `Program.Eval(*cel.Env)` |
| River | `internal/jobs`: Postgres queue (`FOR UPDATE SKIP LOCKED`), retries with backoff, River's job states, cron scheduler with unique keys | `engine.Enqueuer`, `jobs.Handler` |
| jsonschema | `internal/schema`: draft 2020-12 subset used by `schema/0.4`, Python-jsonschema messages | `schema.Set.Validate` |
| yaml.v3 | `internal/rk`: strict YAML parser with positions + writer | `rk.Load`, `rk.Dump` |
| bcrypt/argon2 | PBKDF2-HMAC-SHA256 (`crypto/pbkdf2`), self-describing hash format | `auth.HashPassword` |

## Architecture

```
            .rk files (apps/, site/, connectors/)
                     │  internal/rk (strict YAML, line:col)  internal/schema (JSON Schema)
                     ▼
               internal/meta ── Registry (apps, models, dashboards, flows, site) + Validate (reference checks)
                     │
   ┌─────────────────┼───────────────────────────────┐
   ▼                 ▼                               ▼
internal/store   internal/engine                 internal/changeset
DDL plan/migrate defaults, compute, rules,       propose → validate (sandbox copy) → preview (DDL,
records, naming  naming, submit/cancel/amend,    affected) → approve (person) → apply (write, migrate,
audit, NOTIFY    actions + hooks, list/CEL,      reload live) → revert; risk + layers
                 dashboards, flows, seed
   │                 │  uses internal/cel, internal/cache
   ▼                 ▼
internal/pg ◄── internal/jobs (queue workers, scheduler)   internal/realtime (LISTEN rk_events → SSE)
                     ▲
internal/auth (Rangka ID: sessions, rk_ tokens, OIDC)   internal/mcp (JSON-RPC tools)
                     ▲
               internal/api (REST /api/v1, /auth, /oauth, /mcp, static web) ◄── internal/server (wiring, reload)
                     ▲
               cmd/rangka (validate | migrate | seed | serve)
```

| Package | What it does |
| --- | --- |
| `internal/rk` | Strict YAML reader with the same refusals and positions as `rkfile.py` (tested against it on every workspace file), and a writer for change-set patches. |
| `internal/schema` | The `schema/0.4` JSON Schemas embedded in the binary (`make sync-schemas`; a test fails when they drift) and a validator with Python-compatible messages. |
| `internal/meta` | Loads apps, a site (with company overlays in `custom/`) and connectors into a `Registry`; `Validate` ports every check of `rangka_validate.py` byte for byte, then merges overlays onto the app models. `config.apps` is the installed set. |
| `internal/theme` | Base design tokens (embedded copy of `web/design-system/tokens.json`), the dark derivation of CONTRACTS v0.1 decision 12 and WCAG contrast; same arithmetic as the Python validator. |
| `internal/cel` | The CEL dialect: `today()`, `now()`, `duration()`, date ± duration, `round()`, `sum()`, `.map()`, `option()`, `app_installed()`, `truncate()`, `size()`, `.contains()`, `in`, ternary, link traversal (`customer.payment_days` loads the record). |
| `internal/pg` | The Postgres client. |
| `internal/store` | Model → table compiler (`r_<model>`, child tables, indexes on links/docstatus/unique), diff-based migrations (add tables, columns, indexes; never drops), `rk_meta_files` hashes, record I/O, naming counters, audit, NOTIFY. |
| `internal/engine` | The lifecycle and everything computed from records (see below). |
| `internal/jobs` | Queue (`rk_jobs`) and scheduler (flows with `schedule:` in `system.rk scheduler.timezone`). |
| `internal/realtime` | NOTIFY listener and SSE fan-out with `?models=` filter and 15 s heartbeat. |
| `internal/cache` | Cache interface, memory TTL driver, Valkey stub. |
| `internal/auth` | Passwords, sessions (`rk_session` cookie), API tokens (`rk_…`), OIDC provider (discovery, authorize code + PKCE S256, token, userinfo, JWKS; RS256 key generated on first use and stored in `rk_keys`). |
| `internal/changeset` | Change sets, JSON Patch, risk/layer assessment. |
| `internal/mcp` | MCP over Streamable HTTP (JSON responses), tools fixed by CONTRACTS. |
| `internal/api` | HTTP handlers. |
| `internal/server` | Wires the above, live metadata reload; integration tests. |

## Commands

```
rangka validate --apps <dir> [--site <dir>] [--connectors <dir>] [--json] [app ...]
rangka migrate  --apps <dir> [--site <dir>] --db <url> [--dry-run]
rangka seed     --apps <dir> [--site <dir>] --db <url> [--fixtures <dir>] [--org kopi-senja]
rangka serve    --apps <dir> [--site <dir>] --db <url> [--addr :8080] [--web <dir>] [--templates <dir>] [--connectors <dir>] [--deploy-target dev] [--dev-open]
```

`validate` prints `file:line:col  level  code  path  message` exactly like `rangka_validate.py` and exits 1 on
errors. `migrate` and `serve` refuse metadata with errors. `seed` creates the org (the site directory name),
loads `fixtures/*.rk` the way the prototype loads its seed (names and docstatus kept, defaults and computed
fields filled) and the owner `ceo@saga.co.id` / `rangka-demo` mapped to user record `USR-001`. `serve`
serves `rangka-os/web` at `/` (prototype at `/prototype/`).

## Records and lifecycle

* Table per model: `org_id`, `name` (PK per org), `created_by`, `created_at`, `modified_at`, `x` (JSONB for
  `x_…` company fields from overlays), `docstatus`/`amended_from` when submittable, one typed column per field; table
  fields → child table with `parent`, `parent_field`, `idx`. "" is stored as NULL and read back as the
  CONTRACTS missing value ('' / 0 / false).
* Create: defaults (CEL, in field order, also for new child rows: `item.rate` follows the link), computed
  fields (children first, dependency order), save rules, required fields, link targets exist, select values
  are options. Submittable models get `DRAFT-XXXXX`; others take their naming pattern
  (`config.rk numbering` overrides the model). Counters start after the highest existing name.
* Submit: submit + save rules, `min_rows`; warnings return 422 with `needs_force: true` unless
  `{"force": true}`; then the naming pattern (`INV-2026-00047`), docstatus 1, `on_submit` hooks. Submitted
  records are immutable (PATCH/DELETE → 409). Cancel → 2; amend → new draft with `amended_from`, read-only
  stored values reset; the amendment submits as `INV-2026-00047-1`.
* Submittable records carry a derived `status` (draft, submitted, partly, paid, overdue, cancelled).
* Actions run hooks (`crm.convert_lead` is built in) in the request's transaction; `queue:` actions return
  202 `{job_id}` and run on a worker. Every write appends to `rk_audit` and NOTIFYs `rk_events`.
* Dashboards compute the prototype's `computeWidget()` shapes; the tests compare every widget with the
  prototype's numbers for the seed data (`testdata/prototype-dashboards.json`, `make proto-expected`).
  Dashboards with `cache:` are cached per org and period and dropped on any write.
* Flows: `find`, `find_all`, `create`/`update` (with `set:`), `notify` (log driver), `call` (recorded; no
  connector runtime yet), `if`/`then`/`else`, `wait` (queues the rest), each with `if:`/`as:`.
* Automations (`when: stage changes to 'won'`, `do: crm.mark_won`) run their hook in the write's transaction. Scheduled flows are enqueued once per due minute (unique key), event flows run with
  `POST /api/v1/flows/{name}/run {event: …}`.

## Auth

`/api` and `/mcp` need a session cookie (`POST /auth/login`), an API token (`POST /api/v1/tokens` →
`Authorization: Bearer rk_…`) or an OIDC access token issued by `/oauth/token` (on `/mcp` it needs the
`rangka.mcp` scope). OAuth clients come from `system.rk auth.clients`. Change-set approval needs a person
with a browser session and an approver role (`config.rk ai.approvers`, always `owner`).
`--dev-open` makes anonymous requests act as the org owner; it is for local demos only.

## Tests

`make test` runs unit tests and integration tests (fresh database per test: `internal/testdb`). Without
`RANGKA_TEST_DATABASE_URL` the database tests skip. Notable tests:

* `internal/rk` compares data, positions and error codes with `rkfile.py` on every workspace `.rk` file
  and an edge-case corpus (`testdata/yaml`).
* `internal/meta` checks 0 problems on the real apps + site, byte-identical output with
  `rangka_validate.py` on `testdata/broken-apps` (golden file and live run), and the kernel's site checks
  on `testdata/broken-site`.
* `internal/engine` checks every dashboard widget against the prototype, the invoice lifecycle, rules,
  link traversal and `crm.convert_lead`.
* `internal/server` drives the HTTP API end to end: REST and lifecycle, dashboards and cache, SSE, queued
  actions, the scheduler, OIDC with PKCE, tokens, change sets (apply and revert on a copy of the files),
  auto-apply of low-risk change sets, and MCP.
