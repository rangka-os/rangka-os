# 0039: Serverless kernel and traffic shaping

Status: proposed (8 Nov 2026) · Owner of the change: rangka-os (kernel) · Affects: Rangka Cloud (hosting on
AWS Lambda, router throttling), the web app (realtime by polling), `features.rk` plans (schema), both validators

## Context

Rangka Cloud first planned one always-on Fargate task per business (rangka-cloud spec of 4 Nov). The cost model of
8 Nov showed that a single 0.5 vCPU / 1 GB task in Jakarta costs about $18 a month (about Rp 326.000), more than three
times the net price of the Juragan package (Rp 100.000, about Rp 89.000 after PPN and payment fees). Most small
businesses are idle most of the day, so Dede decided on 8 Nov:

- small and medium packages (Mulai, Juragan, Juragan Pro, Juragan Grup) run each business's kernel on **AWS Lambda**,
  paid per request, against the shared Aurora Serverless v2 cluster; enterprise keeps a dedicated, always-on kernel;
- a cold start of a second or two for a quiet business is accepted;
- traffic above a package's allowance is **slowed down, not blocked**.

Today the kernel assumes one long-running process (`server.Start`):
- the scheduler ticks on a timer in the process (`jobs/scheduler.go`);
- queue workers poll `rk_jobs` in goroutines (`jobs/queue.go`, `FOR UPDATE SKIP LOCKED`, already safe for several
  instances);
- the realtime hub holds a `LISTEN rk_events` connection and serves SSE (`realtime/hub.go`). Events are only
  `pg_notify`'d (`store/audit.go`), never stored;
- `rangka serve` migrates at start, and a change set reloads the registry in the process that applied it
  (`server.Reload`). Other processes never see the new metadata, so only one kernel may run per business;
- PDFs render with headless Chromium in the process (`docprint`);
- files live in a local directory (`files.Local`).

On Lambda nothing runs between requests. The process is frozen after the response and may be discarded at any time.
Many instances can serve one business at once.

`0028` keeps holding: one kernel (now one Lambda function) serves one tenant.

## Decision

### 1. Two run modes, one engine
- `rangka serve` stays as it is: a long-running process with the hub, scheduler and workers. It is used for
  self-hosting, Docker, the desktop bundle, enterprise and local development.
- A second binary, **`rangka-lambda`**, built from `kernel/cmd/rangka-lambda` with build tag `lambda`, wraps the
  same `server.New` and `api.Server` handler for AWS Lambda (`provided.al2023`, arm64). It uses
  `github.com/aws/aws-lambda-go` and an `http.Handler` adapter. These dependencies stay out of the main `rangka`
  binary.
- In Lambda mode the kernel starts **no goroutine that outlives a request**: no hub listener, no scheduler loop, no
  queue workers. Work after the response is not allowed. `server.Start` is not called. A new
  `server.StartRequestOnly` sets up what a request needs.
- The binary dispatches on the shape of the invocation payload:

  | Payload | Handled as |
  | --- | --- |
  | Function URL / ALB / API Gateway HTTP event | the HTTP API, exactly as `rangka serve` serves it |
  | `{"rangka": "tick"}` | §3 tick |
  | `{"rangka": "migrate", "dry_run": bool}` | `rangka migrate` (same code as the CLI), result as JSON |
  | `{"rangka": "seed", "fixtures": bool}` | `rangka seed` (owner user, orgs; fixtures only when asked), result as JSON |
  | anything else | error `unknown_event` |

  An HTTP request cannot produce a non-HTTP payload, so `tick`, `migrate` and `seed` are reachable only by whoever may
  invoke the function (IAM). The router also strips any `/_rangka/` path from public traffic.

### 2. Start fast, never migrate at start
- In Lambda mode the kernel does **not** migrate or seed at start. It reads the schema version. When the database
  is behind the metadata, every request answers `503 {"error": {"code": "not_migrated"}}` until a `migrate`
  invocation has run. The hosting side runs `migrate` before it moves traffic to a new version.
- Metadata is read from `RANGKA_APPS` / `RANGKA_SITE`: a mounted shared directory (EFS on AWS) or a directory baked
  into the package.
- The registry is loaded once per instance. Templates, dashboards and print layouts are compiled lazily on first use,
  not at start.
- A test starts `rangka-lambda` against a migrated database and asserts that start makes no database writes and
  needs at most one query (the version check). The start time is printed so that CI can follow it. The target is
  under 300 ms for the binary's own init, which still has to be measured.

### 3. Tick: scheduler and queue run when invoked
- `tick` does, in order:
  1. take `pg_try_advisory_lock(hashtext('rk_tick'))`; if another instance holds it, answer `{"skipped": true}`
     at once;
  2. `Scheduler.Tick(now)`: enqueue due cron flows and recurring documents, as the in-process loop does today;
  3. run `Queue.RunOne` until the queue has nothing runnable or the **tick budget** is spent (default 40 s,
     `queue.tick_budget_s` in `system.rk`; it must stay under the function timeout);
  4. prune the event log (§4) and expired idempotency rows;
  5. answer `{"enqueued": n, "ran": n, "failed": n, "left": n, "ms": n}`.
- The hosting side invokes `tick` on a schedule (EventBridge Scheduler on AWS, every minute by default; per package,
  see rangka-cloud). `rangka serve` keeps its timer and calls the same functions, so both modes share one code path.
- Latency: a job queued by a request runs at the next tick, so within one tick interval. Actions that are queued
  today (`queue:` on an action, connector sends, imports) already answer `202` with a job id, so their API does not
  change. The UI copy for a queued job says "sedang diproses", never a time.
- A later contract may add an immediate nudge (the function invoking itself asynchronously after commit). It is not
  part of this one.

### 4. Realtime: stored events and polling
- Every event that is `pg_notify`'d on `rk_events` is also inserted, in the same transaction, into a new table
  **`rk_event_log`** `(id bigserial primary key, org text not null, kind text not null, model text, name text,
  payload jsonb not null, at timestamptz not null default now())`. The table has RLS like the model tables
  (0010 §4).
- Rows older than `realtime.log_keep_s` (default 600) are pruned by `tick` and by `rangka serve`'s scheduler.
- New endpoint **`GET /api/v1/events/poll?after=<cursor>`** answers
  `{events: [...], cursor: "<id>", reset: bool, poll_ms: n}`:
  - same filtering as SSE: the caller's org, row scope (0010 §5), model permissions, field visibility (0027);
  - at most 200 events per call, oldest first;
  - `reset: true` when `after` is older than the oldest kept row, or unknown. The client then reloads its open view;
  - with no `after`, it answers the current cursor and no events.
- `GET /auth/me` gains `realtime: {mode: "sse" | "poll", poll_ms: n}`:
  - `sse` under `rangka serve`;
  - `poll` under `rangka-lambda`;
  - `poll_ms` comes from `realtime.poll_ms` in `system.rk` (default 10000). The host may override it per package
    with `RANGKA_POLL_MS`.
- `GET /api/v1/events` (SSE) under `rangka-lambda` answers `409 {"error": {"code": "realtime_poll"}}`, so a client
  that ignores `/auth/me` falls back to polling.
- The web app polls only while the tab is visible (`document.visibilityState`) and backs off to 60 s after
  5 minutes without input.

### 5. Database connections
- Under `rangka-lambda` the pool defaults to 2 connections (`RANGKA_DB_POOL`), closes idle connections after 60 s,
  and sets `application_name = 'rangka-lambda'`.
- No connection proxy is required. The kernel must still work behind one (RDS Proxy, PgBouncer in transaction mode).
  Today `rangka.org` is a session setting, which makes a proxy pin connections. With `RANGKA_DB_TX_SCOPE=1` the
  kernel sets it with `set_config('rangka.org', $1, true)` at the start of every transaction instead. Every query is
  already run in a transaction or a one-statement implicit one; the second case gets wrapped.

### 6. Metadata across instances
- New single-row table **`rk_meta_version`** `(version bigint not null, hash text not null, at timestamptz not
  null)`. Migrate (including a change set's apply and revert) bumps it in the same transaction as the schema change.
  The hash is the bundle hash already printed by `rangka validate`.
- Every kernel process, in both modes, checks the version at most once every 2 s (cached, checked lazily on a
  request). When it is newer than the one it loaded, the process reloads the registry from its metadata directory
  without migrating, then updates the scheduler's flows. This is `server.Reload` minus the migration.
- Change sets write `.rk` files to the shared metadata directory (EFS on AWS) before bumping the version, so a
  reloading instance finds them.
- This lifts the rule that only one kernel may run per business. Rolling deploys and several instances become safe,
  once `tick`'s advisory lock (§3) also guards the scheduler under `rangka serve`.

### 7. PDF rendering through a renderer URL
- `system.rk` gains `print: {renderer: local | http, url: <string>}`, default `local`:
  - `local` is today's in-process Chromium;
  - with `http`, `docprint` POSTs `{html, options}` to `url` with `Authorization: Bearer ${RANGKA_PDF_TOKEN}` and
    receives `application/pdf`.
- The renderer is any service that speaks this shape. Rangka Cloud runs one shared PDF function. Self-hosters can
  keep `local`. The kernel itself depends on no cloud SDK for it.
- `rangka-lambda` refuses to start with `renderer: local` unless `RANGKA_PDF_LOCAL=1` is set (Chromium does not fit
  the small package memory).

### 8. Files
No change. `files.Local` uses a directory, and on AWS that directory is an EFS access point mounted into the function
(`/mnt/rk/<id>`). An object-store driver is a separate contract.

### 9. Traffic shaping: slow down, then refuse
A token bucket per tenant **delays** requests over the allowance instead of refusing them. It refuses only when the
wait would be too long.

- `features.rk` plan limits gain `rate` (schema 0.4, both validators):
  ```yaml
  plans:
    juragan:
      limits:
        rate: {rps: 5, burst: 30, max_wait_ms: 3000, max_waiting: 50}
  ```
  - `rps`: tokens added per second;
  - `burst`: bucket size;
  - `max_wait_ms`: the longest a request is held for a token;
  - `max_waiting`: how many requests may wait at once.
  - The values are positive numbers. `rps` may be a fraction (0.5).
- A request takes one token:
  1. A token is free: the request proceeds.
  2. No token is free: the request waits until one is free, up to `max_wait_ms`. The response carries
     `X-Rangka-Throttle-Ms: <ms waited>`.
  3. The wait would pass `max_wait_ms`, or `max_waiting` requests are already waiting: answer
     `429 {"error": {"code": "rate_limited", "message": "Sistem sedang ramai. Coba lagi sebentar."}}` with
     `Retry-After` in whole seconds.
- Not counted:
  - `GET /healthz`;
  - static web files;
  - `tick`, `migrate` and `seed`;
  - `GET /api/v1/events/poll` (it has its own floor: a caller polling faster than `poll_ms / 2` gets its previous
    answer again, with no new query).
- Sign-in keeps its own failure limiter, and AI chats keep `ai_credits_month` (0028) and the per-person chat limit.
- Unauthenticated routes (sign-in, public portals) use a second bucket per client IP with the same `rate`. Only a
  hash of the IP is kept, in memory.
- Buckets live in process memory. Under `rangka serve` with one process that is the whole tenant. Under Lambda each
  instance has its own buckets, so the kernel's bucket is only a backstop. The **hosting router shapes the tenant as a
  whole** before it invokes the function (rangka-cloud spec), because waiting inside a function is billed time.
- No `rate` in the plan, or no plan, means no shaping. Self-hosted installs behave as today unless they set a plan
  with `rate`.
- `GET /api/v1/plan` (0028 §5) reports `rate` under `limits` and adds `usage.rate: {throttled_ms_today,
  refused_today}` from in-process counters, labelled approximate.

### 10. What does not change
- The REST, MCP and CEL contracts, change-set lifecycle, RLS, plan limits and backups stay the same.
- `rangka serve` keeps SSE. Under `rangka-lambda`, clients see the realtime difference only through
  `/auth/me.realtime` (§4).

## Consequences
- Delivered as separate PRs, each with tests, `rangka serve` green throughout:
  1. `rk_event_log`, the poll endpoint and `/auth/me.realtime`;
  2. `rk_meta_version` and the cross-process reload;
  3. the tick functions, with `rangka serve`'s loops calling them under the advisory lock;
  4. the `print.renderer: http` setting;
  5. traffic shaping and the `rate` schema key;
  6. `cmd/rangka-lambda` and the start test;
  7. `RANGKA_DB_TX_SCOPE`.
- The web app needs a polling client (§4). The Console keeps SSE (it runs against `rangka serve`).
- `make compare-python` must stay identical for the `rate` key and the new `system.rk` keys (`queue.tick_budget_s`,
  `realtime.log_keep_s`, `realtime.poll_ms`, `print`).
- Open, to measure on AWS staging rather than decide here:
  - the cold-start time with VPC + EFS in ap-southeast-3;
  - Aurora connection counts under bursts;
  - whether the event log needs an index on `(org, id)` beyond the primary key;
  - large responses: buffered function URLs cap a response at 6 MB, so big exports (0024) and big PDFs need
    response streaming or a presigned object link.
