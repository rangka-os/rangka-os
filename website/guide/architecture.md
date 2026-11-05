# Architecture and layers

Rangka OS is organised in seven layers. An upper layer may use a lower one, never the reverse.

| Layer | Holds | Files | Who changes it |
| --- | --- | --- | --- |
| 6 · Presentation | Themes, layouts, pages built from UI blocks | `themes/*.rk`, `layouts/*.rk` | Owner, Rangka AI (low risk) |
| 5 · Modules and apps | Modules: models, views, rules, flows, roles, print formats, imports, processes. Apps: the modules they use, menu, dashboards, home, onboarding | `modules/<module>/**.rk`, `apps/<app>/**.rk` | App authors |
| 4 · Organisation | Installed apps, plan, config, company overlays, the company's own tables and automations; organisations and members are data | `config.rk`, `features.rk`, `custom/*.rk`, `models/x_*.rk`, `flows/*.rk` | Owner, Rangka AI (medium risk) |
| 3 · Integrations | Business connectors | `<connector>/connector.rk` | Admin |
| 2 · System capabilities | db, cache, queue, scheduler, realtime, files, search, notify, auth, secrets, audit, observability | `system.rk` | Admin only |
| 1 · Kernel | Loader, validator, migrations, CEL, REST + MCP, event bus, permissions | the `rangka` binary | Rangka OS releases |
| 0 · Deploy | Docker, Helm, desktop, partner one-click, Rangka Cloud | `deploy/` profiles | Outside the OS |

Two rails run alongside: **Rangka Console** reads every layer for maintenance, and **Rangka AI** writes layers 4–6
through change sets only. People use the **web app** at `/app/`; the kernel serves it and the Console (`/console/`)
from `rangka-os/web`.

## An installation

An installation is a **site** folder plus the apps, modules and connectors it uses. `config.rk` names apps; the
kernel installs the modules each app lists and everything those modules `require`, dependencies first. The example
sites live in `rangka-apps/examples/sites/` (`kopi-senja`, `demo-jasa`, `demo-fnb`), their demo data in
`rangka-apps/examples/fixtures/`.

```text
rangka-apps/examples/sites/kopi-senja/
  system.rk        # layer 2: which capabilities and drivers
  features.rk      # layer 4: plans and the apps they allow
  config.rk        # layer 4: installed apps, locale, tax, approvers, theme, layout
  custom/          # layer 4: overlays — company fields and rules on module models
  models/, flows/  # layer 4: the company's own tables (x_…) and automations (contract 0016), when it has any
  themes/          # layer 6
  layouts/         # layer 6
rangka-apps/apps/{crm,jasa_proyek,fnb_cabang}/       # layer 5: apps (menu, dashboards, home, onboarding)
rangka-apps/modules/{contacts,invoicing,stock,…}/    # layer 5: modules (models, rules, flows, roles)
rangka-connectors/<connector>/                       # layer 3
```

The kernel finds `modules/` next to the apps folder (`--modules` or `RANGKA_MODULES` to use another one).

## Inside the kernel

```text
 .rk files ──► loader (strict YAML, line:col) ──► schema + meaning checks ──► metadata
                                                                              │
          ┌──────────────────────────┬──────────────────────┬──────────────────┤
          ▼                          ▼                      ▼                  ▼
   migrations (Postgres)   lifecycle engine (CEL)   REST /api/v1 + SSE     MCP /mcp
                                     │
                     queue · scheduler · cache · audit · Rangka ID
```

The web app's client engine (`web/app/engine.js`, which grew out of the browser prototype) and the Python validator
(`tools/rangka_validate.py`) are the reference semantics; the kernel's tests check it matches them, including dashboard
numbers and validator output byte for byte (`make compare-python`).

## Relationship to Omilo

Omilo is a sister project: a company OS of AI helpers. Rangka is the business-apps OS of records, stock and money.
Omilo signs in with Rangka ID and reaches Rangka through MCP; Rangka never depends on Omilo.
