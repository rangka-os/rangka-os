# Red flags and their fixes

Read with the checklist in [../SKILL.md](../SKILL.md). "Never" rows are redesigned before proposing; the rest are
fixed silently or told as a trade-off (SKILL.md section 5).

## Fit and reuse

| Red flag | Why it hurts | Fix |
| --- | --- | --- |
| An op you cannot tie to a pain or a sentence the owner said | The owner approves something they did not ask for; more to learn, more to break | Drop it. If you think it helps, ask first |
| A value the owner never stated (a deposit %, a late fee, an approval limit) written as a default or a rule | A guess becomes a business rule | Leave it empty and ask; in the studio keep it `open` (`rangka-consult`) |
| `models/x_pelanggan.rk`, `x_barang`, `x_invoice` on a site that has `contacts`, `catalogue` or `invoicing` (`rangka_meta`) | Two customer lists, two price lists; reports never agree | Overlay the core model (`custom/customer.rk` with `x_` fields) or add the app that brings it (`rangka-compose`) |
| A new dashboard or report that repeats one already installed | Two numbers with the same name that differ | Point the owner at the existing one; add only the missing widget (`rangka-dashboards`) |
| A custom app whose modules already make up a catalogue app | The owner misses that app's screens, roles and updates | Add the catalogue app instead |

## Data

| Red flag | Why it hurts | Fix |
| --- | --- | --- |
| A `data` field holding a customer name, item name or price copied from another record | Goes stale on the first rename | `link` to the master record; `peek` shows the extra fields |
| A total stored by hand next to its lines | Total and lines drift apart | `compute` from the lines; the field is `read_only` |
| Money as `int`, `float` or `data` | Loses the money flag, rounding and the high-risk guard | `currency` (money by default) |
| Dates as `data` ("12/11") | Cannot sort, filter or remind | `date`, `datetime` or `daterange` |
| A `select` with options that grow every week (customer names, item names) | Every new value is a change set | A `link` to a small master model |
| Repeating fields `item_1`, `item_2`, `item_3` | Fixed limit, cannot total | A child table (`table` with `of:` an `is_child` model, `rangka-modelling`) |
| `required: true` on something staff only learn later | Staff type "-" to get past it | Not required; a rule `on: submit` instead |
| Removing a select option or a field that holds data | Rows keep the old value; history breaks | Keep it and hide it from the form; the kernel rates removal high |

## Money and stock (never rows)

| Red flag | Fix |
| --- | --- |
| **Never:** an amount, tax, discount or stock quantity decided by you or any LLM at run time | `compute` or a CEL `rule`; the number comes from data the owner entered |
| **Never:** a flow that submits a payment, an invoice or a stock move | The flow creates a draft; a person submits |
| **Never:** editing a submitted document to correct it | Cancel + amend, or a new correcting document |
| A company table that keeps its own payments or stock counts next to the core modules | Use `payments`, `invoicing`, `stock`; if a company table must hold money, say it is rated high and why |
| A high-risk part (money field, hook, submittable model) bundled with harmless labels | Split, so the safe part can be approved now (`rangka-changesets`) |
| Tax rates typed into a rule as numbers | A setting with the year it was checked (`rangka-compliance-id`) |

## Flows and approvals

| Red flag | Why it hurts | Fix |
| --- | --- | --- |
| A flow where a `rule` or a `compute` would do | More moving parts, harder for the owner to read | The lightest tool (`rangka-flows`) |
| A flow on an outside event without `idempotency:` | A replayed event books twice | `idempotency:` on a stable id from the event; refuse an empty one when money or stock is involved |
| A flow that only works if it runs once (adds to a running total) | Double counting after a retry | Find-or-create, or recompute from the source |
| No `description` on a flow, automation or action | Proses bisnis shows a generic sentence | One or two owner sentences with the conditions (contract 0060) |
| An approval with no `when` on a frequent document | Every sale waits for the owner | A threshold the owner stated in `when` |
| An approval whose only approver role is the role that prepares the document | The preparer approves their own refund or discount | Add an approver role the preparer does not hold (contract 0007) |

## People, roles and personal data

| Red flag | Why it hurts | Fix |
| --- | --- | --- |
| A new model granting every role `delete`, `cancel` or `export` | Anyone can remove or take out records | Grant only the verbs each role's work needs; owner and office keep the rest |
| NIK, KTP number, salary, health, home address visible to every role | UU PDP: personal data beyond need | `hidden_for` every role that does not need it; validator `contact_field_visible` warns for users' contact fields |
| A KTP photo (`image`/`attach`) when the number would do | More sensitive data to keep and leak | Store the number; ask the owner before adding a photo |
| Personal data in a notice text, a title field or a rule message | Shown to people who cannot open the record | Keep it behind the link (validator `notice_personal_field`) |
| Your fix needs a change to `roles/` or an existing model's `permissions` | Refused for you (`ai_forbidden_path`) | Propose the rest; tell the owner which setting in the Console an admin changes |

Example: a rental site keeps the renter's KTP number on the booking, hidden from warehouse staff (a company overlay,
contract 0016):

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/overlay.json
model: rental_booking
fields:
  - name: x_no_ktp
    type: data
    label: {id: No. KTP penyewa}
    help: {id: Hanya untuk jaminan sewa. Tidak perlu foto KTP.}
    hidden_for: [rental_staff]
rules:
  - name: x_no_ktp_16
    on: save
    check: "x_no_ktp == '' || (size(x_no_ktp) == 16 && x_no_ktp.matches('^[0-9]+$'))"
    message: {id: No. KTP harus 16 angka}
```

## Owner language

| Red flag | Fix |
| --- | --- |
| English labels, options or messages ("Status: Pending") | Bahasa Indonesia, sentence case ("Menunggu") |
| Platform words in a title, reason or reply ("tambah model x_deposit", "flow", "change set") | What it does: "Catat deposit dan pengembaliannya" (`rangka-consult` section 7) |
| A rule message that only says it failed | Say what to do: "Isi tanggal kembali dulu" |
| `1250000`, `2026-11-27` in text | `Rp 1.250.000`, `27 Nov 2026` |

## Size and honesty

| Red flag | Fix |
| --- | --- |
| Two unrelated requests in one change set | Two change sets, said so (`rangka-changesets`) |
| One change set per field for one feature | One change set for what only works together |
| "Sudah aku buatkan", "sudah jadi" before `applied` | Say what you prepared and what the owner does next |
| A pain the draft does not answer, left unsaid | Say it in one sentence: what is not covered and why |
| A capability named that is not in the catalogue or this change set | Remove it; say what exists instead |
