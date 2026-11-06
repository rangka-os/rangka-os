# 0022 Unit conversion and returned quantities

Status: implemented (6 Nov 2026) · Proposed by: Zein · Owner of the change: rangka-os (kernel, schema, validator) ·
Affects: rangka-apps (cashier refunds of retail and F&B can bring goods back; no change to existing documents)

## Context

Contract 0021 left two things out (its "Out of scope" and open point 7, K-p): converting quantities between units
along a chain, and the "returned qty" a return writes on its source. The first app documents that need them are the
returns of the three commerce apps:

| App | Sale document | Return | What has to be known |
|---|---|---|---|
| Wholesale (`grosir_distribusi`) | delivery note | `sales_return` | delivered minus already returned, per item of the order |
| Retail (`ritel_cabang`) | `outlet_sales` (POS file, refund lines with qty < 0) | goods back on the shelf | refunded minus already put back, per item |
| F&B (`fnb_cabang`) | `outlet_sales` (Moka, items exploded through recipes) | a cancelled order that was never cooked | the same, and the stock that comes back is the recipe's materials, not the menu item |

What the kernel does today:

- Quantities are converted in the app: a line has `packing` (link to `uom_conversion`) and a computed `stock_qty`
  (`packing == '' ? qty : qty * packing.factor`); `lineQty` and the declared `qty:` default read `stock_qty`. Every
  stock document since the pilot works this way.
- A submitted document cannot change (`op.update` refuses `immutable`). Only kernel hooks write read-only fields of
  a submitted document, through `SetSubmitted` (line cogs, `received_qty` of a transfer).
- Wholesale already keeps the returned quantity outside the source: `order_fulfilment` (one record per order,
  `delivered_qty`, `returned_qty`, `invoiced_qty` per item) is kept by flows on `submitted`/`cancelled` of each
  document, and save rules cap a return at `delivered_qty - returned_qty`.
- A declared `in` move cannot use `explode: recipe`. A return from a sale that went through a recipe comes back as
  stock of the menu item (at the sale's cogs per unit), which later sales never consume, because each sale consumes
  the recipe's materials again.

## Decision

### 1. Unit conversion stays in the app

No kernel change. The convention above is the contract: a line that is entered in another unit carries `packing`
and a computed `stock_qty`, and the kernel moves `stock_qty`. A return line takes the same fields as the line it
returns. The kernel never looks up `uom_conversion` itself.

### 2. Returned quantities stay in an app tracker record

No kernel change. A source document that can be returned gets a tracker: a plain (not submittable) record with one
row per item, created by a flow when the source is submitted, and updated by flows on the return's `submitted` and
`cancelled` events. The return's save rules cap its quantity against the tracker; a guard flow refuses cancelling a
source that still has live returns. `order_fulfilment` is the first one; retail and F&B get one for cashier refunds.

Why not a kernel `returned_qty` written onto the source with `SetSubmitted`:

- It would be a second way to change a submitted document, outside amend and outside the audit trail of an update.
- What can come back differs per chain (delivered on a delivery note, refunded on a POS line, consumed by a
  production order), so the kernel would need a rule language for the limit; the app already has one (CEL rules).
- The tracker is an ordinary record: listed, filtered, shown on dashboards, and exported like any other.

Reserved: when a third chain needs the same tracker shape, a `tracks:` key on a stock rule can generate it. Not now.

### 3. Making the return from its source

No new conversion hook. The return links its source; header fields take `default: source.<field>` and lines are typed
or filled by the host from the tracker. The hooks in `conversions.go` stay per document.

### 4. `explode: recipe` on an `in` move (kernel change)

An `in` move may declare `explode: recipe`. Each returned line then brings back the materials of the item's active
recipe, `roundQty(qty × per)` of each, instead of the item itself. Exactly as for `out`:

- an item without an active recipe and of kind good, material or product comes back as itself; a service moves
  nothing;
- the materials' (outlet, material) balances are locked with the rest of the document, in sorted order.

The cost of each material:

| `cost` | Unit cost of a material |
|---|---|
| `average` (default) | the receiving outlet's average for that material (its `standard_cost` when there is none) |
| `{source: link}` | the unit cost at which the linked document took that material out (0021 decision 4, per material); the outlet average when the source took none of it |

`unit`, `amount` and `consumed` are values of the line, not of its materials, so the validator refuses them with
`explode` on an `in` move (`stock_explode_cost`). `cogs` stays out only.

The recipe used is the active recipe at the time of the return, not the one at the time of the sale: a recipe that
changed between the two brings back the new quantities. Returns come within days of the sale, and the alternative
(replaying the source's material moves pro rata) cannot tell which material belonged to which menu item when two
items share one. Cancel reverses the moves like every declared document.

## Impact on the ledger and HPP

- A cancelled F&B order returned with `explode: recipe` and `cost: {source: sale}` puts each material back at the
  value the sale took it out, so stock value and the period's HPP are corrected by exactly the materials that did
  not get used.
- A refund whose goods are thrown away is not returned at all: the sale's move stands, as today.
- The source's `cogs_total` is not changed (history); margin per day is read from sales minus returns in the app.

## Implementation

- Kernel: `kernel/internal/engine/stock_declared.go`: the `in` loop posts `dl.uses` when the rule explodes;
  `inAtCost` posts one incoming quantity at the source cost or the average.
- Schema: `explode` description in `schema/0.5/model.json` (no shape change).
- Validators: `meta/validate_stock.go` and `check_stock` in `tools/rangka_validate.py`: `explode` is allowed on `in`;
  `stock_explode_cost` for `unit`, `amount` or `consumed` with it. Same order and messages.
- Tests: `TestDeclaredReturnExplode` (materials at the sale's cost and at the average, an item without a recipe, a
  service, cancel), `kernel/testdata/stock-apps/apps/declared/models/recipe_return.rk`, golden
  `kernel/testdata/broken-stock.expected.txt`.
- Docs: `website/concepts/rules.md`.
