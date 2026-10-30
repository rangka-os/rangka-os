# Expressions (CEL)

Defaults, computed fields, rules, `depends_on`, dashboard filters and flow conditions are written in a dialect of
[CEL](https://cel.dev). The same dialect runs in the kernel, the Python validator and the browser.

```yaml
compute: round(value * option('stage', 'probability') / 100)
check: stage != 'lost' || lost_reason != ''
default: posting_date + duration(customer.payment_days)
filter: docstatus == 1 && paid_amount < grand_total
if: app_installed('sales')
```

| Item | Meaning |
| --- | --- |
| `== != < <= > >= && \|\| !` `+ - * / %`, `? :`, `in` | Operators |
| `customer.payment_days` | Member access through a link loads the linked record (overlay fields too) |
| `today()`, `now()` | ISO date / datetime in the organisation's timezone |
| `duration('7d')`, `duration(n)` (n days), date ± duration | Date arithmetic; result is an ISO date |
| `round(x)`, `size(x)`, `sum(list)`, `list.map(x, expr)` | Numbers and lists |
| `option('field', 'attr')` | An attribute of the selected option of a select field (e.g. `probability`) |
| `app_installed('sales')` | Whether an app is installed |
| `truncate(s, n)`, `s.contains(t)` | Text |
| `user.name`, `user.roles`, `event.*` | The current user; the triggering event in flows |

Dates are ISO strings and compare as strings. Missing values are `''` for text, date and link, `0` for numbers and
`false` for checks. String literals inside YAML need both quotes: `default: "'new'"`.

The validator checks every identifier against the model's fields (and linked models' fields) and suggests the closest
name when one is wrong.
