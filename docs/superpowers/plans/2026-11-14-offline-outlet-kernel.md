# Offline Outlet, Plan 1: Kernel Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The kernel side of contract 0058:
- paired devices with codes, pairing and PIN unlock;
- document names made on the device;
- `POST /api/v1/edge/events`, which replays `screen.write` under the device's rules;
- head-office exceptions;
- `GET /api/v1/edge/config` and `/edge/data/<set>`.

**Architecture:**
- A device is a new kind of principal (`Via: "device"`), resolved from the `rk_device` cookie (browser) or a `rke_` bearer token (Edge).
- Events go through one handler. It deduplicates by `(org, key)` in `rk_edge_events` and replays each `screen.write` through the same engine calls as REST (`Create`, `Submit`, `Cancel`, `RunAction`). The context carries a "device write" marker, which the engine uses for:
  - names made on the device;
  - the clock at `occurred_at`;
  - computed money fields kept as the device sent them, with a shadow drift check;
  - rule refusals turned into exceptions.

**Tech Stack:** Go 1.24 kernel (stdlib `net/http`, in-tree `pg`, in-tree CEL), Python reference validator, JSON Schema 0.5.

**Spec:** `docs/contracts/0058-offline-outlet.md` (§2, §3, §4, §7, §10, conflict rules). Plan 2 (web app offline) and
Plan 3 (printing, after the spike) follow and consume this plan's routes.

## Global Constraints

- Contract 0058 is accepted before Task 2 lands. Branch `contract/offline-outlet` (worktree `.worktrees/contract-offline-outlet`).
- Device id: `dev_` + 20 hex. Device code: `K1`, `K2`, … per outlet, in pairing order, never reused after revoke.
- Pairing code: 8 characters from `ABCDEFGHJKMNPQRSTUVWXYZ23456789`, single use, valid 15 minutes. Errors: `400 pairing_code_invalid` (one message for unknown, used and expired), `429 too_many_attempts`.
- Browser device cookie: `rk_device`, HttpOnly, SameSite=Strict, Secure as `rk_session`, 400 days. Edge token prefix `rke_`. Only hashes are stored.
- PIN: 4 to 6 digits; all-same digits and straight runs are refused (`422 pin_weak`). 5 wrong tries lock that member's PIN for 15 minutes (`423 pin_locked`). 10 in a row clear it. 20 wrong tries on one device in 15 minutes give `429 too_many_attempts`. PIN sessions last at most 12 hours. Owners and `desk: true` roles get `403 pin_not_allowed`.
- Schema key `naming_on: device`; token `{device.code}`. Errors: `422 device_name_invalid`, `409 duplicate_name`. Exception kind `device_number_gap`.
- Events: header `Rangka-Edge-Sync: v1` (otherwise `400 unsupported_sync_version` with `supported: ["v1"]`). `push_max_items` = 100. Key at most 200 chars, unique per org, kept 400 days. Statuses `accepted` | `duplicate` | `rejected`. Rejected codes: `unknown_kind`, `payload_invalid`, `key_conflict`, `actor_not_allowed`, `occurred_at_too_old`, `shift_other_device`, plus engine refusals that stay refusals (permission, `device_name_invalid`).
- v1 kinds: `screen.write` (ops `create | submit | cancel | action`), `print.done`, `print.failed`, `device.read`. `sale.recorded` and `sale.voided` are not accepted.
- `occurred_at`: at most 5 minutes ahead and 30 days back (`occurred_at_too_old`).
- Exception kinds: `edge_price_drift`, `negative_stock`, `device_number_gap`, `rule_overridden`. Table `rk_exceptions(org_id, outlet, device, kind, ref, detail jsonb, created_at, resolved_by, resolved_at)`.
- Data set `staff` carries name and role only: no e-mail, no PIN material.
- UI and error text: Bahasa Indonesia, sentence case, "kamu". Code and comments in English, citing contract 0058.
- `make vet test compare-python` green; run `make sync-schemas` after editing `schema/0.5`.

## Review Focus

- Two devices of one outlet paired at the same moment must get distinct codes (`K1`, `K2`), never the same. Test in Task 1.
- An event replayed with the same key but a different payload: `rejected` / `key_conflict`, and the stored record is unchanged. Test in Task 5.
- A batch of 3 events where the 2nd is rejected: the 1st and 3rd are still `accepted`, and the results come back in request order. Test in Task 5.
- A revoked device's cookie or token: every route answers `401 device_revoked`, and its PIN sessions end. Test in Task 2.
- A sale sent with `occurred_at` 23:50 the day before, arriving the next morning: rules see the earlier date, and the business date stays the earlier day. Test in Task 6.

---

### Task 1: Devices, codes and pairing

**Files:**
- Create: `kernel/internal/auth/devices.go`, `kernel/internal/auth/devices_test.go`, `kernel/internal/api/devices_api.go`, `kernel/internal/server/devices_api_test.go`
- Modify: `kernel/internal/store/system.sql` (tables `rk_devices`, `rk_device_codes`), `kernel/internal/auth/service.go` (`resolve`: device branch after the `rk_session` branch; `Principal.Device *Device`; `Via "device"`), `kernel/internal/api/server.go` (routes; `Actor()` copies the device)

**Interfaces:**
- Produces (package `auth`):
  - `type Device struct{ ID, Org, Outlet, Code, Name, Kind string; RevokedAt *time.Time }`
  - `func (s *Service) CreatePairingCode(ctx, org, outlet, createdBy string) (code string, expires time.Time, err error)`
  - `func (s *Service) PairDevice(ctx, code, name, kind string) (dev *Device, secret string, err error)`. `kind` is `browser` or `edge`. The secret is the cookie value or the `rke_` token, shown once.
  - `func (s *Service) RevokeDevice(ctx, org, id string) error`
  - `func (s *Service) DeviceFromSecret(ctx, secret string) (*Device, error)`. Returns `ErrDeviceRevoked` for a revoked device.
- Produces (package `engine`): `Actor.Device *DeviceRef` with `type DeviceRef struct{ ID, Code, Outlet string }`. The CEL `device` object becomes `{id, code, outlet}`.
- Routes:
  - `POST /api/v1/devices/codes {outlet}` → `201 {code, expires_at}`. Owner, or a role with the new permission `devices: pair` (author's call: owner and `head_office`).
  - `POST /auth/device/pair {code, name}` → `201 {device_id, code, outlet}` plus the `rk_device` cookie.
  - `POST /api/v1/edge/register` as in 0006 §2: answers the `rke_` token and `{device_id, code, outlet, org, sync}`.
  - `GET /api/v1/devices?outlet=`
  - `DELETE /api/v1/devices/{id}`

- [ ] **Step 1: Write failing tests.**
  - `TestPairBrowserDevice`: create a code for `OUT-01`, pair → 201, cookie `rk_device` set HttpOnly, code `K1`.
  - `TestDeviceCodesPerOutlet`: second device on `OUT-01` gets `K2`; first device on `OUT-02` gets `K1`; after revoking `K2` the next one is `K3`.
  - `TestDeviceCodesConcurrent`: two pairings in parallel give distinct codes.
  - `TestPairingCodeSingleUse`: second use, expired code (clock +16 min) and unknown code all answer `400 pairing_code_invalid` with the identical message.
  - `TestPairingRateLimit`: the 11th wrong code within 15 minutes from one address → `429 too_many_attempts` (author's call: 10 tries per 15 minutes per address).
  - `TestDeviceAloneReadsNothing`: with only the device cookie, `GET /api/v1/r/item` → `401`.
  - `TestEdgeRegisterToken`: `POST /api/v1/edge/register` returns `rke_…`; the token resolves to `Via "device"` with `Kind "edge"`.
- [ ] **Step 2:** `cd kernel && go test ./internal/server/ -run 'Pair|DeviceCodes|DeviceAlone|EdgeRegister'` → FAIL.
- [ ] **Step 3: Implement.**
  - Assign codes under `store.LockKey(ctx, tx, "devicecode:"+org+"/"+outlet)`: `max(code number)+1` over every device of the outlet, revoked ones included.
  - Store `sha256` of secrets only.
  - The device principal alone carries no user: `Human()` is false and record routes answer 401.
- [ ] **Step 4:** Same command → PASS; `make test` PASS.
- [ ] **Step 5: Commit** `kernel: paired devices, codes and pairing (contract 0058 §2)`.

### Task 2: Cashier PIN and unlock

**Files:**
- Create: `kernel/internal/auth/pin.go`, `kernel/internal/auth/pin_test.go`, `kernel/internal/server/pin_api_test.go`
- Modify: `kernel/internal/store/system.sql` (`rk_members.pin_hash`, `pin_fails`, `pin_locked_until`; table `rk_device_unlock_fails(device, at)`), `kernel/internal/auth/service.go` (`openSession` gains method `pin` and a device id on `rk_sessions`; a PIN session's outlets are narrowed to the device's outlet), `kernel/internal/api/server.go` (routes), `kernel/internal/meta` and `schema/0.5/role.json` (role key `desk: bool`, default false) and both validators

**Interfaces:**
- Consumes: `Device`, `DeviceFromSecret` (Task 1).
- Produces:
  - `PUT /auth/pin {pin}`: needs a session opened in the last 15 minutes.
  - `GET /auth/device/staff` → `[{id, name, role}]`: device cookie only. Lists members of the device's outlet who have a PIN and whose role is not `desk: true`.
  - `POST /auth/device/unlock {user, pin}` → session cookie (`rk_session`, method `pin`, at most 12 h).
  - `DELETE /api/v1/members/{user_id}/pin`: owner or `head_office`.
  - Errors: `pin_weak`, `pin_locked`, `pin_not_allowed`, `pin_not_set`, `too_many_attempts`, `device_revoked`.

- [ ] **Step 1: Failing tests.**
  - `TestPinWeakRefused` (`1234`, `0000`, `98765`, `12` → 422 `pin_weak`).
  - `TestUnlockOpensNarrowedSession` (member with outlets `[OUT-01, OUT-02]` unlocks on an `OUT-01` device → `/auth/me` outlets `[OUT-01]`).
  - `TestPinLockout` (5 wrong → `423 pin_locked` on every device; 10 in a row → `pin_not_set`).
  - `TestDeviceUnlockRateLimit` (20 wrong across members on one device → `429`).
  - `TestOwnerCannotUsePin` (`403 pin_not_allowed`).
  - `TestRevokedDeviceEndsPinSessions` (revoke → the PIN session's `/auth/me` is 401 and the cookie gets `401 device_revoked`).
  - `TestRoleChangeClearsPin` (Member API or Tim changes the role → `pin_not_set`).
- [ ] **Step 2:** `go test ./internal/server/ ./internal/auth/ -run 'Pin|Unlock|Revoked'` → FAIL.
- [ ] **Step 3: Implement.**
  - Hash PINs with the kernel's password hash.
  - Wrong PIN and unknown member give one shared message.
  - Audit every pair, revoke, unlock, failure and lockout, with no PIN and no hash in the entry.
  - Clearing a PIN on role, outlet or member change goes in the shared `writeMember`/`removeMember` path (contract 0056) and in `auth.SetOutlets`/`RemoveMember`.
- [ ] **Step 4:** PASS; `make test` PASS; `make compare-python` identical (role key `desk`).
- [ ] **Step 5: Commit** `kernel: cashier PIN and device unlock (contract 0058 §2)`.

### Task 3: Head-office exceptions

**Files:**
- Create: `kernel/internal/store/exceptions.go`, `kernel/internal/api/exceptions_api.go`, `kernel/internal/server/exceptions_api_test.go`
- Modify: `kernel/internal/store/system.sql` (`rk_exceptions`, index on `(org_id, resolved_at, created_at)`), `kernel/internal/api/server.go`

**Interfaces:**
- Produces:
  - `func store.RecordException(ctx, q pg.Querier, x Exception) error`, with `type Exception struct{ Org, Outlet, Device, Kind, Ref string; Detail map[string]any }`.
  - `GET /api/v1/exceptions?outlet=&kind=&open=1` → `{items: [{id, outlet, device, kind, ref, detail, created_at, resolved_by, resolved_at}]}`. Owner and `head_office` only. Other roles get `403`.
  - `POST /api/v1/exceptions/{id}/resolve` → `200`.
- [ ] **Step 1: Failing tests.**
  - `TestExceptionsListAndResolve` (record two, list `open=1` → 2, resolve one → 1, `resolved_by` is the caller's e-mail).
  - `TestExceptionsForbiddenToCashier`.
  - `TestExceptionsOrgScoped` (an exception in `pt_beta` never shows).
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** PASS, `make test`.
- [ ] **Step 5: Commit** `kernel: head-office exceptions (contract 0058 §10)`.

### Task 4: Names made on the device

**Files:**
- Modify: `kernel/internal/meta/types.go` (`Model.NamingOn string`), `schema/0.5/model.json` (`naming_on: enum [device]`) then `make sync-schemas`, `kernel/internal/meta/validate.go` and `tools/rangka_validate.py` (`naming_on: device` needs a string `naming` containing `{device.code}`; otherwise error `naming_on_device`), `kernel/internal/engine/numbering.go` (`{device.code}` token from `Actor.Device`), `kernel/internal/engine/lifecycle.go` (`Create`/`Submit` take the name from the device write; see below)
- Create: `kernel/internal/engine/devicename.go`, `kernel/internal/engine/devicename_test.go`, `kernel/testdata/device-apps/` (a copy of `testdata/screens/modules/kasir` whose `penjualan` and `sesi_kasir` have `naming_on: device` and schemes `STRK-{outlet.code}-{device.code}-{YY}{MM}-{#####}` / `SESI-{outlet.code}-{device.code}-{YY}{MM}-{###}`), `kernel/testdata/broken-naming-on.expected.txt` + fixture

**Interfaces:**
- Consumes: `Actor.Device` (Task 1), `store.RecordException` (Task 3).
- Produces:
  - `func engine.WithDeviceWrite(ctx, dw DeviceWrite) context.Context`, with `type DeviceWrite struct{ Name string; OccurredAt time.Time; Money map[string]any; Event string }`. Tasks 5 and 6 fill `OccurredAt` and `Money`.
  - `func (e *Engine) checkDeviceName(m *meta.Model, a Actor, name string, at time.Time) error`

Rule: for a model with `naming_on: device`, a write in a `DeviceWrite` context must bring `Name`. The name is used at the moment the model's name would be made: at create for non-submittables, at submit for submittables. It must match the scheme rendered with the actor's outlet code, the device code and the `YY`/`MM` of `OccurredAt`, with any number. Otherwise `422 device_name_invalid`. An existing name gives `409 duplicate_name`. When the number is greater than the device's last number + 1 for that period, the write is accepted and a `device_number_gap` exception is recorded with `{expected, got}`. The last number per (device, series, period) is kept in `rk_number_series` with the series key `device:<id>:<model>`. Without a `DeviceWrite`, the model keeps the kernel's own numbering with `{device.code}` dropped from the pattern (`STRK-BTR-2611-00007`).

- [ ] **Step 1: Failing tests** (engine, testdb).
  - `TestDeviceNameAccepted` (`STRK-OUT01-K1-2610-00001` at submit → record has that name).
  - `TestDeviceNameWrongDevice` (K2's name from K1 → `device_name_invalid`).
  - `TestDeviceNameWrongPeriod` (`2609` with `OccurredAt` in October → invalid).
  - `TestDeviceNameDuplicate` (`409 duplicate_name`).
  - `TestDeviceNameGap` (00001 then 00003 → accepted plus one `device_number_gap` with `expected 2, got 3`).
  - `TestNoDeviceKernelNumbering` (head office submit → `STRK-OUT01-2610-00001`).
  - Validator golden `broken-naming-on`.
- [ ] **Step 2:** `go test ./internal/engine/ -run DeviceName` and `go test ./internal/meta/ -run Broken` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** PASS; `make test`; `make compare-python` identical.
- [ ] **Step 5: Commit** `kernel: names made on the device (contract 0058 §3)`.

### Task 5: `POST /api/v1/edge/events`

**Files:**
- Create: `kernel/internal/api/edge_events.go`, `kernel/internal/store/edge_events.go`, `kernel/internal/server/edge_events_test.go`
- Modify: `kernel/internal/store/system.sql` (`rk_edge_events(org_id, key, device, seq, kind, payload_sha256, status, code, ref, occurred_at, received_at)`, unique `(org_id, key)`), `kernel/internal/api/server.go` (route, device principal allowed here), the existing prune job (also prunes `rk_edge_events` older than 400 days)

**Interfaces:**
- Consumes: `DeviceFromSecret`, PIN session (Task 2), `WithDeviceWrite`/`checkDeviceName` (Task 4), `engine.Create/Submit/Cancel/RunAction`.
- Produces:
  - Request: `POST /api/v1/edge/events {events: [{key, kind, seq, occurred_at, actor, payload}]}`. `payload` for `screen.write` is `{screen, model, op, name?, body}`.
  - Answer: `200 {results: [{key, status, code?, message?, ref?, record?}]}`, one result per event, in request order. `record` is the resulting record as REST would return it, so online writes are synchronous.
  - The caller is the device (cookie or `rke_` token). `actor` is a user id that must be a member of the device's outlet (`rejected` / `actor_not_allowed`).

Each event runs in its own unit of work, in `seq` order, under the `actor` as `engine.Actor` with `Device` set, its outlets narrowed to the device's outlet, and `WithDeviceWrite{Name, OccurredAt, Event: key}`.

- [ ] **Step 1: Failing tests** (with the `device-apps` fixture).
  - `TestEventsCreateSubmitSale` (`create` then `submit` with a device name → 2× `accepted`, `ref` is the name, `record` present).
  - `TestEventsDuplicate` (same key again → `duplicate`, same `ref`, one record).
  - `TestEventsKeyConflict` (same key, different payload → `rejected`/`key_conflict`, record unchanged).
  - `TestEventsBatchOrder` (3 events, 2nd invalid → accepted, rejected, accepted).
  - `TestEventsActorNotInOutlet` (`actor_not_allowed`).
  - `TestEventsUnknownKind` (`sale.recorded` → `rejected`/`unknown_kind`).
  - `TestEventsVersionHeader` (missing header → `400 unsupported_sync_version`, `supported: ["v1"]`).
  - `TestEventsTooOld` (`occurred_at` 31 days back → `occurred_at_too_old`).
  - `TestEventsCancelVoid` (`cancel` op on the sale's device name → `accepted`, the record is cancelled).
  - `TestEventsMaxItems` (101 events → `400 too_many_events`).
  - `TestEventsAuditViaDevice` (audit rows of the replayed write carry `{"via":"device","device":"K1","occurred_at":…}`).
- [ ] **Step 2:** `go test ./internal/server/ -run Events` → FAIL.
- [ ] **Step 3: Implement.**
  - Hash the canonical JSON of `{kind, payload}` for conflicts.
  - A refusal from the engine maps to `rejected` with its code and message.
  - `print.done`, `print.failed` and `device.read` are stored and acknowledged only (`accepted`). Their handling is later work.
- [ ] **Step 4:** PASS; `make test`.
- [ ] **Step 5: Commit** `kernel: edge events with screen.write (contract 0058 §4)`.

### Task 6: Replay rules for device writes

**Files:**
- Modify: `kernel/internal/engine/lifecycle.go` (`prepare`, `check`, `Submit`, `Cancel`), `kernel/internal/engine/engine.go` (the CEL clock reads `DeviceWrite.OccurredAt` when present), `kernel/internal/engine/devicename.go` (or a new `devicewrite.go`), `kernel/internal/api/edge_events.go` (passes `Money` from the body)
- Test: `kernel/internal/engine/devicewrite_test.go`, cases added to `kernel/internal/server/edge_events_test.go`

**Interfaces:**
- Consumes: `DeviceWrite` (Task 4), `store.RecordException` (Task 3).
- Produces: behaviour only.

In a `DeviceWrite` context:
1. **Clock.** `today()`, `now()` and the business date use `OccurredAt` (in the org's location).
2. **Money.** For every computed field of type `currency` (and child rows' currency computes), when the body carries a value, that value is stored. The kernel's own computed value is calculated alongside. If they differ, record `edge_price_drift` with `{field, device, kernel}` per field.
3. **Rules.** A `save`, `submit` or `cancel` rule that fails is turned into an accepted write plus a `rule_overridden` exception with `{rule, message}`. Exceptions to this: rule errors on a model without `naming_on: device` stay refusals, and permission errors, `device_name_invalid`, `duplicate_name` and `actor_not_allowed` always stay refusals.
4. **Approvals.** A write that would be held for approval is `rejected` with `approval_needed_offline`. The device never offers it, so this is a guard only.
5. **Shift of another device.** A write whose link field targets a record of a `naming_on: device` model that was
   created by a different device (its `rk_edge_events`/audit device differs; author's call: store `created_device`
   as a system column on such records at create) is refused with `shift_other_device`. Test:
   `TestDeviceWriteShiftOtherDevice` (sale from K2 on K1's shift → `rejected`/`shift_other_device`).
6. **Stock.** If a stock move from the write leaves a balance below zero, record `negative_stock` with `{item, outlet, balance}`. Find where stock refusal or warning happens for negative balances (`engine` stock posting, contract 0021). Device writes must never be refused for stock.

- [ ] **Step 1: Failing tests.**
  - `TestDeviceWriteClockAtOccurredAt` (rule `date == today()`, occurred 23:50 the day before, kernel clock next morning → accepted with the earlier date).
  - `TestDeviceWriteMoneyKept` (device total 45000, kernel computes 46000 → stored 45000, one `edge_price_drift`).
  - `TestDeviceWriteMoneySame` (no exception).
  - `TestDeviceWriteRuleOverridden` (shift closed by head office, then a sale on it → accepted plus `rule_overridden`).
  - `TestDeviceWritePermissionStillRejects`.
  - `TestDeviceWriteApprovalRejected`.
  - `TestDeviceWriteNegativeStock` (accepted plus `negative_stock`).
  - `TestRestWriteUnchanged` (the same body over REST still recomputes and still refuses on the rule).
- [ ] **Step 2:** `go test ./internal/engine/ -run DeviceWrite` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** PASS; `make test`.
- [ ] **Step 5: Commit** `kernel: replay rules for device writes (contract 0058 §10)`.

### Task 7: Config and data sets

**Files:**
- Create: `kernel/internal/api/edge_data.go`, `kernel/internal/server/edge_data_test.go`
- Modify: `kernel/internal/api/server.go`

**Interfaces:**
- Consumes: the device principal (Task 1).
- Produces:
  - `GET /api/v1/edge/config` with `If-None-Match` → `304`, or `200 {version, outlet: {code, name, timezone}, device: {id, code}, screens: [names], data_sets: [names], sync: {push_max_items: 100, pull_interval_s: 60}}`. `version` is a hash of the answer.
  - `GET /api/v1/edge/data/<set>?cursor=` → `{items: [{name, deleted, row}], next_cursor, done}`, 500 items per page. A missing cursor starts a full copy.

Data sets are the models named by the reference slots of the work screens whose roles include any role of the outlet's members: `catalogue.model`, its option models, `catalogue.available`'s model (rows of this outlet only), tables, the price model, `tax`. Add `staff` = members of the outlet `{id, name, role}`. Rows are read through the engine as the system actor, filtered by the outlet scope where the model has an outlet field.

- [ ] **Step 1: Failing tests.**
  - `TestEdgeConfig` (lists the `device-apps` counter screen and its sets; second call with `If-None-Match` → 304).
  - `TestEdgeDataPaging` (600 items → two pages, `done` on the second).
  - `TestEdgeDataOutletScoped` (available stock rows of another outlet absent).
  - `TestEdgeStaffNoEmail` (rows have `id`, `name` and `role`, and no `email`).
  - `TestEdgeDataNeedsDevice` (a session cookie without a device → 401).
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** PASS; `make test`.
- [ ] **Step 5: Commit** `kernel: edge config and data sets (contract 0058 §7)`.

## Not in this plan

- Plan 2 (web app): pairing screen `#/pasang`, device outbox, persistent storage, service worker sending, PIN pad and offline verifiers, device-side computes (`engine.js`), offline shift open and close, sync pill and queue panel, exceptions list.
- Plan 3: printer spike, then the printer layer.
- rangka-apps: `pos_sale` and `pos_shift` move to `naming_on: device`, in its own PR after this lands.
