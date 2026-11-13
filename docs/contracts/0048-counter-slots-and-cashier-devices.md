# 0048 Counter slots and cashier devices

Status: proposed (13 Nov 2026) · Owner of the change: rangka-os (schema 0.5, both validators, kernel auth, web app) ·
Affects: rangka-apps (`modules/catalogue`, `modules/pos`, `modules/kitchen`, a new table model), rangka-cloud (Console →
Perangkat, §5) · Builds on 0006, 0010, 0021, 0025, 0027, 0030, 0034, 0038, 0039, 0042, 0043 · Slices B, C, D and F of
`docs/pos-coverage.md`

## Context
- The POS design (`rangka.pen`, "Rangka POS · Tablet & HP") needs things the `counter` slots of 0030 §2, 0034 and
  0039 cannot say: options on an item (size, sugar, extras with a price), dining tables, cash in and out during a
  shift, card and transfer payments, a PIN on a shared tablet, a dark kitchen display, and the tablet chrome.
- What the code has today (rangka-os `main`, rangka-apps `main` bc70e62):
  - `item` (catalogue) has `variant_group` / `variant_label`: a variant is its own item with its own SKU. There is no
    option or modifier model anywhere in `rangka-apps/modules`.
  - `pos_sale_item` has `item`, `packing`, `qty`, computed `rate`, `discount_pct`, `amount`, `note`. `pos_sale.buyer`
    ("Pembeli atau meja") is free text; there is no table model. Kitchen tickets are made when a sale is submitted
    (flow `kitchen_ticket_on_sale`), i.e. at payment.
  - `pos_payment.method` has options `cash`, `qris`. `pos_sale` computes `qris_amount`, `cash_received`,
    `change_amount` from those two values; `pos_shift` keeps `cash_total`, `qris_total` (written by flows under the
    read lock of 0025) and `expected_cash = opening_cash + cash_total`.
  - The validator already checks `payments.methods` against the options of `payments.method`. The renderer
    (`web/app/counter.js`) hard-codes `"cash"` and `"qris"` (`saleBody`, the tender panel).
  - The kernel stores and loads one level of child rows (`store.loadChildren`, `deleteChildren`): a table inside a
    child model is written but never read back or deleted. Nested tables are not usable.
  - No device table, pairing route or PIN exists in the kernel (`kernel/internal/store/system.sql`). 0006 §2 pairing
    is proposed, not built. The CEL `device` object of 0030 §4 is always empty. rangka-cloud's "Perangkat" is the
    list of signed-in sessions under account security, not outlet devices.
  - The work shell (0030 §3, `web/app/shell.js` `workShell`) already exists: header with the company name and an
    account button, bottom bar with up to 5 screens and "Lainnya", which becomes an 88px left rail at 768px and wider.
    "N antre" sits in each screen's own header (`screens.js`).
  - Theme mode is chosen as: the person's pick, else `theme.mode` of the company, else light (`themeOf`).

## Decision
Rules of 0030 §2 hold for every slot below: slots name fields, the screen never prices, taxes or totals; the model's
computes do, and the screen shows them through `POST /api/v1/compute` (0034). Money and stock never depend on an LLM.

### 1. `catalogue.options`: options chosen before the line enters the cart

**Where options live (rangka-apps `modules/catalogue`).** Groups are shared across items (one "Gula" group for 30
drinks), so they are top-level models, not rows of the item:

| Model | Fields |
| --- | --- |
| `item_option_group` | `group_name`, `required` (check: one choice needed), `multi` (check: more than one allowed), `max` (int, 0 = no limit) |
| `item_option` | `group` (link to `item_option_group`), `label`, `price_delta` (currency, may be 0, never negative in v1), `active` (check) |
| `item` gains table `option_groups` of `item_option_group_row` | `group` (link to `item_option_group`) in display order |

Variants as separate items (`variant_group`) stay for retail SKUs; options are for made-to-order goods.

**How a choice reaches the sale (rangka-apps `modules/pos`).** Not a table on the line (nested tables are not
usable, Context). The document gets a second table:
- `pos_sale_item` gains `line_key` (data): a key the screen gives the line (`L1`, `L2`…), unique within the sale.
- `pos_sale` gains table `options` of `pos_sale_option` {`line` (data: the `line_key`), `option` (link to
  `item_option`), `qty` (float), `price_delta` (compute `option.price_delta`), `amount` (compute
  `round(qty * price_delta)`)}.
- `pos_sale.subtotal` becomes the lines' amounts plus `options.map(o, o.amount).sum()`; the other computes follow.
- Save rules in the model (not the screen): every option row names an existing line and carries that line's `qty`;
  the option belongs to a group of the line's item; required groups have a choice; `multi` and `max` hold. Rules that
  CEL cannot express are enforced by a flow guard in the module (see Open).

**Slot.**
```yaml
counter:
  catalogue:
    model: item
    options:
      groups: {table: option_groups, link: group}  # a table of the catalogue model; its row's link to the group model
      group: {title: group_name, required: required, multi: multi, max: max}  # fields of the group model
      model: item_option                            # the option model; must link to the group model
      link: group
      title: label
      price: price_delta                            # shown as "+Rp 5.000"; the model prices it
      filter: "active != false"
  document:
    line: {item: item, qty: qty, price: rate, note: note, key: line_key}
    options: {table: options, line: line, option: option, qty: qty}
```

**Renderer.** Groups and options load with the catalogue (list calls, cached for offline like the catalogue). Tapping
an item that has groups opens "Pilih varian": single groups as radio, multi as checkboxes up to `max`, required groups
marked; "Tambah" is disabled until each required group has a choice; qty and note in the same dialog. The cart shows
the chosen labels under the line. A tap on an item adds to an existing line only when item, options and note are the
same. The sale body carries `line_key` per line and one option row per choice with the line's qty. Offline, the
"perkiraan" sum adds `price_delta × qty`; online the breakdown is the model's (0034).

**Validator (Go and Python, same codes).** `options.model` and the group model are models the screen may use
(`unknown_model`); every named field exists (`unknown_field`); `groups.table` is a table of the catalogue model,
`groups.link` and `options.link` are links to the same model, `required`/`multi` are checks, `max` an int, `price` a
number (`screen_slot_type`); `document.options.table` is a table of the document, `option` links to `options.model`,
`line` is data, `qty` a number, `line.key` is a data field of the line (`screen_slot_type`); `document.options`
without `catalogue.options` or without `line.key`, and the reverse, is `screen_slot_type`.

### 2. `customer.table`: dining tables and a table map

**Model (rangka-apps, a new module `dining` or in `pos`; the module owner decides).** `dining_table` {`table_name`,
`area` (select or link), `seats` (int), `outlet` (link, row scope 0010), `active`}. `pos_sale` gains `table` (link to
`dining_table`), `guests` (int) and `bill_asked` (check). `buyer` stays for a name or queue number.

**State is derived, never stored by the screen.**
- *Kosong*: no draft document links the table.
- *Terisi*: a draft (`docstatus == 0`) links it; the map shows its age (`created_at`) and its `document.total`.
- *Minta tagihan*: that draft has `bill_asked` true (set by "Minta tagihan", or by printing the bill).
- The renderer lists `document.model` with `docstatus == 0 && <link> != ''` (row scope on top) and keeps the rows
  whose link is a loaded table. Reserved and cleaning states are not in this contract (Open).

**Open bills become server drafts when `table` is set.** "Simpan" creates the draft (`POST` with an
Idempotency-Key, no submit); later changes are `PATCH` of the lines; "Bayar" patches the payments and submits, as
today. Picking a busy table loads its draft (`?children=1`) into the cart. `hold` (device-local, 0030) stays for
counters without tables.

**Move and merge.** Move is a conditional `PATCH` of the draft's link (`_if: {table: <old>}`, 0030 §6a), offered only
onto a free table. Merge (two bills into one) needs one atomic write: two client writes could leave lines on both
bills and charge them twice. It is left out of the slot until the module has a merge action or flow (Open).

**Slot.**
```yaml
customer:
  field: buyer
  table:
    model: dining_table
    link: table                 # a link of the document to the table model
    title: table_name
    area: area                  # the map's sections
    seats: seats
    filter: "active != false"
    match: {outlet: outlet}     # table field: session field, as 0039
    guests: guests              # a number field of the document, asked when a free table is opened
    bill: bill_asked            # a check field of the document
```
**Renderer.** With `table`, the counter opens on the map (by `area`, a card per table with its state), plus "Bawa
pulang" that goes straight to the catalogue without a table. Offline, the map shows the last loaded states labelled
"data terakhir <jam>"; opening another device's bill needs the connection.

**Validator.** `unknown_model`, `unknown_field` as above; `link` links to `table.model`; `guests` is a number, `bill` a
check, `seats` a number (`screen_slot_type`); `match` keys are fields of the table model and values fields of
`session.model`, and `match` without `session` is `screen_slot_type` (same as 0039).

### 3. `session.moves`: cash in and cash out during a shift

**Model (rangka-apps `modules/pos`).** `pos_cash_move`, submittable (nothing links to it, 0030 §10): `shift` (link),
`direction` (select `in` / `out`), `amount` (currency, > 0), `reason` (select: `float_topup`, `purchase`,
`deposit`, `other`), `note`. A flow on submit and on cancel updates new `pos_shift.cash_in_total` and
`cash_out_total` under the read lock of 0025, like `pos_sale_on_submit`. `expected_cash` becomes
`opening_cash + cash_total + cash_in_total - cash_out_total` on the shift and on `pos_shift_close`, still
`hidden_for: [cashier]`. The close's "a sale arrived after the figures were read" check covers moves too. A cash out
above a limit can need head office through an approval (0007); that is a model rule.

**Slot.**
```yaml
session:
  moves:
    model: pos_cash_move
    link: shift                         # its link to the session model
    fields: [direction, amount, reason, note]
    set: {}                             # optional, as other set: blocks
```
**Renderer.** "Kas masuk/keluar" in the session area opens a short form with `fields`, creates and submits (one outbox
job, queued offline like a sale) and lists this session's moves (`<link> == <session>`). The cashier sees the moves
they typed, never the expected amount: blind close stays.

**Validator.** `unknown_model`, `unknown_field`; `link` links to `session.model`; `fields` are plain fields (no table,
no compute, not read-only) (`screen_slot_type`); the model is submittable (`screen_not_submittable`); `set` as other
`set:` slots.

### 4. `payments`: card and transfer as recorded methods
No processing, no terminal integration: the cashier takes the payment on the EDC or checks the transfer, then records
it with the reference they read.
- New key `payments.cash` (default `cash`): the option of `method` that is cash. Cash is the only method that gives
  change; every other method in `methods` is exact and gets "<label> penuh", a partial amount and a `ref` box.
- The renderer sends one row per method used and takes method labels from the field's options. The `"qris"` special
  case goes; dynamic QRIS stays with the connector work (pos-coverage #18).
- rangka-apps: `pos_payment.method` gains `card` ("Kartu debit/kredit") and `transfer` ("Transfer bank"); `pos_sale`
  replaces `qris_amount` by `noncash_amount` (every method but cash), keeps "change only from cash" and "non-cash not
  over total", and a rule that card and transfer rows have a `ref`; `pos_shift` keeps a total per method; the
  accounting flow maps each method to its account.
- Validator: `payments.cash` is one of `methods` and an option of `method` (`screen_slot_type`). The existing check
  that `methods` are options stays.

### 5. Registered devices and cashier PIN

**What it is.** A tablet registered to one outlet. On it, staff pick their name and type a 4 to 6 digit PIN instead of
an e-mail and password. The PIN only works on a registered device of an outlet the person belongs to. This replaces
0030 §8's "phase 1 shared tablets use normal sign-in" for browsers; Edge (0006) keeps its own local PIN later.

**Kernel (rangka-os).**
- `rk_devices` (`id` `dev_` + 20 hex, `org_id`, `outlet`, `name`, `kind` `browser` | `edge`, `token_hash`,
  `created_by`, `created_at`, `last_seen_at`, `revoked_at`) and `rk_device_codes` (pairing codes). One table for
  browsers and Edge, so 0006 §2 reuses it.
- **Pairing.** An owner, or a member allowed by a new permission (Open), creates a code for one outlet: 8 characters
  from 0006's alphabet, single use, 15 minutes. On the tablet, `#/pasang` takes the code:
  `POST /auth/device/pair {code, name}` → `201 {device_id, outlet}` and the cookie `rk_device` (HttpOnly,
  SameSite=Strict, Secure as `rk_session`, 400 days). Errors `400 pairing_code_invalid` (one message for unknown, used,
  expired), `429 too_many_attempts`. The device token alone reads and writes no data.
- **Staff list.** `GET /auth/device/staff` (device cookie) → `[{id, name}]`: members whose outlets include the
  device's outlet, whose every role has `desk: false` (role.json), and who have a PIN. No e-mail in the answer.
- **PIN.** A member sets their own PIN with a session opened in the last 15 minutes: `PUT /auth/pin {pin}`. 4 to 6
  digits; all-same and straight runs (`1234`, `0000`) are refused (`422 pin_weak`). Stored per membership
  (`rk_members.pin_hash`, the kernel's password hash). An owner or head office can clear it (`DELETE
  /api/v1/members/{user}/pin`, Open: route shape). Removing a member or changing their role or outlets (0043) clears
  the PIN and revokes PIN sessions, like other sessions.
- **Unlock.** `POST /auth/device/unlock {user, pin}` (device cookie) → a session in `rk_sessions` with
  `method: pin` and the device id, 12 hours at most. The session is the member's own (roles, row scope) with outlets
  narrowed to the device's outlet. Members with `owner` or any `desk: true` role cannot use a PIN
  (`403 pin_not_allowed`); they sign in fully. The CEL `device` object of 0030 §4 is `{id, outlet}` for such
  sessions.
- **Limits.** 5 wrong PINs for one member lock that member's PIN for 15 minutes on every device (`423 pin_locked`);
  10 in a row clear the PIN (`pin_not_set` until they set a new one after a full sign-in). 20 wrong attempts on one
  device in 15 minutes stop that device's unlocks for 15 minutes (`429 too_many_attempts`). One error message for a
  wrong PIN and an unknown member. Every pair, revoke, unlock, failure and lockout is an audit entry (no PIN, no hash).
- **Lock.** "Kunci" ends the PIN session (`POST /auth/logout`) and shows the staff list. The web app locks after a
  period without input (default 5 minutes, Open: where it is configured).
- **Revoke.** A revoked device's cookie gets `401 device_revoked`; its PIN sessions end at once.
- `auth.password` (0042 §6) does not apply: a PIN is not a password sign-in, and pairing is an owner's act.

**Offline.** Unlocking another person needs the connection. While offline, the person whose session is on the
device can lock and unlock again locally: after each online unlock the device keeps a PBKDF2-SHA256 verifier of that
person's PIN (WebCrypto, random salt, 600 000 iterations) in IndexedDB; it unlocks only the session cookie that
person already holds on that device, is dropped on lock-out, sign-out or 5 wrong local tries, and is never sent.
Switching cashier offline, and an outbox that survives a cashier change offline, come with Edge (0030 §8, 0006).
Queued jobs stay scoped to org and user (0030 §7): another person's queue waits until that person unlocks online.

**rangka-cloud (closed, Console → Perangkat).** Lists devices per outlet, creates pairing codes, renames and revokes,
shows "PIN aktif" per staff member and clears a PIN. It calls the kernel routes above with a token of a new scope
`devices:write`, minted like `members:write` (0043 §1: owner session only, scope-limited). Cloud never sees a PIN, a
hash or a device token. The kernel web app has the same owner page (Pengaturan → Perangkat) for self-hosters.

**Validator.** No `.rk` key is added, so no validator code.

### 6. Screen `theme`
- `screen.json` gains `theme: light | dark` (no `system`). While `#/w/<screen>` is open the web app sets that mode on
  the whole page, above the person's pick and the company's `theme.mode`; leaving the screen restores the previous
  mode. Use: `theme: dark` on `dapur`.
- Colours stay the theme's tokens (0038 and `theme.rk` unchanged); only the mode switches.
- Validator: the enum is checked by the JSON Schema (`schema`); no new code.

### 7. The work frame on a tablet
Renderer only, no `.rk` key. It extends the work shell of 0030 §3 and applies to the same people (every role
`desk: false`, a home screen) and to every PIN session (§5). Desk users keep the sidebar with "Kerja".
- **Rail** (768px and wider): the person's work screens in the apps' `work:` order, up to 8, then "Lainnya". Below
  768px the bottom bar keeps 5. A `queue` screen shows a badge: the list `total` of its `source.filter` and the first
  `columns.values` (e.g. new tickets), fetched every 60 seconds while online.
- **Top bar**: outlet name (open session's outlet, else the device's, else `user.outlets[0]`); a sync pill:
  "Online", "Offline sejak 12.31" or "N antre", which opens the outbox panel ("Kirim sekarang", refused jobs). The
  per-screen "N antre" notice moves here. Avatar with initials and name, opening the account menu.
- **Lock** ("Kunci") in the top bar for PIN sessions (§5); for other sessions the account menu keeps "Keluar".
- The screen's `theme` (§6) applies to the frame too.

## What stays out
- Payment processing (EDC integration, dynamic QRIS), split bill by party, PB1 and service charge, promo, loyalty.
- Reservations and table cleaning states; a separate `floor` screen type (pos-coverage #24 proposed one; the map is a
  step of the counter here, so the closed set of 0030 §2 does not grow).
- Supervisor PIN override (0030 Open): a later contract on top of §5.
- Options that move stock (an extra shot using espresso): recipes on options are module work later.

## Open
1. Whether the kernel CEL can check "required groups have a choice" per line (it needs the line's item's
   `option_groups` table through a link). If not, a module flow guard on save enforces it.
2. Whether a line's `discount_pct` also applies to its options' amounts (module money rule).
3. Kitchen tickets for table service: today they are made at payment. A dine-in bill needs "Kirim ke dapur" on the
   draft (a flow on draft update in `modules/kitchen`, sending only lines not sent yet). Module owner to decide.
4. Merge of two table bills: a module action or flow that moves lines and deletes the source in one transaction.
   Actions call kernel hooks; modules cannot ship hooks yet. Option: a flow triggered by a `merge_into` field.
5. Two devices editing one table's draft: the later `PATCH` of the lines wins (as `roll`, 0030). `_if` on a
   revision field would stop it; the kernel has no record revision today.
6. Who may create pairing codes besides the owner: a permission on a system page, or a role flag (`role.devices`).
7. Route shape for clearing a member's PIN, and whether Cloud uses 0043's subject-keyed routes for it.
8. Idle lock time: fixed 5 minutes, a `system.rk auth` key, or a screen key.
9. Whether the person's own theme pick should win over a screen's `theme` (proposed: no, a shared display is set up
   for the room).
10. PIN reuse: staff may reuse a bank PIN. The UI says not to; nothing can check it.
11. Rangka-apps keys before the release: use `experimental:` (0000 §4) in `kasir.rk` until the slots ship.

## Plan (one PR each, cheapest first; rangka-os unless named)
| # | Content | Repos |
|---|---|---|
| P1 | §6 `screen.theme`: schema, `make sync-schemas`, both validators (schema only), web app mode switch; `dapur.rk` `theme: dark` | rangka-os, then rangka-apps |
| P2 | §4 `payments.cash`: schema, both validators, `make compare-python`; renderer generic methods (`saleBody`, tender). rangka-apps: `pos_payment` options, `pos_sale` `noncash_amount` and ref rule, shift totals per method, accounting mapping | rangka-os, rangka-apps |
| P3 | §7 work frame: rail up to 8, top bar with outlet, sync pill and outbox panel, avatar, queue badges | rangka-os (`shell.js`, `screens.js`, `app.css`) |
| P4 | §3 `session.moves`: schema, validators, renderer. rangka-apps: `pos_cash_move`, flows on submit and cancel, shift and close `expected_cash` | both |
| P5 | §1 options. rangka-apps first: `item_option_group`, `item_option`, `item.option_groups`, `pos_sale_item.line_key`, `pos_sale.options`, rules, kitchen ticket lines show options. rangka-os: schema, validators, "Pilih varian" dialog, cart and body | both |
| P6 | §2 tables. rangka-apps: `dining_table`, `pos_sale.table/guests/bill_asked`, guard "one draft per table", kitchen send on draft (Open 3). rangka-os: schema, validators, table map, server drafts, move | both |
| P7 | §5 kernel: `rk_devices`, `rk_device_codes`, pairing, staff list, PIN set/clear, unlock sessions, limits, audit, CEL `device`, `devices:write` scope, owner page Pengaturan → Perangkat | rangka-os |
| P8 | §5 web app: `#/pasang`, staff picker, PIN pad, lock, offline verifier | rangka-os |
| P9 | §5 Console → Perangkat | rangka-cloud |

Each PR: tests in `web/app/test/counter.test.js` / `test_screens.py` for the renderer, Go and Python validator cases
with identical reports (`kernel/internal/meta/validate_screen*.go`, `tools/rangka_validate.py`).
