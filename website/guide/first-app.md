# Your first app

Build a tiny **service tickets** app on the `contacts` module, in five files.

## 1. A module and an app

Since [contract 0001](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0001-modules-and-apps.md) metadata comes in two levels, both in `rangka-apps`:

- a **module** (`modules/<name>/module.rk`) holds models, rules, flows and roles, and `requires` other modules;
- an **app** (`apps/<name>/app.rk`) is what a site installs: the modules it uses, the menu, dashboards and home page.
  An app never defines models.

Modules and apps share one namespace, so the module is `tickets` and the app is `service`:

```yaml
# modules/tickets/module.rk
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/module.json
name: tickets
version: 0.1.0
title: {id: Tiket servis, en: Service tickets}
requires: {rangka: '>=0.5', contacts: ^1.0, users: ^1.0}
```

```yaml
# apps/service/app.rk
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/app.json
name: service
version: 0.1.0
title: {id: Servis, en: Service}
rangka: '>=0.5'
modules: {contacts: ^1.0, users: ^1.0, tickets: ^0.1}
dashboards: [service_overview]
menu:
  - group: {id: Servis}
    items: [ticket, customer]
home: {model: ticket, view: kanban}
```

## 2. A model in the module

```yaml
# modules/tickets/models/ticket.rk
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json
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

## 3. A dashboard in the app

```yaml
# apps/service/dashboards/service_overview.rk
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/dashboard.json
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

Add the app to a site. In `examples/sites/kopi-senja`, add `service` to `apps:` in `config.rk`
(`apps: [crm, service]`) and to the `apps:` of each plan in `features.rk`. The kernel installs the app's modules
(`contacts`, `users`, `tickets`) and whatever they `require`. Then, with the repos cloned side by side:

```bash
cd rangka-os/kernel
make validate APPS=../../rangka-apps/apps SITE=../../rangka-apps/examples/sites/kopi-senja
RANGKA_FIXTURES=../../rangka-apps/examples/fixtures/kopi-senja \
  make run-demo APPS=../../rangka-apps/apps SITE=../../rangka-apps/examples/sites/kopi-senja
```

Both commands find `modules/` next to `apps/` (pass `--modules` to `rangka` to use another folder). The validator
checks every reference: the `customer` link (the `tickets` module must `require` `contacts`), the CEL in the rule and
filters, the dashboard columns, that the app defines no models, and that the plan allows the app. The Python
reference gives the same report:

```bash
python3 tools/rangka_validate.py ../rangka-apps/apps --site ../rangka-apps/examples/sites/kopi-senja --connectors ../rangka-connectors
```
