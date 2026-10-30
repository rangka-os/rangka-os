# Dashboards

A dashboard is a `.rk` file in `apps/<app>/dashboards/`, listed in `app.rk` under `dashboards:` (the first one is the
app's home). Widgets are computed from records when the page opens; nothing is stored.
Reference: [dashboard.rk](/reference/schema/dashboard).

```yaml
name: sales_overview
label: {id: Ringkasan penjualan}
default_period: 90
cache: 5m
widgets:
  - {name: revenue, type: number, label: {id: Penjualan}, model: sales_invoice, aggregate: sum, field: grand_total,
     filter: docstatus == 1, period: posting_date, compact: true, span: 4}
  - {name: receivable, type: number, label: {id: Piutang}, model: sales_invoice, aggregate: sum, field: grand_total,
     filter: docstatus == 1 && paid_amount < grand_total, good_when: down, compact: true, span: 4}
  - {name: by_week, type: chart, chart: line, label: {id: Penjualan per minggu}, model: sales_invoice,
     group_by: posting_date, grain: week, aggregate: sum, field: grand_total, filter: docstatus == 1, span: 8}
  - {name: unpaid, type: list, label: {id: Belum lunas}, model: sales_invoice, filter: paid_amount < grand_total,
     sort: due_date, limit: 5, columns: [name, customer, due_date, grand_total], span: 12}
```

| Widget | Key properties |
| --- | --- |
| `number` | `aggregate` (count, sum, avg, min, max), `field`, `compact` (Rp 51,1 jt), `good_when` |
| `chart` | `chart` (bar, line, donut, funnel), `group_by` (select, link, check, date with `grain`, data), `sort: -value`, `limit` |
| `list` | `columns`, `sort`, `limit` |
| all | `model`, `filter` (CEL), `period` (follows the 7/30/90-day switch and compares with the period before), `span` of 12, `if` |

`GET /api/v1/dashboards/sales_overview?period=30` returns every widget computed; with `cache:` it sets `X-Cache: hit|miss`.
