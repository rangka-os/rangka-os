# Your first app

Build a tiny **service tickets** app on top of `core`, in four files.

## 1. The manifest

```yaml
# apps/service/app.rk
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/app.json
name: service
version: 0.1.0
title: {id: Servis, en: Service}
requires: {rangka: '>=0.4', core: ^1.0}
dashboards: [service_overview]
menu:
  - group: {id: Servis}
    items: [ticket]
```

## 2. A model

```yaml
# apps/service/models/ticket.rk
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/model.json
name: ticket
label: {id: Tiket servis, en: Service ticket}
naming: TKT-{YYYY}-{####}
title_field: subject
realtime: true
fields:
  - {name: subject, type: data, label: {id: Keluhan}, required: true, in_list: true}
  - {name: customer, type: link, to: customer, label: {id: Pelanggan}, required: true, in_list: true}
  - name: priority
    type: select
    label: {id: Prioritas}
    default: "'normal'"
    chip: true
    in_list: true
    options:
      - {value: low, label: {id: Rendah}}
      - {value: normal, label: {id: Normal}}
      - {value: urgent, label: {id: Mendesak}, tone: bad}
  - {name: due_date, type: date, label: {id: Target selesai}, default: today() + duration('3d')}
  - {name: done, type: check, label: {id: Selesai}}
rules:
  - name: urgent_needs_due_soon
    on: save
    check: priority != 'urgent' || due_date <= today() + duration('1d')
    message: {id: Tiket mendesak harus selesai paling lambat besok}
views:
  list: {columns: [name, subject, customer, priority, due_date], sort: due_date}
  kanban: {group_by: priority}
```

## 3. A dashboard

```yaml
# apps/service/dashboards/service_overview.rk
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/dashboard.json
name: service_overview
label: {id: Ringkasan servis}
widgets:
  - {name: open, type: number, label: {id: Tiket terbuka}, model: ticket, filter: '!done', good_when: down, span: 4}
  - {name: by_priority, type: chart, chart: donut, label: {id: Per prioritas}, model: ticket, group_by: priority, filter: '!done', span: 8}
  - name: due
    type: list
    label: {id: Jatuh tempo}
    model: ticket
    filter: '!done'
    sort: due_date
    limit: 5
    columns: [subject, customer, priority, due_date]
    span: 12
```

## 4. Install and validate

Add `service` to the site's `config.rk` (`apps: [core, crm, sales, service]`) and to the plan in `features.rk`, then:

```bash
cd rangka-os/kernel && make validate && make run-demo
```

The validator checks every reference: the `customer` link, the CEL in the rule and filters, the dashboard columns,
and that the plan allows the app.
