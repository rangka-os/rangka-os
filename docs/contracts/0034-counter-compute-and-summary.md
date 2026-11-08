# 0034 Counter: dry compute, summary and availability

Status: proposed (8 Nov 2026) · Owner of the change: rangka-os (kernel, schema, validators, web app) · Affects:
rangka-apps (`modules/pos` may name the new slots) · Builds on 0027, 0030 · Slice A of `docs/pos-coverage.md`

## Context
- The POS design (`rangka.pen`, "Rangka POS · Tablet & HP") shows the bill breakdown while the cashier is still
  adding items: subtotal, promo, PB1, rounding, total. The counter of 0030 (WS3) can only show its own
  `qty × price` sum, marked as an estimate. 0030 §2 forbids a screen to compute prices, tax or totals.
- The model already knows how to compute all of this (computes, defaults, `settleMoney`), but only when it saves a
  record. Saving a draft per tap would take numbers, run flows, fill the audit log and leave drafts behind.
- The design also shows what is left of an item ("Sisa 4", "Habis"). The catalogue slot has no field for it.

## Decision

### 1. `POST /api/v1/compute/{model}`
- Body: the record as it would be sent to `POST /api/v1/r/{model}` (tables included).
- The kernel runs, inside a transaction that is always rolled back: hidden fields dropped (0027), normalise,
  defaults, computes of the rows and the document, money settling, the caller's `create` permission, row scope
  (`Guard`), `on: save` rules and `min_rows`.
- Answer `200 {record, errors, warnings}`. `record` is the record as `create` would store it, without `name`, with
  fields hidden from the caller removed. Failed save rules and `min_rows` are issues in `errors`, not a `422`,
  because an unfinished cart is not a mistake.
- Errors stay errors where a create would fail before rules: unknown model `404`, a child model `400`, a value of
  the wrong type `422`, no `create` permission `403`, outside the row scope `403`.
- Nothing is written: no number is taken, no hook, flow, automation, event, audit entry or idempotency key.
  There is no `Idempotency-Key` handling; the call can be repeated freely.
- `submit` rules are not run (the cart is not submitted yet); the real submit still checks them.

### 2. Counter slot `document.summary`
```yaml
document:
  model: pos_sale
  total: grand_total
  change: change_amount
  summary: [subtotal, discount_total, tax_amount, rounding, grand_total]
```
- At most 8 number fields of the document (`unknown_field`, `screen_slot_type`, Go and Python).
- The web app sends the cart to §1 (debounced, and again before the tender opens) and shows those fields in
  order under the cart; the last one is the total line and the "Bayar" button shows it. A field the answer lacks
  (hidden from the cashier) is skipped.
- Without `summary`, or while the compute call fails (offline), the counter shows its own sum labelled "perkiraan",
  as in WS3. The sale itself is never blocked by a failed compute.

### 3. Catalogue slot `available`
- A number field of the catalogue model: what is left to sell (stock on hand or "bisa dibuat" from a recipe,
  computed by the module).
- The card shows "Sisa N" at 5 or less and "Habis" at 0 or less, muted. It never blocks a sale: stock rules
  (0006 §5, 0021) decide, the screen does not.
- Checked by both validators as a number field (`unknown_field`, `screen_slot_type`).

## What stays out
- No compute endpoint for updates (`PATCH`); the counter only creates. Add it when a screen needs it.
- No caching or rate limit beyond the normal API limits.
- Modifiers, tables and PB1 are not in this contract (slices B–D).

## Contract changes (rangka-os)
1. Kernel: `Engine.Compute`, route `POST /api/v1/compute/{model}`.
2. Schema 0.5 `screen.json`: `counter.document.summary`, `counter.catalogue.available`; `make sync-schemas`.
3. Validators (Go and Python, `make compare-python`): both slots.
4. Web app counter: breakdown from §1, availability badges, layout per the design (tablet rail, cards, keypad,
   portrait and phone).
