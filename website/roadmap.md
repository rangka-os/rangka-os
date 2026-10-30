# Status and roadmap

**v0 — October 2026.** The kernel, Console and Rangka AI run and are tested together; not ready for production.

| Area | Status |
| --- | --- |
| `.rk` format, 14 schemas, Go + Python validators (byte-identical) | Done |
| Migrations, document lifecycle, rules, computed fields, overlays | Done |
| REST, SSE realtime, Postgres queue and scheduler, memory cache, audit | Done |
| Rangka ID: passwords, sessions, API tokens, OIDC provider | Done (passkeys, refresh tokens, org switching: not yet) |
| Change sets with risk, approval, preview, apply, revert; MCP | Done |
| Dashboards (number, bar, line, donut, funnel, list) | Done |
| Rangka Console | v0: 26 screens, demo and live |
| Rangka AI | v0: tested offline and against the kernel; not yet with a real model |
| Go dependencies | In-tree stand-ins for pgx, chi, cel-go, River, jsonschema, yaml.v3 — **next: swap in the real libraries** |

## Next

1. Real Go libraries; CI on GitHub Actions; releases with binaries.
2. Relations: related lists, `on_delete`, many-to-many, trees.
3. Connector runtime; QRIS and WhatsApp end to end; sending email and push.
4. Model permissions enforced; organisation switching; passkeys.
5. POS app with offline sales; Stock and Ledger apps.
6. Rangka AI on a real model; Rangka Cloud onboarding.
