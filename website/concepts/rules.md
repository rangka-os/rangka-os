# Rules, actions and hooks

## Rules

```yaml
rules:
  - name: lost_needs_reason
    on: save                 # save | submit
    check: stage != 'lost' || lost_reason != ''
    message: {id: Tulis alasan kalah supaya tim bisa belajar}
  - name: open_needs_close_date
    on: save
    level: warn              # error (default) blocks; warn asks to confirm
    check: stage == 'won' || stage == 'lost' || expected_close != ''
    message: {id: Peluang terbuka sebaiknya punya perkiraan closing}
```

Errors return 422 `{errors:[{field, rule, code, message}]}`; warnings come back as `_warnings` on save, and on submit
as 422 with `needs_force: true` until the client resends with `{"force": true}`.

## Actions

Buttons on a record that run a named hook:

```yaml
actions:
  - name: convert
    label: {id: Jadikan pelanggan + peluang}
    hook: crm.convert_lead
    if: converted_deal == ''
    queue: {retries: 3, backoff: 30s}   # optional: run in the background, returns 202
```

## Hooks and automations

Hooks are named Go functions in the kernel or an app (`crm.convert_lead`, `crm.mark_won`, `stock.post_moves`,
`ledger.post_receivable`). `on_submit:` and `on_cancel:` attach them to the document lifecycle, and `automations:` run
them when a condition becomes true on save — the CRM turns a won deal into a draft invoice this way:

```yaml
automations:
  - name: won_to_invoice
    when: stage changes to 'won'
    if: app_installed('sales')
    do: crm.mark_won
```

 Apps declare the
hooks they provide in `app.rk`; the validator refuses unknown ones.

## Declared stock moves

A document that moves stock declares how, and keeps `on_submit: [stock.post_moves]` and
`on_cancel: [stock.reverse_moves]` (contract 0021). A delivery note:

```yaml
stock:
  date: delivery_date
  moves:
    - {lines: items, out: outlet, reason: delivery, cogs: cogs}   # out at the outlet average, line cogs
  cogs_total: cogs_total
```

Each move names a table (`lines`), one outlet (`in:` or `out:`, a header link or `line.<field>`), a `reason` from
`stock_move.reason`, and optionally `if:` (CEL with `doc` and `line`), `item:`, `qty:`, `explode: recipe` (out: consume the recipe's materials; in: bring them back, contract 0022),
`cogs:` (out) and `cost:` (in: `average`, `{unit: f}`, `{amount: f}`, `{source: link}` for the cost the linked
document took out, `{consumed: true, split: f}` for the value this document took out). Out moves post before in
moves; all `consumed` rules (outputs and by-products) share one pool, split by qty or the share field. `{unit: f}`
is a price per stock unit (use `{amount: f}` for a line total). `{source: link}` must name a submitted document:
a return from a cancelled one is refused; from a sale exploded through a recipe, the item comes back at the
sale's cogs per unit (as stock of the menu item, which later sales do not consume: add `explode: recipe` to the
in move to bring back its materials instead, each at the cost the sale took it out); from a source that moved no stock (an invoice made from a delivery note), at the outlet average. A line whose outlet or item is empty is refused. `negative: refuse` fails the submit instead
of letting an outlet go below zero; it is checked on each out move, before the in moves. An `if:` that is not true
or false fails the submit (422 `stock_if`). Cancel reverses every move.
The six built-in stock documents (`goods_receipt`, `stock_transfer`, `stock_transfer_receipt`, `stock_count`,
`stock_adjustment`, `outlet_sales`) keep their own rules and cannot declare one.

**One document per chain moves stock.** If a delivery note and its invoice both declared `out`, stock would leave
twice; guard the second with `if`, for example on the invoice `if: "doc.delivery_note == ''"`.
