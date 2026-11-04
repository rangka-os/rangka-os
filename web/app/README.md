# Rangka end-user web app

The browser app people use every day: dashboards, lists, kanban, forms with child tables, approvals, print,
attachments and CSV imports. It is plain React (UMD) plus the design system, with no build step, and it talks only to
a Rangka kernel over `/api/v1`. The kernel serves this folder at `/app/` (`/` redirects there). Routes are hash routes,
so deep links survive a reload and the kernel needs no SPA fallback:

`#/d/<dashboard>` · `#/r/<model>` · `#/r/<model>/new` · `#/r/<model>/<name>` · `#/approvals` · `#/import/<import>` · `#/proses` · `#/proses/<process>`

The UI is Bahasa Indonesia first and works from 400px (phone) to desktop. Metadata changes are not made here: they go
through the Console and change sets.

## Files

| File | What |
| --- | --- |
| `index.html` | Loads React, the design system, `tokens.css`, then the files below; app CSS (`ap-*`, incl. the ≤ 600px rules) |
| `tokens.css` | Design tokens (Ink, Bata, Kapur), light and dark |
| `engine.js` | CEL subset, defaults, computed fields, rules and status; `RangkaEngine.create(meta, lookup)`. Reference semantics for the kernel |
| `api.js` | `RangkaAppAPI`: every kernel call, `ApiError`, feature detection, `parseRupiah` / `rupiah` |
| `shell.js` | Router, sign-in, top nav, sidebar from `meta.apps[].menu`, org switcher, 401 handling; mounts last |
| `list.js` | List (server search, quick filters, sort, pages) and kanban |
| `form.js` | Form, field editors, link search, child grid, live preview, lifecycle bar, actions, server errors |
| `fields.js` | Field editors and read-only renderers |
| `dashboard.js` | Dashboard view from `GET /dashboards/{name}` |
| `approvals.js` | Approval inbox and the "Menunggu persetujuan" banner |
| `docs.js` | Print menu and attachments panel |
| `import.js` | Import wizard: upload, preview or row errors, commit |
| `process.js` | "Proses bisnis": the process list and the read-only canvas (`../canvas`, `RangkaCanvas.Mounted` with `readOnly: true`) |
| `test/*.test.js` | `node --test` units for the pure parts of each file |
| `test_app.py` | Live Playwright suite against a real kernel |

## Feature detection

Each pilot feature is on only when the kernel has it; a missing route (`404 not_found "no such endpoint …"`) or an
absent meta key turns it off and its buttons disappear.

| Feature | On when |
| --- | --- |
| Org switcher | `GET /auth/me` returns more than one entry in `orgs` |
| Approvals | `GET /api/v1/approvals` exists |
| Print | `meta.models[m].prints` (or a top-level `meta.prints`) lists formats and `can.print` is not `false`. Cetak checks with `HEAD …/print?as=html` (403/404 become a message), then opens the HTML view in a new tab; on `501 pdf_unavailable` Unduh PDF offers "Cetak dari browser" (HTML view + `window.print()`) |
| Attachments | `GET /api/v1/r/<model>/<name>/attachments` exists and is not `501 files_disabled` |
| Imports | `meta.imports` is not empty |
| Processes | `GET /api/v1/processes` answers 200 with at least one process; 404, 403 or any failure turns the "Proses bisnis" entry and `#/proses` off. The canvas is read only: no edit form, no layout save |
| Buttons per action | `meta.models[m].can[action]` when present |

## Run

```bash
make -C kernel run-demo        # temp Postgres + Kopi Senja on :8080
open http://localhost:8080/app/
```

Sign in with `ceo@saga.co.id` / `rangka-demo`. Add more people with
`RANGKA_USER_PASSWORD=… rangka user add <email> --org kopi-senja --role sales_rep` (`--db` defaults to `$DATABASE_URL`).

## Test

```bash
node --test web/app/test/*.test.js
python3 web/app/test_app.py [--width 1280|400] [--require-all] [--shots DIR] [--keep] [-k NAME]
```

`test_app.py` builds the kernel, starts a throwaway Postgres, migrates, seeds, adds a `sales_manager` and a
`sales_rep`, serves, and runs every scenario in a fresh Chromium context. A scenario fails on an exception, a console
error or an unexpected HTTP error. `--width 400` runs the same scenarios as a phone (400px, mobile viewport and touch);
`test_no_horizontal_scroll` is the scenario that checks the main screens do not scroll sideways and that controls are
at least 40px tall. React 18.3.1 is vendored in `vendor/` (see `vendor/README.md`); the suite fails on any request to a script CDN.

Features another stream builds run live when the kernel has them and otherwise against `page.route` mocks that answer
exactly the Fixed-interface shapes. When the kernel has the approvals, imports or print kinds, the harness writes a real
definition into the temp site copy (approval overlay, import definitions, `print/*.rk` + `letterhead.rk` for the demo
company) before migrate. The run ends with `live: … · mocked: …`. `--require-all` fails when anything ran
on a mock: the integrator's gate once the approvals, print, attachments, imports and multi-org streams are merged. Without
Go, Playwright or the PostgreSQL binaries the suite prints SKIP and exits 0, but with `--require-all` it exits 1.
