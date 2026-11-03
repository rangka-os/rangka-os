# Rangka end-user web app

The browser app people use every day: dashboards, lists, kanban, forms with child tables, approvals, print,
attachments and CSV imports. It is plain React (UMD) plus the design system, with no build step, and it talks only to
a Rangka kernel over `/api/v1`. The kernel serves this folder at `/app/` (`/` redirects there). Routes are hash routes,
so deep links survive a reload and the kernel needs no SPA fallback:

`#/d/<dashboard>` · `#/r/<model>` · `#/r/<model>/new` · `#/r/<model>/<name>` · `#/approvals` · `#/import/<import>`

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
| `test/*.test.js` | `node --test` units for the pure parts of each file |
| `test_app.py` | Live Playwright suite against a real kernel |

## Feature detection

Each pilot feature is on only when the kernel has it; a missing route (`404 not_found "no such endpoint …"`) or an
absent meta key turns it off and its buttons disappear.

| Feature | On when |
| --- | --- |
| Org switcher | `GET /auth/me` returns more than one entry in `orgs` |
| Approvals | `GET /api/v1/approvals` exists |
| Print | `meta.models[m].prints` (or a top-level `meta.prints`) lists formats |
| Attachments | `GET /api/v1/r/<model>/<name>/attachments` exists and is not `501 files_disabled` |
| Imports | `meta.imports` is not empty |
| Buttons per action | `meta.models[m].can[action]` when present |

## Run

```bash
make -C kernel run-demo        # temp Postgres + Kopi Senja on :8080
open http://localhost:8080/app/
```

Sign in with `ceo@saga.co.id` / `rangka-demo`. Add more people with
`rangka user add --db … --org kopi-senja --email … --password … --role sales_rep`.

## Test

```bash
node --test web/app/test/*.test.js
python3 web/app/test_app.py [--width 1280|400] [--require-all] [--shots DIR] [--keep] [-k NAME]
```

`test_app.py` builds the kernel, starts a throwaway Postgres, migrates, seeds, adds a `sales_manager` and a
`sales_rep`, serves, and runs every scenario in a fresh Chromium context. A scenario fails on an exception, a console
error or an unexpected HTTP error. `--width 400` runs the same scenarios at phone width (and checks nothing scrolls
sideways and controls are at least 40px tall). React 18.3.1 is vendored in `vendor/` (see `vendor/README.md`); the suite fails on any request to a script CDN.

Features another stream builds run live when the kernel has them and otherwise against `page.route` mocks that answer
exactly the Fixed-interface shapes. The run ends with `live: … · mocked: …`. `--require-all` fails when anything ran
on a mock: the integrator's gate once the approvals, print, attachments, imports and multi-org streams are merged. Without
Go, Playwright or the PostgreSQL binaries the suite prints SKIP and exits 0, but with `--require-all` it exits 1.
