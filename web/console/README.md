# Rangka Console v0

The open-source control pane of Rangka OS: what an owner or operator opens to see and maintain every mapping of the
installation, layer by layer (0 deploy · 1 kernel · 2 system capabilities · 3 connectors · 4 organisation · 5 apps ·
6 presentation), and to move change sets through their lifecycle.

Plain JS on React 18 UMD and the Rangka design system (`../design-system/bundle.js`, `window.Rangka`). No build step.

| File | What it is |
| --- | --- |
| `index.html` | Loads React (cdnjs), the DS bundle, `tokens.css`, `demo-data.js`, `api.js`, `console.js`; local `cn-*` styles on DS tokens |
| `console.js` | Screens, shell (TopNav + Sidebar grouped by layer), ⌘K command menu, theme switch |
| `api.js` | `RangkaConsoleAPI({baseUrl})`: the data adapter for CONTRACTS.md `/api/v1`, with an in-memory demo backend |
| `build_demo.py` | Builds `demo-data.js` and `tokens.css` from the real `.rk` files and `tokens.json` |
| `demo-data.js`, `tokens.css` | Generated. Do not edit by hand |
| `test_console.py` | Headless Playwright check in demo mode (every screen, 1440/390, light/dark, lifecycle, contract rules) |
| `test_live.py` | Optional: the same against a real kernel (temp Postgres, temp copy of the site); prints SKIP when Go/Postgres/the build are unavailable |

## Run

Serve `rangka-os/web` (the Console reads `../design-system/`):

```sh
cd rangka-os/web/console && python3 build_demo.py      # after any .rk change
cd .. && python3 -m http.server 8000
# demo:   http://localhost:8000/console/?demo=1
```

**Against a kernel.** Serve the Console from the kernel's origin (so the `rk_session` cookie applies), or set the base
URL before `console.js` loads:

```html
<script>window.RANGKA_CONSOLE_CONFIG = { baseUrl: "http://localhost:8080" }</script>
```

The adapter probes `GET /api/v1/system/status` (2.5 s). 200 + JSON → kernel mode; 401/403 → kernel mode with a sign-in
form (`POST /auth/login`); network error, timeout, 404 or a non-JSON answer → demo mode. `?demo=1` forces demo.
The top bar shows **Mode demo** (purple) or **Terhubung** (green); the side panel says why.

## Screens (UI in Bahasa Indonesia)

- **Ringkasan**: number cards; the layer stack 6→0 with OK / warn / off lozenges per capability, app, connector and
  deploy target; recent change sets; recent audit; live SSE events.
- **Kapabilitas sistem** (`#/system`, `#/system/<cap>`, `#/system/source`): kernel and deploy, database, cache (hit
  rate, memory), realtime (connections per model), Rangka ID (issuer, OIDC clients from `system.rk`), queue (counts
  per state, jobs table with state/kind filters), scheduler (cron, next/last run, failures), notify channels, files,
  search, secrets, audit. `system.rk` as text with line numbers or parsed JSON.
- **App & model** (`#/apps`, `#/apps/model/<m>/<tab>`, `#/apps/{dashboard,flow,role,app}/<name>`): apps → models,
  company overlays, dashboards, flows, roles; per model: the app's fields and rules, and separately the
  **Lapisan perusahaan** (fields/rules/view additions from `custom/<model>.rk`, split back out of the merged model the
  kernel serves, with the overlay source), views with overlay additions marked, permissions, capabilities used, source.
- **Change set** (`#/changesets`, `#/changesets/<id>`): filterable list; detail with lifecycle Steps, ops (readable
  JSON Patch before preview, line diff of the `.rk` text after), validation findings, preview SQL + affected counts,
  risk and layers, history timeline. Buttons Validasi / Pratinjau / Tolak / Setujui / Terapkan / Kembalikan follow
  `RangkaConsoleAPI.TRANSITIONS`. Setujui asks for confirmation; it is disabled with an explanation when the risk is
  high and the user is not the owner, or the user's role is not in `config.ai.approvers`. **Usulkan perubahan**
  (top bar) proposes a `replace` of any layer 2–6 file.
- **Konektor**: installed connectors, health, triggers/actions, credentials (secret flags only), used by, recent events.
- **Organisasi & member** (`#/orgs/<id>`): plan from `features.rk` (with `extends`), limit usage bars, capabilities,
  `config.rk` defaults, members with roles and 2FA.
- **Tampilan**: resolved theme tokens (light + dark swatches, overridden tokens marked, derived dark values noted),
  layout settings and a shell preview painted with the theme's tokens.
- **Audit log**: filter by kind, actor, organisation; paginated.

Demo mode adds a role switch (Pemilik / Manajer sales) in the side panel to show the owner-only rule.

## Demo data

`build_demo.py` reads `rangka-apps/apps/*`, `rangka-apps/sites/kopi-senja/*`, `rangka-connectors/*` with
`tools/rkfile.py`. Metadata and file texts are real. Change sets are computed: each op is applied to a copy, the
reference validator `tools/rangka_validate.py` runs on the copy (the "delete deal.source" change set really fails with
`unknown_field`), and before/after texts become the preview diff. Status numbers, schedules' run times (computed from
the real cron expressions), jobs, audit entries, orgs and members are simulated. Timestamps are rebased onto the
viewer's clock. The demo backend implements every transition, applies/reverts file texts and metadata, writes audit
entries, and emits `record` / `job` / `changeset` events every 3.5 s.

## Test

```sh
npm pack react@18.3.1 react-dom@18.3.1 && for f in *.tgz; do tar xzf $f; done   # in a temp dir
python3 test_console.py --react-dir <dir with react(-dom).production.min.js> [--shots <dir>]
python3 test_live.py --react-dir <same dir> [--shots <dir>]      # optional, needs Go + PostgreSQL 16
```

## Contract (v0.1)

The adapter follows CONTRACTS.md "Kernel v0 additions" and "v0.1 decisions": `/meta` with `flows`, `roles`,
`connectors`, `site{config, features, themes, layouts, system}`, `installed`, models carrying `app` and `overlay`;
`GET /files` and `GET /files/{path}` (falling back to the `/files/meta` aliases); `/system/status` with `deploy`,
`schema_version`, `connectors[]`, `auth.clients[]` objects; change sets with `created_at`/`updated_at`,
`preview.files[{path, before, after}]` and validator items `{level, file, pos, path, code, message}`; approve only from
`previewed`, high risk owner-only (403 `owner_required` is shown as such); audit `?kind=&actor=&org=`; errors
`{error:{code,message}}` and 422 `{errors, warnings}`. Theme dark values use decision 12 (`api.js` `deriveDark` is a
port of `rangka_validate.derive_dark` and matches it). Status numbers the kernel does not report (cache memory,
secrets, audit counts …) show as "Tidak dilaporkan" or "—".

The demo has one simulated, already-applied overlay (`custom/customer.rk`, change set `cs_ai5ig3wq`), generated by
`build_demo.py` and never written to the site. The Rangka AI demo change set "Tambah nomor PO di faktur" creates
`custom/sales_invoice.rk`; nothing under `apps/` changes.
