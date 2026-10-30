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
| Numbers | `int`, `float`, `currency` (rupiah, no decimals), `percent`, `rating`, `duration` |
| Choice | `check`, `select`, `multiselect` |
| Dates | `date`, `datetime`, `daterange` |
| Relations | `link`, `dynamic_link`, `table` (child rows) — see [Relations](./relations) |
| Files | `attach`, `image`, `signature` |
| Other | `json` |

## Common field properties

`label` (Bahasa Indonesia required, English optional), `required`, `default` and `compute` (CEL), `read_only`,
`hidden`, `in_list`, `in_filter`, `unique`, `index`, `help`, `depends_on` (CEL: show the field only when true).
`money: true` and `stock: true` mark fields whose changes are high risk; currency fields are money by default.

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
