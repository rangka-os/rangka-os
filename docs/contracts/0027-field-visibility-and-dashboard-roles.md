# 0027 Field visibility (hidden_for) and dashboard roles

Status: accepted (6 Nov 2026) · Owner of the change: rangka-os (kernel) · Affects: rangka-apps models that set
`hidden_for` (worker pay) and dashboards that set `roles` (project margin, retail tax), which until now were only
hints for the screens

## Context

Schema 0.5 lets a field say `hidden_for: [roles]` and a dashboard say `roles: [...]`, but the kernel enforced
neither: worker pay marked hidden for sales, purchasing, head office and outlet staff was returned by the API to
anyone who could read workers (gladi 6 Nov: outlet staff of the production app read worker wages), and a
dashboard meant for finance opened for every role that could read its models. Apps worked around it by taking
read access away from whole models (rangka-apps `docs/permissions.md`).

## Decision

1. **Who.** A field with `hidden_for` is hidden from an actor whose every role is in the list: one role that may
   see it is enough, as grants add up. The owner and kernel actors (seed, hooks, schedules) see every field. A money
   field's IDR twin (`<field>_idr`) follows its field.
2. **Reads.** A hidden field is removed from what reaches the actor: `GET` of a record, list rows, the result of
   create, update, submit, cancel and amend, MCP reads, prints, export rows (and its column), dashboard list
   widgets, and `GET /api/v1/meta` (the field is not described). Rows of table fields are redacted with their own
   model's `hidden_for`.
3. **No oracle.** In a filter the caller writes, a hidden field reads as its zero value (also through links, table
   rows and `option()`); `q`
   does not search it; a sort by it is ignored; an aggregate grouping or totalling it is refused with 403
   `field_hidden`; a link whose title field is hidden shows the stored code.
4. **Writes.** A hidden field in a create, import or update from the actor is dropped, so they cannot set what they
   cannot see; on an update the stored value stays, also in table rows matched by row name. The result of an
   approval or a held action is redacted like any other write result. Flows, hooks and computes still read and write it (metadata is trusted).
5. **Dashboards.** A dashboard with `roles` opens only for an actor holding one of them (and the owner): others get
   403 and `meta.dashboards` leaves it out. A widget whose `field`, `group_by`, `sort` or a `columns` entry is
   hidden from the actor is returned as `{"hidden": true}`.

## Consequences

- Apps can give a role read on a model and still keep some of its fields from that role; rangka-apps may restore
  read access that it removed only to protect pay.
- Permission `if:` conditions are evaluated on the stored record, so a grant may still depend on a hidden field.
- Expressions in metadata (computes, widget filters, flows) see every field: a widget filter that tests a hidden
  field still counts with it.
- `system/audit` keeps full values; it is owner-only.
- Known gaps (audit 2026-11-07, low): the record guard of prints and attachments evaluates `if:` on the redacted
  record; an approval's snapshot hash covers hidden fields; an import preview shows the file's values to whoever
  uploaded it. Apps that copy a hidden value into a field without `hidden_for` (rangka-apps assignment and timesheet
  `cost_rate`) show it there; such fields need their own `hidden_for`.
