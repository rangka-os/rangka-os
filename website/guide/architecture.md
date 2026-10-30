# Architecture and layers

Rangka OS is organised in seven layers. An upper layer may use a lower one, never the reverse.

| Layer | Holds | Files | Who changes it |
| --- | --- | --- | --- |
| 6 · Presentation | Themes, layouts, pages built from UI blocks | `themes/*.rk`, `layouts/*.rk` | Owner, Rangka AI (low risk) |
| 5 · Apps | Models, views, rules, flows, dashboards, roles | `apps/<app>/**.rk` | App authors |
| 4 · Organisation | Installed apps, plan, config, company overlays; organisations and members are data | `config.rk`, `features.rk`, `custom/*.rk` | Owner, Rangka AI (medium risk) |
| 3 · Integrations | Business connectors | `<connector>/connector.rk` | Admin |
| 2 · System capabilities | db, cache, queue, scheduler, realtime, files, search, notify, auth, secrets, audit, observability | `system.rk` | Admin only |
| 1 · Kernel | Loader, validator, migrations, CEL, REST + MCP, event bus, permissions | the `rangka` binary | Rangka OS releases |
| 0 · Deploy | Docker, Helm, desktop, partner one-click, Rangka Cloud | `deploy/` profiles | Outside the OS |

Two rails run alongside: **Rangka Console** reads every layer for maintenance, and **Rangka AI** writes layers 4–6
through change sets only.

## An installation

An installation is a **site** folder plus the apps and connectors it uses:

```text
sites/kopi-senja/
  system.rk        # layer 2: which capabilities and drivers
  features.rk      # layer 4: plans and what they allow
  config.rk        # layer 4: installed apps, locale, tax, approvers, theme, layout
  custom/          # layer 4: overlays — company fields and rules on app models
  themes/          # layer 6
  layouts/         # layer 6
rangka-apps/apps/{core,sales,crm}/   # layer 5
rangka-connectors/<connector>/       # layer 3
```

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

The browser prototype (`web/prototype/engine.js`) and the Python validator (`tools/rangka_validate.py`) are the
reference semantics; the kernel's tests check it matches them, including dashboard numbers and validator output byte
for byte.

## Relationship to Omilo

Omilo is a sister project: a company OS of AI helpers. Rangka is the business-apps OS of records, stock and money.
Omilo signs in with Rangka ID and reaches Rangka through MCP; Rangka never depends on Omilo.
