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

### Number series

Firms often number documents their own way, such as `INV/PTA/26/0012`, and restart every year or month. A site
describes this in `config.rk` under `numbering:`, and models point at a series by name:

```yaml
# config.rk
numbering:
  invoice:
    pattern: "INV/{ORG}/{YY}/{####}"
    reset: yearly                       # yearly | monthly | never
  purchase_order: {pattern: "PO/{ORG}/{YY}{MM}/{###}", reset: monthly}
  sales_invoice: INV-{YYYY}-{#####}     # a string is the older per-model override

# model
naming: {series: invoice, pattern: "INV-{YYYY}-{#####}"}   # pattern is the app's default
```

| Token | Value |
| --- | --- |
| `{YYYY}` `{YY}` `{MM}` | year, 2-digit year and month of the submit time, in the site timezone |
| `{ORG}` | the org's short code: `orgs.<id>.code` in `config.rk`, else the org id in upper case |
| `{#…}` | the counter, zero-padded to the number of `#` (1 to 9); exactly one per pattern |

`reset` defaults from the pattern: `monthly` with `{MM}`, `yearly` with `{YYYY}` or `{YY}`, otherwise `never`. A series
must repeat its reset period in the number: `yearly` needs `{YY}` or `{YYYY}`, `monthly` needs `{MM}` and a year token.
A counter that outgrows its width is printed in full.

A new number always continues after the highest existing matching name, so data opened with real numbers mid-period is skipped over, never reissued. Changing a pattern or org code mid-period keeps counting, because the counter is per org, series and period. Until `orgs:` is allowed in `config.rk`, `{ORG}` is the org id in upper case.

A model's number is resolved in this order:

1. `numbering.<model>` is a string: the older per-model override.
2. The model names `series: S` and `numbering.S` is an object: that series.
3. `numbering.<model>` is an object: a series named after the model.
4. The model names `series: S` with a default `pattern`: that pattern.
5. The model's string `naming`, else `<MODEL>-{#####}`.

Numbers are gap-free and kept per organisation: each org has its own counter, taken in the same transaction as the
submit, so a rolled-back submit skips nothing and no number is issued twice. See
[contract 0009](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0009-number-schemes.md).
