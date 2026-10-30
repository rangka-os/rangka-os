# Rangka OS

**An open-source, declarative operating system for business apps** — CRM, sales, POS, inventory, ERP — built for
Indonesian small and medium businesses.

Every app, model, rule, dashboard, theme, layout and backend capability is a `.rk` file (strict YAML, validated by
JSON Schema). The kernel turns those files into a Postgres database, a REST + MCP API, realtime events and screens,
and changes them while the system runs through reviewed **change sets** — so an owner, the Console or an AI agent
can adapt the software without an engineer writing code.

> **Status: v0 (October 2026).** Everything below runs and is tested, but the kernel still uses small in-tree
> stand-ins for its third-party Go libraries (see [Status](#status)). Not production-ready.

## The Rangka family

| Repo | What | Licence |
| --- | --- | --- |
| **[rangka-os](https://github.com/rangka-os/rangka-os)** (this repo) | Kernel, schemas, validator, design system, Console, docs | AGPL-3.0 + app exception (planned) |
| [rangka-apps](https://github.com/rangka-os/rangka-apps) | Apps (core, sales, crm), templates, example site | to be decided |
| [rangka-connectors](https://github.com/rangka-os/rangka-connectors) | Business integrations as `connector.rk` (WhatsApp, QRIS, …) | to be decided |
| [rangka-ai](https://github.com/rangka-os/rangka-ai) | Agent that changes an installation through change sets | AGPL-3.0 |
| rangka-cloud | Managed hosting, onboarding chat, billing | private |

## How it fits together

```
 6  Presentation    themes/*.rk · layouts/*.rk            ← owners, Rangka AI (low risk)
 5  Apps            apps/<app>/{app,models,dashboards,flows,roles}.rk   ← app authors
 4  Organisation    config.rk · features.rk · custom/<model>.rk overlays ← owners, Rangka AI
 3  Integrations    rangka-connectors/<c>/connector.rk     ← admin
 2  Capabilities    system.rk: db · cache · queue · scheduler · realtime · files · search · notify · auth
 1  Kernel          this repo: rangka validate · migrate · seed · serve (Go)
 0  Deploy          Docker · Helm · desktop · partner one-click · Rangka Cloud
```

An installation is a **site** folder (`system.rk`, `features.rk`, `config.rk`, `themes/`, `layouts/`, `custom/`)
plus the apps and connectors it uses. App authors declare needs in one line — `realtime: true` on a model,
`schedule: "0 8 * * 1-5"` on a flow, `queue:` on an action, `cache: 5m` on a dashboard — and `system.rk` decides which
driver serves them. By default everything runs on one Postgres and one binary.

A small model:

```yaml
# yaml-language-server: $schema=https://rangka.dev/schema/0.4/model.json
name: deal
label: {id: Peluang, en: Deal}
realtime: true
fields:
  - {name: customer, type: link, to: customer, required: true}
  - name: stage
    type: select
    options:
      - {value: prospect, label: {id: Prospek}, probability: 10}
      - {value: won, label: {id: Menang}, probability: 100, tone: ok}
  - {name: value, type: currency}
  - {name: weighted_value, type: currency, compute: round(value * option('stage', 'probability') / 100)}
views:
  kanban: {group_by: stage, sum: value}
```

## Quick start

Requirements: Go 1.24+, PostgreSQL 16 (binaries on the machine; the scripts start a throwaway cluster), Python 3 with
`pyyaml` and `jsonschema` for the reference tools. Clone the repos side by side:

```bash
mkdir rangka && cd rangka
git clone git@github.com:rangka-os/rangka-os.git
git clone git@github.com:rangka-os/rangka-apps.git
git clone git@github.com:rangka-os/rangka-connectors.git

cd rangka-os/kernel
make run-demo        # temp Postgres → migrate → seed → serve on :8080 (Ctrl-C cleans up)
```

Then open:

| URL | What |
| --- | --- |
| http://localhost:8080/console/ | Rangka Console (sign in: `ceo@saga.co.id` / `rangka-demo`) |
| http://localhost:8080/prototype/ | The apps rendered from metadata |
| http://localhost:8080/api/v1/meta | Metadata API |
| `POST http://localhost:8080/mcp` | MCP endpoint for AI clients |

Other commands:

```bash
make test             # every package against a throwaway Postgres
make validate         # validate rangka-apps + the kopi-senja site + rangka-connectors
make compare-python   # Go validator vs tools/rangka_validate.py (must be byte-identical)
python3 tools/rangka_validate.py ../rangka-apps/apps --site ../rangka-apps/sites/kopi-senja --connectors ../rangka-connectors
```

The Console also runs without a kernel: `cd web && python3 -m http.server 8000`, then open
http://localhost:8000/console/?demo=1.

## Repository layout

| Path | What |
| --- | --- |
| [`kernel/`](kernel/README.md) | Go kernel: `.rk` loader + validator, migrations, CEL, lifecycle, REST, SSE, queue, scheduler, cache, Rangka ID (sessions, API tokens, OIDC provider), change sets, MCP |
| [`schema/0.4/`](schema/0.4) | JSON Schemas for all 14 `.rk` kinds: app, model, dashboard, flow, overlay, role, connector, template, system, theme, layout, features, config, changeset |
| [`tools/`](tools) | Reference validator `rangka_validate.py`, strict `.rk` reader `rkfile.py`, `rk_convert.py` (JSON → `.rk`), API-docs and prototype builders |
| [`templates/`](templates) | Theme templates (`standard`, `retail`, `clinic`) and layout templates (`top-nav`, `side-only`, `pos`, `portal`) |
| [`web/design-system/`](web/design-system) | React component bundle (`window.Rangka`, 76 exports), tokens, TypeScript types — Atlassian-grade structure in Rangka colours (Ink, Bata, warm stone), light and dark |
| [`web/console/`](web/console/README.md) | Rangka Console, the control pane for operators: capabilities, apps and models, change sets, connectors, organisations, themes, audit |
| [`web/prototype/`](web/prototype) | Browser-only renderer of the apps; the reference semantics for the kernel |
| [`docs/contracts.md`](docs/contracts.md) | The binding contract: REST, SSE events, MCP tools, CEL dialect, change sets, v0.1 decisions |
| [`docs/site/`](docs/site) | API reference page generated from metadata |

## Concepts in one page

- **`.rk` files** are YAML with strict rules so every tool reads them the same way: YAML 1.2 types only; unquoted
  `yes`/`no`/`on`/`off`, leading-zero numbers (phone, NPWP) and `08:30` are refused; no duplicate keys, tabs or anchors.
  Errors point at `file:line:col`. VS Code gets completion through the `$schema` comment on the first line.
- **Expressions** (defaults, computed fields, rules, filters) use a CEL dialect with `today()`, `option()`,
  `app_installed()`, link traversal such as `customer.payment_days`, and more — see `docs/contracts.md`.
- **Documents** (`is_submittable`) follow draft → submitted → cancelled, with amendment; numbering happens at submit;
  submitted documents are immutable.
- **Company layer**: an organisation's own fields and rules live in `custom/<model>.rk` overlays (`x_` names), merged
  at load, stored in a JSONB column, never colliding with app upgrades.
- **Change sets** are the only way a running installation changes: propose → validate → preview (SQL, affected
  records, before/after files) → approve (a person; high risk = owner only) → apply → revert.
- **Rangka ID** is built in: password login, API tokens and an OpenID Connect provider so Omilo and other apps sign in
  with the same account.

## Status

| Area | v0 |
| --- | --- |
| Validator (Go + Python), migrations, lifecycle, dashboards, REST, SSE, queue, scheduler, memory cache, Rangka ID + OIDC, change sets, MCP | Working, tested on Postgres 16 |
| Console | 26 screens, demo and live mode, Playwright tests |
| Go dependencies | In-tree stand-ins for pgx, chi, cel-go, River, jsonschema, yaml.v3 and bcrypt (each behind one package) — **to be replaced with the real libraries next** |
| Not yet | Valkey drivers, file uploads, sending email/push/WhatsApp (logged only), connector runtime, model `permissions:` enforcement, payments, SQL-level list filters, OIDC refresh tokens, relations (reverse lists, on-delete, many-to-many) |

## Contributing

Issues and pull requests are welcome. Before a PR: `make test`, `make validate` and `make compare-python` must pass,
and any change to `.rk` semantics must land in both validators and in `docs/contracts.md`. UI copy is Bahasa Indonesia;
code, comments and docs are English.

## Licence

Intended: AGPL-3.0, with an exception so apps and templates built on Rangka OS may use their own licence, plus a
contributor licence agreement. The `LICENSE` file is not committed yet; until it is, all rights are reserved.
