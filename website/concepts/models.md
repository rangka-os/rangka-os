# Models and fields

A **model** is the centre of everything: it becomes a table, an API resource, a list, a form and, if you ask, a
kanban board. It lives in `apps/<app>/models/<name>.rk`. Full property list: [model.rk reference](/reference/schema/model).

```yaml
name: customer
label: {id: Pelanggan, en: Customer}
naming: CUST-{####}
title_field: customer_name
fields:
  - {name: customer_name, type: data, label: {id: Nama}, required: true, in_list: true}
  - {name: phone, type: phone, label: {id: Telepon}, unique: true}
  - {name: npwp, type: npwp, label: {id: NPWP}}
  - {name: payment_days, type: int, label: {id: Tempo (hari)}, default: '14'}
```

## Field types

| Group | Types |
| --- | --- |
| Text | `data` (one line), `text` (long), `phone`, `npwp` (16 digits), `color` |
| Numbers | `int`, `float`, `currency` (rupiah, no decimals), `money` (any currency, see below), `percent`, `rating`, `duration` |
| Choice | `check`, `select`, `multiselect` |
| Dates | `date`, `datetime`, `daterange` |
| Relations | `link`, `dynamic_link`, `table` (child rows) — see [Relations](./relations) |
| Files | `attach`, `image`, `signature` |
| Other | `json` |

## Money in other currencies

`currency` is rupiah only. For documents in USD or another currency use `money`:

```yaml
fields:
  - {name: currency, type: link, to: currency, default: "'IDR'"}
  - {name: rate, type: float, label: {id: Kurs}}
  - {name: total, type: money, currency_field: currency, compute: "sum(items.map(i, i.amount))"}
```

- `currency_field` holds the ISO code: a `link` (to the currency model, whose record names are ISO codes) or `data`. A `select` cannot hold upper-case ISO codes, because option values must be lower case. `rate_field` (default `rate`) holds rupiah per
  one unit of the foreign currency. An empty currency means IDR.
- The kernel adds a read-only twin `total_idr`: whole rupiah, `round(total × rate)`, half away from zero. Use the
  twin in lists, dashboards and reports. A foreign document without a positive rate is refused.
- On a child row, such as an invoice line, the rate comes from the parent document only. A line's own `rate`
  (often its unit price) is ignored, so put `rate` on the header.
- A header's twin is `round(header amount × rate)`, not the sum of the line twins. The two may differ by at most
  half a rupiah per line.
- A money compute that fails (too large, division by zero) blocks the save.
- Twins are read-only and carry `money_of` in `/api/v1/meta`. Clients drop them when writing a model back, and a
  sent `total_idr` is ignored.
- IDR documents stay whole rupiah. Foreign values keep up to 4 decimals; more is refused, not rounded.
- In JSON a `money` value is a string (`"total": "1250.5"`) and the twin is an integer (`"total_idr": 19849812`).
  Input accepts a string or a number. Never do money arithmetic in client floats.

Full rules: [contract 0013](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0013-money-and-currency.md).

## Common field properties

`label` (Bahasa Indonesia required, English optional), `required`, `default` and `compute` (CEL), `read_only`,
`hidden`, `in_list`, `in_filter`, `unique`, `index`, `help`, `depends_on` (CEL: show the field only when true).
`money: true` and `stock: true` mark fields whose changes are high risk; `currency` and `money` fields are money by default.

## Views

Every model gets a list and a form without configuration. The `views` block only overrides them:

```yaml
views:
  list: {columns: [name, customer, stage, value], sort: -modified_at}
  kanban: {group_by: stage, sum: value, card: [customer, expected_close, owner]}
  form:
    layout:
      - section: {id: Rincian}
        columns: [[deal_name, customer], [stage, value, expected_close]]
```

## System fields

Every record has `name` (its id, from `naming`), `created_by`, `created_at`, `modified_at`, `org`. Document models add
`docstatus` and `amended_from`, and a derived `status` unless they define their own. Models may define their own
`owner` or `status` fields.

## Naming

`naming: INV-{YYYY}-{#####}` numbers records per organisation; documents get their number at submit and carry
`DRAFT-…` before that. A site's `config.rk` may override the pattern per model.
