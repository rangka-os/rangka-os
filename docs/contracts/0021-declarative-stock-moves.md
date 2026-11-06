# 0021 Declarative stock moves

Status: implemented (6 Nov 2026) · Proposed by: Zein · Owner of the change: rangka-os (kernel, schema,
validator) · Affects: rangka-apps (new stock documents can be added without a kernel release; existing documents
unchanged)

## Context

`stock.post_moves` (`postStockMoves`, `kernel/internal/engine/stock.go:157-287`, switch from line 176) is a Go `switch` on the model name with six cases:
`goods_receipt`, `stock_transfer` (one or two step), `stock_count`, `stock_adjustment`, `stock_transfer_receipt` and
`outlet_sales`. Any other model gets `"stock.post_moves has no rule for <model>"` and moves nothing. Every new
document that moves stock therefore needs a kernel change and a release before an app can use it.

The next app documents are exactly that:

| Document | Direction | Needed by |
|---|---|---|
| Delivery note (surat jalan) | out of a warehouse or vehicle | wholesale |
| Sales return | in, at the cost it left at; damaged goods into a quarantine outlet | wholesale, retail, F&B |
| Purchase return | out | wholesale, retail |
| Production result | materials out, finished goods in at the value of those materials | manufacturing, F&B central kitchen |
| Cashier sale | out, through the recipe, with cogs | retail and F&B cashier |
| Medicine dispensing | out | clinic |

Today `sales_invoice` and `customer_order` never move stock, so a wholesaler cannot run its core loop at all. The
manufacturing workaround (consume with `stock_adjustment`, raise the product with `stock_count`) values the new units
at the average or `standard_cost`, not at the materials used, so its HPP is wrong.

What the six cases have in common is small: for each line with a quantity, take the item out of (or put it into) an
outlet named by a field, value it at the average or at a given value, and write `stock_move` + `stock_balance`
through `stockPost.move`. Locking (`lockAll`), moving average, rounding, the negative flag and `stock.reverse_moves`
are already generic. Only the "which lines, which direction, which outlet, which value" part is hard-coded.

## Decision

### 1. A `stock:` block on a submittable model

A model declares how its submit moves stock. The model keeps `on_submit: [stock.post_moves]` and
`on_cancel: [stock.reverse_moves]`; the hook reads the block instead of a `switch` case.

```yaml
stock:
  date: delivery_date            # header field for posting_date; default the model's first required date field
  moves:
    - lines: items               # a table field of the model
      out: outlet                # header field (link to outlet), or line.<field>
      reason: delivery           # must be an option of stock_move.reason
      cogs: cogs                 # optional: line field (read_only currency) that gets the value taken
  cogs_total: cogs_total         # optional: header field that gets the sum of all `cogs`
```

Keys of one `moves` entry:

| Key | Meaning |
|---|---|
| `lines` | Required. Table field whose rows move. |
| `in` / `out` | Exactly one. The outlet: a header field, or `line.<field>` for a per-line outlet. |
| `item` | Line field with the item. Default `item`. |
| `qty` | Line field with the quantity in stock uom. Default: `stock_qty` when the line model has it, else `qty` (today's `lineQty`). |
| `if` | Optional CEL, evaluated per line with `doc` and `line`. False skips the line. |
| `reason` | Required. Written to `stock_move.reason`. |
| `explode` | `recipe` consumes each line through the item's active recipe (today's `consumption`), as `outlet_sales` does. Out only here; on an `in` move it brings the materials back (contract 0022 §4). |
| `cost` | `in` only. How the incoming value is set (§2). Default `average`. |
| `cogs` | `out` only. Line field that gets the value taken out by that line. |

A line with no quantity (`<= qtyEps`) is skipped, as today.

### 2. Valuation of incoming moves (`cost`)

Outgoing moves are always valued at the outlet's moving average (`item.standard_cost` when there is none), exactly as
`stockPost.out` does now. Incoming moves pick one of:

| `cost` | Value of the move | Use |
|---|---|---|
| `average` | `round(qty × avg)` of the receiving outlet (standard cost if none) | stock found, gifts |
| `{unit: <line field>}` | `round(qty × field)` | manual unit cost |
| `{amount: <line field>}` | the field as a fixed value | goods receipt (today's `amount` rule) |
| `{source: <header link field>}` | the unit cost of the `stock_move`s that the linked document posted for the same item (their mean, `round(Σ value / Σ qty)`); falls back to `average` when the source has no move for the item | sales return from a delivery note or cashier sale |
| `{consumed: true, split: qty \| <line field>}` | the sum of the values the `out` entries of **this** document took, split over the incoming lines by qty or by a share field; the last line takes the remainder so the values add up to the rupiah | production result |

### 3. Order of posting

1. Evaluate every entry and line, build the list of (outlet, item) keys, and `lockAll` them in sorted order before any
   move (same deadlock rule as today).
2. Post all `out` entries in file order, then all `in` entries in file order. `consumed` needs the out values first.
3. Write `cogs` lines and `cogs_total` with one `SetSubmitted`, as `postOutletSales` does.

### 4. Validator checks (load time)

- `stock:` only on a submittable model whose `on_submit` has `stock.post_moves`.
- `lines` is a `table` field; `in`/`out` name a link to `outlet` on the header or on the line model; `item` is a link
  to `item`; `qty` is a number.
- Exactly one of `in`/`out`. `cogs` only with `out`; `cost` only with `in`. `explode` was `out` only here; 0022 §4 allows it
  on `in` with cost `average` or `{source}`.
- `reason` is an option of `stock_move.reason` in the installed stock module.
- `cogs` / `cogs_total` are `read_only` currency fields.
- `{source: f}`: `f` is a link to a model that itself posts stock. `{consumed: …}`: the document has at least one
  `out` entry.
- `explode: recipe` without a `recipe` model: warning (each item then consumes itself).
- A model named like one of the six legacy cases that also has `stock:` is an error (one rule per model).

`stock` is a key of `model.json` in schema 0.5. It is not written under `experimental:`: the validators never implemented that block, and the kernel release that reads the key is the same release whose schema accepts it. An app that declares `stock:` needs that release; an older kernel refuses the unknown key at validate time instead of silently moving nothing.

### 5. Reversal, refusal, negative stock

- `stock.reverse_moves` is unchanged: it already reverses every `stock_move` with the document as voucher, at the
  original unit cost, dated the day of the cancel. It works for every declared document with no new code.
- Declared moves never refuse for lack of stock, like today; the `negative` flag and `stock_negative_alert` apply.
- `cogs` and `cogs_total` are kept on cancel (history), like `outlet_sales`.

## Compatibility with the six existing models

- The six models keep their Go code path and need no `.rk` change. `stock.post_moves` looks for `stock:` first and
  falls back to the `switch` otherwise, so an app pinned to an older tag sees no difference.
- `stock_count` (reads the balance and writes `system_qty`/`difference`), the two-step `stock_transfer` /
  `stock_transfer_receipt` pair (state, `received_qty`, `over_receipt`) and the `outlet_sales` duplicate check stay
  in Go: they are state machines, not move rules.
- `goods_receipt`, `stock_adjustment` and the one-step `stock_transfer` can be expressed as declarations
  (examples A.1). Step two, optional: re-express them in rangka-apps and keep the Go cases only as the fallback,
  with a test that the declared and the Go version post identical moves on the current fixtures.
  `receivedOnPO` stays a Go side effect of `goods_receipt` either way.
- `stock_move.reason` is a `select` in `modules/stock/models/stock_move.rk`. New reasons (`delivery`,
  `return_in`, `return_out`, `production_in`, `production_out`, `dispense`) are added there by rangka-apps; the
  kernel only checks against what the model lists.
- `CoreHooks` and hook names do not change.

## Impact on the ledger and HPP

- There is no inventory journal in the kernel today: `ledger.go` handles receivables, payables and allocation only.
  HPP lives in `stock_move.value` (moving average) and in `cogs` fields. This contract adds no journal.
- Every declared out move is valued like today's outlet sale, so `cogs` on a delivery note or cashier sale is the
  same HPP the F&B dashboard already reads; margin = net − cogs works per document.
- A sales return with `cost: {source: …}` comes back at the cost it left at, so HPP of the period is reduced by
  exactly what the original sale charged. Coming back at today's average instead would move the difference into
  the stock value and change the average for every later sale.
- A production result with `cost: {consumed: …}` puts finished goods in at the value of the materials used (plus
  nothing else in V1; labour and overhead are K-r/B15). This replaces the adjustment + count workaround.
- **One document per chain moves stock.** If both a delivery note and its invoice declared `out`, stock would leave
  twice. The app guards it with `if`, for example on `sales_invoice`: `if: doc.delivery_note == ''` (invoice moves
  stock only when sold without a delivery note). The kernel cannot know the chain; this is a rule for app authors,
  listed in `website/concepts/rules.md`.
- A later accounting connector (Accurate, plan X7) can read `stock_move` grouped by `voucher_type` and `reason` to
  build HPP and inventory journals, without a kernel change.

## Examples

### A.1 Existing documents written as declarations (for the equivalence test)

```yaml
# goods_receipt
stock:
  date: received_on
  moves:
    - {lines: items, in: outlet, reason: receipt, cost: {amount: amount}}

# stock_adjustment (reason waste → move reason waste needs two entries)
stock:
  date: adjustment_date
  moves:
    - {lines: items, out: outlet, reason: waste, if: "doc.reason == 'waste'"}
    - {lines: items, out: outlet, reason: adjustment, if: "doc.reason != 'waste'"}
```

### A.2 Delivery note (surat jalan)

```yaml
name: delivery_note
on_submit: [stock.post_moves]
on_cancel: [stock.reverse_moves]
stock:
  date: delivery_date
  moves:
    - lines: items
      out: outlet              # warehouse, or the vehicle outlet for canvassing
      reason: delivery
      cogs: cogs
  cogs_total: cogs_total
```

### A.3 Sales return (retur jual)

Good items back into the warehouse at the cost they left at; damaged items into a quarantine outlet.

```yaml
name: sales_return
stock:
  date: return_date
  moves:
    - lines: items
      in: outlet
      if: "line.condition == 'good'"
      reason: return_in
      cost: {source: delivery_note}
    - lines: items
      in: quarantine_outlet
      if: "line.condition == 'damaged'"
      reason: return_in
      cost: {source: delivery_note}
```

A purchase return is the mirror: `out: outlet`, `reason: return_out`, valued at the average.

### A.4 Production result (hasil produksi)

```yaml
name: production_result
stock:
  date: production_date
  moves:
    - lines: materials         # what the batch actually used
      out: outlet
      reason: production_out
    - lines: outputs           # finished goods and by-products
      in: outlet
      reason: production_in
      cost: {consumed: true, split: cost_share}   # cost_share: percent per output line
```

Example: 10 kg flour (avg 12.000) and 2 kg butter (avg 90.000) out = Rp 300.000. Output 100 pcs bread
(share 100%) comes in at Rp 300.000, unit cost 3.000.

### A.5 Cashier sale (penjualan kasir)

```yaml
name: pos_sale
stock:
  date: sale_date
  moves:
    - lines: lines
      out: outlet
      explode: recipe          # same consumption as outlet_sales
      reason: sale
      cogs: cogs
  cogs_total: cogs_total
```

The difference with `outlet_sales` is only the duplicate check, which a cashier sale (one receipt) does not need.

## Alternatives rejected

1. **One Go case per new document (the status quo).** Every delivery note, return or production model waits for a
   kernel release; app models cannot be renamed or split without touching the kernel; the AI builder cannot create
   a stock document.
2. **One generic `stock_entry` document that other documents convert into** (ERPNext style). Two documents per
   business event, two cancel paths, and the owner sees a technical document they did not ask for. It also does not
   solve cogs on the business document.
3. **Moves written by flows or automations** (`create stock_move`). Bypasses locking order, moving average,
   rounding and reversal; `stock_move` and `stock_balance` are read-only for every role on purpose (contract 0010 §5).
4. **Hook arguments instead of a block** (`on_submit: [{hook: stock.post_moves, args: …}]`). Same expressiveness,
   but the rule is then not a schema-checked part of the model, and the Console and builder cannot show it.
5. **Per-app Go or WASM plugins.** There is no plugin runtime, and it gives up the "apps are `.rk` only" rule.

## Contract changes (rangka-os)

- Schema: `stock` property on `model.json` (next schema version), with the keys of §1 and §2.
- Validator: the checks of §4.
- Kernel: `postStockMoves` reads `stock:` when present; new `postDeclaredMoves` built on `stockPost` (`lockAll`,
  `move`, `out`, `consumption`); `{source}` reads `stock_move` by `voucher_type`/`voucher`; `{consumed}` uses the
  transfer-receipt remainder rule.
- Docs: `website/concepts/rules.md` gets the declared rule and the "one document per chain" note.
- No change to `stock.reverse_moves`, `CoreHooks`, `stock_move` or `stock_balance` fields.

## Out of scope (other plan items)

- Order → delivery note → invoice → return conversions and "returned qty" on the source (K-p, B16). `{source}` here
  only reads cost; the conversions belong in a separate contract next to `conversions.go`.
- Lot, serial, expiry and bin dimensions (K-q, B13). A later `lot: line.<field>` key on a move entry is reserved.
- Labour and overhead in production cost, multi-level BOM (K-r, B15).

## Decisions on the open points (6 Nov 2026)

1. Key and version: a plain `stock:` key on `model.json` 0.5 (see §4), no `experimental:` block.
2. CEL in `if`: `doc` (the header) and `line` (the row) only inside the `stock:` block. Header fields are also
   visible by name, as in other model expressions.
3. `negative: refuse` is a key of the block (default `allow`, today's behaviour). A refused submit returns 409
   `negative_stock` and moves nothing.
4. Return cost: the source document's unit cost (`round(Σ|value| / Σ|qty|)` of its non-reversal moves for the
   item); the receiving outlet's average when the source posted none.
5. `stock_move.reason` stays a `select`; the validator checks every `reason:` against its options.
6. The Go rules stay. `goods_receipt`, `stock_adjustment` and `outlet_sales` are proven equivalent to their
   declarations (A.1, plus `outlet_sales` with `explode: recipe` and `cogs`) by
   `TestDeclaredMovesEquivalent`: the same script in two orgs gives identical moves, balances, cogs and
   reversals. The one-step `stock_transfer` is not expressible move for move (its in value is the value the same
   line took out), so it stays Go only, like `stock_count` and the two-step pair.
7. Conversions and returned quantities: a separate contract (K-p), `0022-unit-conversion-and-returned-qty.md`.
8. A declared `sale` move sets `stock_balance.last_sale_date` like the Go rules, because it goes through the same
   `stockPost.move`.

9. After the audit (reports `2026-11-06-kernel-k-o`): all `consumed` rules of a document share one pool (a
   by-product table does not take the whole value again); a `{source}` that is not submitted is refused
   (`source_not_submitted`), and a source that consumed the item through a recipe gives its cogs per unit; a line
   with an empty outlet or item is refused (`stock_outlet_missing`, `stock_item_missing`); a value taken out with
   no consumed line to hold it is refused (`consumed_nowhere`); a `datetime` date posts on the organisation's
   local date; `if` is checked by the validator (`doc` and `line` members) and a non-bool result is 422
   `stock_if`; `{source}` must link to a document that moves stock; warnings `no_recipe` and
   `cogs_total_unused`.

## Implementation

- Schema: `stock` and `$defs/stock_rule` in `schema/0.5/model.json`.
- Kernel: `kernel/internal/engine/stock_declared.go` (`stockSpecOf`, `postDeclaredMoves`); `postStockMoves`
  uses it for any model with a `stock:` block that is not one of the six built-in documents.
- Validators: `meta/validate_stock.go` and `check_stock` in `tools/rangka_validate.py`, same order and messages.
  Codes: `stock_hook`, `stock_legacy`, `stock_direction`, `stock_consumed`, `bad_stock_field`, `not_table`,
  `unknown_field`, `unknown_option`. `if` is checked like other expressions, with `doc` (the header) and `line` (the row) as links.
- Tests: `engine/stock_declared_test.go` (equivalence; delivery note with cogs; sales return at source cost and
  into quarantine; production at the value of the materials, split by share; `negative: refuse`; cancel),
  `kernel/testdata/stock-apps` and `broken-stock-apps` in `make compare-python` and a golden file.
