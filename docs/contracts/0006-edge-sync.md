# 0006: Edge sync (`edge-sync/v1`)

Status: proposed (4 Nov 2026) · Owner: rangka-os (kernel) and rangka-edge · Affects: rangka-edge, Console (pairing),
rangka-connectors (device connectors, later) · Stream: S13 of the pilot program · Builds on 0003

## Context
- 0003 makes `rangka-edge` the agent at the outlet. It runs device connectors, keeps working offline and syncs
  with the kernel. It leaves the protocol to this file.
- The outlet must keep selling and printing without internet. Anything it records offline (a sale, a print, a
  scanner read) has to reach the kernel exactly once, however many times the link drops mid-request.
- Money and stock are never decided by an LLM. The protocol carries facts recorded at the outlet, and the rules
  that settle conflicts are written here, deterministic and testable.

## Decision

### 1. Shape
- HTTPS + JSON under `/api/v1/edge/`, same error body as the rest of `/api/v1`: `{error: {code, message}}`.
- Every request carries `Rangka-Edge-Sync: v1`. A kernel that doesn't speak the version answers
  `400 unsupported_sync_version` with `supported: ["v1"]` in the error body. The kernel keeps the previous
  version for at least one release (0003).
- After registration every request carries `Authorization: Bearer <device token>`.

### 2. Device identity and registration
- An owner or outlet manager creates a **pairing code** for one outlet in the Console: 8 characters from
  `ABCDEFGHJKMNPQRSTUVWXYZ23456789`, single use, valid 15 minutes.
- `POST /edge/register`
  ```json
  {"pairing_code": "K7Q2M9XD", "device": {"name": "Kasir 1", "platform": "windows/amd64", "version": "0.1.0"}}
  ```
  → `201`
  ```json
  {"device_id": "dev_8f3c…", "token": "rke_…", "site": "kopi-senja", "org": "pt-senja", "outlet": "OUT-02",
   "sync": {"push_max_items": 100, "pull_interval_s": 60}}
  ```
  Errors: `400 pairing_code_invalid` (unknown, used or expired; one message for all three), `429 too_many_attempts`.
- `device_id` is `dev_` + 20 random hex. The token is shown once, stored hashed by the kernel (`rk_edge_devices`)
  and revoked from the Console. A revoked or unknown token answers `401 device_revoked`.
- A device token acts as the actor `device:<device_id>`, fixed to one org and one outlet (0010 row scope). It can
  push events and pull config and data. It can never approve, apply or revert a change set, decide an approval or
  read another outlet (`403 permission_denied`).
- Cashiers unlock the Edge with a local PIN (0003 §3). PINs never leave the device and are not part of this
  protocol; the event records which cashier with `actor` (below).

### 3. Outbound: events with idempotency keys
- The Edge writes every fact to its local durable queue **before** acting on it or showing it as done, then
  pushes in order.
- `POST /edge/events`
  ```json
  {"events": [
    {"key": "dev_8f3c…:000000000042", "kind": "sale.recorded", "seq": 42,
     "occurred_at": "2026-11-04T09:15:02+07:00", "actor": "cashier:siti",
     "payload": {"…": "…"}}
  ]}
  ```
  → `200`
  ```json
  {"results": [{"key": "dev_8f3c…:000000000042", "status": "accepted", "ref": "outlet_sales/OS-02-0042"}]}
  ```
- **Idempotency key.** Required, at most 200 characters, stable for the life of the event: the Edge sets it once
  at enqueue and never changes it. The default form is `<device_id>:<seq>` with `seq` zero-padded to 12 digits;
  an event that already has a natural key (a print job id) may use it, prefixed by the device id.
  The kernel stores keys per site (`rk_edge_events`, unique `(site, key)`) for at least 400 days.
- **Statuses**, one per event, in request order:
  | status | meaning | Edge does |
  | --- | --- | --- |
  | `accepted` | stored now | ack, drop from queue |
  | `duplicate` | key already stored; the stored result is returned (`ref`) | ack, drop from queue |
  | `rejected` | permanently invalid: `code` + `message` (`unknown_kind`, `payload_invalid`, `key_conflict`) | move to dead-letter, show in status, never retry by itself |
  A replay of a key with a different payload is `rejected` / `key_conflict`; the stored event stays as it was.
- Only `accepted` and `duplicate` ack an event. A network error, a timeout, `5xx` or `429` means "unknown": the
  Edge keeps the batch and retries the same events with the same keys, with exponential backoff (1 s doubling to
  5 min, ± 20 % jitter, `Retry-After` honoured). `401 device_revoked` stops syncing; the queue is kept.
- At most `push_max_items` events per request. Events are pushed in `seq` order; the kernel processes a batch in
  order and answers every item, so one rejected event never blocks the rest.
- `occurred_at` is the device clock and is what reports use; the kernel also stores `received_at`. The kernel
  never reorders a device's events by time; `(device_id, seq)` is the order.
- **Kinds** for v1: `sale.recorded`, `sale.voided`, `stock.counted`, `print.done`, `print.failed`,
  `device.read` (raw trigger from a device connector, 0003 §1). The kernel maps kinds to records through the
  installed modules (e.g. `outlet_sales` in `rangka-apps`), deterministically. Unknown kinds are `rejected`.

### 4. Inbound: config and reference data
- `GET /edge/config` with `If-None-Match: "<version>"` → `304`, or `200`
  ```json
  {"version": "cfg_…", "outlet": {"code": "OUT-02", "name": "…", "timezone": "Asia/Jakarta"},
   "connectors": [{"name": "escpos_printer", "version": "0.1.0", "rk": "…connector.rk text…"}],
   "settings": {"print": "escpos", "receipt_format": "receipt_80mm"},
   "data_sets": ["item", "price_list", "tax"]}
  ```
  The connectors are the site's installed `via: device` connectors (0003 §2).
- `GET /edge/data/<set>?cursor=<c>` → `{items: [...], next_cursor, done}`. A missing cursor starts a full copy;
  `items` carry `{name, deleted, row}`. The Edge swaps its local copy of a set only once `done` is true, so a
  half-pulled set is never used.
- The Edge pulls every `pull_interval_s` when online and after reconnecting, before pushing.
- Reference data is **read-only** at the outlet. The Edge never sends master-data edits; prices, items and
  tax change only in the kernel through change sets.

### 5. Conflict rules
1. **Sales win.** A sale recorded at the outlet is a fact. The kernel accepts it with the prices, discounts and
   tax the outlet charged, even if the price list changed while the outlet was offline. A price difference is
   flagged for head office (exception `edge_price_drift`), not corrected.
2. **Stock follows sales.** A sale that pushes stock below zero is still accepted; stock goes negative and an
   exception appears for head office (pilot Review Focus). A sale is never rejected for missing stock.
3. **Kernel wins for reference data.** Items, prices, tax, outlet settings and connectors come only from the
   kernel; the newest pulled version replaces the local one.
4. **Voids reference the sale's key.** `sale.voided` carries `sale_key`; a void whose sale hasn't arrived yet is
   held by the kernel until it does (order by `seq` makes this rare). Submitted documents still change only
   through cancel + amend in the kernel.
5. **Numbers.** The outlet numbers receipts locally from a device series (`{OUTLET}-{DEVICE}-{######}`); the
   kernel's own document numbers (0009) are assigned when the event is accepted and returned in `ref`. Neither
   is ever decided by an LLM.
6. **No LLM in the path.** Rangka AI and Omilo hold no device tokens and cannot push events; event payloads are
   never rewritten by an LLM on either side.

### 6. Heartbeat and status
- `POST /edge/heartbeat {queue: {pending, dead, oldest_pending_at}, last_push_at, last_pull_at, version}` →
  `204`. Feeds `/system/status.edge[]` and the Console's device list (online when seen in the last 5 minutes).

## Kernel work (future; not built in S13)
Nothing in this file exists in the kernel yet. In order:
1. Tables `rk_edge_devices` (device, org, outlet, token hash, revoked_at, last_seen_at), `rk_edge_pairing`
   (code hash, outlet, expires_at, used_at) and `rk_edge_events` (site, key, device, seq, kind, payload sha256,
   result, ref, occurred_at, received_at).
2. Pairing codes in the Console; `POST /edge/register`; device tokens and the `device:` actor.
3. `POST /edge/events` with dedup by key and the `rejected` codes, mapping kinds to module hooks.
4. `GET /edge/config`, `GET /edge/data/<set>`.
5. `POST /edge/heartbeat` and `/system/status.edge`.
6. Shared Go types for this protocol move to `kernel/pkg/edgesync` (0003); until a tagged `rangka-os` release has
   them, `rangka-edge` keeps its own copy and an in-repo kernel stub that follows this file.

## Edge side (S13, `rangka-edge`)
Durable local queue with idempotency keys (replaying a key is a no-op, survives restart), a runner that pushes with
the backoff above, an HTTP client for §2–3, an in-repo kernel stub, and the CLI `rangka-edge enqueue | status |
flush | run | register`. Device connectors and POS are later work (0003 build order).

## Consequences
- An outlet can be offline for days; nothing it recorded is lost or doubled, and head office sees drift and
  negative stock as exceptions instead of silent fixes.
- The kernel keeps every event key for 400 days; that is the window in which an outlet can come back and replay.
