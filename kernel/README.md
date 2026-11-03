# rangka-os kernel (v0)

The Rangka OS server: one Go binary that validates `.rk` metadata, migrates Postgres from it, runs the
document lifecycle, serves the REST API, realtime events, the queue and scheduler, Rangka ID (sessions,
API tokens, an OpenID Connect provider), change sets and the MCP endpoint. The binding contract is
[`../docs/contracts.md`](../docs/contracts.md); the reference semantics are the browser prototype
(`../web/app/engine.js`) and `../tools/rangka_validate.py`.

```
make test       # all tests against a throwaway Postgres (started and removed by scripts/pgtemp.sh)
make run-demo   # temp Postgres → migrate → seed → serve :8080 with --dev-open; Ctrl-C cleans up
make validate   # rangka validate on testdata/catalogue-v0 (rangka-apps as of v0) + rangka-connectors
make compare-python   # Go vs rangka_validate.py on the v0 catalogue, the broken copies and the modules/apps fixtures (must be identical)
```

Then open http://localhost:8080/app/, http://localhost:8080/api/v1/meta, or point an MCP client at
`POST http://localhost:8080/mcp`. Without `--dev-open`, sign in with `ceo@saga.co.id` / `rangka-demo`. More people:
`RANGKA_USER_PASSWORD=<p> rangka user add <email> --org kopi-senja --role <role>` (see `rangka user` below;
`--reset-password` and `--create-org` are opt-in).

## Stand-ins (for now)

The decided stack was chi, pgx v5, cel-go, River, santhosh-tekuri/jsonschema and yaml.v3. When this was
built the sandbox could not reach the Go module proxy (proxy.golang.org answered 403), so the kernel started
on the Go standard library with a small in-tree stand-in behind each seam. pgx v5 and bcrypt are now the real
libraries (contract 0005 §1, stream S1); the others are still stand-ins, and swapping each one in is local to
one package:

| Decided | In-tree stand-in | Seam |
| --- | --- | --- |
| chi | `net/http` `ServeMux` patterns (Go 1.22+) | `internal/api/server.go` `Handler()` |
| cel-go | `internal/cel`: CEL grammar + macros, heterogeneous numeric equality, CONTRACTS functions | `cel.Compile`, `Program.Eval(*cel.Env)` |
| River | `internal/jobs`: Postgres queue (`FOR UPDATE SKIP LOCKED`), retries with backoff, River's job states, cron scheduler with unique keys | `engine.Enqueuer`, `jobs.Handler` |
| jsonschema | `internal/schema`: draft 2020-12 subset used by `schema/0.4`, Python-jsonschema messages | `schema.Set.Validate` |
| yaml.v3 | `internal/rk`: strict YAML parser with positions + writer | `rk.Load`, `rk.Dump` |

Done: `internal/pg` is a thin wrapper over pgx v5 (`pg.Querier`: `Query`, `Exec`; pool, tx, LISTEN/NOTIFY, `CopyTo`
and `CopyFrom`), and passwords use bcrypt (`auth.HashPassword`; PBKDF2 hashes from earlier builds are upgraded at
the next sign-in).

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
               cmd/rangka (validate | migrate | backup | restore | seed | serve)
```

| Package | What it does |
| --- | --- |
| `internal/rk` | Strict YAML reader with the same refusals and positions as `rkfile.py` (tested against it on every workspace file), and a writer for change-set patches. |
| `internal/schema` | The `schema/0.4` JSON Schemas embedded in the binary (`make sync-schemas`; a test fails when they drift) and a validator with Python-compatible messages. |
| `internal/meta` | Loads apps, a site (with company overlays in `custom/`) and connectors into a `Registry`; `Validate` ports every check of `rangka_validate.py` byte for byte, then merges overlays onto the app models. `config.apps` is the installed set. |
| `internal/theme` | Base design tokens (embedded copy of `web/design-system/tokens.json`), the dark derivation of CONTRACTS v0.1 decision 12 and WCAG contrast; same arithmetic as the Python validator. |
| `internal/cel` | The CEL dialect: `today()`, `now()`, `duration()`, date ± duration, `round()`, `sum()`, `.map()`, `option()`, `app_installed()`, `truncate()`, `size()`, `.contains()`, `in`, ternary, link traversal (`customer.payment_days` loads the record). |
| `internal/pg` | The Postgres client. |
| `internal/store` | Model → table compiler (`r_<model>`, child tables, indexes on links/docstatus/unique), diff-based migrations (add tables, columns, indexes; risk levels, reverse steps, destructive steps behind a flag), `rk_meta_files` hashes, record I/O, naming counters, audit, NOTIFY. |
| `internal/engine` | The lifecycle and everything computed from records (see below). |
| `internal/jobs` | Queue (`rk_jobs`) and scheduler (flows with `schedule:` in `system.rk scheduler.timezone`). |
| `internal/realtime` | NOTIFY listener and SSE fan-out with `?models=` filter and 15 s heartbeat. |
| `internal/cache` | Cache interface, memory TTL driver, Valkey stub. |
| `internal/auth` | Passwords, sessions (`rk_session` cookie), API tokens (`rk_…`), OIDC provider (discovery, authorize code + PKCE S256, token, userinfo, JWKS; RS256 key generated on first use and stored in `rk_keys`). |
| `internal/changeset` | Change sets, JSON Patch, risk/layer assessment. |
| `internal/mcp` | MCP over Streamable HTTP (JSON responses), tools fixed by CONTRACTS. |
| `internal/api` | HTTP handlers. |
| `internal/backup` | `rangka backup` and `rangka restore`: native `COPY` of every table plus site, apps and files, with checksums. |
| `internal/server` | Wires the above, live metadata reload, the production backup before migrations (`PreMigrateBackup`); integration tests. |

## Commands

```
rangka validate --apps <dir> [--site <dir>] [--connectors <dir>] [--json] [app ...]
rangka migrate  --apps <dir> [--site <dir>] --db <url> [--dry-run] [--allow-destructive] [--rollback n]
rangka backup   --db <url> [--apps <dir>] [--site <dir>] [--out <file>] [--org <id>] [--files <dir>]
rangka restore  <file> --db <url> [--into <dir>] [--files <dir>]
rangka seed     --apps <dir> [--site <dir>] --db <url> [--fixtures <dir>] [--org kopi-senja]
rangka serve    --apps <dir> [--site <dir>] --db <url> [--addr :8080] [--web <dir>] [--templates <dir>] [--connectors <dir>] [--deploy-target dev] [--files-dir <dir>] [--dev-open]
rangka user add    <email> --org <id> --role <role> [--outlets a,b] [--record USR-001] [--name "..."]
                   [--reset-password] [--create-org] [--db <url>]
rangka user remove <email> --org <id> [--db <url>]
```

`validate` prints `file:line:col  level  code  path  message` exactly like `rangka_validate.py` and exits 1 on
errors. `migrate` and `serve` refuse metadata with errors. `seed` creates the org (the site directory name),
loads `fixtures/*.rk` the way the prototype loads its seed (names and docstatus kept, defaults and computed
fields filled) and the owner `ceo@saga.co.id` / `rangka-demo` mapped to user record `USR-001`. `serve`
serves `rangka-os/web` at `/` (app at `/app/`, `/` redirects there).

`migrate --dry-run` prints each statement with its risk and changes nothing; destructive steps need
`--allow-destructive`; `--rollback n` undoes the last n migrations. `backup` writes one verified archive (every
table, site, apps, uploads) and `restore` rebuilds it into an empty database and checks the checksums. With
`RANGKA_ENV=production`, `migrate`, `serve`'s start-up migration and change-set applies first write
`<RANGKA_BACKUP_DIR or <site>/backups>/rangka-all-<time>-pre-migrate.tar.gz`. Details:
[CLI reference](../website/reference/cli.md).

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

* `GET /auth/me` returns `user` (with `outlets`, the member's outlets in the current org), `orgs`
  (`[{id, name, code, roles}]`, every membership, sorted by id) and `org` (the current one).
* `POST /auth/org {"org": "<id>"}` switches a browser session to another org the user belongs to, sets the
  `rk_org` cookie and answers like `/auth/me`. Not a member: 403 `permission_denied`. API and OIDC tokens
  have a fixed org: 400 `org_fixed`. Sessions only.
* `GET /api/v1/orgs` lists only the caller's orgs.
* `rangka user add` creates the user or updates the membership (role, `--record`; `--outlets` replaces the
  member's outlets, `--outlets ""` clears them). The email is the first argument or `--email`. The password
  comes from the `RANGKA_USER_PASSWORD` environment variable (or `--password`, which lands in shell history)
  and is needed only for a new account or with `--reset-password`; an existing account's password is never
  changed otherwise. An unknown org is refused unless `--create-org` is given. `--db` and `--org` default to
  `$DATABASE_URL` and `$RANGKA_ORG`. `rangka user remove <email> --org <id>` ends a membership and exits 1 with
  `<email> is not a member of <org>` when it is not one. Missing or wrong flags exit 2.

### Multi-org and row scope

Contract: [`0010`](../docs/contracts/0010-multi-org-and-row-scope.md).

* Per-org settings live in `config.rk` under `orgs:` (name, code, `config:` overrides). `rangka migrate`,
  `seed` and `serve` upsert `rk_orgs` from it; code reads them with `Registry.OrgConfig(org, key)`.
* A role with `scope: {field, from}` limits a member to records whose outlet field is in `user.outlets`.
  The engine enforces it for lists, dashboards, records, actions and SSE.
* Every model table has row-level security keyed by org: the kernel sets `rangka.org` on each pooled
  connection from the request context. Superusers and `BYPASSRLS` roles skip RLS, so `serve` logs a warning
  when connected as one. Hosted installs must connect as a plain role that owns the tables.

## Attachments

Files on records (a signed BAST on a project, proof of transfer on a payment), contract
[0011](../docs/contracts/0011-attachments.md). Not to be confused with `/api/v1/files`, which serves the `.rk` files.

```
POST   /api/v1/r/{model}/{name}/attachments   multipart, part "file"  → 201 {id, filename, content_type, size, sha256, created_by, created_at, url}
GET    /api/v1/r/{model}/{name}/attachments   → {data: [item…]}, newest first
GET    /api/v1/attachments/{id}               → the bytes (inline for images and PDF, attachment otherwise)
DELETE /api/v1/attachments/{id}               → 204
```

* Bytes live under a root directory, first match wins: `rangka serve --files-dir` / `$RANGKA_FILES_DIR`, then
  `system.rk files.path`, then `./data/files` (relative to where `serve` runs). Content-addressed per org
  (`<root>/<orgdir>/<sha[0:2]>/<sha>`, where `<orgdir>` is the org id, or `_` + 16 hex of its sha256 when the id is not a plain slug); metadata is in `rk_attachments`. Back up the root together with the database.
* Limit `files.max_upload_mb` (default 20): larger uploads get 413 `too_large`. `files: {enabled: false}` answers
  501 `files_disabled`; `driver: s3` is refused at start-up until a driver exists.
* Reading needs the record to be visible to the caller (org scope today; role permissions once contract 0005 §4
  lands), upload and delete need `write`. Upload works in any docstatus; delete
  is refused (409 `submitted`) once a submittable record is submitted. Attachments follow the record when submit
  renames it and go with it when it is deleted. Uploads and deletes are audited.
  Downloads send `nosniff` and `Content-Security-Policy: sandbox`, except PDF (Chromium's viewer refuses sandboxed
  documents).

## Print formats

Printable documents (quotation, invoice, PO, statement of account), contract
[0008](../docs/contracts/0008-print-formats.md). A model gets a format from a `print/<name>.rk` plus a Go
`html/template`; the reference layouts are in [`../templates/print/`](../templates/print/).

```
GET /api/v1/r/{model}/{name}/print?format=<print>&as=html|pdf
```

* `format` defaults to the model's `default: true` format, else the first by name; none → 404 `no_print_format`.
  `as=html` (default) is `text/html`; `as=pdf` is rendered by headless Chromium; any other `as` → 400 `bad_request`.
* `RANGKA_CHROMIUM` is the path of the browser. Without it the kernel looks for `chromium`, `chromium-browser`,
  `google-chrome`, `google-chrome-stable` or `headless_shell` on `PATH`, then the macOS Chrome app.
  `RANGKA_CHROMIUM=off`, or a path that does not exist, means no browser: `as=pdf` answers 501 `pdf_unavailable`
  with a `hint` pointing at the HTML version. A conversion is cut off after 30 s; a failed one answers 500
  `pdf_failed` (the detail goes to the log). At most `RANGKA_PDF_CONCURRENCY` (default 2) browsers run at once;
  further requests wait for a slot.
* The route sits behind the record guard (S1): the record is read as the caller, so another org's record or one
  outside the caller's row scope is 404 `not_found`, as for `GET /r/{model}/{name}`; a caller who can read it but
  lacks the `print` permission gets 403 `permission_denied`. Every print is audited as `record.print`; a `HEAD`
  starts no browser and writes no audit entry.
* `.Org` is the org's `org:` settings (`orgs.<id>.config.org`, else the site's `org:` in `config.rk`) plus `name`
  and `code` from `orgs.<id>`; `.Org.pkp` falls back to `tax.pkp`.
* `/api/v1/meta` lists each model's formats as `prints`. `rangka validate` checks formats and letterheads
  (`template_missing`, `template_outside`, `template_error`, `logo_missing`).

## Tests

`make test` runs unit tests and integration tests (fresh database per test: `internal/testdb`). Without
`RANGKA_TEST_DATABASE_URL` the database tests skip. Tests and the Makefile find `rangka-apps` and `rangka-connectors` by searching upward from this checkout (so git worktrees work); set `RANGKA_WORKSPACE` to the folder that holds them to override. Tests that need them skip when they are missing, except when `CI` is set (any non-empty value) or `RANGKA_REQUIRE_SIBLINGS=1`: then a missing checkout fails the test (`testdb.Sibling`), so a CI run cannot pass by skipping. Notable tests:

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
* `internal/backup` seeds the demo, backs it up, restores it into a fresh database and compares every table's
  checksum, plus corrupt, newer and unsafe archives.
* `internal/server` also holds the permission matrix and `TestPreMigrateBackup`.

CI (`.github/workflows/kernel.yml`) runs `make vet`, `make test` and `make compare-python` on every push and
pull request to `main`, with `rangka-apps` and `rangka-connectors` checked out next to this repo.
