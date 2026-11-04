# 0018 List aggregates

Status: accepted (5 Nov 2026) · Owner of the change: rangka-os (kernel, web app) · Affects: nothing in rangka-apps

## Context

The redesigned list view shows totals and group summaries above the rows (for example the sum of invoice totals, or
counts per customer). `GET /api/v1/r/{model}` returns pages, so the client cannot compute these without loading every
row. The kernel should answer them in one query that matches the list exactly.

## Decision

1. **Endpoint.** `GET /api/v1/aggregate/{model}?q=&filter=&group=<field>&fn=<kind>:<field>[,…]`
   - `kind` is one of `count|sum|avg|min|max`. `count` needs no field.
   - It matches the same rows as `GET /api/v1/r/{model}` with the same `q` and `filter`, under the same read grant and
     row scope. There is no paging.
2. **Response.**

   ```json
   {
     "count": 142,
     "values": {"sum:total": 51250000},
     "groups": [
       {"key": "CUST-0012", "label": "CV Maju Jaya", "count": 4, "values": {"sum:total": 3900000}}
     ],
     "other": {"count": 9, "values": {"sum:total": 1150000}}
   }
   ```

   `other` is present only when groups overflowed (more than 50); otherwise the key is left out.

3. **Group rules.**
   - The 50 largest groups by count are returned, ties broken by key.
   - The remaining groups are summed into `other` (present only when groups overflowed).
   - Empty values share one group with `key: null, label: "(kosong)"`.
   - For a link field, `label` is the linked record's title.
4. **Errors.**
   - `400` when `group` is not a field of the model, or is a `table` field.
   - `400` when `sum|avg|min|max` names a non-number field. Currency, int, float and percent are numbers; `money`
     (Decimal) fields are not and are refused.
   - `400` on a bad `filter`.
   - `404` for an unknown model.
   - `403` without a read grant.
5. **No preview variant.** There is no aggregate under `/preview…`. In preview mode the app hides totals.

## Clarifications (4 Nov 2026)

- A child model answers `400`, the same as the list.
- The aggregate reuses the list's exact matching code: a `filter` that fails to evaluate on a row leaves that row
  out, as the list does. Only a `filter` that does not compile (a syntax error) is a `400`.
- `count` always equals the list's `total` for the same `q` and `filter`.
- Any field present on the output record may be grouped, including system and computed columns such as `status` and
  `docstatus`. A name that is neither a model field nor a known system column is a `400`.
- `sum|avg|min|max` count a record whose field is empty as `0` (`avg` divides by every matched record, `min` can be
  `0` because of an empty value). This is the dashboard engine's aggregate, shared so both answer the same.
- The group-by view opens the empty group with the filter `field == null`. In CEL, `x == null` is true for a missing or
  empty value (`''`), and `x != null` is its negation; this holds in the kernel and in `web/app/engine.js`, in every CEL
  context (list and aggregate `filter`, rules, automations, dashboard widget filters and permission `if:`). `in` keeps
  plain equality, so `x in [null]` does not match `''`.

## Contract changes (rangka-os)

- Kernel: new route `/api/v1/aggregate/{model}` sharing the list's `q`, `filter`, grant and row-scope code.
- Web app: list totals and group summaries read this endpoint.

## Open

- Grouping by more than one field.
- Date grains for `group` on date fields (the dashboard engine already has them).
