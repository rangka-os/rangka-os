# 0075 Dashboard tables and heatmaps

Status: proposed (22 Nov 2026) · Owner of the change: rangka-os (kernel, validators, web app) · Affects: rangka-apps
(`outlet_sales`, `pos`, apps `fnb_cabang`, `ritel_cabang`, `kasir`) · Builds on 0010, 0030 · Issue rangka-apps#115

## Context
- A multi-outlet owner asks two things of a dashboard: "which outlet does best, and is it better than last
  period?" and "when are we busy?". 0030 widgets (`number`, `chart`, `list`) answer neither: a chart has one value per
  group, a list shows records.
- Moka, Majoo, ESB and Square show an outlet comparison as a table (one row per outlet; sales, receipts, average
  receipt, with a change against the previous period) and busy hours as a grid of weekday by hour, darker is busier.

## Decision

### 1. Two widget types
`type: table` and `type: heatmap` join `number`, `chart`, `list` in `dashboards/*.rk`. Both read the same rows as a
chart: row scope, hidden fields and the `period` window apply unchanged. A widget that touches a hidden field returns
`{hidden: true}`.

### 2. `table`
- `group_by` (required): select, link, check or date field; one row per group. `columns` (required, at most 8): objects
  `{name, label, fn, field, expr, format}`.
- A column has either `fn` (`count`, `sum`, `avg`, `min`, `max` over the group's records; all but `count` need a numeric
  `field`) or `expr` (CEL over the names of earlier columns of the same row only, no record access). A division by zero
  or any failure leaves the cell empty (null), never 0.
- `format`: `number`, `currency`, `percent`. Default: currency for a currency field, else number.
- `sort`: a column name or `label`, `-` prefix for descending. `limit` cuts the rows.
- `compare: previous_period` (needs `period`): the previous window of the same length is read too, and each row gets
  `prev`, `delta` and `trend` per column (same shape as a `number` widget: `up|down|flat`, `"12%"`, or `"baru"` when the
  previous value was 0). A group active before and silent now is still a row, with zeros.
- Output: `{columns, rows: [{key, label, count, values, prev?, delta?, trend?}], total, compare}`. `total` is computed
  over all rows (not summed from the rows shown), so averages and ratios stay right.

### 3. `heatmap`
- `group_by` (rows) and `series_by` (columns): select, link, check or int fields. `aggregate` and `field` as in `number`.
  At most 50 rows and 24 columns. `series_range: [from, to]` with an int `series_by` makes every integer in the range a
  column even when empty (`[0, 23]` for hours).
- Output: `{rows, cols, values, counts, max, rupiah}`. A cell with no records is null, so "no sales" and "zero" never
  look the same. `max` is the legend scale.

### 4. CEL functions
- `hour_of(t)`: hour 0 to 23 of a datetime in the site timezone (UTC when none is set); `weekday_of(t)`: 1 (Monday) to
  7 (Sunday). Both return null for null or `""`. An ISO date has no time of day: `hour_of` is null and `weekday_of` is
  that date's weekday. A malformed value is an error.
- Example: `2026-11-11T23:30:00Z` in Asia/Jakarta is 06:30 on the 12th: `hour_of` 6, `weekday_of` 4.
- Computed fields may use them (`compute: hour_of(sold_at)`), which is how a model gets an int `hour` and `weekday` to
  group a heatmap by.

### 5. Schema and validators
- `schema/0.5/dashboard.json` gains the two types, `series_by`, `series_range`, `compare`, and the `table_column` def.
  Additive: existing dashboards validate unchanged.
- Both validators (Go `meta.validate`, Python `tools/rangka_validate.py`) check the same rules and `make -C kernel
  compare-python` stays identical: table needs `group_by` and `columns`; each column has exactly one of `fn` and `expr`;
  `expr` names only earlier columns; `sort` is a column or `label`; `compare` needs `period`; heatmap needs `group_by` and
  `series_by` of allowed types; `series_range` only with an int `series_by`, `from <= to`, at most 24 columns.

### 6. Not in v1
Outlet targets, a franchise network average, drill-down from a cell, and a `compare` on heatmaps.

## Web
`web/app/dashboard.js` renders `table` (sticky first column, previous-period delta under each value, totals row, a card
per row under 490px) and `heatmap` (neutral single-hue scale, legend with min and max, weekday labels, cells readable by
screen readers). Brand colour is not used.
