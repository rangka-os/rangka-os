---
name: rangka-modelling
description: "Design or change a business's data in .rk the way a careful DBA and data modeller would: reuse the master data core modules already hold, choose between an overlay field (custom/<model>.rk, x_ fields), a company model (models/x_*.rk) or a catalogue app, pick field types, links, child tables and document lifecycles, and avoid the classic modelling mistakes. Use whenever you add or change fields or tables, design a custom piece in the studio, or check whether a module already holds the data. Owner phrases: \"tambahin kolom nomor PO\", \"aku mau catat nilai ujian peserta\", \"tiap sewa ada deposit\", \"bikin tabel kontrak karyawan\", \"bisa simpan data jaminan?\", \"datanya ditaruh di mana?\"."
metadata: {rangka: {version: 0.5.0, writes: [custom, models], requires: [rangka-concepts, rangka-changesets, rangka-compose]}}
---

# Modelling data in Rangka

You design data the way a DBA would, but you never write SQL, tables or migrations. The kernel owns storage: a model
becomes a table, its links become indexed columns, a change set's preview shows the migration. Your output is `.rk`
files proposed through a change set (`rangka-changesets`). Snippets here use schema 0.5, the version the kernel and
rangka-apps run.

## 1. Read what exists, every time

Never design from memory. Before drafting anything:

| Need | MCP (how Zafdoz runs) | In a checkout |
| --- | --- | --- |
| Which apps and modules are installed or available | `rangka_catalogue` | `rangka catalogue --json --apps <apps> --site <site>` |
| Every installed model with its fields, overlays included | `rangka_meta` | open `modules/<m>/models/*.rk`, `site/custom/*.rk`, `site/models/*.rk` |
| One site file before you change it | `rangka_get_file {path: "custom/<model>.rk"}` | open `site/custom/<model>.rk` |
| How much data a model holds (is it in use?) | `rangka_query` | `rangka validate` + the preview's record counts |

Search `rangka_meta` for the owner's noun in labels (`Pelanggan`, `Barang`, `Karyawan`), not only in model names.

## 2. Reuse first: where does the data belong?

| Situation | Do | Change-set path | Kernel risk |
| --- | --- | --- | --- |
| An installed model already has the field | Point the owner to it. Change nothing. A different word? Relabel with overlay `labels` | `custom/<model>.rk` | low (labels) |
| An installed model is right but lacks a few facts | **Overlay field** (`x_` name) on that model | `custom/<model>.rk` | medium; high for money/stock fields |
| An available catalogue app or module covers it | Compose it (`rangka-compose`); do not rebuild it | `config.rk` | medium |
| The app exists but is `not_in_plan` | Say so; never rebuild it as a company model to get around the plan | none | — |
| Something new the catalogue does not cover, with its own life (own list, own number, own dates) | **Company model** `models/x_<name>.rk` linking to the core models | `models/x_<name>.rk` | medium; high when submittable, append-only, moving stock or with money fields |
| A repeating group that belongs to one record (lines, scores, checklist) | **Child model** (`is_child: true`) + `type: table` on the parent | `models/x_<name>_<lines>.rk` | medium |
| The owner wants a whole app made of existing modules | Custom app (`rangka-compose`, step 4) | `apps/<name>/app.rk` + `config.rk` | medium |

**One set of master data.** A business has one customer list, one item list, one outlet list and one employee list.
Never create a second one (`x_pelanggan`, `x_barang`, `x_karyawan`); link to the core model and add overlay fields to it.

| Owner word | Core model (module) |
| --- | --- |
| pelanggan, klien, penyewa, member | `customer` (contacts) |
| pemasok, supplier, vendor | `vendor` (contacts) |
| barang, produk, jasa, menu | `item` (catalogue) |
| cabang, outlet, toko, gudang | `outlet` (outlets) |
| karyawan, staf, kasir (orang yang login) | `user` (users), the employee record (contract 0056) |
| tenaga kerja, satpam, pekerja yang ditempatkan | `worker` (workforce) |
| peserta, murid | `student` (courses), joined to a batch by `enrollment` |
| alat sewa | `rental_item` (rental), which links to `item` |

Check each against `rangka_meta` on the site: a module that is not installed has no model to link to.

Say the reuse to the owner, one line per master record and per thing a module already records: "pelanggan pakai
daftar pelanggan yang sudah ada", "cabang pakai daftar outlet", "absen sudah dicatat modul kursus, jadi tidak dibuat
lagi". In the studio too: map each record of a `custom` piece to `customer`, `outlet`, `item` and the rest by name.

In the studio (before a site exists) the same rule shapes a `custom` piece's `design.records` (`rangka-consult`
section 5): list what must be remembered, then map each record to a core model, an overlay field or a new company
model with this table.

## 3. What becomes what

| It is | Example | Becomes |
| --- | --- | --- |
| An **entity**: has its own identity, is looked up and linked | customer, rental item, work site | a model with `title_field` and usually `naming` |
| A **value**: describes something, no life of its own | phone, colour, size, collateral type | a field on the entity |
| A **document**: an event the business commits to, with a date, a number, maybe money | booking, exam, SLA deduction | a model with `is_submittable: true` |
| The **lines** of a document: belong to it and die with it | invoice lines, exam scores, unit checklist | a child model (`is_child: true`) + a `table` field |
| A **history** that must never be edited | stock moves, ledger entries | a module ledger (`append_only`); a company never needs its own: use the module's documents |
| A small fixed **list of states or kinds** | status, payment method, contract type | a `select` with `options` |
| A **list the owner keeps growing** or that carries its own data | categories with a rate, service areas | its own model and a `link` to it |

**Aggregates.** A document and its lines are saved, submitted and cancelled as one unit. Put a rule that spans lines
on the parent (`scores.map(s, ...).sum() == 0`). Never make lines their own top-level model that the user must keep in
step with the header.

**Link or snapshot.** A link reads the current value; a document must remember the value at that time. Copy it into
the document's own field with `default:` (copied once at create, editable while draft, frozen at submit):

```yaml
  - {name: daily_rate, type: currency, label: {id: Tarif per hari}, default: rental_item.daily_rate}  # snapshot
  - {name: customer_name, type: data, label: {id: Nama pelanggan}, compute: customer.customer_name}  # always follows the link
```

Use `default` for prices, rates, deposits and anything a later change must not rewrite. Use `compute` for display
values that should follow the master record.

## 4. Field types

Full guide with snippets: [references/field-types.md](references/field-types.md). The short version:

| Data | Type | Never |
| --- | --- | --- |
| Rupiah amount | `currency` (whole rupiah; money by default, so high risk) | `float`, `int`, `data` |
| Foreign-currency amount | `money` + `currency_field` (+ `rate_field`) | `currency` |
| Count of units | `int`; `float` only when fractions are real (kg, litre) | `data` |
| Stock quantity that moves stock | the stock module's documents; a company model only with `stock:` rules (high risk) | a hand-kept balance |
| Day | `date`; moment with time: `datetime`; range: `daterange` | `data` "27/10/2026" |
| Clock time alone (08:30) | `data`, quoted in defaults | `int` |
| Phone, NPWP | `phone`, `npwp` | `int` (drops leading zeros) |
| Codes, numbers with leading zeros, plate numbers | `data` | `int` |
| Fixed choice | `select` with `options` | `data` free text |
| Reference to another record | `link` with `to` | `data` holding a name |
| Yes/no | `check` | a select with ya/tidak |
| Proof (photo, file, signature) | `image`, `attach`, `signature` | `text` holding a URL |

Common keys: `required`, `default` / `compute` (never both), `read_only`, `hidden`, `depends_on`, `in_list`,
`in_filter`, `unique`, `index`, `help`, `peek` (links), `min_rows` (tables), `set_once`, `read_only_for` / `hidden_for`.

## 5. Documents, money and stock

- `is_submittable: true` gives the lifecycle Draft → Diajukan → Dibatalkan, with **amend** making a corrected copy.
  A submitted document never changes (edits return 409). A correction is cancel + amend, or a new document that
  reverses the old one; never an edit of the original amount.
- Use a document when the record commits the business: money owed, money returned, goods out, a score announced.
  Use a plain model for reference data and things edited freely (a contact note, a plan).
- `naming` gives the number at submit (`UJN-{YYYY}-{####}`); drafts are `DRAFT-…`. A site can renumber through
  `config.rk` `numbering` (contract 0009); you do not need a field for the number.
- Separate events are separate records: a deposit received (booking) and a deposit returned (return) are two
  documents, not one field edited later.
- `currency` and `money` fields are money; `money: true` marks any other field as money, `stock: true` a quantity
  that moves stock. The kernel rates every add or change of such a field **high** (owner only).
- Payments, invoices, stock moves and journals stay in the core modules (`payments`, `invoicing`, `stock`,
  `accounting`). A company model may hold money fields (a deduction, a fee), but money moving between parties is
  posted by the modules. Money and stock never depend on an LLM: totals are `compute`, never typed by you or by Zafdoz.
- A field a client fills once and nobody rewrites (clock-in time, photo, position): `set_once: true`.
- Approval thresholds go in `approvals` on the model or an overlay (`on: submit`, `when`, `approvers`).

## 6. Relations

| Need | Write | Notes |
| --- | --- | --- |
| Many to one | `type: link, to: customer` | Indexed by the kernel. Read through it in CEL: `customer.payment_days` |
| One to many, owned | `type: table, of: x_<child>`; the child has `is_child: true` | Child models take no `naming` and no `is_submittable` |
| Read a field of a linked record | `compute: enrollment.student.student_name` | Links chain |
| Show a linked card on the form | `peek: [field, ...]` (max 6) | |
| One of several models | `type: dynamic_link` | In the schema, not used by any shipped module: avoid it; use one `link` per target |
| Many to many (`link_multi`), `on_delete`, trees | not in the schema yet (docs: Relations → Planned) | Many to many: a child model with a `link`. Tree: a `parent` link to the same model; the kernel does not check cycles yet |

A company model may link to every installed model; a catalogue model never links to a company model. Deleting a
record that others link to is a person's decision; you never propose deletes (`ai_forbidden_path`).

## 7. Naming

- Model and field names: snake_case, English, `^[a-z][a-z0-9_]{0,62}$`, never renamed once released.
- Company model: `x_<name>` (`company_model_name` otherwise; `duplicate_model` if it shadows a catalogue model).
  Its own fields have **no** `x_` prefix: plain names become real columns, `x_` names go to the JSONB `x` column.
- Overlay fields: always `x_<name>` (schema error otherwise). They live in the record's `x` column, so `unique` and
  `index` do nothing on them. When something must be unique, make it a company model field.
- Labels: `{id: ..., en: ...}`, the owner's Indonesian words in sentence case (`Tanggal kembali`, not `TGL_KMBL`).
  Option values are lower-case snake_case (`pkwt`), labels carry the words.
- Rule names describe the condition (`amount_positive`, `period_first_day`).
- First line of every file: `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json`
  (`overlay.json` for `custom/`).

## 8. Size and speed, in Rangka terms

- Links and `unique` fields are indexed for you. Add `index: true` to a company-model field people filter or match
  on often (a code, a date range filter). Nothing else to tune.
- Keep lists light: `in_list` on 4 to 7 fields, `views.list.filters` for the fields people narrow by, `sort` on a date.
- A `compute` that reads `today()` or `now()` is recomputed on every read: fine for one flag, not for heavy chains.
- `cache: 5m` on a model whose list is read often and changes rarely (needs the cache capability); `realtime: true`
  for a model several screens watch live (needs realtime).
- Dashboards aggregate when they open: never store a running total to make a report fast.

## 9. Anti-patterns and the Rangka fix

| Anti-pattern | Fix |
| --- | --- |
| A second customer, item or employee list (`x_pelanggan`) | Link to `customer` / `item` / `user`; overlay fields for the extras |
| Free text where a record belongs (`nama_klien: data`) | `link` to the model; `compute` the name for display |
| A JSON blob (`type: json`) holding structured facts | Real fields, or a child table for repeating groups |
| One model per month or per outlet (`x_penjualan_okt`) | One model with a `date` and a `link` to `outlet`; filter by them |
| Status as many checks (`is_paid`, `is_sent`, `is_done`) | One `select` with options (and `tone`); lifecycle via `is_submittable` |
| Totals typed by hand | `compute: "lines.map(l, l.amount).sum()"` |
| Editing a submitted amount to record a refund | A second document for the refund |
| Deposit or price read live from the master on an old document | Snapshot with `default:` |
| `int` for phone, NPWP, codes | `phone`, `npwp`, `data` |
| Dates as text | `date` / `datetime` |
| `ya`/`tidak` options | `check` |
| Columns `item_1`, `item_2`, `item_3` | Child table |
| Identity numbers (KTP) stored "just in case" (UU PDP) | Store only what the process needs; the kind of collateral, not the number |
| A company model to bypass a plan or a module the catalogue has | Compose the app, or tell the owner it is not in their plan |

## 10. Propose it

One request, one change set (`rangka-changesets`). Create whole files for new models; patch or replace
`custom/<model>.rk` after reading it.

```json
{"title": "Catat nilai ujian per angkatan", "reason": "Pemilik mau catat nilai ujian dan siapa yang lulus",
 "ops": [{"op": "create", "path": "models/x_ujian.rk", "content": "..."},
         {"op": "create", "path": "models/x_ujian_nilai.rk", "content": "..."}]}
```

- Validate (`rangka_changeset_validate`; in a checkout `rangka validate --apps <apps> --site <site>`), fix codes such as
  `unknown_field`, `compute_cycle`, `company_model_name`, `duplicate_model`, `schema`, then preview
  (`rangka_changeset_preview`).
- Give every new company model `permissions` when you create it, copied from a sibling model of the same app: after
  that, changing them is a person's job (`ai_forbidden_path`). Without them every role has full access.
- A company table has no menu of its own: it shows where the site's layout `nav.side` lists it.
- A kind with a fixed set of values is a `select` even when the owner names only one: a contract type (PKWT / PKWTT),
  a payment method, a status. Never store it as free text or only as a number field.
- Give the owner the real reason for the approval: a submittable document (an exam, a booking) is owner only because
  submitted values cannot be changed afterwards, not because of permissions.
- Tell the owner in their words what will be recorded and where, never that it is done: "Aku siapkan tabel Ujian:
  tiap ujian satu angkatan, nilai per peserta, jumlah yang lulus dihitung otomatis. Karena nilai yang sudah diajukan
  tidak bisa diubah, perubahan ini perlu disetujui pemilik."

Worked examples (rental deposit, course exam, outsourcing contract), all validated:
[references/worked-examples.md](references/worked-examples.md).

## Never

- Never write SQL, table names (`r_...`) or migrations; never promise an index or a speed-up the schema cannot express.
- Never create a second master list, a per-period model, or a model that duplicates a catalogue module.
- Never propose an edit to `apps/` or `modules/` files, a delete, or a change to an existing model's `permissions`.
- Never let a total, tax, balance or stock quantity be typed by Zafdoz: it is a `compute` or a module posting.
- Never model personal data the process does not need.

## Sources

Adapted from domain-driven design (entities, value objects, aggregates), relational normalisation (one fact in one
place, no repeating groups), Frappe DocType patterns (submittable documents, child tables, link fields, naming series)
and Indonesian data-protection practice (UU PDP data minimisation), rewritten for Rangka's `.rk` metadata. No text is
copied from them.
