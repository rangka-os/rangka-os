# 0030 Work screens (staff tools declared in `.rk`)

Status: accepted (8 Nov 2026), built in stages (see Implementation) · Owner of the change: rangka-os (schema,
validator, kernel, web app) · Affects: rangka-apps (modules ship screens, apps bind them to roles), rangka-edge (later,
§8) · Builds on 0001, 0003, 0006, 0008, 0009, 0010, 0011, 0019, 0027

## Context
- The web app serves the owner and the office: a sidebar of lists, forms and dashboards (0019). Every role lands on
  the same home (`homeHash`, the first dashboard of the first app) and sees the same shell, only with fewer models.
- Apps now have staff who do one job many times a day, often on a phone or a shared tablet: the cashier, the
  kitchen, the salesman on a route, the driver, the guard on patrol, the worker clocking in at a client site, the
  instructor taking attendance, the warehouse handing out rental equipment, the line operator, the bill collector.
  An inventory of nine business types (53 staff tasks) shows they fall
  into seven screen patterns and need five device capabilities (scan, photo, location, signature, print).
- A generic form is the wrong tool for them: too many taps, no camera, no location, no scan, no offline, and the owner's
  dashboard as the first thing they see.
- The layout schema already reserves `shell: pos` (0019 Open: "project 2"). This contract replaces that idea with a
  wider one: screens are declared per job, not one POS shell per site.

## Decision

### 1. Kind `screen` (`screens/<name>.rk` in a module)
A module ships its work screens next to its models, like print formats (0008). A screen is UI over the module's own
models (and those it `requires`); it creates and changes records only through the normal record API, so permissions,
row scope (0010), hidden fields (0027), rules, flows, approvals (0007), stock moves (0021) and numbering (0009) apply
exactly as for a form.

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/screen.json
name: kasir                    # = file stem, unique per site
type: counter                  # one of the seven types in §2
label: {id: Kasir, en: Cashier}
icon: cashier
roles: [cashier]               # who may open it (owner always may); each must be a role of the site
device: tablet                 # phone | tablet | any (layout hint; never a lock)
needs: [print]                 # capabilities from §4; the screen still opens without them, with a notice
offline: queue                 # none | queue | edge (§7, §8)
if: "user.outlets.size() > 0"  # optional CEL over user; false hides the screen
<type>: {...}                  # the type's block, §2
```

Validator rules (Go and Python, `make compare-python`): `name` equals the file stem (`name_mismatch`); a screen name is
unique per site, the first unit (modules first, by name) keeps it (`duplicate_screen`); models a slot names are models
of the screen's module or its `requires` (`unknown_model`), fields are fields of that model (`unknown_field`), roles are
roles of some module or app, or `owner` (`unknown_role`); type-specific checks in §2 (`screen_slot_type`, e.g. a table
or a computed field as an `entry` field, a `price` that is not a money field; `screen_not_submittable` for
`submit: true` on a model that is not submittable; `unknown_action`, `unknown_print` with the types that name them);
a screen whose `type` block is missing or that carries the block of another type is `screen_type_mismatch`;
`offline: queue|edge` on a type that may not queue (§7) is `screen_offline`; `if` and `set` values are checked for
balanced parentheses (`cel_syntax`).

### 2. Seven types, closed set
Types are rendered by the kernel web app. Modules choose a type and fill its slots; they never ship HTML or JS.
Adding a type is a contract change.

| type | Job | Slots (required **bold**) | Writes |
|---|---|---|---|
| `counter` | Sell or take payment at a counter: catalogue, cart, tender, receipt | **`catalogue`** {model, filter, search, group_by, price, barcode, image}, **`document`** {model, lines, line: {item, qty, price, note}, set: {field: CEL}}, `payments` {table, method, amount, methods}, `session` {model, link, state, open: [fields], close: {model, link, fields}, blind (bool, default true: the cashier counts cash without seeing the expected amount)}, `hold` (bool), `customer` {field, required}, `print`, `submit` (default true) | creates one `document` per sale with its lines and payment rows, then submits it |
| `queue` | Work through cards that change state: kitchen tickets, today's work orders, pick lists, today's rental jobs | **`source`** {model, filter, sort}, **`card`** {title, subtitle, lines, fields, age_from}, **`steps`** [{when: CEL, action: submit / action name / {set: {...}}, label}], `columns` {by: field, values}, `refresh` (seconds, default 10) | runs the step's action on one record |
| `roll` | One parent record, many rows marked fast or added by scan: class attendance, team attendance, stock count, receiving | **`document`** {model, find or create: CEL, set}, **`rows`** {table, label, mark: {field, values, default} or qty: field} (default: every row starts at e.g. `present` and staff change only the exceptions, as Teachworks and Jackrabbit do), `add_by` (scan / search / none), `scan` {field, match}, `submit` | updates the parent's table, then submits |
| `route` | Today's list of places to visit, check in, act there | **`stops`** {model, filter, sort, title, address, location}, `visit` {model, link, set, capture: [location, photo]}, **`actions`** [{label, screen or create: model, set}] | creates a visit record (optional) and opens another screen or form with the stop prefilled |
| `handover` | Hand items over with proof: rental out/in, proof of delivery, BAST | **`document`** {model, find or create, set}, **`rows`** {table, unit or item, qty, condition}, `scan` {field, match}, `photos` {field, min}, `signature` {field, by}, `print`, `submit` | fills rows, attaches photos and signature, submits, prints |
| `clock` | Clock in and out: workers at client sites, outlet staff | **`record`** {model, set}, **`in`** {field}, **`out`** {field}, `site` {from: CEL, location, radius_m}, `capture` [photo, location], `shift` {model, filter} | creates one record per clock-in, sets out on clock-out |
| `entry` | A short phone form, save and next: hours, expense with receipt, waste, batch output, overtime | **`model`**, **`fields`** [names, at most 8, plain fields: no table, no compute], `set` {field: CEL}, `submit` (bool), `again` (bool, default true); `capture` comes with §4's widgets (WS6) | creates one record (and submits when asked) |

Rules shared by every type:
- **Slots name fields, never compute.** `set`, `filter` and `if` are CEL with `user`, `org`, `tax`, `today()`,
  `now()`, `device` (§4) and `stop` (route) or `doc` (roll/handover parent); the same CEL subset as dashboards. Prices,
  tax and totals are computed by the model's computes and rules, never by the screen.
- **`set` is evaluated by the kernel.** `GET /api/v1/screens/<name>` answers `{name, screen, set}` with the type
  block's `set` evaluated for the caller at that moment (a `now()` is RFC 3339 in the org timezone); the web app asks
  again for every new record. A screen the caller may not open is 404. The values prefill the record; they are not
  trusted more than what a form sends.
- **Blocks whose renderer is not built yet** are free-form objects in `screen.json`; the PR that adds the renderer
  gives the block its shape and its validator checks. The web app shows a notice for such a screen.
- **Writes are ordinary API calls** (`POST /api/v1/r/<model>`, `.../submit`, `.../actions/<name>`) by the signed-in
  user, plus the idempotency key of §6. A screen cannot do what a form by the same user could not.
- **One screen, one document.** A screen that must move stock, post money or start an approval does it through the
  document it submits (0021 `stock:`, ledger hooks, 0007), so rangka-auditor reviews the module, not the screen.
- `filter` defaults include the actor's row scope automatically (0010); a screen never widens it.

### 3. Binding screens in the app: roles and the home per role
Modules ship screens; apps decide who gets them, like menus (0001). New key in `app.json`:

```yaml
work:
  - {screen: kasir, roles: [cashier], home: true}
  - {screen: dapur, roles: [kitchen], home: true}
  - {screen: terima_kiriman, roles: [outlet_staff]}
  - {screen: waste, roles: [outlet_staff]}
```
- A screen not named in any installed app's `work` is not offered (a module can ship screens an app does not use).
  `roles` here narrows the screen's own `roles`; it never adds a role the screen does not allow (`screen_role_not_allowed`).
- **Home per role.** On sign-in the web app opens, in order: the first `work` entry with `home: true` for one of the
  user's roles; else the layout's `home`; else today's `homeHash`. The owner keeps the dashboard home.
- **Work shell.** A user whose every role says `desk: false` (role.json) and who has a home screen gets the work shell: no sidebar, the home screen full screen, a bottom bar with their other
  screens (at most 5) and "Lainnya" for lists they may read. Everyone else gets the sidebar with a "Kerja" group that
  lists their screens. This retires `shell: pos` (it stays valid in the enum and keeps falling back, 0019).
- `GET /api/v1/meta` adds `screens: {name: {...the screen, module}}` filtered to the caller (roles, `if`, installed app)
  and `work: [{screen, home}]` for the caller, in the apps' `work:` order (a screen bound twice appears once). Screens
  not allowed are not described (same rule as 0027 dashboards). The owner opens every bound screen whose `if` holds,
  and `home` is never true for them.
- Validator: `work.screen` names a screen of the app's modules (`unknown_screen`); `work.roles` only narrows
  (`screen_role_not_allowed`); a second `home: true` for the same role is a warning (`duplicate_home`), the first wins.
- Route `#/w/<screen>` in the web app; `#/w/<screen>?stop=<name>` and `?doc=<name>` for prefilled opens.

### 4. Capabilities (`needs`) and the widgets behind them
The five device needs map to browser APIs first, so nothing costs money and no native app is needed in phase 1.

| need | Phase 1 (browser, web app) | Phase 2 (rangka-edge) | Field it fills |
|---|---|---|---|
| `scan` | camera via `BarcodeDetector` where supported, else a JS decoder in `web/app/vendor`; a USB/Bluetooth HID scanner types into the focused input (no driver) | `scanner.scanned` from a device connector (0003) | the slot's `scan.field` |
| `photo` | `<input type=file accept=image/* capture>`; resized on the device to 1600px JPEG; stored as an attachment (0011) with `taken_at` and, when `location` is also needed, the position | n/a | an `image` field (stores the attachment id) |
| `location` | `navigator.geolocation` once at the action, with accuracy; refused permission = the action shows why and stops when the slot requires it | n/a | a new `geo` field type: `{lat, lng, accuracy_m, at}` |
| `signature` | canvas pad, saved as PNG attachment with signer name | n/a | a `signature` field (attachment id) |
| `print` | the record's print format (0008) at `80mm` or `A5` in a hidden frame and the browser print dialog; Android with a Bluetooth printer uses the system print service | ESC/POS through Edge (0003 §4), cash drawer kick | n/a |

Kernel changes behind the widgets: field type `geo` (jsonb, validated shape, CEL access `f.lat`, `f.lng`,
`distance_m(a, b)`); `image` and `signature` stored as attachment ids with the attachment checked to belong to the
record (today plain text); a CEL `device` object `{id, outlet}` that is empty until a device is paired (§8).
`clock.site.radius_m` is checked twice: on the device for the message, and in the kernel by a compute using
`distance_m`. A clock-in outside the radius is **not refused**: the record is saved with `outside_site: true` and the
reason the worker typed, and waits for the supervisor (approval 0007), as Talenta and Gadjian do (pending review).
Fake-GPS detection beyond that is out of scope (supervisor reviews the photo).

### 5. Numbering per outlet and device (B18)
Per-outlet numbers use the field token of 0009 §2a (built by the cashier work, rangka-os `feat/naming-field-token`):
`STRK-{outlet.code}-{YY}{MM}-{#####}`, with one counter per resulting prefix, so every outlet has its own series
without a new table. This contract only adds, for offline devices later, `{DEVICE}` (a short device code, §8; `W` + 2
chars for a browser that is not paired) and a per-day `{DD}`. Gapless per series as today.

### 6. Idempotent writes
Every write a screen makes carries `Idempotency-Key: <client id>` (made when the user presses the button, kept until
an answer arrives). The record API honours it on `POST /api/v1/r/<model>`, `.../submit`, `.../cancel`, `.../amend` and
`.../actions/<name>`; a key is 1 to 128 printable ASCII characters (else 400 `idempotency_key`) and belongs to one org
and one actor (another user's same key is another key). The kernel keeps `rk_idempotency (org, actor, key)` for 7 days:
- The first request claims the key (`pending`) and runs. The engine marks the key `committed`, with the first record it
  wrote, **inside the writing transaction**; a 2xx answer is then stored (`done`). A request that fails (4xx, 5xx)
  releases a still pending key, so a corrected retry runs.
- A replay with the same method, path and body answers the stored answer with header `Idempotency-Replayed: true`
  (and `Location` again for a 201). If the server stopped between commit and storing the answer, the replay answers
  the record it wrote (200) once the claim is 2 minutes old (before that it is still running). A different method, path or body is `409 idempotency_conflict`. The same request still
  running is `409 idempotency_in_progress` with `Retry-After: 2`; a pending claim older than 2 minutes never committed
  and is taken over.
- Each claim carries a random token; the commit inside the write succeeds only for the request that still holds the
  claim, so a request whose stale claim was taken over rolls back (`409 idempotency_in_progress`) and one key never
  writes twice. The hash covers method, path and the body without `_occurred_at` (a queued replay adds it).
- The replay target of submit and cancel is the document the verb acted on (not a record its hooks wrote first).
- A stored answer is replayed to the same actor without checking permissions again (for 7 days); sessions and API
  tokens of one user share the key space.
This is what makes a double tap, a dropped connection or a phase-1 queue safe, and it is the same rule edge events
already follow (0006 §3).

### 7. Offline, phase 1: browser queue (`offline: queue`)
- The web app keeps an IndexedDB outbox (`rangka-outbox`) per device, with jobs scoped to org and user (another person
  signed in on the device never sends them). A press is a job with its own keys (create, then submit when the screen
  submits). A write that fails with no connection, a `5xx` or `idempotency_in_progress` waits and is replayed in order
  on reconnect, every 15 seconds while the screen is open, and from "Kirim sekarang"; any other `4xx` (rule refused,
  permission) is shown to the user until dismissed and never replayed silently. On a screen without `offline: queue`
  the same press keeps its keys, so pressing again after a lost answer never writes twice.
- The screen (and later its reference data: `catalogue`, `stops`, `source` of the user's scope) is cached after each
  successful load and shown with "data terakhir <time>" when offline.
- Allowed only for `counter`, `roll`, `route`, `clock`, `entry` (`screen_offline`). The kernel accepts a late write like
  any other. A replayed write carries `_occurred_at` (RFC 3339, the moment the user pressed the button) in its body: the
  kernel takes it out of the body, keeps it as `occurred_at` in the audit entries of the records written, and refuses a
  time more than 5 minutes ahead or 30 days back (`422 occurred_at`; the web app then sends once without it). Business
  dates come from the screen's `set:` values, which the kernel evaluated when the screen was opened, so a sale queued
  before midnight keeps its day.
- Conflict rules are those of 0006 §5: sales win, stock may go negative with an exception, reference data from the
  kernel wins.
- Limits stated to the user on screen: the queue works while the tab stays open (it survives a reload; an installable
  PWA with a service worker is a later step); clearing the browser loses the queue, so the count ("N antre") stays in the
  header until it is 0.

### 8. Offline, phase 2: rangka-edge (`offline: edge`)
For the counter with printer and drawer, and later patrol and salesman routes with long dead zones:
- A paired device (0006 §2) gets `GET /edge/config.screens`: the screens bound to the outlet's roles, and
  `data_sets` derived from their reference slots (`catalogue.model`, `price_list`, `tax`, the `stops` model).
- The web app talks to the local Edge at `http://127.0.0.1:<port>` when present (device token, printer, drawer,
  queue that survives a closed browser) and to the kernel otherwise. Same screens, same `.rk`.
- New event kind in 0006 §3: `screen.write` `{screen, model, op: create|submit|action, body, idempotency_key}`. The
  kernel replays it as the cashier's write under the device actor plus `actor` (0006), through the same API path as §6,
  so module code never sees the difference. This generalises `sale.recorded`, which stays as an alias.
- Staff on a shared paired device unlock with a local PIN (0003 §3); the PIN maps to a user of that outlet. Phase 1
  shared tablets use normal sign-in per user (quick switch = sign out, sign in), which is acceptable for the first pilots.

### 9. Example: the cashier (rangka-apps `modules/pos`)
Models of rangka-apps `feat/kasir` (8 Nov): `pos_sale` (submittable; `shift`, `outlet`, `date`, `buyer`, table `items`
of `pos_sale_item` {item, packing, qty, rate computed, discount_pct, note}, `discount_amount`, table `payments` of
`pos_payment` {method cash|qris, amount, ref}, computes `grand_total`, `cash_amount`, `qris_amount`, `change_amount`;
`stock:` block exploding the recipe, 0021), `pos_shift` (Sesi kasir, **not** submittable: numbered when opened,
`state` open/closed, because a submitted record is renamed from `DRAFT-` and links to it would break) and
`pos_shift_close` (Tutup sesi, submittable; `shift`, `counted_cash`, expected cash `hidden_for: [cashier]`).

```yaml
# modules/pos/screens/kasir.rk
name: kasir
type: counter
label: {id: Kasir}
icon: cashier
roles: [cashier]
device: tablet
needs: [print, scan]
offline: queue
counter:
  catalogue:
    model: item
    filter: "row.kind != 'material'"
    search: [item_name, sku, barcode]
    group_by: category
    price: rate
    barcode: barcode
  document:
    model: pos_sale
    lines: items
    line: {item: item, qty: qty, price: rate, note: note}
    set: {outlet: "user.outlets[0]", date: "today()"}
  payments: {table: payments, method: method, amount: amount, methods: [cash, qris]}
  session:
    model: pos_shift
    link: shift
    state: {field: state, open: open, closed: closed}
    open: [opening_cash]
    close: {model: pos_shift_close, link: shift, fields: [counted_cash]}
  hold: true
  print: pos_receipt
```

```yaml
# apps/fnb_cabang/app.rk (excerpt)
work:
  - {screen: kasir, roles: [cashier], home: true}
  - {screen: dapur, roles: [kitchen], home: true}
```

A kitchen display is then pure `.rk`: a flow makes a `kitchen_ticket` per submitted sale (or per station), and

```yaml
name: dapur
type: queue
roles: [kitchen]
device: tablet
queue:
  source: {model: kitchen_ticket, filter: "row.status != 'served' && row.date == today()", sort: created}
  card: {title: table_no, subtitle: sale, lines: items, age_from: created}
  columns: {by: status, values: [new, cooking, ready]}
  steps:
    - {when: "row.status == 'new'", set: {status: cooking}, label: {id: Masak}}
    - {when: "row.status == 'cooking'", set: {status: ready}, label: {id: Siap}}
    - {when: "row.status == 'ready'", set: {status: served}, label: {id: Diantar}}
  refresh: 5
```

The same design states the other fields' screens (the plan lists one per business): `roll` for class attendance on
`course_session.attendance`, `clock` for workers on a new `attendance_log` model with `site` from the assignment's
client site, `route` for salesmen over `customer` filtered by `route_day`, `handover` for `rental_checkout` and
`rental_return`, `queue` + `entry` for `work_order` and `production_batch`.

### 10. Kernel facts found by the cashier work (8 Nov)
- **Links to a submittable record break on submit.** Submitting renames `DRAFT-…` to the series number, and links held
  by other records are not rewritten. Until the kernel rewrites them (or numbers on create), a record that others link
  to while it is open (a shift, a route day, a site visit) must not be submittable; its closing is a separate
  submittable document (`session.close.model`). Same rule for `clock` (an `attendance_log` is linked by nothing) and
  `route` visits.
- **`on: cancel` rules are accepted by the schema but never evaluated.** Screens that void (counter) rely on a flow
  for same-day checks until the engine runs them. Proposed kernel fix: evaluate `on: cancel` rules in the cancel path
  (small PR, audited).

## What stays out
- No screen-specific code paths in the kernel for money, stock or tax; those stay in models (auditable).
- No custom HTML, JS or CSS from modules. Look follows the web app tokens; density is the type's.
- No native mobile app in this contract. The web app installable as a PWA is phase 1; Android packaging (TWA) is a
  later decision because it needs a paid Play developer account.
- No face recognition or fake-GPS SDK (paid, privacy-heavy). Photo + location + a model rule + owner review.
- Customer-facing screens (customer display, self order, portal) are not work screens; `portal` stays in 0019 Open.

## Contract changes (rangka-os)
1. Schema 0.5: `screen.json` (new kind), `app.json` gains `work`, `model.json` gains field type `geo` and the
   attachment semantics of `image`/`signature`, number scheme tokens `{DEVICE}` and `{DD}` (0009; per-outlet numbers already use the 0009 §2a field token).
   `make sync-schemas`. Change-set paths accept `modules/<m>/screens/<name>.rk` (layer 5, like dashboards).
2. Validator (Go and Python, `make compare-python`): the codes in §1 and §3.
3. Kernel: registry loads `screens/`; `/api/v1/meta` `screens` and `work` per caller; `Idempotency-Key` on create and
   submit (§6); `_occurred_at`; `geo` type with `distance_m` in CEL; attachment-backed `image`/`signature`; numbering
   tokens.
4. Web app: route `#/w/<screen>`, home per role, work shell with bottom bar, "Kerja" group in the sidebar, renderers for
   the seven types (built in the order of the plan, `counter` first), widgets for scan, photo, location, signature and
   80mm print, IndexedDB outbox.
5. Later (phase 2, with rangka-edge): 0006 `screen.write`, `config.screens`, local Edge bridge, PIN unlock.

## Implementation (rangka-os, one PR each, in this order)
| Stage | Content | State |
|---|---|---|
| WS1 | `screen.json`, `app.work`, change-set path `modules/<m>/screens/`, validators, registry, `/meta` `screens` + `work`, `GET /api/v1/screens/{name}`, home per role, `#/w/<screen>`, "Kerja" group, work shell, renderer `entry` | merged |
| WS2 | `Idempotency-Key` on create, submit, cancel, amend and actions (`rk_idempotency`); `_occurred_at`; IndexedDB outbox; cached screen | this PR |
| WS3 | renderer `counter`, print from the screen, `on: cancel` rules evaluated | |
| WS4, WS6, WS9a, WS7, WS5, WS8, WS9b | `queue`; `geo` + photo/signature; `clock`; `roll`; camera scan; `handover`; `route` | |

## Open
- Should `entry` and `roll` also be offered to the owner on the desk as quick actions? Proposed: yes, through the
  "Kerja" group; no new concept.
- Live updates for `queue`: polling with `modified > last` (phase 1) vs an SSE stream of record changes per model
  (later, shared with dashboards).
- Per-line kitchen routing (stations) needs either a ticket per station (flow, `.rk` only) or a `queue.split_by` slot.
  Proposed: ticket per station first; add the slot only if two apps need it.
- Whether `device` should become a hard requirement for `counter` once printers go through Edge.
- Supervisor override at the counter (void, discount above a limit) with a supervisor PIN on the same device, as
  majoo, Shopify and Odoo do. Phase 1: the action needs a role the cashier lacks, so it goes to Persetujuan (0007)
  and a supervisor approves on their own phone; phase 2: PIN unlock on a paired device (§8).
