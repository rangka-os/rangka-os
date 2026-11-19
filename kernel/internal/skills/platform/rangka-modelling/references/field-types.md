# Field types: which one, and how to write it

Every type below is in `schema/0.5/model.json` (`$defs/field/type`). Storage is the kernel's business; the "stored
as" column only explains behaviour (sorting, filtering, uniqueness). Snippets are fields of a company model; in an
overlay the same field needs an `x_` name.

## Text

| Type | Use for | Snippet |
| --- | --- | --- |
| `data` | one line: names, codes, plate numbers, item codes with leading zeros, clock times | `{name: plate_no, type: data, label: {id: Nomor polisi}, in_list: true}` |
| `text` | long notes | `{name: notes, type: text, label: {id: Catatan}}` |
| `phone` | phone or WhatsApp numbers (keeps the leading 0 or +62) | `{name: phone, type: phone, label: {id: Nomor WhatsApp}}` |
| `npwp` | tax number (16 digits) | `{name: npwp, type: npwp, label: {id: NPWP}}` |
| `color` | a colour the owner picks (labels, tags) | `{name: tag_color, type: color, label: {id: Warna}}` |

- Codes such as `007` or `0812...` are text. In `.rk` a default that looks like a number with a leading zero must be
  quoted: `default: "'007'"`.
- Clock times have no type of their own: `data` with a rule on the format, as `course_session` does
  (`start_time`, `end_time`).

## Numbers

| Type | Use for | Snippet |
| --- | --- | --- |
| `int` | counts: units, days, sessions | `{name: qty, type: int, label: {id: Jumlah unit}, required: true, default: '1'}` |
| `float` | measures with fractions: kg, litre, score | `{name: score, type: float, label: {id: Nilai}}` |
| `currency` | rupiah, whole numbers; money by default (high risk) | `{name: amount, type: currency, label: {id: Jumlah}, required: true}` |
| `money` | an amount in another currency; needs `currency_field` | see below |
| `percent` | discounts, rates, probability | `{name: discount, type: percent, label: {id: Diskon}, default: '0'}` |
| `rating` | a 1 to 5 score from a person | `{name: satisfaction, type: rating, label: {id: Kepuasan}}` |
| `duration` | a length of time | `{name: shift_length, type: duration, label: {id: Lama shift}}` |

```yaml
  - {name: currency, type: link, to: currency, label: {id: Mata uang}, default: "'IDR'"}
  - {name: rate, type: float, label: {id: Kurs}}
  - {name: total_fx, type: money, currency_field: currency, label: {id: Total (valas)}, compute: "sum(lines.map(l, l.amount_fx))"}
```

The kernel adds a read-only rupiah twin (`total_fx_idr`) for lists and dashboards. `money` needs the `currency`
module installed for the `link`; otherwise `currency_field` may be a `data` field.

- A stock quantity (`stock: true`) is high risk and only means something with a model-level `stock:` rule
  (contract 0021). A company rarely needs it: use the stock module's documents.
- `money: true` on a non-currency field (a `float` deposit count, say) marks it money for risk.

## Choice

| Type | Use for | Snippet |
| --- | --- | --- |
| `check` | yes/no | `{name: active, type: check, label: {id: Aktif}, default: 'true'}` |
| `select` | one of a fixed, short list | below |
| `multiselect` | several of a fixed list (tags) | `{name: days, type: multiselect, label: {id: Hari kerja}, options: [{value: mon, label: {id: Senin}}, {value: tue, label: {id: Selasa}}]}` |

```yaml
  - name: status
    type: select
    label: {id: Status}
    default: "'open'"
    options:
      - {value: open, label: {id: Terbuka}, tone: info}
      - {value: won, label: {id: Menang}, tone: ok, closed: true}
      - {value: lost, label: {id: Kalah}, tone: bad, closed: true}
```

- Option values are lower-case `^[a-z0-9_]+$`; the words go in `label`. `tone` colours the chip; `probability`
  feeds `option('stage', 'probability')`.
- Adding options to a catalogue model's select: overlay `options: {<field>: [...]}`. Removing an option is high risk
  (rows may hold it); on a submittable model any option change is high.
- Pick a `link` instead when the list grows with the business or each entry has its own data (a rate, an owner).
- Any visible, writable `select` gives the model a board (Papan) in the web app.

## Dates

| Type | Use for | Snippet |
| --- | --- | --- |
| `date` | a day | `{name: start_date, type: date, label: {id: Mulai}, required: true, default: today()}` |
| `datetime` | a moment (clock-in, handover) | `{name: handed_at, type: datetime, label: {id: Waktu serah terima}, default: now()}` |
| `daterange` | a range edited as one value | `{name: leave, type: daterange, label: {id: Cuti}}` |

- Dates are ISO strings and compare as strings. `start_date + duration(days)`, `add_months(d, 1)`,
  `days_between(a, b)` work in CEL.
- A month is a `date` (the first day) plus a rule `period.endsWith('-01')`, never a model per month.
- Two `date` fields (`start_date`, `end_date`) are easier to filter and validate than a `daterange`; the shipped
  modules use two dates.

## Relations

```yaml
  - name: customer
    type: link
    to: customer
    label: {id: Pelanggan}
    required: true
    in_list: true
    peek: [phone, payment_days]
  - {name: lines, type: table, of: x_order_line, label: {id: Rincian}, min_rows: 1}
```

The child model:

```yaml
name: x_order_line
is_child: true
fields:
  - {name: item, type: link, to: item, label: {id: Barang}, required: true}
  - {name: qty, type: int, label: {id: Jumlah}, required: true, default: '1'}
  - {name: price, type: currency, label: {id: Harga}, default: item.rate}
  - {name: amount, type: currency, label: {id: Jumlah harga}, compute: qty * price}
```

`dynamic_link` exists in the schema but no shipped module uses it; prefer one `link` per target model.
(`item.rate` is the catalogue's selling price; read field names from `rangka_meta`, never guess them.)

## Files and proof

| Type | Use for | Snippet |
| --- | --- | --- |
| `attach` | any file (contract PDF) | `{name: contract_file, type: attach, label: {id: Berkas kontrak}}` |
| `image` | a photo of the record | `{name: photo, type: image, label: {id: Foto}, set_once: true}` |
| `signature` | a signature drawn on screen | `{name: signature, type: signature, label: {id: Tanda tangan}}` |
| `geo` | a position `{lat, lng, accuracy_m, at}` | `{name: position, type: geo, label: {id: Lokasi}, set_once: true}` |

## Other

`json` stores free structure. It is for machine data (a connector's raw payload), never for facts an owner reads,
filters or reports on.

## Common keys

| Key | Means | Note |
| --- | --- | --- |
| `required` | must be filled to save | |
| `default` | CEL, evaluated when the record is made | snapshot of a linked value; string literals need both quotes `"'open'"` |
| `compute` | CEL, recomputed on save (on every read when it uses `today()`/`now()`) | never together with `default`; cycles fail with `compute_cycle` |
| `read_only` | clients cannot write it | computed fields are read-only by nature |
| `depends_on` | CEL: show only when true | `depends_on: x_jaminan == 'barang'` |
| `in_list` / `in_filter` | shown in the list / offered as a filter | |
| `unique` | no two records share the value (per organisation) | real columns only: no effect on `x_` overlay fields |
| `index` | the kernel indexes it | links are indexed already |
| `set_once` | a client fills it once | proof fields |
| `read_only_for` / `hidden_for` | per role | adding or changing them is high risk |
| `help` | a sentence under the field | owner's words |
