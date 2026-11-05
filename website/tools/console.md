# Rangka Console

The control pane of Rangka OS: one place that knows every mapping of an installation, for maintenance and for
reviewing changes. It ships in `rangka-os/web/console` and is served by the kernel at `/console/`.

| Screen | Shows |
| --- | --- |
| Ringkasan | Health of every layer as a stack (OK / warning / off), version, uptime, recent change sets and audit entries, live events |
| Kapabilitas sistem | One card per capability — database, cache hit rate, queue (jobs by state), scheduler (next and last run), realtime connections, Rangka ID and OIDC clients, notify channels, files, search — plus `system.rk` source |
| App & model | Apps → models, dashboards, flows, roles; fields, views with a form preview, rules, permissions, which capabilities a model uses, the company-layer overlay, and the `.rk` source with line numbers |
| Proses | The [process canvas](/guide/process-canvas) of each process; an edit in the side panel becomes a change set |
| Change set | Lifecycle steps, a readable diff of every file, validation, preview SQL and affected records, risk and layers, history; validate, preview, approve, reject, apply and revert buttons per state. **Usulkan perubahan** proposes a change to any layer 2–6 file |
| Konektor | Installed connectors: health, triggers, actions, which credentials are secret, where they are used |
| Organisasi & member | Plan with usage bars, config defaults, members with roles and two-factor status |
| Tampilan | Theme tokens in light and dark (overrides marked, derived dark values noted), a preview of the layout shell |
| Audit log | Every write and change-set transition, filterable by kind, actor and organisation |

The screens sit in a Frappe-style sidebar grouped by layer, with ⌘K search. When the kernel runs the AI sidecar,
**Tanya Raka** (⌘J) opens [Raka](/guide/assistant) here too; a change Raka proposes opens on the Change set screen,
where a person approves it.

Run it against a kernel (sign in with your Rangka ID), or with no kernel at all in demo mode:

```bash
cd rangka-os/web/console && python3 build_demo.py     # regenerate demo data from the real .rk files
cd .. && python3 -m http.server 8000                   # http://localhost:8000/console/?demo=1
```
