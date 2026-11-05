# Flows and automations

A flow is a list of steps that runs on an event or on a schedule, through the queue.
Reference: [flow.rk](/reference/schema/flow).

```yaml
# modules/pipeline/flows/morning_follow_up.rk
name: morning_follow_up
label: {id: Pengingat follow-up pagi}
schedule: 0 8 * * 1-5          # cron, in system.rk scheduler.timezone
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
| `find`, `find_all` | Load one / many records with a CEL `where` |
| `create`, `update` | Write a record with `set:` values (CEL) |
| `notify` | Send through a `system.rk` notify channel (logged only in v0, and written to the audit log) |
| `call` | Call a connector action (recorded in the audit log in v0; the connector runtime is not built yet) |
| `if` / `then` / `else` | Branch; every step may also carry an `if:` guard |
| `for_each` / `as` / `do` | Run the steps under `do` once per item of a list (a `find_all` variable), at most 2000 items, no `wait` or `approve` inside ([contract 0031](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0031-recurring-documents.md)) |
| `wait` | Wait for a duration (`wait: 2h`) or until a condition holds (`wait: {until: …}`, checked every minute); the rest of the flow runs later from the queue. Top level only |
| `approve` | Stop until a person with one of the `approvers` roles approves; reject ends the flow ([contract 0007](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0007-document-approvals.md)). Top level only |

`on:` takes a connector trigger (`whatsapp_business.message_received`) or a model event (`deal.updated`);
`schedule:` takes a cron. A scheduled flow runs once per due time even with several kernel nodes.

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
