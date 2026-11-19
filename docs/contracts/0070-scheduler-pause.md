# 0070: Scheduler pause

Status: proposed (19 Nov 2026) · Owner of the change: rangka-os (kernel) · Affects: rangka-cloud (Kendali staff
console, provisioning), self-hosters (a new CLI command)

## Context

Rangka Cloud staff need to stop a business's scheduled work for a while: a business in dunning, an incident with a
connector vendor, a migration window. Kendali (`/staf`) today "pauses schedules" by deleting the business's
EventBridge tick. That only works for a kernel in request-only mode (0040 §1). On ECS, and anywhere `rangka serve`
runs, the kernel ticks its own scheduler loop every 5 seconds (`jobs/scheduler.go`), so deleting the outside tick
changes nothing. Deleting the tick also stops the queue, which strands jobs that were already accepted (a connector
send, an import).

The pause belongs in the kernel, stored in the kernel's database, so every run mode and every instance obeys it.

## Decision

### 1. CLI

```text
rangka scheduler pause --until <RFC3339> [--reason <text>] [--by <who>] [--db <url>]
rangka scheduler resume [--by <who>] [--db <url>]
rangka scheduler status [--json] [--db <url>]
```

- Same database and environment settings as the other operator commands (`rangka user add`, `rangka plan grant`):
  `--db`, else `DATABASE_URL`, else `.env`. No metadata is read, so `--apps`/`--site` are accepted and ignored. Rangka
  Cloud runs it as a one-off task with the kernel image.
- `pause` without `--until` is refused (exit 2). `--until` is RFC 3339 with an offset, must be in the future, and at
  most **30 days** ahead (`jobs.MaxPause`). `--reason` is at most 500 characters. `--by` defaults to `$USER`, else
  `operator`; Cloud passes the staff member's email.
- `pause` while paused replaces `until` and the reason; the pause keeps its start (`since`).
- `resume` ends the pause now. Resuming a scheduler that is not paused is not an error (exit 0, prints "the
  scheduler was not paused").
- `status --json` prints one line:

  ```json
  {"paused": true, "until": "2026-11-21T00:00:00Z", "reason": "menunggak", "since": "2026-11-19T03:12:00Z", "by": "staf@…"}
  ```

  - `until`: RFC 3339 (UTC) while paused, else `null`.
  - `reason`: the current pause's reason, else `""`.
  - `since`: when the current pause started; when not paused, when the last pause ended (resumed, or its `until`
    passed); `null` when the scheduler was never paused.
  - `by`: who set the current pause, or who ended the last one (`system` when it lapsed); absent when never paused.
  `pause` and `resume` with `--json` print the same shape after the change.
- Exit 0 on success, 2 on a usage error, 1 on any other error (database unreachable, table missing).

### 2. Storage

Single-row table created by `rangka migrate` (system SQL, `create table if not exists`):

```sql
rk_scheduler_state (
  id int primary key default 1 check (id = 1),
  paused boolean, paused_until timestamptz, reason text,
  paused_at timestamptz, paused_by text,      -- the current or last pause
  resumed_at timestamptz, resumed_by text     -- when and by whom the last pause ended
)
```

The pause is installation-wide: one kernel database serves one business on Rangka Cloud (0028), and on a host with
several orgs (rangka-os#263) it pauses all of them. It has no RLS; only the scheduler and the CLI read it.

### 3. Behaviour

- **Every tick reads the state** (no cache beyond the tick): the `rangka serve` loop (every 5 s) and
  `POST /_rangka/tick` (0040 §3) both go through `Scheduler.Tick`.
- **While paused**, every cron flow slot and connector poll slot that comes due is skipped and logged
  (`scheduler: paused: skipped <org>: <flow> due <time>`). Skipped slots are **never caught up**: not when the pause
  ends, and not by an instance that starts afterwards with `scheduler.catch_up_s` (request-only) or reloads
  metadata. The kernel remembers the last pause window `[paused_at, end)` and skips slots due inside it.
- **Jobs already in the queue keep running**: workers under `rangka serve` and the queue part of `/_rangka/tick`
  are not paused. A paused tick answers `"paused": true` next to its usual report.
- **When `until` passes**, the next tick resumes: the row is marked ended (`resumed_at = paused_until`,
  `resumed_by = 'system'`), once, even with several instances. Slots from `until` on run as usual.
- **State unreadable** (database error): the tick enqueues nothing and leaves the slots due, so they run on the next
  tick that can read it. A database without the table (a new binary before `rangka migrate`) reads as never paused,
  logged once.
- `GET /system/status` (Console) shows the state under `scheduler.pause`, same shape as `status --json`.
- Not paused: the event-log and notice chores of a tick, realtime, and requests. Recurring work that is not a
  `schedule:` flow or a connector poll does not exist in the scheduler today.

### 4. Audit

Each change writes an `rk_audit` row with `org_id = ''` (installation-wide), target `scheduler`:

| kind | actor | data |
| --- | --- | --- |
| `scheduler.paused` | `--by` | `until`, `reason`, `previous_until` when it moved an existing pause |
| `scheduler.resumed` | `--by`, or `system` when `until` passed | `paused_at`, `until`, `reason`, `lapsed` |

## Consequences

- rangka-cloud replaces "delete the EventBridge tick" with a one-off task running `rangka scheduler pause --until …
  --reason … --by <staff>` (and `resume`), and reads `status --json` for Kendali. The EventBridge tick stays in place
  for request-only kernels, so queued jobs keep running.
- A pause cannot be open-ended; a longer pause has to be renewed before it lapses, which keeps a forgotten pause from
  silently stopping a business's schedules for good.
- A business whose kernel runs an older release than the CLI that paused it ignores the pause. Cloud must only offer
  the pause for kernels from the release that ships this contract.
