# CEL in Rangka: cheat sheet

Rangka runs its own CEL interpreter (`kernel/internal/cel`), the dialect in `docs/contracts.md` "CEL dialect". The
same expressions appear in rules (`check`), approvals (`when`), computed fields, list filters, dashboards and flows
(`if`, `where`, `set`, `for_each`, `id`, and `{...}` parts of `title` and `template`). Results below were checked
against the kernel's evaluator.

## What is in scope

| Where | Names you can read |
| --- | --- |
| rule `check`, approval `when`, field expressions | the record's fields by name (`grand_total`, `customer.payment_days` follows a link), `user`, `org`, `tax`; `parent` in a child row |
| flow `if`, step `if`, `set`, `for_each` | `event`, every earlier `as:` variable, `user`, `org`, `tax` |
| flow `where` | the searched model's fields by name, plus the flow's variables (`name == event.item`) |
| `{...}` in `title` / `template` | the same as the step |

`event` for `<model>.<event>` flows holds the record's fields at the top level plus `model`, `name`, `op`, `record`
and `before` (the stored record before the write; `null` on `created`).

## Operators and macros

`== != < <= > >= && || ! + - * / %`, `cond ? a : b`, `x in list`, `'key' in map`, list literals `[a, b]`, map
literals `{'item': r.item, 'qty': r.qty}`.

List macros: `list.map(x, expr)`, `list.filter(x, cond)`, `list.exists(x, cond)`, `list.all(x, cond)`,
`list.exists_one(x, cond)`, `size(list)` or `list.size()`, `list.sum()` / `sum(list)`, `list[0]`, `list.contains(v)`,
`list.join(sep)` (text items joined with `sep`; other items written as in a message). The validator checks fields read on
a macro's variable against the child model, and reports a method the kernel lacks as `unknown_method`.

Text methods: `contains`, `startsWith`, `endsWith`, `matches` (regex), `replace(old, new)`, `lowerAscii`,
`upperAscii`, `trim`. `has(x.f)` is true when the key is present and not null.

## Functions (the validator knows exactly these; anything else is `unknown_function`)

| Function | Gives | Example |
| --- | --- | --- |
| `today()` | `YYYY-MM-DD` in the site timezone | `due_date < today()` |
| `now()` | RFC 3339 datetime | |
| `duration('7d')` | a duration (`d h m s`) | `today() - duration('7d')` → `2026-11-12` |
| `add_months(date, n)` | date n calendar months later, day clamped | `add_months(start_date, 1)` |
| `days_between(a, b)` | whole days a → b, 0 when either is empty | `days_between(due_date, today()) >= 14` |
| `round(x)`, `int(x)` (toward zero), `double(x)`, `string(x)` | numbers and text | `'Rp ' + string(int(grand_total))` |
| `min(...)`, `max(...)` | over arguments or one list | |
| `size(x)`, `sum(list)` | | |
| `truncate(s, n)` | text cut to n characters | |
| `option('field', 'attr')` | an attribute of the chosen select option | `option('stage', 'probability')` |
| `app_installed('stock')` | whether an app or module is installed | flow `if:` on a site that may lack a module |
| `clock()` | `HH:MM` now, site timezone | happy-hour windows |
| `hour_of(t)`, `weekday_of(t)` | hour 0 to 23 and weekday 1 (Monday) to 7 of a datetime, site timezone | peak-hour reports |
| `epoch(t)`, `timestamp(n)` | Unix seconds and back | connector poll cursors |
| `distance_m(a, b)` | metres between two `geo` values | |
| `random_code(n)` | random code, flows only (`flow_only_function` elsewhere) | coupon codes |

Globals: `user` (`user.name`, `user.roles`, `user.outlets`), `org`, `tax`, `event`, `true`, `false`, `null`.

## Values

- Dates are ISO text and compare as text: `due_date < today()` is right; never parse them.
- Empty values: `''` for text, date and link fields, `0` for numbers, `false` for checks. `x == null` also matches
  `''`. Test a link with `customer != ''`.
- Money (`currency` fields) is whole rupiah: write thresholds as `5000000`, never `5.000.000` or `5jt`.
- Numbers in a template print raw (`7500000`) and dates print ISO. There is no rupiah or date formatter: keep amounts
  and dates out of notice text, or shorten them the way `invoicing/invoice_due_digest` does ("12,5 juta").

## Traps (seen in real files)

| Wrong | Why | Right |
| --- | --- | --- |
| `event.before.status == 'won'` on an `.updated` flow that also sees creates | reading a field of `null` is an error | `event.before != null && event.before.status != 'won' && event.status == 'won'` |
| `rec.field` after a `find` that may find nothing | the variable is `null` | guard: `if: rec != null`; `rec != null ? rec.item_name : event.item` |
| `set: {status: Lunas}` | `set:` values are CEL; `Lunas` is an unknown name | `set: {status: "'Lunas'"}` |
| `set: {open: no}` | strict YAML refuses `no` | `set: {open: 'false'}` (CEL false) or `open: false` |
| `names.joinAll(', ')`, `x.title()` | a method the kernel lacks: the validator reports `unknown_method`; at run time a failed `{...}` part is left as raw text in the message | use the methods listed above, for example `{names.join(', ')}` |
| `grand_total > 5.000.000` | not a number | `grand_total > 5000000` |
| `amount > 5000000` in a `when:` on a foreign-currency model | the foreign amount, or a rate the maker typed | also hold `currency != '' && currency != 'IDR'` |
| `due_date == today() - 14` | number minus text | `days_between(due_date, today()) == 14`, or a window |
| `size(x > 0` | unbalanced parentheses | `cel_syntax` at validate: fix the brackets |
| a flow `if:` that errors | the run is skipped silently | test `rangka validate`; keep flow `if:` simple |
| a step `if:` that errors | the run fails (and an event flow fails the person's save) | guard every member access |

Logic short-circuits: `a != null && a.x > 0` never reads `a.x` when `a` is null.

## Templates

`title` and `template` are text with `{expr}` parts: `'Faktur {inv.name} telat {days_between(inv.due_date,
today())} hari'`. A part that fails to evaluate stays in the text as written, so a typo shows up in the owner's
inbox: check every part against the variables the step can see.
