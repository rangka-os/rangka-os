# rangka-os

Open-source, declarative operating system for business apps (ERP, POS, CRM) for Indonesian SMEs. Every app, model,
rule, dashboard, theme, layout and backend capability is a `.rk` file (strict YAML with JSON Schemas); the kernel
turns them into a database, a REST + MCP API and screens, and changes them at runtime through reviewed change sets.

| Folder | What |
| --- | --- |
| `kernel/` | Go kernel: `rangka validate · migrate · seed · serve`. REST, SSE realtime, queue, scheduler, cache, Rangka ID (OIDC), change sets, MCP. See `kernel/README.md` |
| `schema/0.4/` | JSON Schemas for every `.rk` kind: app, model, dashboard, flow, overlay, role, connector, template, system, theme, layout, features, config, changeset |
| `tools/` | Reference validator (`rangka_validate.py`), strict `.rk` reader (`rkfile.py`), converters, docs + prototype builders |
| `templates/` | Theme and layout templates (`rangka/standard`, `rangka/retail`, `rangka/clinic`, `top-nav`, `side-only`, `pos`, `portal`) |
| `web/design-system/` | React component bundle (`window.Rangka`, 76 exports), tokens, types — Atlassian-style look, light and dark |
| `web/console/` | Rangka Console: the control pane for maintenance (demo mode + live against the kernel) |
| `web/prototype/` | Browser-only renderer of the apps (reference semantics) |
| `docs/` | `contracts.md` (REST, MCP, CEL, change sets), API reference page |

Layers: 0 deploy · 1 kernel · 2 system capabilities (`system.rk`) · 3 connectors · 4 organisation (`config.rk`,
`features.rk`, `custom/*.rk` overlays) · 5 apps · 6 presentation (`themes/`, `layouts/`).

Quick start (Go 1.24, Postgres 16): `cd kernel && make run-demo`, then open `http://localhost:8080/console/` and sign in
as `ceo@saga.co.id` / `rangka-demo`. Apps come from the rangka-apps repo (clone it next to this one).

Status: v0. The kernel currently uses in-tree stand-ins for pgx, chi, cel-go, River, jsonschema and yaml.v3 (see
`kernel/README.md`); swapping in the real libraries is the first task once the repo builds with network access.
