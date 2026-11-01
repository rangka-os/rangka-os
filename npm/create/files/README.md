# {{NAME}}

A [Rangka OS](https://rangka-os.github.io/rangka-os/) project (template: {{TEMPLATE}}). Everything that
describes the business is a `.rk` file:

| Folder | What | Who changes it |
| --- | --- | --- |
| `apps/` | Ready apps (core, CRM, sales): models, dashboards, flows, roles | Upgraded as a whole; don't edit |
| `site/` | Your company layer: `config.rk`, `features.rk`, `system.rk`, `themes/`, `layouts/`, `custom/` overlays | You, the Console or Rangka AI, through change sets |
| `connectors/` | Integrations such as WhatsApp Business | Upgraded as a whole |
| `fixtures/` | Data loaded by `npm run seed` | You |

## Run it

```bash
npm install
npm run db       # Postgres 16 in Docker on port 54329 (or point DATABASE_URL in .env at your own)
npm run setup    # validate, create tables, load fixtures, create the owner account
npm run dev      # http://localhost:8080/console/  ·  API /api/v1  ·  MCP /mcp
```

`npm run dev` uses `--dev-open`: every request acts as the owner, for your own machine only.
`npm start` serves with sign-in (Rangka ID): the owner account is in `.env`.

## Change things

Edit a file under `site/`, then `npm run validate`. To add a field to a model, write an overlay in
`site/custom/<model>.rk` (fields start with `x_`) rather than editing `apps/`. See the
[docs](https://rangka-os.github.io/rangka-os/concepts/organisation).

Rangka OS is a v0 preview: not for production data yet.
