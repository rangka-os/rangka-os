# Status and roadmap

**v0.5 pre-releases — November 2026.** The kernel, web app, Console and Rangka AI run and are tested together.
Pre-releases `v0.5.0-alpha.*` are on GitHub Releases and npm (`next` tag). Not ready for production data yet.

| Area | Status |
| --- | --- |
| `.rk` format, schema 0.5 (20 kinds; 0.4 still published), Go + Python validators (byte-identical) | Done |
| Modules and apps (contract 0001), overlays, migrations with dry run, rollback and soft delete | Done |
| Document lifecycle, rules, computed fields, number series, document approvals | Done |
| Declarative stock moves with recipes and returns (0021, 0022), recurring documents (`add_months`, `for_each`, 0031) | Done |
| Plan limits (`rangka plan`, `GET /plan`, 0028), CSV export of lists and dashboards (0024), field visibility (0027) | Done |
| REST, SSE realtime, Postgres queue and scheduler, memory cache, audit, list aggregates | Done |
| Model permissions in REST, MCP, SSE, print and attachments; multi-org with row scope per outlet and Postgres RLS | Done |
| Attachments (local files), print formats (HTML, PDF through headless Chromium), spreadsheet imports | Done |
| Backup and restore (`rangka backup`, `rangka restore`); backup before migrations in production mode | Done (scheduled backups in `system.rk`: not yet) |
| Rangka ID: passwords, sessions, API tokens, OIDC provider, org switching | Done (passkeys, two-factor, sign-in through another identity provider: not yet) |
| Change sets with risk, approval, preview, live preview, apply, revert; MCP | Done |
| Dashboards (number, bar, line, donut, funnel, list), process canvas | Done |
| Work screens for staff (0030): entry screens, offline outbox with idempotency keys, the POS counter (cart, hold, split tender, sessions, receipt; `/compute`, 0034, stock per outlet 0039) | v0 (queue, geo, clock, roll, scan, handover, route: not yet) |
| Responsive layout (0038), nested navigation (0037), 44px touch targets in the web app, Console and design system | Done |
| Rangka skills for AI agents and the catalogue over MCP and REST (0035, 0036) | Done (installing from the catalogue: not yet) |
| Web app at `/app/` and Rangka Console | v0 |
| Rangka AI (Raka) and the kernel's chat relay | v0: tested offline and against the kernel; not yet run with a real model |
| Releases: binaries, ghcr.io image, npm `@rangka-os/cli` and `@rangka-os/create` | Pre-releases only |
| Go dependencies | pgx v5 and bcrypt are real; chi, cel-go, River, jsonschema and yaml.v3 are still in-tree stand-ins |

## Next

1. Swap the remaining stand-ins (chi, cel-go, River, jsonschema, yaml.v3), one library at a time.
2. Connector runtime (contract 0002) and connector secrets (0017); WhatsApp → lead first, then QRIS.
3. Delivery drivers: email and push for `notify`; S3 for files; Valkey for cache and realtime.
4. Relations: related lists, `on_delete`, many-to-many, trees.
5. Edge agent for outlet devices and offline sync (contracts 0003, 0006).
6. Rangka AI on a real model; Rangka Cloud onboarding.
7. Passkeys, two-factor, single sign-on with Omilo; scheduled backups.
