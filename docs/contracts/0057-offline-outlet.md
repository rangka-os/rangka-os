# 0057 Offline outlet: paired PWA devices and the kernel side of edge-sync

Status: proposed (14 Nov 2026) · Owner of the change: rangka-os (kernel, web app) · Affects: rangka-apps
(`modules/pos`), rangka-edge (later, same protocol) · Amends 0006, 0030 §7 and §8, 0048 §5 · Builds on 0003, 0009,
0025, 0027, 0056

## Context
- An outlet must keep selling when the internet drops: take cash, print the receipt, open the drawer, switch cashier,
  close the shift. When the connection is back every transaction reaches the kernel once, on its own or from a
  button, and the cashier can see that it did. (Dede, 14 Nov 2026.)
- Targets: PC and Android tablets and phones now; iPhone and iPad later. The app is the installed PWA, not a native
  app.
- What exists (rangka-os `main` 0f78f9c, rangka-apps `main` 0850f30):
  - The web app is an installable PWA (`pwa.js`, `sw.js`: network first, app shell cached, `/api/` never cached).
  - Work screens with `offline: queue` (the cashier `kasir.rk` has it) keep unsent presses in IndexedDB
    (`outbox.js`), scoped to org and user, replayed in order every 15 s while the tab is open, each with
    Idempotency-Keys (0030 §7). Lost when the browser's data is cleared; another cashier's queue waits.
  - The cashier sale is `pos/pos_sale`: submit = paid; the kernel names it at submit
    (`STRK-{outlet.code}-{YY}{MM}-{#####}`), posts the stock moves and adds to the shift's totals under the read lock
    (0025). A shift is `pos_shift`, named at create (`SESI-…`); its close is `pos_shift_close` (blind, 0027).
  - Kernel: nothing of 0006 (no `rk_edge_*`, no `/edge/*`) and nothing of 0048 §5 (no `rk_devices`, pairing or PIN).
  - rangka-edge: queue, push client and a stub kernel; no device drivers.
  - Barcode scanners already work (they type, `scan.js`). Receipt printers and drawers do not: a browser can only
    reach them through Web Bluetooth, WebUSB or Web Serial, which Chrome and Edge have on Android and desktop and
    Safari does not.

## Decision

### 1. Scope
- **Now:** the PWA on a **paired device** (§2) in Chrome or Edge, on Android and on Windows, macOS or Linux, is a full
  offline cashier. No native app, no extra box.
- **Later, not here:** iPhone/iPad (an outlet hub per 0006, or rangka-edge on a PC as print server); rangka-edge
  device drivers. They use the same protocol (§4), so nothing here is thrown away.
- Money and stock never depend on an LLM. Everything below is deterministic.

### 2. Paired devices (0048 §5, adopted with these changes)
- One table `rk_devices` for browsers and rangka-edge: `id` (`dev_` + 20 hex), `org_id`, `outlet`, `code`, `name`,
  `kind` (`browser` | `edge`), `token_hash`, `created_by`, `created_at`, `last_seen_at`, `revoked_at`. 0006 §2's
  `rk_edge_devices` is this table.
- **Device code.** Each device gets a short code, unique within its outlet, assigned at pairing: `K1`, `K2`, … in
  order, never reused after revoke. It goes into document numbers (§3).
- **Pairing** as 0048 §5 (`#/pasang`, 8-character code, 15 minutes, single use). A browser gets the `rk_device` cookie;
  rangka-edge gets a bearer token (0006 §2). Same table, same revoke.
- **Cashier PIN and unlock** as 0048 §5, with one change for offline: the device keeps the PIN verifier of every
  person who unlocked **online on this device in the last 14 days** (not only the current one), so cashiers can
  switch while offline among people who have worked on this device recently. Someone who never unlocked here online
  cannot start offline. A verifier is dropped on lock-out, on revoke of the device, and when the person is removed
  from the outlet (seen at the next sync).

### 3. Document numbers made on the device
- A document created on a paired device carries a name the device makes, so a printed receipt is final online or
  offline:
  - receipt: `STRK-{outlet.code}-{device.code}-{YY}{MM}-{#####}`, e.g. `STRK-BTR-K1-2611-00042`
  - shift: `SESI-{outlet.code}-{device.code}-{YY}{MM}-{###}`
  - the counter resets each month and runs per device. (Dede chose this over temporary offline numbers.)
- New number scheme token for 0009: `{device.code}`, and a model key `naming_on: device` (schema 0.5) meaning "the
  name comes with the write from a paired device; the kernel checks it instead of making it". The kernel refuses a
  name that does not match the scheme for that device and outlet (`422 device_name_invalid`) or already exists
  (`409 duplicate_name`, unless it is the same idempotency key, then `duplicate`). A gap in a device's sequence is
  not an error; it is shown to head office (exception `device_number_gap`).
- A document of such a model created **without** a device (head office in the browser) keeps the kernel's own
  numbering (0009) under the outlet's normal series, e.g. `STRK-BTR-2611-00007`.
- rangka-apps `pos_sale` and `pos_shift` move to `naming_on: device` with the schemes above.

### 4. One protocol for the browser and rangka-edge (kernel side of 0006 §3–§6)
- The browser's queue sends **events**, not REST replays: `POST /api/v1/edge/events` (0006 §3), authenticated by the
  device cookie (browser) or token (Edge). A REST write from a paired device while online also goes through events,
  so there is one path.
- **Event kinds** (amends 0006 §3):
  - `screen.write` `{screen, model, op: create | submit | cancel | action, name?, body, actor, occurred_at}` (0030 §8): the
    kernel replays it as the `actor`'s write under the device, through the same engine path as the REST API, so module
    code never sees the difference. `sale.recorded` and `sale.voided` are dropped from v1 (never built anywhere); the
    cashier's sale is a `screen.write` of `pos_sale`.
  - `shift.opened`, `shift.closed`: `screen.write` of `pos_shift` / `pos_shift_close` (§6), named for clarity in the
    status list only; same handling.
  - `print.done`, `print.failed`, `device.read` as 0006.
- **`screen.write` payloads by op** (each event carries `key`, `kind`, `seq`, `occurred_at`, `actor`):
  - `create` `{screen, model, body, name?, submit?}`: `submit: true` creates and submits in one unit of work (the paid
    sale); `name` is the device name, taken at submit (at create for a model that is not submittable).
  - `submit` `{model, name, draft, body?}`: `draft` is the key of an earlier `create` event of the same device (its
    stored `ref`) or a draft name the device got online; anything else is `rejected` / `payload_invalid`.
  - `cancel` `{model, name}`: `name` is the submitted document's (device) name.
  - `action` `{model, name, action, args?}`.
  - The answer per event is `{key, status, code?, message?, ref?, record?}`; `record` is the record as REST returns
    it. `ref` is the document name (amends 0006's `model/name`). Rejected outcomes are stored with their code and
    message, so a replay answers the same; a transient kernel failure (lost race, database unreachable, timeout)
    answers the whole request `503` and the device resends the batch (done events come back `duplicate`); any other
    kernel fault is that event's `rejected` / `internal_error`, so one event never blocks the queue.
- `actor` is a member of the device's outlet; the kernel refuses an event whose actor is not (`rejected`,
  `actor_not_allowed`). The actor's roles and row scope apply as if they pressed the button online.
- Idempotency, statuses (`accepted` / `duplicate` / `rejected`), order by `(device, seq)`, backoff, and 400-day key
  storage (`rk_edge_events`) as 0006 §3.
- `occurred_at` is when the button was pressed (replaces 0030 §7's `_occurred_at`); the kernel stores it in the audit
  entries and uses it for business dates. The 30-days-back limit of 0030 §7 holds; beyond it `rejected`
  (`occurred_at_too_old`) and head office decides.

### 5. Durable queue on the device (amends 0030 §7)
- The outbox belongs to the **device**, not to a user: one queue per paired device in IndexedDB, each event carrying
  its `actor`. A cashier switch, a closed tab or a reboot keeps it; another cashier's events are sent by the device
  like any other.
- The app asks for persistent storage (`navigator.storage.persist()`) at pairing and shows a warning when the browser
  refuses ("Penyimpanan belum permanen: jangan hapus data browser").
- The service worker sends the queue too (Background Sync where the browser has it), so it drains after the tab is
  closed. Every 15 s while open, at reconnect, and from "Kirim sekarang".
- Unpaired browsers keep 0030 §7 as it is (per-user queue, REST replay).

### 6. Shift open and close offline
- Opening a shift offline: `pos_shift` with a device name (§3) and the opening float; the sales link it by that name.
- Closing offline: the cashier counts the drawer; `pos_shift_close` is queued like a sale. Because events of a device
  arrive in `seq` order, every sale of that shift reaches the kernel before its close, so the kernel's figures are
  complete when it handles the close. The close stays blind (0027): the device shows the cashier nothing it would
  not show online.
- **A shift belongs to one cashier on one device** (Dede, 14 Nov 2026). When another cashier unlocks the device, they
  open their own shift with their own float; the drawer is counted per person, as `own_shift` already requires. A
  device may hold several open shifts (one per cashier); the counter shows the unlocked cashier's own. A sale on
  another device cannot link a shift (`rejected`, `shift_other_device`).
- **Voids offline** (author's call, open to correction): cancelling a receipt on the same day while its shift is
  open works offline, as `screen.write` op `cancel` referencing the sale's device name; the receipt prints "BATAL".
- A close the kernel refuses (shift already closed by head office) is `rejected` and listed for head office; the
  counted cash is kept in the event.

### 7. Reference data on the device (kernel side of 0006 §4)
- `GET /api/v1/edge/config` and `GET /api/v1/edge/data/<set>` (0006 §4) for browsers and Edge.
- Data sets = the reference data of the screens bound to the outlet's roles (0030 §8): catalogue items, prices,
  options and option groups (0048 §1), stock available per outlet (0039), tables (0048), tax; plus `staff`: members
  of the outlet with name and role only (no e-mail, no PIN material; verifiers stay per §2).
- Kept in IndexedDB per device, swapped only when a set is complete. Replaces the work screens' "data terakhir"
  cache on paired devices; the screen shows the time of the last complete pull.
- Read-only at the outlet; the kernel wins (0006 §5.3).

### 8. Printing and the drawer from the browser
- The web app gets a printer layer with three transports: **Web Bluetooth** (BLE printers), **Web Serial**
  (Bluetooth SPP/RFCOMM printers and USB-serial, where the browser offers it), **WebUSB** (USB printers the OS
  lets the browser claim). Output is ESC/POS from the receipt print format (0008); the drawer opens with the printer's
  kick command.
- The printer is chosen once per device (Pengaturan → Perangkat → Printer) and remembered; printing works offline.
- A receipt is printed from the local sale, with its device number (§3), before the event is sent.
- **Spike first:** before building, test the pilot outlets' actual printer models on an Android tablet and a Windows
  PC through each transport, and keep only the transports that work. A model no transport reaches is listed as
  "pakai rangka-edge" (later).

### 9. What the cashier sees (the sync pill of 0048 §7)
- Top bar: "Online · semua terkirim", "Offline sejak 12.31 · 8 antre", "Mengirim 3 dari 8…", or "2 ditolak".
- Tapping it opens the queue: each event with time, cashier, what it is (Struk STRK-BTR-K1-2611-00042, Rp 45.000),
  state (antre, terkirim, ditolak with the kernel's reason), "Kirim sekarang", and the time of the last complete
  data pull.
- Sending starts on its own at reconnect; the pill turns to "semua terkirim" only when the kernel answered
  `accepted`/`duplicate` for every event.

### 10. Details settled
- **Totals on the device are binding.** The device computes a document's computed fields (prices, options,
  discounts, PPN, totals, change) with the model's own computes over the pulled data sets, using the web app's CEL
  evaluator (`engine.js`, extended to the dialect `pos_sale` uses; same results as the kernel's on a shared fixture
  set, checked in CI). For a `screen.write` from a paired device the kernel stores the computed money fields the
  device sent, recomputes in shadow, and records `edge_price_drift` when they differ. (Author's call.)
- **Rules on replay.** For device writes `today()` and `now()` evaluate at `occurred_at`. A save, submit or cancel rule
  that would refuse a document already paid at the outlet (e.g. `shift_open` after head office closed the shift)
  becomes an exception for head office, and the write is accepted. Permissions, the actor check (§4) and
  `device_name_invalid` still reject. (Author's call.)
- **Approvals offline are blocked** (Dede, 14 Nov 2026). An action that needs an approval online (e.g. a discount
  above `big_discount`'s threshold) is not offered while offline; the screen says "Perlu persetujuan, tunggu online".
- **Actor trust.** The kernel trusts the actor a paired device names (the device is paired by an owner and revocable,
  and the PIN gate is on the device); every write is audited "via device K1". (Author's call.)
- **Exceptions.** A kernel table `rk_exceptions` (`org`, `outlet`, `device`, `kind`, `ref`, `detail`, `created_at`,
  `resolved_by`, `resolved_at`) with `GET /api/v1/exceptions` for head office and owners, and a list under the sync
  panel for them. Kinds here: `edge_price_drift`, `negative_stock`, `device_number_gap`, `rule_overridden`. (Author's
  call.)
- **Online writes from a paired device** go through `/edge/events` synchronously: the answer carries the per-event
  status and the resulting record, so the cashier still sees a refusal and the kernel's numbers at once. (Author's
  call.)
- **When the number is taken.** The basket stays local on the device until payment; the device name is taken at
  payment (submit). An abandoned basket uses no number. (Author's call.)

## Conflict rules (0006 §5, restated for this path)
1. Sales win: the kernel accepts the prices and discounts the device charged; a difference with the current price
   list is an exception (`edge_price_drift`), not a correction.
2. Stock follows sales: stock may go negative; exception for head office. Never refuse a sale for stock.
3. Kernel wins for reference data.
4. A void references the sale by its device name; a void that arrives before its sale is held (order by `seq` makes
   this rare).
5. Numbers: §3. No LLM decides any of it.

## Not in this contract
- iPhone/iPad, rangka-edge drivers and the outlet hub (later; same protocol).
- QRIS and card payments offline (they need the network; the screen says so).
- Editing reference data at the outlet.

## Work, in order
| # | What | Repo |
| --- | --- | --- |
| 0 | Printer spike: pilot printer models × (Web Bluetooth, Web Serial, WebUSB) on Android and Windows | rangka-os (throwaway) |
| 1 | `rk_devices`, device codes, pairing, PIN and unlock incl. offline verifiers (§2) | rangka-os |
| 2 | `{device.code}` token, `naming_on: device`, name checks, gap exception; both validators and schema (§3) | rangka-os |
| 3 | `POST /edge/events` with `screen.write`, dedup, actor checks, `occurred_at`; `rk_edge_events` (§4) | rangka-os |
| 4 | `GET /edge/config`, `/edge/data/<set>` incl. `staff` (§7) | rangka-os |
| 4b | Device-side computes: extend `engine.js` to the `pos_sale` dialect, shared fixtures checked against the kernel (§10) | rangka-os |
| 4c | Replay rules for device writes, `rk_exceptions` and its list (§10) | rangka-os |
| 5 | Device outbox, persistent storage, service worker sending, events instead of REST (§5) | rangka-os web app |
| 6 | Offline shift open and close (§6) | rangka-os, rangka-apps |
| 7 | Printer layer and Pengaturan → Perangkat (§8, after the spike) | rangka-os web app |
| 8 | Sync pill and queue panel (§9) | rangka-os web app |
| 9 | `pos_sale`, `pos_shift` to `naming_on: device` | rangka-apps |

## Consequences
- A paired PC or Android device sells, prints, switches cashier and closes the shift without internet, and nothing it
  recorded is lost or doubled.
- Receipts carry their final number from the moment they are printed.
- rangka-edge and a future iOS hub plug into the same events and data sets.
