# 0070 Lots and serial numbers on stock moves

Status: implemented in rangka-os (19 Nov 2026, kernel, schema, validators, GS1 parser: slice a of rangka-os#260);
the stock module models and declarations are rangka-apps work (slice b) · Proposed by: Zein · Owner of the change:
rangka-os (kernel, schema 0.5, validators, web app) · Affects: rangka-apps (catalogue `item.tracking`, stock
registers, line fields and `stock:` declarations of every document that moves stock) · Builds on: 0010 §5, 0021,
0022, 0026, 0030 §4 (WS5 scan), 0058 · Fills the `lot:` key 0021 reserved ("Out of scope", K-q / B13)

## Context

Stock in Rangka has one dimension: outlet and item (`stock_balance`), valued at a moving average (0021). Expiry
exists only on `production_batch`, so a dashboard of batches that expire soon lists batches whether or not any of
them is still in stock. Serial numbers exist only on `rental_unit`, outside stock. Pharmacies, clinics, wholesalers of
food and FMCG, manufacturers and anyone selling serialised goods need to know which lot is where, sell the lot that
expires first, refuse a serial that is not on the shelf, and trace a lot after a recall. Competitors sell this as a
standard tier feature.

This contract adds lot and serial as **quantity dimensions** of stock. Value does not change: it stays per outlet and
item at the moving average, so every report, HPP figure and flow that reads `stock_move.value`, `stock_balance` or
`cogs` keeps its meaning.

## Decision

### 1. Item tracking

The kernel reads `item.tracking`, the way it reads `item.standard_cost` and the active recipe:

| `tracking` | Meaning |
|---|---|
| empty or `none` | Moves exactly as before: no lot rows, no serial checks. |
| `lot` | Quantity per lot. An incoming line with no lot number goes to the no-lot bucket. |
| `lot_expiry` | `lot` plus an expiry date per lot; an incoming line must name its lot (`lot_required`). |
| `serial` | One `serial_no` per unit; whole quantities only. |

The dimensions switch on only when the registry has the registers of §2 (lots: `stock_lot` with `item`, `lot_no` and
`stock_lot_balance` with `outlet`, `item`, `lot`, `qty`; serials: `serial_no` with `item`, `serial`, `state`,
`outlet`), `stock_move` has the matching `lot` / `serial_nos` field (§2), and `item` has a `tracking` field. An app
without them moves stock as today.

### 2. Kernel-written registers

Shipped by the stock module, read-only for every role, written only by the stock hooks (like `stock_balance`, 0010 §5;
they are not `append_only` because hooks update them):

- `stock_lot`: one per item and lot number, created by the first incoming move with that number. Fields the kernel
  writes: `item`, `lot_no`, `expiry_date`, `made_date`, `first_voucher_type`, `first_voucher` (the optional ones only
  when the model has them). `notes` may be editable by head office.
- `stock_lot_balance`: `qty` per (`outlet`, `item`, `lot`). The row with an **empty lot is the no-lot bucket**: the
  kernel always writes it as `stock_balance.qty` minus the real lots of that outlet and item, so stock from before
  tracking was switched on, or moved by a document that does not carry lots, sits there. The row is created when it
  is first not zero.
- `serial_no`: one per (`item`, `serial`): `state`, `outlet`, and when the model has them `prev_state`, `lot`,
  `last_voucher_type`, `last_voucher`, `in_date`, `out_date`. States: `in_stock`, `in_transit`, `sold`, `returned`
  (to the vendor), `consumed`, `scrapped`, `cancelled` (the receipt that brought it in was cancelled).
- `stock_move` gains `lot` (link `stock_lot`, empty for the no-lot bucket) and `serial_nos` (text, one per line).

### 3. Move entry keys (schema 0.5 `stock_rule`)

```yaml
stock:
  moves:
    - lines: items
      in: outlet
      reason: receipt
      cost: {amount: amount}
      lot: {no: lot_no, expiry: expiry_date, made: made_date}   # in: the line's lot number and dates
      serials: serial_nos                                     # line text field, one serial per line
    - lines: items
      out: outlet
      reason: delivery
      cogs: cogs
      lot: {pick: lot}          # out: optional line link to stock_lot; empty = FEFO
      serials: serial_nos
      expired: skip             # out only: skip (default) | allow | refuse | first
```

Validator (Go and Python, same order and messages), per entry:

- `lot.no` is a `data` field of the line model, `lot.expiry` and `lot.made` are `date` fields, `lot.pick` is a link to
  `stock_lot`, `serials` is a `text` (or `data`) field: `bad_lot_field`; a name that is not a field: `unknown_field`.
- `lot.pick` is for `out` moves only, `lot.no`/`expiry`/`made` for `in` moves only, `expired` for `out` moves only,
  and `lot` or `serials` cannot go with `explode: recipe` (they name the line's own item, not its materials):
  `lot_direction`.
- `lot:` without a `stock_lot` model, `serials:` without a `serial_no` model: warnings `no_lot_model`,
  `no_serial_model`.
- `expired` outside its four values: schema error.

A rule without `lot` still moves lot items through lots (FEFO on out, the no-lot bucket on in) and refuses nothing for
lots; a rule without `serials` moves serial items by quantity and leaves the register alone. So an app pinned to an
older declaration keeps working, and lot balances stay true.

### 4. Incoming moves

For a lot item, the line's lot number finds or creates `stock_lot` (`item`, `lot_no`), under a lock on that pair.

- An existing lot with another expiry is refused `409 lot_expiry_mismatch` ("Lot <no> sudah tercatat kedaluwarsa
  <date>, bukan <date>"). A line with no expiry takes the lot's.
- A new lot of a `lot_expiry` item without an expiry is refused `409 expiry_required`.
- No lot number: a `lot_expiry` item is refused `409 lot_required` when the rule declares `lot` (so expiry control
  cannot be bypassed); otherwise the quantity goes to the no-lot bucket.
- A return (`cost: {source: …}`) with no lot number goes back into the lot its source took for that item when the
  source took exactly one real lot and nothing from the bucket; else into the bucket (a return is not refused for a
  missing lot).

The move is one `stock_move` with `lot` set; `stock_lot_balance` of the lot gets the quantity. Value, `stock_balance`
and the average are exactly as before.

### 5. Outgoing moves and FEFO (deterministic)

For a lot item and a line with no `lot.pick`:

1. read the `stock_lot_balance` rows of (outlet, item) with `qty > 0`, under the `stock_balance` lock of the pair;
2. order by `expiry_date` ascending (no expiry last), then the lot's creation, then lot number;
3. under `expired`: `skip` (default) drops lots with `expiry_date < posting_date`; `first` orders expired lots first
   (an adjustment for expired or wasted goods); `allow` keeps them in FEFO order; `refuse` answers `409 lot_expired`
   when the fresh lots plus a positive no-lot bucket do not cover the line;
4. take from each lot until the line is covered; the rest comes out of the no-lot bucket, which may go negative with
   the item (flagged `negative`, the same alert as today). Stock is never refused for quantity (0021 §5).

With `lot.pick` the kernel takes that lot only (any `expired` mode but `refuse` takes an expired pick); more than its
balance takes the rest from the bucket. A pick of another item's lot is refused `409 lot_wrong_item`.

One line therefore writes one `stock_move` per lot, same voucher and reason. **Value split to the rupiah:** the line's
value is what one move would take, `round(qty × avg)` (standard cost when there is no average); each part is
`round(part_qty × avg)` and the last part takes the remainder, so the parts add up to the single move's value, `cogs`
and `cogs_total` are unchanged, and the balance keeps the average one move would keep when it does not stay above zero.
`TestLotsFEFOValueSplit` posts the same receipts and delivery for a lot item and an untracked twin and compares.

### 6. Serials

For a serial item on a rule that declares `serials`:

- the line quantity must be a whole number equal to its count of distinct serials (`409 serial_count_mismatch`; a
  serial written twice: `409 serial_duplicate`);
- in: each serial must not be `in_stock` or `in_transit` anywhere (`409 serial_in_stock`); a transfer receipt takes
  only serials in transit (`409 serial_not_sent`); the serial is created or reopened `in_stock` at the outlet;
- out: each serial must be `in_stock` at that outlet (`409 serial_not_here`, a hard refusal: a serial is an identity,
  not a quantity); the state follows the reason: `transfer_out` → `in_transit`, `production_out` → `consumed`,
  `return_out` → `returned`, `waste`/`adjustment`/`count` → `scrapped`, any other (`delivery`, `sale`, …) → `sold`.

Serial items are not lot-tracked in v1 (`serial_no.lot` is reserved).

### 7. Go-rule documents

- `goods_receipt` and `stock_adjustment` may now declare `stock:` (0021 decision 6 proved them equivalent); the
  declaration wins and the Go rule stays the fallback. `receivedOnPO` stays a Go side effect of a declared
  `goods_receipt`. `stock_transfer`, `stock_transfer_receipt`, `stock_count` and `outlet_sales` stay Go only
  (`stock_legacy` still refuses `stock:` on them).
- Without a declaration the Go rules read conventional line fields when the line model has them: `lot_no`,
  `expiry_date`, `made_date` (in), `lot` (out, link `stock_lot`), `serial_nos`. A `stock_adjustment` with reason
  `expired` or `waste` takes expired lots first.
- `stock_transfer` out picks lots by FEFO (or the line's `lot`) like any out move and records them on its moves, with
  the line's serials going `in_transit`. `stock_transfer_receipt` brings in the lots that left, earliest expiry first
  (the bucket part last); a partial receipt takes the earliest-expiring sent lots, the receipt that takes the
  remainder takes exactly what is left per lot. Each receipt line's value is split over its lots like §5. Serials:
  the receipt line names the serials that arrived (each one the transfer sent and still in transit), or, when it
  takes the item's whole remainder, gets every serial still in transit; a partial line of a serial transfer with no
  serials is refused `serial_count_mismatch`. A one-step transfer brings the same lots and serials in.
- `stock_count`: for a lot item whose line model has `lot_no`, a line counts one lot: `system_qty` is that lot's
  quantity (0 for a lot not seen before, which the incoming difference creates like an incoming lot), and the
  difference posts into that lot only; a line with no lot number counts the no-lot bucket. Lots not on the count are
  untouched, so a full count of an item lists every lot and the bucket. Serials are not counted in v1.
- `outlet_sales` (a POS file, the record of sales that happened) takes out through FEFO with `expired: allow`.

### 8. Reversal

`stock.reverse_moves` reverses move by move as before; each reversal carries the move's `lot` and `serial_nos`, so lot
balances and serial states return exactly. Serials are undone last move first (a one-step transfer's `transfer_in`
before its `transfer_out`): reversing an out move needs the serial still in the state that move left it in and puts
it back `in_stock`; reversing an in move needs it still `in_stock` at that outlet and sets `in_transit` (a transfer
receipt), its `prev_state` when this document moved it last (a cancelled return makes it `sold` again), or
`cancelled`. A serial that has moved on since is refused `409 serial_moved`: cancel the later document first. A
cancelled receipt whose lot has since been sold drives that lot negative, like item stock today.

### 9. Device writes (0058)

A device write is never refused for stock. `lot_expired`, `lot_required`, `expiry_required`, `lot_expiry_mismatch` and
the serial refusals become exceptions of kind `stock_<code>` with the item, outlet and lot or serial in the detail;
the move posts by quantity (the no-lot bucket, the register untouched for that serial). The same body over REST is
refused.

### 10. GS1 scan

`web/app/scan.js` gains a deterministic GS1 element-string parser next to `cleanCode`/`eanValid`:
`RangkaApp.parseGS1(code)` reads AI (01) GTIN-14 (check digit verified), (10) lot, (11) production date, (15) best
before, (17) expiry (YYMMDD, day 00 = last day of the month), (21) serial, from the printed form `(01)…(17)…(10)…` or
the scanner form (`]C1`, `]d2`, `]Q3` symbology identifiers, GS after a variable field, or bare digits starting
`01` and longer than a GTIN). It returns `null` for codes that are not GS1 strings (a plain EAN, a unit code, a QR with
text). `RangkaApp.gs1Line(parsed)` gives the line fields: `barcode` (the GTIN-14 without leading zeros, as printed on
the pack), `lot_no`, `expiry_date`, `made_date`, `serial_nos`. The scanner dialog passes the parsed AIs as a second
argument of `onCode`; DataMatrix joins the formats it reads. A keyboard scanner that drops GS cannot split (10) from a
following AI; the printed form always parses. No Edge release is needed.

## Decisions on the open points (19 Nov 2026, autonomous, see DECISIONS.md)

1. `serial_not_here` is a hard `409`; a site flag to relax it can come later. Device writes record an exception
   instead (§9).
2. Moving average per outlet and item stays; no per-lot cost in v1. Every report and GL flow reads the outlet average.
3. `expired: refuse` on cashier sales is a site override of the sale's declaration (`custom/pos_sale.rk` with
   `expired: refuse`), not an item flag; the kernel offers the mode, the app chooses.
4. `rental_unit` and the stock serial register stay two registers, linked by `rental_unit.stock_serial` when both
   modules are installed; rental checkout and return do not move stock.
5. The GS1 parser ships in v1 (§10).

## Alternatives rejected

1. **Per-lot cost (FIFO or specific identification).** Doubles the kernel work and changes every HPP figure; deferred.
2. **A lot dimension on `stock_balance` itself** (one row per outlet, item, lot). Every reader of `stock_balance`
   (reports, dashboards, flows, `last_sale_date`) would have to sum rows; the separate register keeps them unchanged.
3. **An explicit no-lot row maintained by every move** instead of the derived bucket. It drifts when tracking is
   switched on for an item with stock, or when a document without lots moves a lot item; deriving it from
   `stock_balance` keeps the invariant `Σ stock_lot_balance = stock_balance.qty` by construction.
4. **Refusing an out move that lots do not cover.** Contradicts 0021 §5 (stock follows the business, negative is a
   flag) and 0058 (devices are never refused).
5. **Serial checks as flags like negative stock.** A serial sold twice is a data error, not a timing issue; refusal
   keeps the register true (device writes excepted).

## Not in this contract

- The stock module models, line fields on the ten line models, the declarations of `goods_receipt`,
  `stock_adjustment`, `delivery_note`, `sales_return`, `production_batch`, `pos_sale`, `outlet_return`, the
  `lot_expiry_digest` flow, the `lot_trace` print, dashboards and fixtures (rangka-apps, slice b of rangka-os#260).
- Lots on serial items, serial counts, warranty and service history, BPOM / SATUSEHAT formats, bins (P2-bin_locations).
- Lot columns on `outlet_sales` files (POS files have none; FEFO applies).

## Implementation

- Schema: `lot`, `serials`, `expired` in `$defs/stock_rule` of `schema/0.5/model.json` (kernel copy synced).
- Kernel: `kernel/internal/engine/stock_lots.go` (tracking, `lotFor`, the derived bucket `syncBucket`, `fefo`,
  serial register, `inLine`/`outLine`/`inParts`, `postStockCount`, `serialsReverse`); `stock.go` (`moveX` with the
  move's lot, serials and kept average; Go rules through `inLine`/`outLine`; reversal); `stock_declared.go`
  (`declaredSpec`, `DeclarableLegacy`, rule keys, `sourceLot`); `transfers.go` (`transferLots`, receipt lot and serial
  following).
- Validators: `stockLots` in `meta/validate_stock.go` and `check_stock_lots` in `tools/rangka_validate.py`; codes
  `bad_lot_field`, `lot_direction`, warnings `no_lot_model`, `no_serial_model`; `stock_legacy` no longer applies to
  `goods_receipt` and `stock_adjustment`.
- Tests: `engine/stock_lots_test.go` (`TestLotsFEFOValueSplit`, `TestLotsExpiredModes`, `TestLotsNoLotBucket`,
  `TestLotsTransferFollowsLots`, `TestLotsCountPerLot`, `TestSerials`, `TestLotsReturnIntoLot`,
  `TestLotsReverseEverything`, `TestLotsDeclaredGoodsReceipt`, `TestLotsDeviceWriteNeverRefused`);
  `kernel/testdata/stock-apps` (registers in `stock`, declared documents in `lots`) and `broken-stock-apps`
  (`lot_broken`) in `make compare-python` and the golden file; `web/app/test/scan.test.js`.
