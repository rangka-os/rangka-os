# Flows and automations

A flow is a list of steps that runs on an event or on a schedule, through the queue.
Reference: [flow.rk](/reference/schema/flow).

```yaml
# apps/crm/flows/morning_follow_up.rk
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
# apps/crm/flows/whatsapp_to_lead.rk
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
| `notify` | Send through a `system.rk` notify channel (logged only in v0) |
| `call` | Call a connector action (recorded in the audit log in v0) |
| `if` / `then` / `else` | Branch; every step may also carry an `if:` guard |
| `wait` | Wait for a duration or an event |

`on:` takes a connector trigger (`whatsapp_business.message_received`) or a model event (`deal.updated`);
`schedule:` takes a cron. A scheduled flow runs once per due time even with several kernel nodes.
Money and stock never depend on a flow driven by an LLM.
