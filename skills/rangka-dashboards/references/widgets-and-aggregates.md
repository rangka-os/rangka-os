# Widgets and aggregates: how the kernel computes them

Read with `SKILL.md`. Facts here follow the kernel's dashboard engine and validator (schema 0.5 `dashboard.json`).

## Dashboard keys

| Key | Meaning |
| --- | --- |
| `name`, `label`, `description` | `name` equals the file name; a site file uses the installed dashboard's name |
| `periods` | the period choices in days, default `[7, 30, 90]` |
| `default_period` | days used when the viewer picks none, default 30 |
| `roles` | who may open it; omit for everyone who can read its models |
| `cache` | `5m`, `1h`, …: reuse computed widgets that long (needs `system.cache`); a write drops the cache |
| `widgets` | at least one |

## Widget keys

| Key | Applies to | Notes |
| --- | --- | --- |
| `name`, `type`, `model` | all | required; `name` unique in the dashboard |
| `label`, `description` | all | `{id: ...}`; put the period or scope in the label when it is not obvious |
| `filter` | all | CEL over one record, same dialect as rules: `docstatus == 1 && outstanding > 0` |
| `period` | all | a `date` or `datetime` field the 7/30/90 choice applies to |
| `aggregate`, `field` | number, chart | `count` (default) needs no field; `sum`/`avg`/`min`/`max` need a `currency`, `int`, `float` or `percent` field |
| `chart`, `group_by`, `grain` | chart | `group_by` is a select, link, check, date or data field; `grain` (`day`, `week`, `month`) only with a date |
| `sort` | chart, list | list: a field, `-` for descending; chart: `value`/`-value` (aggregate) or `label` |
| `limit` | chart, list | 1 to 50; a list shows 10 when unset, a chart shows every group |
| `columns` | list | 1 to 6 fields, required for lists |
| `span` | all | 1 to 12, default 3 |
| `good_when` | number | `up` (default) or `down` |
| `compact` | number | money as `Rp 12,5 jt` |
| `if` | all | CEL; false hides the widget. `app_installed('<app>')` guards a model of an optional app |

## The period

- `period: <date field>` keeps records whose date is on or after **today minus N days**. There is no upper bound:
  a booking dated next week counts. Add `<date> <= today()` to the filter when the model holds future dates.
- A number with `period` also computes the **previous** N days (from today minus 2N up to today minus N) and returns
  `prev`, `trend` (`up`, `down`, `flat`) and `delta` (percent change, or `baru` when the previous value was 0).
- A widget without `period` ignores the choice: right for "saat ini" figures (receivables, stock, units out, active
  subscriptions), wrong for flows of money ("omzet") unless the filter bounds the dates itself.
- Calendar ranges are filters, not periods: this month `posting_date >= truncate(today(), 7) + '-01'`; the next 7 days
  `session_date >= today() && session_date < today() + duration(7)`; aging buckets
  `due_date < today() - duration(30) && due_date >= today() - duration(60)`.

## Charts

- Groups keep a natural order: select groups follow the option order (this is what makes a funnel read top-down),
  date groups go oldest first, others by first appearance. `sort` overrides it; `limit` keeps the first N after sorting.
- Group labels: a select shows the option label (and its tone colour), a link shows the linked record's title (or its
  code when the viewer cannot read it), a check shows Ya/Tidak, an empty value "Tanpa nilai".
- `grain: week` buckets start on Monday; `month` labels read `Okt 2026`.

## Who sees what

- Each viewer's row scope (outlets, own records) and permissions apply: a cashier and the owner can see different
  numbers on the same dashboard. Never compare their numbers as if they were the same.
- A widget that sums, groups, sorts or shows a field hidden from the viewer (`hidden_for`) is hidden for them.
- A model the viewer cannot read gives an `error` with `permission_denied` instead of a value.

## Counting right (facts and grain)

Think of each model as a table of facts at one grain: one row per invoice, per booking, per enrollment, per stock move.

- **Additive** measures (rupiah amounts, quantities) can be summed across rows and periods.
- **Snapshot** measures (`outstanding`, `seats_left`, a stock balance) are true now; sum them across records, never
  across dates.
- **Ratios and rates** (margin %, collection rate, utilisation) cannot be summed or averaged across rows. Show the
  parts (sum of margin and sum of sales), or the rate one recap row holds for one period.
- **Header vs line.** A document's total lives on the header; its lines are a lower grain. Summing both, or summing
  lines while counting headers, double counts. Use lines only for a per-item mix, and only when the line model carries
  the document's state and date.
- **Distinct.** The engine has no distinct count. Count on the model where the thing is one row, or keep a recap model
  filled by a flow (as `subscription_month` and `rental_schedule` are) and count that.
- **Cross-model figures.** A widget reads one model. "Laba kotor setelah retur" in `fnb_overview` is two widgets with
  a description telling the owner how they combine; it is not one number.

## Copy patterns from the real dashboards

| Need | Look at (rangka-apps) |
| --- | --- |
| sales total, trend, top customers, unpaid list | `apps/crm/dashboards/sales_overview.rk` |
| receivables aging buckets | `apps/faktur_piutang/dashboards/piutang_ringkas.rk` |
| per-outlet sales, negative stock queue | `apps/fnb_cabang/dashboards/fnb_overview.rk` |
| rental today, late, deposit held | `apps/sewa_alat/dashboards/sewa_ringkas.rk` |
| course seats, sessions this week, overdue | `apps/kelas_kursus/dashboards/kursus_ringkas.rk` |
| MRR, collection rate from a recap model | `apps/langganan_jasa/dashboards/langganan_ringkas.rk` |
| quotation outcomes, ready to bill | `apps/jasa_proyek/dashboards/jasa_overview.rk` |
