# 0073 Portal pages: customer links to one document

Status: proposed, v1 implemented in rangka-os (20 Nov 2026); §9 order_page added 28 Nov 2026 (rangka-apps#119) · Owner of the change: rangka-os (schema, validators,
kernel routes, `web/portal`) · Affects: rangka-apps (`portal/` in modules, first `invoicing/portal/tagihan.rk`),
rangka-cloud (nothing in v1: the portal is a path on the kernel host) · Builds on: 0008, 0010, 0017, 0019 §5, 0027,
0030, 0040 §9 · Issue: rangka-os#264

## Context

- A business sends a customer an invoice on WhatsApp or paper. The customer then asks "berapa yang belum saya bayar,
  ke rekening mana?" and the owner answers by hand. Only 2 to 3 of 14 competitors give the customer a self-service page.
- The data already exists. `invoicing` has `sales_invoice` (`outstanding`, `due_date`), its print format, and
  `payment_instruction` (the bank account and static QRIS copied onto each submitted invoice, with the invoice number
  as the payment reference).
- The kernel has no customer-facing surface. `layout.shell: portal` is only an enum value (0019 §5). Every route under
  `/api/v1` needs a member. A customer must not become a member: members count toward plan `users` (0028), and roles
  open back-office data.
- 0040 §9 already shapes requests without credentials per client address, and names "public portals" among them.

## Decision

### 1. Kind `portal`

- A module ships `portal/<name>.rk` (schema `schema/0.5/portal.json`). The file stem is the page name, unique per site
  like screens (0030 §1). A page is active while its module is installed.
- A page has a `type` from a closed set rendered by the kernel. A module fills slots; it never ships HTML or JS.
  - v1 builds `document`: one record, opened by a customer link (§3), and `order_page`: a guest orders from the QR
    of a table (§9).
  - Reserved for later contracts and refused by the validator today (`portal_type_unbuilt`): `documents` (the list of
    a signed-in customer, v2), `catalogue`, `booking` and `intake` (online store, self booking, intake form). They
    share this contract's link, exposure and abuse rules.

```yaml
# modules/invoicing/portal/tagihan.rk
type: document
model: sales_invoice
label: {id: Tagihan, en: Invoice}
customer: customer                    # link field to the customer: the page shows its title
amount: outstanding                   # the headline amount; 0 shows "Lunas"
due: due_date
fields: [posting_date, due_date, grand_total, paid_amount, outstanding]
lines: {field: items, columns: [description, qty, rate, amount]}
print: sales_invoice                  # optional: "Lihat faktur" opens this format
payment:                              # optional: how to pay, read from one related row
  model: payment_instruction
  link: sales_invoice                 # the field of payment_instruction that names the document
  fields: [bank, account_no, holder, reference, instructions]
  qris: qris_payload                  # a text field holding a static QRIS string, shown as a QR image
  qris_label: [qris_merchant_name, qris_nmid]
share: [finance_manager, sales_manager]  # roles besides owner that make and revoke links; empty = owner only
lifetime_days: 30                     # default 30, at most 90
```

### 2. Exposure is declared, never inferred

- The page shows only the fields it lists (`fields`, `lines.columns`, `payment.fields`, `payment.qris`,
  `payment.qris_label`), plus the record name, the customer's title, `amount`, `due` and the document state.
- A field with any `hidden_for` (0027) is never shown, whatever roles it names: a customer holds none of them.
  The validator refuses it (`portal_field_hidden`), and fields marked `hidden: true` too. Table, attachment, image,
  signature, geo, dynamic link and JSON fields are refused as well (`portal_field_type`); a table is shown only
  through `lines`.
- Linked records are not opened. A link field shows its target's title (its `title_field`), nothing else.
- Payment: the kernel reads the first row of `payment.model` whose `payment.link` equals the record name. No other
  record of the organisation is reachable from a page.
- The print format (`print`) is rendered as for a member (0008), with every `hidden_for` field (and the IDR twin of
  one) removed from the record first. It must belong to the page's model (`portal_print_model`). A module names a
  format only when the whole document is meant for the customer, as an invoice is.
  - Every `related:` list of the format must be matched on the document's `name` (`portal_print_related`), so the
    print reads no other document of the organisation. The kernel checks it again at run time, so a site override
    cannot bring an unanchored list back: the print then answers 404. The format's `require` checks run as for a
    member; a failing one answers 404 too, naming nothing.
  - Link records: the page's `customer` record is passed whole, minus `hidden_for` fields (so a field hidden from
    cashiers, such as the buyer's NPWP in rangka-apps, is not on the customer's copy). Every other link gives only
    its name and title.
- A `hidden: true` field only keeps a field off the form; the print still shows it when the format does (a unit
  name kept for prints). The page itself never lists one.

### 3. Customer links

- A link is `https://<kernel host>/p/<org>/<page>/<token>`. The token is 32 random bytes (256 bits), base64url.
- The kernel stores only `sha256(token)` in `rk_portal_links (id, org_id, page, model, record, token_hash,
  expires_at, revoked_at, created_by, created_at, last_view_at, views)`, with row-level security like model tables.
  A token is shown once, when it is made. A database copy never yields a working link.
  - Why not an HMAC over org, record and expiry with a per-org key (the issue's sketch): a stored hash gives
    per-link revocation and a list of live links without a key to keep, rotate or back up, and it does not depend on
    the secrets driver (0017), which is read-only on `env` sites.
- Lookup is by hash and must match the path's org and page; any mismatch, an unknown token, a deleted record, a
  record of a submittable model still in draft, or a page whose module is uninstalled answers the same
  `404 portal_link_unknown`. Changing one character of a token is a 404.
- An expired or revoked link answers `410 portal_link_expired` ("Tautan sudah kedaluwarsa, minta tautan baru"). Both
  look the same to the customer. Since the token matched a real link, the answer also carries `org` (name, legal name,
  letterhead phone), so the page says whom to ask. A 404 names no one: its path is whatever the caller typed.
- Lifetime: `lifetime_days` of the page, default 30, at most 90. A member may ask for fewer days when making a link.
  A link stays valid after the document is paid, so the customer can see "Lunas" and the receipt.
- A cancelled document (`docstatus` 2) shows "Dibatalkan" and no payment instruction and no print. The kernel follows
  the amendment chain (`amended_from`, at most 20 hops) and names the newest submitted document that replaces it;
  a draft amendment is not named (its name is temporary), and a chain that ends cancelled names nothing. The page
  then says the document is cancelled and to contact the seller, never that nothing is owed.
- Links are made only for a document a member can read, and for a submittable model only once submitted.
- A document whose `currency` field names another currency than the organisation's gets no link in v1
  (`409 portal_currency_unsupported`): its prices live in `_fx` twins the page does not list. Amounts carry the
  document's currency.
- A reverse proxy in front of the kernel logs paths: strip or hash `/p/` paths in its access log. The kernel's own
  logs name the route pattern, never the token.

### 4. API

Members (signed in, `/api/v1`):

| Route | Who | Does |
| --- | --- | --- |
| `GET /api/v1/portal` | any member | the installed pages the caller may share: `[{name, label, model}]` |
| `POST /api/v1/portal/{page}/links` `{record, days?}` | owner and `share` roles who can read the record | `201 {id, url, expires_at}`; the only answer that carries a token |
| `GET /api/v1/portal/{page}/links?record=` | same | links of one record: `id, created_by, created_at, expires_at, revoked_at, last_view_at, views`, no token |
| `POST /api/v1/portal/{page}/links/revoke` `{record, id?}` | same | revokes one link, or every live link of the record; `{revoked: n}` |

Customers (no credentials):

| Route | Answer |
| --- | --- |
| `GET /p/{org}/{page}/{token}` | the page shell (`web/portal/index.html`), with status 200, 404 or 410 as §3 |
| `GET /p/{org}/{page}/{token}/data` | the page as JSON (§5) |
| `GET /p/{org}/{page}/{token}/print` | the print format as HTML, with the print CSP of 0008 |

- Every `/p/` answer carries `Cache-Control: no-store`, `Referrer-Policy: no-referrer` (the token is in the path),
  `X-Robots-Tag: noindex, nofollow` and `X-Content-Type-Options: nosniff`. The shell's CSP allows scripts and styles
  from `/portal/` only, and images from `data:`.
- Audit: `portal.link.create` and `portal.link.revoke` (actor: the member; target `<model>/<record>`; data: page, link
  id, expiry), `portal.view` (actor `portal:<link id>`; data: page, kind `page` or `print`). An entry never carries a
  token or its hash. `portal.view` is written at most once an hour per link; the link's `views` counts every GET.
  HEAD is not a view.
- `GET /api/v1/portal` items also carry `lifetime_days`.

### 5. Page data

`GET …/data` answers:

```json
{"org": {"name": "…", "legal_name": "…", "phone": "…"},
 "page": {"name": "tagihan", "label": "Tagihan", "type": "document"},
 "doc": {"name": "SI-2026-00012", "state": "open|paid|cancelled", "customer": "Toko Maju",
         "amount": {"label": "Sisa tagihan", "value": 1250000, "currency": "IDR"},
         "due": {"label": "Jatuh tempo", "value": "2026-12-01"}, "replaced_by": ""},
 "fields": [{"name": "posting_date", "label": "Tanggal", "type": "date", "value": "2026-11-20"}],
 "lines": {"label": "Barang/jasa", "columns": [{"name": "qty", "label": "Qty", "type": "float"}], "rows": [{"qty": 2}]},
 "payment": {"fields": [{"name": "bank", "label": "Bank", "type": "data", "value": "BCA"}],
             "qris": {"image": "data:image/png;base64,…", "label": "Kopi Senja · NMID ID1234"}},
 "print": true, "expires_at": "2026-12-20T03:00:00Z"}
```

- `state` is `cancelled` for `docstatus` 2, else `paid` when `amount` is 0 or less, else `open`.
- `payment` is null when the document is cancelled or paid, or has no payment row.
- Labels are the field labels in Bahasa Indonesia (`id`), as the app shows them.
- When a payment field named `holder` differs from the seller's name (ignoring case, punctuation and PT, CV, UD,
  Tbk), the page says so under it ("Nama rekening berbeda dari nama usaha …"): an owner's personal account is common,
  and a silent mismatch reads as a scam.

### 6. Abuse limits

- 0040 §9: `/p/` is shaped per client address like other requests without credentials (it is not static).
- On top, the kernel keeps its own per-address bucket for `/p/`, whatever the plan: 1 request per second, burst 30,
  then `429 rate_limited` with `Retry-After`. Guessing a 256-bit token is not feasible; the bucket bounds load.
  Behind a reverse proxy, set `RANGKA_TRUSTED_PROXIES`, or every customer shares the proxy's bucket.
- Data and print answers are built from one record and at most one payment row and one table field.

### 7. Customers and plans

Portal customers are not members, have no `rk_users` row, and never count toward plan `users` or any other limit
(0028). Opening a link uses no plan quota. PDF prints are not offered on the portal in v1 (HTML only, printable from
the browser), so `pdf_month` is not spent by customers.

### 8. Not in v1

- `pay` action (a "Bayar" button that calls a payment connector synchronously and shows its QRIS or checkout). It
  needs a live gateway account (NEEDS-ZEIN). When it comes, the synchronous call lives in this contract, limited to
  declared portal actions, reusing `connectors.Runtime.Call`; flow `call` stays queued.
- v2 customer accounts: `rk_portal_accounts`, sign-in by one-time code over WhatsApp (fallback email), the `documents`
  page, statement, orders, subscriptions, courses and rentals.
- `portal_link(record)` CEL helper, so prints and WhatsApp templates carry a link, and a "Salin tautan pelanggan" button
  in the app. v1 makes links through the API above.
- Custom domains (`<bisnis>.rangka.id`); the path `/p/<org>/…` works self-hosted and hosted without extra DNS.
- Owner settings for which page kinds are on: a page is on while its module is installed.

### 9. Order pages (`order_page`)

A guest at a table scans a QR and orders from the menu without an account; the order lands in the business's own
model (rangka-apps `self_order`), where staff take it. Payment is not part of v1: it stays at the cashier.

```yaml
# rangka-apps modules/self_order/portal/pesan.rk
type: order_page
model: self_order                     # the record a guest creates
label: {id: Pesan, en: Order}
anchor: {model: dining_table, field: table, title: table_name}   # what a link names, and the order field that gets it
menu:
  model: item
  filter: "kind != 'material' && active != false"   # CEL over the menu model, run as the link's actor
  title: item_name
  price: rate
  group_by: category                  # a link shows its target's title
  sold_out: ''                        # optional check field: true shows Habis and refuses the row
lines: {field: items, item: item, qty: qty, note: note}
guest: guest_name                     # optional text fields of the order
note: note
status: {field: status, open: [new], cancel: cancelled}
share: [head_office]
lifetime_days: 365                    # default 365, at most 3650: a printed QR lives until it is rotated
```

- **Links.** A link is a §3 link whose record is the anchor (a table), not an order. Its default life is 365 days and
  at most 3650 (a document keeps 30 and 90). Rotating a sticker is revoking the anchor's links and making a new one.
  `POST /api/v1/portal/{page}/qr {records?}` (owner and `share` roles, each anchor readable by the caller) does that
  for the listed anchors, or every anchor with `active` not false (at most 200), and answers
  `{data: [{record, title, id, url, path, expires_at, qr}]}` with `qr` a `data:image/png` of the URL, for an A4 sheet.
  It is the only answer besides `POST …/links` that carries tokens.
- **What a guest can do, and nothing more.**
  - `GET …/data[?mine=a,b]`: `org`, `page`, `anchor {title, outlet}` (the anchor's title and, when it links an
    `outlet`, that outlet's title), `menu [{name, title, price, group, sold_out}]` (at most 500 rows that pass `filter`,
    sorted by title; no other field of a row leaves the kernel), `limits`, `fields` (which text inputs the page has),
    `currency`, and `orders`: for at most 10 names in `mine`, the orders made by this same link (`created_by` is
    `portal:<link id>`) on its own anchor in the last 12 hours, as `{name, status, label, open, cancelled,
    can_cancel, lines [{title, qty, note}], created_at}`. A name of another link, table or day is left out silently.
  - `POST …/order {lines: [{item, qty, note?}], guest?, note?}`: creates one record of `model` as the actor
    `portal:<link id>` (a system actor) with the anchor field set to the link's record and only the declared fields.
    The request never sets a price, the anchor or any other field: prices are the model's computes. Refused: no
    line or more than 30 (`order_empty`, `order_too_long`), an item not in the menu now (`409
    order_item_unavailable`) or sold out (`409 order_item_sold_out`), a qty that is not a whole number 1 to 50
    (`order_qty`), a note over 140 or a guest name over 40 characters, a body over 16 KB, 3 open orders (status in
    `open`) already on the anchor in the last 12 hours (`409 order_too_many_open`), a refusal of the model
    (`409 order_refused` with the message of the rule that refused; any other message stays in the kernel's log).
  - `POST …/order/{name}/cancel`: an order of this link, still open, at most 2 minutes old: a conditional update of
    the status field to `cancel` (a staff member's write in between turns it into `409 order_not_cancellable`).
    The module's rules and flows see the link's actor (`user.email` is `portal:<link id>`).
- **Abuse limits** on top of §6: per link 3 creates or cancels, then one every 20 seconds; per address 20, then one
  every 2 seconds (every guest on a restaurant's WiFi shares one address); both answer `429 rate_limited` with
  `Retry-After`. Audit: `portal.order` and `portal.order.cancel` (actor `portal:<link id>`, target `<model>/<name>`,
  data: page, link, anchor), never a token.
- **Exposure.** Every named field goes through §2: a `hidden_for` or `hidden: true` field, or a refused type, is
  refused by the validator and dropped again by the kernel. The guest never reads the anchor record beyond its title,
  nor any record of the model other than its own orders' status and lines.
- **Offline.** The page needs the guest's connection. An order waits in the model until staff take it; the page
  polls its status every 10 seconds and, after 5 minutes still open, tells the guest to call a waiter.
- **Not in v1.** Pay first (a `pay` action with the payment rail of contract 0063), item photos, options and
  modifiers (0048 §1), a guest's member lookup, delivery.

## Validation

Both validators (Go and `tools/rangka_validate.py`) report the same codes:

| Code | When |
| --- | --- |
| `schema` | the file breaks `portal.json` (unknown keys, `lifetime_days` outside 1 to 90 for `document`, 1 to 3650 for `order_page`) |
| `portal_slot_missing` | an `order_page` lacks `anchor`, `menu`, `lines` (with `item` and `qty`) or `status`; a `document`'s `lines` lacks `columns` |
| `portal_slot_unused` | a slot of the other type: `customer`, `amount`, `due`, `fields`, `print`, `payment` or `lines.columns` on an `order_page`; `anchor`, `menu`, `guest`, `note`, `status` or `lines.item`/`qty`/`note` on a `document` |
| `duplicate_portal` | a second unit ships a page name |
| `portal_type_unbuilt` | `type` is a reserved type (§1) |
| `portal_unknown_model` | `model` or `payment.model` is not a model |
| `portal_unknown_field` | a listed field is not a field of its model (`customer`, `amount`, `due`, `fields`, `lines`, `payment`) |
| `portal_field_hidden` | a listed field has `hidden_for` or `hidden: true` |
| `portal_field_type` | a listed field is a table, attachment, image, signature, geo, dynamic link or JSON field; `lines.field` is not a table; `customer` is not a link; `amount` is not a number; `payment.link` is not a link to the page model; `payment.qris` is not a text field |
| `portal_print_model` | `print` is not a print format of the page model |
| `portal_print_related` | the `print` format has a related list not matched on the document's `name` |
| `portal_unknown_role` | a `share` role is not a role of any loaded unit |

On an `order_page`, `portal_unknown_model` also covers `anchor.model` and `menu.model`; `portal_unknown_field` covers
every named field and a `status` value that is not an option; `portal_field_type`: `anchor.field` not a link to the
anchor, `anchor.title`, `menu.title`, `guest`, `note`, `lines.note` not text, `menu.price` and `lines.qty` not numbers,
`menu.group_by` not a link, select or text, `menu.sold_out` not a check, `lines.field` not a table, `lines.item` not a
link to the menu model, `status.field` not a select.

## Order of work

1. Contract, schema, both validators (rangka-os). Done with this contract.
2. Kernel: `rk_portal_links`, member routes, customer routes, audit, rate bucket, tests (rangka-os). Done.
3. `web/portal`: the `document` page, mobile first, with expired, unknown and cancelled states (rangka-os). Done.
4. rangka-apps: `invoicing/portal/tagihan.rk`. Done.
5. `order_page` (§9): schema, both validators, kernel routes, `web/portal/order.js`, rangka-apps `self_order`
   (rangka-apps#119). Done.
6. Next: CEL `portal_link`, the app's "Salin tautan pelanggan", the WhatsApp template parameter, then `pay` (§8).
