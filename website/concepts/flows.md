# Flows and automations

A flow is a list of steps that runs on an event or on a schedule, through the queue.
Reference: [flow.rk](/reference/schema/flow).

```yaml
# modules/pipeline/flows/morning_follow_up.rk
name: morning_follow_up
label: {id: Pengingat follow-up pagi}
schedule: 0 8 * * 1-5          # cron, in the org's timezone (else system.rk scheduler.timezone)
steps:
  - {find_all: activity, where: "!done && due_date <= today()", as: due}
  - if: size(due) > 0
    notify: owner
    channel: email
    template: {id: 'Ada {size(due)} aktivitas yang harus di-follow-up hari ini'}
```

```yaml
# modules/pipeline/flows/whatsapp_to_lead.rk
on: whatsapp_business.message_received
steps:
  - {find: lead, where: phone == event.from, as: lead}
  - if: lead == null
    create: lead
    set: {lead_name: event.profile_name, phone: event.from, source: "'whatsapp'"}
idempotency: event.message_id
```

| Step | Does |
| --- | --- |
| `find`, `find_all` | Load one / many records with a CEL `where`, newest first (or `sort:`, `limit:`). Plain conditions joined by `&&` (`field == value`, `field in list` on text fields and `name`, `docstatus == n`/`!= n`, the value read from `event` or flow variables) are read in SQL; the whole `where` is still checked on every row, and a `find` without `sort:` stops at its first match |
| `create`, `update` | Write a record with `set:` values (CEL) |
| `notify` | Tell people ([contract 0066](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0066-kernel-notices.md)): a role (or `owner`) gets a notice in the app's notifications (kind `app.<module>.<flow>`, or `site.<flow>` for a company flow); a target that resolves to field values or a variable tells the members those values name (their email or user record). Also written to the audit log as `notify.<channel>` Email also goes out over SMTP when `notify.email` sets `driver: smtp` (queued, with retries). |
| `call` | Call a connector action (recorded in the audit log in v0; the connector runtime is not built yet) |
| `if` / `then` / `else` | Branch; every step may also carry an `if:` guard |
| `for_each` / `as` / `do` | Run the steps under `do` once per item of a list (a `find_all` variable), at most 2000 items, no `wait` or `approve` inside ([contract 0031](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0031-recurring-documents.md)) |
| `wait` | Wait for a duration (`wait: 2h`) or until a condition holds (`wait: {until: …}`, checked every minute); the rest of the flow runs later from the queue. Top level only |
| `approve` | Stop until a person with one of the `approvers` roles approves; reject ends the flow ([contract 0007](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0007-document-approvals.md)). Top level only |

`on:` takes a connector trigger (`whatsapp_business.message_received`) or a model event (`deal.updated`);
`schedule:` takes a cron. A scheduled flow runs once per due time for every organisation on the host, even with several kernel nodes; with several organisations their runs are spread over `scheduler.stagger_s` (contract 0010 §9).

## Notices

A `notify` step puts a notice in the notifications of the people it names (the bell in the app's top bar), and on
a hosted kernel the host also mails them ([contract 0066](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0066-kernel-notices.md)).
A module declares the notices it raises in `module.rk`, so their roles, severity and label live in one place:

```yaml
# modules/payments/module.rk
notices:
  - name: gateway_mismatch
    label: {id: Selisih saldo pembayaran, en: Payment balance mismatch}
    severity: high                 # info | warn | high; default info
    to: [owner, finance]           # roles; owner is always allowed
    can: {model: gateway_mismatch, action: read}   # optional: only roles that hold this permission
    link: record                   # record | approvals | none
```

```yaml
- notify: owner                    # added to the declaration's to:
  notice: gateway_mismatch         # app.payments.gateway_mismatch
  title: {id: 'Selisih saldo {m.date}'}        # at most 80 characters
  template: {id: 'Laporan DOKU {m.closing}, catatan {m.ledger}.'}   # at most 300
  record: m                        # the link, and only people who can read it are told
  outlet: m.outlet                 # CEL: members of other outlets are left out
  id: "'gm:' + m.name"             # CEL: the same id is one notice
```

Without `notice:` a step to a role tells that role with kind `app.<module>.<flow>` (`site.<flow>` for a company
flow). Keep the text to what every recipient may read in the app: numbers, amounts, names; never a customer's phone,
address or bank account (`notice_personal_field`). The validator also reports `unknown_notice`, `duplicate_notice`,
`notice_role_cannot_read` and `notice_too_long`; a change set from Raka whose flow tells someone by email address is
raised to high (`notice_literal_email`).

## Record-event flows

A flow with `on: <model>.created`, `.updated`, `.submitted` or `.cancelled` runs inside the write that raised the
event: in the same transaction and as the person (or token) who wrote. It can do no more than they could by hand.

The `event` holds a copy of the record's fields at top level (`event.negative`, `event.item`), and on top of them:

| Key | Holds |
| --- | --- |
| `model` | the model name (`deal`) |
| `name` | the record's name (`DEAL-00012`) |
| `op` | `created`, `updated`, `submitted` or `cancelled` |
| `record` | the record after the write |
| `before` | the record as stored before the write; `null` on `created` |

`before` lets a flow act on a change only, for example when a deal becomes won:

```yaml
on: deal.updated
steps:
  - {find: deal, where: "name == event.name", as: record}
  - if: "record.stage == 'won' && (event.before == null || event.before.stage != 'won')"
    update: record
    set: {probability: "100"}
```

A flow step that fails aborts the write: the person sees the error and nothing is stored. What the write answers is
the record as the flows left it. Writes made by a flow, by a connector event flow or by a scheduled flow never start
record-event flows, so a flow cannot trigger itself.
Money and stock never depend on a flow driven by an LLM.
