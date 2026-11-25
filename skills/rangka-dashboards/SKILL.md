---
name: rangka-dashboards
description: "Answer how the business is doing from its dashboards, and design or change dashboard widgets (number, chart, list) that an owner acts on: picking a few KPIs per business type, period and previous-period comparison, good_when, correct sums (submitted documents only, no double counting), and the site dashboard file that replaces an installed one. Use when an owner asks for numbers or a home screen: \"omzet minggu ini berapa\", \"cabang mana paling laku\", \"tagihan yang telat\", \"tambahin angka murid baru di dashboard\", \"di layar depan aku mau lihat apa aja\"."
metadata: {rangka: {version: 0.5.0, writes: [dashboards], requires: [rangka-concepts, rangka-changesets]}}
---

# Rangka dashboards

A dashboard is a file of widgets. The kernel computes every widget from records when the dashboard opens; nothing is
stored. Each widget reads **one model**, keeps the records its CEL `filter` accepts, and shows one of three things:

- `number`: one aggregate (`count`, `sum`, `avg`, `min`, `max`), with the previous period beside it when `period` is set;
- `chart`: the aggregate per group (`group_by` a select, link, check, date or data field), drawn as `bar`, `line`,
  `donut` or `funnel`;
- `list`: the first rows (max 50) of the filtered, sorted records, up to 6 `columns`.

Schema: `schema/0.5/dashboard.json`. Field reference and edge cases: [widgets-and-aggregates.md](references/widgets-and-aggregates.md).

## 1. Two jobs, two paths

| The owner | Do |
| --- | --- |
| asks a question ("omzet minggu ini?", "cabang mana paling laku?") | Answer from an existing dashboard (section 2). Change nothing. |
| asks to see something every day ("tambahin di depan…") | Propose a site dashboard change (sections 3 to 6). |
| is in the studio, no system yet ("nanti aku lihat apa aja?") | Pick 4 to 6 KPIs from [kpi-by-business.md](references/kpi-by-business.md), one KPI per line (count them: never two in one bullet), and say which home dashboard of the chosen app shows them. Write no dashboard file: none can be validated before the site exists. |

## 2. Answering a question

1. Find a widget that already answers it. MCP: `rangka_meta` lists each installed dashboard and its widgets; in a
   checkout read `apps/<app>/dashboards/*.rk` and `<site>/dashboards/*.rk` (a site file replaces the app's).
2. Compute it. MCP: `rangka_dashboard {name, period}` with `period` in days (7 for "minggu ini", 30 for "bulan
   ini"). The result is per widget: `value`, `prev`, `trend`, `delta` for numbers; `data` (label, value, count) for
   charts; `rows` and `total` for lists. Over REST: `GET /api/v1/dashboards/{name}?period=7`.
3. Say what the number covers, in one line: the period is the **last N days up to today**, not the calendar week or
   month; money widgets count submitted documents only.

   > Penjualan bersih 7 hari terakhir Rp 18.450.000, naik 12% dari 7 hari sebelumnya. Paling laku Outlet Dago
   > (Rp 7.200.000). Angka ini dari penjualan yang sudah diajukan; 3 draf belum masuk hitungan.

4. No widget answers it: `rangka_query {model, filter, sort, limit}` lists records (up to 500, plus `total`). Use it to
   name a few records or a count (`total`), never to add up money in your head: say the total needs a widget and
   offer to propose one.
5. A widget comes back `hidden` or with `error.code: permission_denied`: the asker's role cannot read that field or
   model. Tell them who can see it; never work around it with another query.

Numbers come from the kernel. Never estimate, round up or invent a figure, and never give a number a dashboard did
not return. When `rangka_dashboard` cannot compute (offline, an error) or the records are empty, say so in one
sentence, then name the dashboard and the widget that answers it ("Penjualan bersih" and "Penjualan bersih per outlet"
on the home dashboard) so the owner can open it; still propose no change.

## 3. Pick what to show: few numbers the owner acts on

For every widget, name the action it triggers. No action, no widget.

| Kind | What it is | Example (rental) |
| --- | --- | --- |
| lagging | the result, after the fact | nilai sewa dipesan, uang masuk |
| leading | moves before the result; the owner can still act | unit di luar, pesanan minggu depan |
| work queue | records someone must handle today | telat kembali, faktur lewat jatuh tempo |
| risk | money or stock exposure | deposit dipegang, tunggakan > 60 hari |

A good home dashboard has 2 to 4 numbers, 1 to 2 charts and 1 to 2 work-queue lists: 6 to 10 widgets. Start from
the business's menu in [kpi-by-business.md](references/kpi-by-business.md) and keep only what the owner said they
care about. Owner words map to KPIs there too ("omzet" is net sales of submitted documents, not invoices drafted).

### When the owner asks for a long list or an anti-pattern

"Masukin semua angka biar lengkap", a pie of every area, an average of monthly rates: push back before you build.
For each item, one line: keep it, or swap it for the version in section 8 and say why in the owner's words ("jumlah
user tidak bikin kamu melakukan apa-apa; yang belum bayar bisa langsung ditagih"). Then propose only the kept and
swapped items, 10 widgets at most on the dashboard, or ask first when a swap changes what the owner gets. Never add a
vanity count, a monthly-rate average or a many-slice donut just because it was asked.

## 4. Choose the widget

| To show | Widget |
| --- | --- |
| one figure and whether it got better | `number` with `period` and `good_when` |
| a trend over time | `chart`, `chart: line` (or `bar`), `group_by` a date field, `grain: week` or `month` |
| a ranking (per outlet, item, customer) | `chart: bar`, `sort: -value`, `limit: 5` to `10` |
| parts of a whole, at most 5 groups | `chart: donut` over a select with few options |
| stages in order (lead → deal → won) | `chart: funnel` over a select whose options are in stage order |
| records to act on | `list`, `sort` by the most urgent date, 4 to 6 `columns` |

- `period: <date field>` applies the dashboard's 7/30/90-day choice and adds the previous equal period, `trend` and
  `delta`. Without it the widget counts all matching records ("saat ini" numbers: stock, receivables, units out).
- `good_when: down` on numbers where less is better (late, overdue, drafts waiting, negative stock); the default is up.
- `compact: true` on money numbers (`Rp 12,5 jt`); leave it off where the exact rupiah matters (a cash count).
- `span` is the width on a 12-column grid (default 3): numbers 3, charts 6, lists 6 or 12. Each row adds up to 12.
- Order: work queues and today's numbers first, then the period numbers, then charts.

## 5. Get the numbers right

- **Money only from finished documents.** On a submittable model filter `docstatus == 1` (0 is draft, 2 cancelled).
  Show drafts as their own work-queue count (`docstatus == 0`, `good_when: down`), never inside a total.
- **Sum the header, not the lines,** for totals: `sales_invoice.grand_total`, `outlet_sales.net_total`. Child lines
  (`is_child: true`, such as `sales_invoice_item`) carry no `docstatus` or date of their own unless the model copies
  them (`wage_line` copies `work_date` and `cancelled`): a widget over plain lines counts drafts and cancelled
  documents and cannot take a period. Check the line model's fields before using one.
- **One fact, one model.** A widget reads one model; it cannot join. Pick the model whose one row is the thing
  counted: active students on `enrollment`, units out on `rental_schedule`, money in on `payment`.
- **No distinct count.** `count` counts rows. "Pelanggan yang beli" is not `count` on invoices; count on the model
  where one customer is one row (with a filter), or say it needs a recap model.
- **No average of averages.** `avg` is the plain mean of rows. Never `avg` a percent or rate stored per month or per
  outlet; show the rate of one period (`max` with a filter on that period, as `langganan_ringkas` does) or the sum
  of the parts.
- **Date grain and range.** `period` keeps rows dated from today minus N days, future dates included; add
  `<date> <= today()` when future rows exist. A calendar month is a filter:
  `posting_date >= truncate(today(), 7) + '-01'`. Grouping by date without `grain` draws one bar per day
  (`missing_grain`).
- **Aggregate rupiah fields.** `sum`/`avg`/`min`/`max` need `currency`, `int`, `float` or `percent`; a foreign-currency
  `money` field is `bad_field_type`.

## 6. Propose the change

Only a **site dashboard** can be written from the site, and it **replaces** an installed dashboard of the same name
(contract 0015 §3). A new dashboard name is `unknown_dashboard`; dashboards inside `apps/` belong to app authors. So
"add a widget" means: copy the installed dashboard, change it, save it under the same name.

1. Read the current one. MCP: `rangka_get_file {path: "dashboards/<name>.rk"}` (a site file, if it exists); else
   `rangka_get_file {path: "apps/<app>/dashboards/<name>.rk"}`. In a checkout: the same paths under the site and the
   apps folder. Which app's dashboard is the owner's home is in its `app.rk` (`home: {dashboard: …}`).
2. Check every model and field you use exists and is installed (`rangka_meta`). On a site dashboard every installed
   model is visible, company `x_` models and fields included.
3. Propose with `rangka_changeset_propose`:
   - first change: `{"op": "create", "path": "dashboards/<name>.rk", "content": "<the whole file>"}`, keeping the
     widgets the owner still uses and the `roles`, `cache`, `default_period` of the original. Copy `roles` exactly:
     adding or dropping a role changes who can open the dashboard, and that is not what the owner asked;
   - later changes: `patch` that file (`{"op": "add", "path": "/widgets/2", "value": {...}}`).

   In a checkout: write `<site>/dashboards/<name>.rk`, then `rangka validate --apps <apps> --site <site>`.
4. `rangka_changeset_validate`, then `rangka_changeset_preview`. Risk is low. The owner's live preview of the change
   set (`GET /api/v1/preview/{id}/dashboards/{name}`) computes the new widgets on real data; a widget over a table
   not migrated yet shows an error there instead of a value. Before proposing, sanity-check a new filter with
   `rangka_query` on the same model and filter: a `total` of 0 usually means a wrong filter.
5. Tell the owner what you prepared, one line per new widget with the action it is for, and end with "cek
   pratinjaunya, lalu setujui kalau sudah pas". Never say it is done (`rangka-changesets`).

   > Aku sudah siapkan usulan dashboard Sewa hari ini: tambah "Uang masuk" (pembayaran yang sudah diajukan, 30 hari
   > terakhir, dibanding 30 hari sebelumnya) dan daftar "Telat kembali" untuk dihubungi. Cek pratinjaunya, lalu setujui
   > kalau sudah pas.

A site dashboard stops following the app's later changes to that dashboard. Say so when you replace one for the first
time.

Two complete, validated examples: [worked-dashboards.md](references/worked-dashboards.md).

## 7. Validator codes you will meet

| Code | Fix |
| --- | --- |
| `unknown_dashboard` | the site file's name must be an installed dashboard; edit that one instead |
| `unknown_model`, `unknown_field` | read `rangka_meta` and use a real name; never guess |
| `optional_model` | in an app dashboard, a model of an optional app needs `if: app_installed('<app>')` |
| `bad_field_type` | aggregate a `currency`/`int`/`float`/`percent` field; `period` needs a date field |
| `grain_without_date`, `missing_grain` | `grain` only with a date `group_by`; always set it there |
| `line_without_time` | a line chart needs a date `group_by`; use `bar` for categories |
| `duplicate_widget` | widget names are unique in a dashboard |

## 8. Anti-patterns and the fix

| Instead of | Do |
| --- | --- |
| vanity counts (jumlah barang, jumlah pengguna, jumlah pelanggan terdaftar) | a number tied to an action: pelanggan yang belum bayar, barang stok minus |
| 15 widgets "biar lengkap" | 6 to 10; offer a second dashboard for a role instead of a longer one |
| a sum over drafts and submitted together | `docstatus == 1` in the total, drafts as a separate count |
| summing invoice lines for omzet | the header total; lines only for per-item mix where the line carries state and date |
| `avg` of a monthly rate over many months | the rate of one month, or the parts summed |
| a donut with 12 slices | `chart: bar`, `sort: -value`, `limit: 8` |
| a line chart per customer | `bar` ranking; `line` only over a date grain |
| a total without `period` that should be "this month" | `period` (rolling) or a calendar-month filter, and say which in the label |
| a number with no comparison | `period` so the owner sees naik/turun, with the right `good_when` |
| a list of everything | a filtered work queue sorted by urgency, `limit` 10 to 20 |

## 9. Never

- Never report a figure the kernel did not compute, or sum money yourself from `rangka_query` rows.
- Never put a draft or cancelled document into a money total.
- Never name a model or field you did not see in `rangka_meta` or the files.
- Never write `apps/<app>/dashboards/` files or a new dashboard name from a site; replace the installed one.
- Never use a dashboard to work around a role's hidden fields or row scope.
- Never say the dashboard is changed until `rangka_changeset_status` says `applied`.

## Sources

Adapted, concepts only, from: KPI design practice (leading vs lagging indicators, few actionable measures per
audience), Kimball dimensional modelling (one fact per grain, additive vs non-additive measures, no double counting
across line and header), and data-visualisation practice (chart choice by question, few categories per part-to-whole
chart, comparison against a baseline). Rewritten in Rangka terms; no text copied.
