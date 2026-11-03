# Reference print layouts

Starting points for Indonesian business documents (contract
[0008](../../docs/contracts/0008-print-formats.md) §6). A module copies a layout next to its
`print/<name>.rk` and adapts it; a firm overrides it in its site's `print/` folder.

| File | Document | Model |
| --- | --- | --- |
| `quotation.html` | Surat penawaran harga | `quotation` |
| `sales_invoice.html` | Faktur | `sales_invoice` |
| `purchase_order.html` | Pesanan pembelian | `purchase_order` |
| `statement.html` | Laporan tagihan (statement of account) | `customer`, with `related.invoices` |

The kernel's test fixtures (`kernel/testdata/print-apps/**/print/`, `kernel/testdata/print-site/print/`) hold
byte-identical copies, rendered into `kernel/internal/docprint/testdata/*.golden.html`. After editing a
layout, copy it to the fixtures and run `go test ./internal/docprint/ -run Golden -update` in `kernel/`, then
review the golden diff and print one to PDF.

## Fields the layouts read

Models that use other names either rename their fields or adapt the layout.

**quotation** (`.Doc`): `name, revision, quotation_date, valid_until, customer, contact_person, subject,
items[item, description, qty, uom, rate, discount, amount], subtotal, discount_amount, dpp, ppn,
grand_total, payment_terms, delivery_terms, notes`. `.Links.customer`: `customer_name, address`.

**sales_invoice** (`.Doc`): `name, posting_date, due_date, customer, customer_po, quotation,
items[item, description, qty, uom, rate, discount, amount], subtotal, discount_amount, down_payment, dpp,
ppn, grand_total, currency, notes`. `.Links.customer`: `customer_name, address, npwp`.

**purchase_order** (`.Doc`): `name, order_date, delivery_date, vendor, ship_to,
items[item, description, qty, uom, rate, amount], subtotal, ppn, grand_total, payment_terms, notes`.
`.Links.vendor`: `vendor_name, address, npwp`.

**statement** (`.Doc` is the customer): `name, customer_name, address, npwp`. `.Related.invoices`:
`name, posting_date, due_date, grand_total, paid_amount`.

All layouts also read `.Letterhead` (through the `letterhead`, `bank_accounts` and `signature` partials,
plus `footer.id` and `signatory`), `.Org.pkp` and `.Tax`. The quotation and the invoice also read the record's
optional `ppn_rate`, `tax_rate` and `dpp_ratio`; the PO reads `ppn_rate` and `tax_rate`.

## Behaviour worth knowing

- **PPN.** The quotation and the invoice show the DPP and PPN rows only when `.Org.pkp` is true; the
  invoice's e-Faktur note likewise. The PO shows its `ppn` row when the amount is not zero (the vendor's tax,
  not the firm's status). No layout hard-codes a rate or factor (a test greps for them):
  - rate: the record's `ppn_rate`, else its `tax_rate`, else `.Tax.ppn_rate` (default 12), printed with
    `number` as `PPN 12%`, `PPN 11%` or `PPN 11,5%`;
  - DPP factor: the record's `dpp_ratio`, else `.Tax.dpp_ratio` (default `11/12`), labelled
    `DPP nilai lain (11/12)`, or plain `DPP` when the factor is `1` or `1/1`.

  `.Tax` is the org's config `tax` deep-merged over the site's, so an org that sets only `ppn_rate` keeps the
  site's `dpp_ratio`. The amounts come from the record's `dpp` and `ppn`; the layout computes no tax.
- **Optional rows.** Diskon (document level) and Uang muka show only when not zero, as negative amounts.
  Item discount shows as a percent only when not zero.
- **Currency.** The invoice formats amounts with `money … .Doc.currency` (empty means IDR) and prints
  `Terbilang` only for IDR, since `terbilang` spells rupiah.
- **Revision.** `Rev.01`: the layout formats `revision` with `number` and pads one digit with a zero,
  because the value is an `int64` from the database but a `float64` from JSON, and `printf "%02d"` fails
  on a float.
- **Row numbers.** `{{sub $i -1}}` is `$i + 1` (there is no `add` helper).
- **Statement.** `Sisa` is `sub grand_total paid_amount`; a row with nothing open says `Lunas`, others show
  `overdue due_date` (days past due at print time). The aging table comes from `aging .Related.invoices`.
  The format's related list holds **open invoices only** (`filter: docstatus == 1 && grand_total > paid_amount`),
  oldest first, `limit: 1000`. Listing paid history too would let a long-standing customer's paid invoices
  fill the limit and push the newest (most likely open) ones off the page, so `Total sisa` and the aging would
  understate what the customer owes. When more open invoices match than the limit, `.Truncated.invoices` is
  true and the page prints "Daftar dipotong: hanya N tagihan terlama yang ditampilkan."

## Writing templates: guard nested values

Missing top-level keys print empty, but a key that is present with a nil value stops the render when the
template reaches into it: `{{.Doc.contact.name}}` fails with "nil pointer evaluating interface {}.name" when `contact` is null.
Wrap every nested access in `{{with}}` and use its `{{else}}` for a fallback:

```html
{{with .Links.customer}}<div>{{.customer_name}}</div>{{else}}<div>{{.Doc.customer}}</div>{{end}}
```

Inside `with` and `range` the dot changes; name what you need from outside first (`{{$cur := ...}}`) or
use `$` for the root. Use the helpers for every value a reader sees: `rupiah`/`money` for amounts,
`date` for dates, `number` for quantities, `terbilang` for the total in words.
