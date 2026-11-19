# 0069 Counter checkout fields, line flag and line amount; parent and clock() in CEL

Status: proposed (19 Nov 2026) · Owner of the change: rangka-os (schema, validators, kernel CEL scope, web app) ·
Affects: rangka-apps (`modules/pos` names the slots; new module `promotions`, rangka-apps#103; `loyalty`, rangka-apps#102,
reuses them) · Amends 0030 §2 and 0034 §2 · Builds on 0027, 0034, 0058 §7 and §10

Number: claimed when the PR opened (0069 was the next free number on `main`; issues that proposed 0069 or 0072 for
this or other work pick the next free one when their contract PR opens).

## Context
- A promo the owner sets up (percent, rupiah off, buy X get Y, a bill promo with a voucher code, a free gift) is
  evaluated by the model, not the screen (`docs/pos-coverage.md` row 32). The counter can only send the item, qty and
  note of each line and a buyer text, so the cashier has no place to pick a bill promo, type a code, mark a gift line
  or link a customer, and the cart shows `qty × price` per line even when the model takes a promo off.
- A line's promo can depend on the outlet of the sale (a promo for one outlet), but a child row's compute cannot read
  its document: the kernel keeps the document as the row's parent scope only for money settling.
- A happy hour (14:00 to 16:00) needs the wall clock in the site's timezone. `now()` is an RFC 3339 string in the
  process timezone, and CEL has no hour accessor.

## Decision

### 1. Counter slots
```yaml
document:
  model: pos_sale
  line: {item: item, qty: qty, price: rate, note: note, amount: amount, flag: is_gift}
  summary: [gross_total, promo_total, discount_total, grand_total]
  checkout: [customer, {field: promotion, filter: "kind in ['bill_pct', 'bill_off', 'free_gift']"}, promo_code]
```
- `checkout`: at most 6 fields of the document, each a name or `{field, filter}`. Types `link`, `data`, `select`,
  `check`, `int`, `float`, `currency`; not computed, not `read_only`, not a table (`unknown_field`,
  `screen_slot_type`, Go and Python). `filter` only on a link (`screen_slot_type` otherwise) and narrows its picker
  (list filter over the linked model, AND the picker's own `active != false`).
- The web app shows the checkout fields folded to one line above the breakdown ("Pelanggan · Promo nota · Kode");
  open, each is the form's field editor (a link is a search picker). Filled values ride in the sale's body and in
  every compute call (0034 §1), so the breakdown and its errors follow them live. An empty value or an unticked check
  is left out of the body, so the model's default holds. Held bills and a sale waiting to be resent keep them.
- `line.flag`: a `check` field of the line, not computed or read-only. Each cart line shows a switch labelled with
  the field's label ("Produk gratis promo"); a flagged line is sent with the field `true` and is never merged with
  another line of the same item.
- `line.amount`: a number field of the line (may be computed). With `summary`, each cart line shows the model's
  amount from the last compute; when it is lower than `qty × price` the plain amount shows struck through, and 0
  shows "Gratis". Without it, or offline without a compute, the line shows `qty × price` as before.

### 2. `parent` in a child row's expressions
- In a child model's `default`, `compute` and `depends_on`, `parent` is the document the row belongs to:
  `parent.<field>` reads the document's value as the document's own expressions do (a link is a link, so
  `parent.outlet.code` loads the outlet). Tables of the document are left out (no cycles through siblings). A field
  of the child named `parent` wins, as fields win over globals.
- The kernel computes rows before the document, so `parent.<field>` sees the document's stored and default values,
  not its computes of this pass. Defaults of new rows run after the document's defaults.
- A row evaluated without its document (never in normal use) does not know `parent`: the expression fails and the
  compute is its zero value.
- Validators (Go and Python): `parent` is a name only in a child model's field expressions; `parent.<member>` must be
  a field (or system field) of every visible document with a table of the child (`unknown_field`).
- `engine.js` passes the document to row computes the same way, and compares a linked record by its name, as a link
  compares in the kernel.

### 3. `clock()`, `random_code()`, `int()`, `min()`, `max()`
- `clock()`: the wall clock of `now()` in the site's timezone (`timezone`, else `scheduler.timezone`, else UTC) as
  `HH:MM`, so a window compares as text: `clock() >= '14:00' && clock() < '16:00'`. A device write evaluates it at
  its `occurred_at` (0058 §10). A compute that calls it is a clock compute (recomputed on reads like `today()`), so a
  module puts time windows in flows or rules, not in money computes of documents that stay.
- `random_code(n)`: n characters (1 to 32) of the pairing-code alphabet (no 0/O, 1/I/L; 0006 §2) drawn from
  `crypto/rand`, for codes a flow creates (single-use coupons) that must not be guessable from the record's name or
  time. A new value on every call, so both validators refuse it in a field's `default`, `compute` or `depends_on`
  (`flow_only_function`); `engine.js` does not evaluate it.
- `min`, `max` were evaluated by the kernel but missing from the validators' function list; they are listed now.
- `engine.js` evaluates the dialect a cashier device needs for these computes: `c ? a : b`, `%`, `int()`, `min()`,
  `max()`, `clock()` (the device's clock, or `META.clock`), and the string methods `upperAscii`, `lowerAscii`,
  `trim`, `contains`, `startsWith`, `endsWith`. A shared fixture (`web/app/test/fixtures/cel-parity.json`) runs in
  both `cel_parity_test.go` and `engine.test.js`. Integer division stays a known difference (the kernel divides two
  ints as ints): modules wrap a quotient in `int()` over a float field instead. The rest of the `pos_sale` dialect
  (`replace`, `matches`, `in`) remains 0058 work item 4b.

### 4. Data sets for paired devices
- The models a counter's `checkout` link fields name (a promotion, a customer) are data sets of the device
  (0058 §7), like the catalogue. A `data` field (a coupon code) is not: a coupon is checked by a flow on the server,
  so a coupon sale made offline is refused or becomes an exception when it replays (0058 §10).

## What stays out
- A per-field link filter in model metadata (`model.json`): the filter lives on the screen slot.
- Line-level `set:` values and line fields other than item, qty, note and flag.
- Promo logic in the kernel: promos are a rangka-apps module (`promotions`), computed by the model.

## Contract changes (rangka-os)
1. Schema 0.5 `screen.json`: `counter.document.checkout`, `counter.document.line.flag`, `counter.document.line.amount`;
   `make sync-schemas`.
2. Validators (Go and Python, `make compare-python`): the three slots; `parent` in child expressions; functions
   `min`, `max`, `clock`, `random_code` (`flow_only_function` in field expressions).
3. Kernel: `parent` in the child scope (`engine/scope.go`), `clock()` and `random_code()` (`cel/eval.go`, `Env.Loc`), clock computes
   include `clock()`; edge data sets include checkout link targets (`api/edge_data.go`).
4. Web app: `counter.js` (checkout box, line flag switch, line amount), `fields.js` (link picker filter), `engine.js`
   (dialect above), `app.css`; tests `counter.test.js`, `engine.test.js`, `cel_parity_test.go`.
5. Docs: `docs/contracts.md` CEL dialect, `docs/pos-coverage.md` row 32.
