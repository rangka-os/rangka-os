# 0039 Counter: stock per outlet on the catalogue

Status: proposed (8 Nov 2026) · Owner of the change: rangka-os (schema 0.5, both validators, web app) · Affects:
rangka-apps (`modules/pos/screens/kasir.rk`) · Builds on 0021, 0030, 0034 §3

## Context
- 0034 §3 added `catalogue.available`: a number field of the catalogue model, shown as "Sisa N" / "Habis".
- Stock is not a field of the item. In rangka-apps it lives per outlet in `stock_balance` (`outlet`, `item`, `qty`),
  written by stock moves (0021). `item.stock` is never written, so naming it shows every item as "Habis".
- A cashier sells from one outlet: the one of the open session. The badge must show that outlet's balance, not a
  company total.

## Decision
`catalogue.available` also accepts an object that reads a balance model:

```yaml
catalogue:
  model: item
  available:
    model: stock_balance        # a model of the screen's module or its requires
    item: item                  # a link field of that model to the catalogue model
    qty: qty                    # a number field of that model
    match: {outlet: outlet}     # balance field: session field (both must exist); the rows of the open session's outlet
```

- The web app lists the balance model with the list filter `<field> == '<session value>'` for each `match` pair
  (AND), the caller's row scope on top (0010), and sums `qty` per `item`. It loads with the catalogue, again after
  every sale, and is cached for offline use like the catalogue.
- An item **without a balance row shows no badge** (a service, a product made from a recipe, an item never stocked
  here). A row with `qty <= 0` shows "Habis", `qty <= 5` "Sisa N". The badge never blocks a sale (0006 §5, 0021).
- `match` needs the `session` slot; with a session value that is empty, no badges show.
- The string form of 0034 §3 stays: a number field of the catalogue model.

Validator (Go and Python, same codes as other slots): `available.model` is a model the screen may use
(`unknown_model`); `item` is a link to the catalogue model (`unknown_field`, `screen_slot_type`); `qty` is a number
field (`screen_slot_type`); each `match` key is a field of the balance model and each value a field of
`session.model` (`unknown_field`); `match` without `session` is `screen_slot_type`.

## What stays out
- No kernel aggregate endpoint for this; `GET /r/<model>?filter=…` with `limit: 2000` covers an outlet's items.
  An outlet with more stocked items than the limit shows badges for the first rows only (sorted by item).
- "Bisa dibuat" from recipes (the design's "62 porsi") is a module compute, not this slot.

## Contract changes (rangka-os)
1. Schema 0.5 `screen.json`: `catalogue.available` is `name | {model, item, qty, match}`; `make sync-schemas`.
2. Validators: the checks above, `make compare-python`.
3. Web app counter: balance lookup per the open session, cached, badges.
