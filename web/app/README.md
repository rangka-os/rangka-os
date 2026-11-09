# Rangka end-user web app

The browser app people use every day: dashboards, lists, kanban, forms with child tables, approvals, print,
attachments and CSV imports. It is plain React (UMD) plus the design system, with no build step, and it talks only to
a Rangka kernel over `/api/v1`. The kernel serves this folder at `/app/` (`/` redirects there). Routes are hash routes,
so deep links survive a reload and the kernel needs no SPA fallback:

`#/d/<dashboard>` · `#/r/<model>` · `#/r/<model>/new` · `#/r/<model>/<name>` · `#/approvals` · `#/import/<import>` · `#/proses` · `#/proses/<process>` · `#/w/<screen>`

The UI is Bahasa Indonesia first and works from 400px (phone) to desktop.

### Shell

`layout.shell` from `GET /api/v1/meta` picks the frame (contract 0019):

- **`side-only`** (the default; also `side`, `pos` and `portal` until those exist): the Frappe sidebar. On top the
  business switcher (Sendi tile, company, your role); it opens a menu with the apps, the org switch (when you belong to
  more than one org), theme, help, profile and sign-out. Then search (⌘K), the pinned entries (Beranda, Persetujuan with
  its count, Proses bisnis, Impor data) and the layout's `nav.side` groups (else each app's menu). The footer holds the
  collapse button; collapsed state is kept in `localStorage["rk.sidebar.collapsed"]`.
  At 600px and below the sidebar is a drawer opened from a 48px bar (menu, company, search).
  With `layout.responsive.phone: tabs` (contract 0038) a phone gets bottom tabs from `responsive.tabs` plus a last Menu
  tab that opens the drawer; the top-nav shell then drops its own menu button, and toasts sit above the tabs. Escape
  closes the drawer.
- **`top-nav`**: the TopNav (app switcher, search, account menu) with the sidebar below it.

⌘K or Ctrl+K opens the command menu in both: Halaman (every sidebar entry), Buat baru (`layout.nav.create`) and Data
(the first 5 matches per model in the sidebar, from `GET /api/v1/r/<model>?q=`). `theme.density` sets
`data-density` on `<html>` (`compact`, `standard`, `comfortable`).

### Work screens (contract 0030)

- **Home per role.** `meta.work` lists the screens the user may open, in the apps' `work:` order. The first one with
  `home: true` is where they land (`homeHash`); the owner has none and keeps the dashboard home.
- **Kerja group.** Everyone with screens gets a "Kerja" group at the top of the sidebar.
- **Work shell.** A user whose every role is `desk: false` and who has a home screen gets no sidebar: a 48px bar
  (company, account menu), the screen full width, and a bottom bar with up to 5 screens plus "Lainnya" (the lists and
  approvals they may open, as a sheet). Ubah and previews always use the desk shell.
- **Renderers** (`screens.js`): `#/w/<screen>` loads `GET /api/v1/screens/<name>` (the screen plus `set:` evaluated by
  the kernel for the user) and renders `RangkaApp.workRenderers[type]`. Built: `entry` (a short form: the listed
  fields, "Simpan" or "Simpan & ajukan", then an empty form again with "Baru disimpan") and `counter` (`counter.js`:
  open the caller's session or ask for one, catalogue tiles with groups, search and barcode Enter, cart with qty and
  notes, Tahan, tender with Uang pas / round notes / QRIS full or part and the change, receipt print through the
  browser dialog, blind session close). Other types show a notice until their renderer ships.
- **Safe writes** (`outbox.js`): every press sends `Idempotency-Key`s, so a double tap or pressing again after a lost
  answer writes once. A screen with `offline: queue` keeps unsent presses in IndexedDB ("N antre" in the header, sent
  on reconnect, every 15 s or with "Kirim sekarang"); a write the server refuses later stays on screen until dismissed.
  The screen is cached and shown with "data terakhir <jam>" offline.

### Install as an app (PWA)

- **Installable.** `manifest.webmanifest` (Sendi icons in `icons/`, standalone, shortcut "Setujui") and `sw.js` make
  `/app/` installable: Chrome and Edge offer it, and the account menu shows "Pasang aplikasi". iPhone and iPad have no
  prompt, so the menu shows "Pasang di layar utama" with the Share-sheet steps. Once installed, neither entry shows.
- **Network first.** `sw.js` asks the kernel first for every app file and uses its cache only when the network fails
  or takes over 4 s. A cached file is therefore never older than a reachable kernel, and upgrades need no
  cache-busting. Google Fonts are cached on first use.
- **Never cached:** `/api/`, `/auth/`, `/oauth/`, `/mcp` and `/.well-known/`. Record data stays out of Cache Storage
  (shared phones, org switch); work screens keep their own per-user copy (`outbox.js`).
- **Offline: last-seen pages** (`readcache.js`).
  - Every answer the person already got for the session, meta, dashboards, lists, list sums, records (with their
    attachment lists), approvals and processes is kept on the device (IndexedDB `rangka-read`). Nothing is
    prefetched; change-set previews, the event stream, AI and work screens are not kept here.
  - Without a connection, `api.js` answers those GETs from the device copy. The app opens on the page it was on,
    read only (like a preview): no create, save, submit, kanban moves or Ubah. A record shows "Simpan saat online".
  - The strip under the top bar says "Menampilkan data terakhir dilihat <hari ini 14.05>". A page never opened on
    this device says so.
  - When the server answers again, the strip goes and the view loads afresh.
  - The copy belongs to one person in one org. A different person or org on `/auth/me` empties it first, and so do
    sign-out, an org switch and any 401. At most 400 answers and 8 MB, least recently used out first.
  - With no copy at all (another phone, cleared data), the app says "Belum ada koneksi" and opens by itself once
    back online.
- **Updates.** When `sw.js` changes (a new `PRECACHE` list or `VERSION`), the new worker waits. A "Versi baru tersedia"
  toast, or "Muat versi baru" in the account menu, switches over and reloads.
- **When the worker is off.** `pwa.js` does not register the worker inside a frame (Rangka Cloud embeds the app), on
  plain HTTP other than localhost (a LAN address), or with `?sw=0`. The app then works as a normal website.
- **Kernel headers.** The kernel serves `sw.js`, the manifest and the page with `Cache-Control: no-cache`.
- **Adding a file.** A new local file in `index.html` must also go into `PRECACHE` in `sw.js`. `test/sw.test.js`
  fails until it does.

### Ubah (change and preview)

- **Who sees it.** Only people with `meta.changesets.propose` (a browser session and the owner, or a role in
  `config.ai.approvers` / `config.ai.proposers`) get a "Mode" section (Pakai / Ubah tampilan dan data) in the account menu: the avatar menu
  in the top-nav shell, the business switcher's menu in the side shell. The Ubah bar has "Selesai" to go back to Pakai.
  Everyone else gets no Mode section, and `?ubah=1` does nothing. The mode is off while offline (it writes change sets).
- **What it does.** In Ubah people rearrange dashboards, forms, list and kanban columns and the menu, and add fields,
  stages, approvals, automations and tables. Every edit is a change set (contract 0015). A low-risk edit by an approver is
  applied at once with an "Urungkan" toast (undo is the change set's `revert`). Medium and high risk edits stop at
  `previewed` and open a card with the risk and the dry run; "Setujui & terapkan" is for approvers (the owner for high risk).
- **Deep links.** `/app/?mode=ubah#/d/<dashboard>` or `#/d/<dashboard>?ubah=1` opens in Ubah.
  `#/<route>?preview=<change set id>` opens a read-only preview of a pending change set (reads go to `/api/v1/preview/{id}/…`,
  writes are refused); "Keluar pratinjau" leaves it.
- **Embedding (Rangka Cloud).** In a frame the app posts to `window.parent`, with target origin `*` and no record data,
  for every change set it creates, applies or reverts:
  `{type: "rangka:changeset", event: "created" | "applied" | "reverted", changeset: {id, title, status, risk}}`.
  `created` comes right after the proposal; an edit that is applied in the same step posts `applied` next.

## Files

| File | What |
| --- | --- |
| `index.html` | Loads React, the design system, `tokens.css`, then the files below; app CSS (`ap-*`, incl. the ≤ 600px rules) |
| `tokens.css` | Design tokens (Ink, Bata, Kapur), light and dark |
| `app.css` | The app's look from Dede's Pencil design `rangka.pen`: app-only token values (light from the pen's app preview, dark from its `jg-*` dark theme), the white sidebar with Kapur selection, outlined buttons, the Bata "Buat …" button, status pills, table cards, board columns, dashboard cards, the green approve button and sign-in. Loaded after the inline CSS, so the Console and `tokens.json` stay as they are |
| `engine.js` | CEL subset, defaults, computed fields, rules and status; `RangkaEngine.create(meta, lookup)`. Reference semantics for the kernel |
| `api.js` | `RangkaAppAPI`: every kernel call, `ApiError`, feature detection, `parseRupiah` / `rupiah` |
| `shell.js` | Router, sign-in, the shell from `layout.shell` (sidebar or top nav), ⌘K, density, org switcher, 401 handling, Ubah state and the menu editor; mounts last |
| `ubah.js` | Change-set op builders (pure), Ubah helpers, and the shared Ubah UI: mode toggle, bar, preview banner, change-set card, add dialogs |
| `list.js` | List (server search, quick filters, sort, pages) and kanban |
| `form.js` | Form, field editors, link search, child grid, live preview, lifecycle bar, actions, server errors |
| `fields.js` | Field editors and read-only renderers |
| `dashboard.js` | Dashboard view from `GET /dashboards/{name}` |
| `approvals.js` | Approval inbox and the "Menunggu persetujuan" banner |
| `docs.js` | Print menu and attachments panel |
| `readcache.js` | The device copy of the pages a person saw, for reading without a connection (per user and org, LRU) |
| `pwa.js` | Installable app on the page side: registers `sw.js`, install prompt, update toast, online state, status-bar colour |
| `sw.js` | Service worker: network-first app shell, Google Fonts cache, never API data |
| `manifest.webmanifest`, `icons/` | Web app manifest and the Sendi icons (192, 512, maskable, Apple touch) |
| `outbox.js` | Safe writes for work screens: idempotency keys, the IndexedDB outbox and the read cache (contract 0030 §6, §7) |
| `counter.js` | The `counter` work screen (cashier): catalogue, cart, hold, tender, session, receipt (contract 0030 §2) |
| `screens.js` | Work screens (`#/w/<screen>`, contract 0030): the route view and the type renderers (`entry`) |
| `import.js` | Import wizard: upload, preview or row errors, commit |
| `ai.js` | Raka (contract 0020): `AssistantRoot` with the ⌘J ask bar and the chat drawer, streamed over `api.aiChat`; shown only when `meta.features.ai`. The Console loads it too |
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
| Ubah | `meta.changesets.propose`; "Setujui & terapkan" needs `approve` (and `approve_high` for high risk) |
| Raka (⌘J, sidebar "Tanya Raka", ⌘K) | `meta.features.ai` is `true`: the kernel runs with `--ai-url`, the sidecar answered `GET /healthz` within 30 s and `config.ai.enabled` is not `false` (contract 0020). Answers stream from `POST /api/v1/ai/chat`; a change set card opens `?preview=<id>` or Ubah. Raka never approves or applies. History stays in the tab under `rk.ai.<org>.<email>` and is removed on sign-out |

## Run

```bash
make -C kernel run-demo        # temp Postgres + Kopi Senja on :8080
open http://localhost:8080/app/
```

When `rangka-ai` is built next to `rangka-os` (`npm run build` there; or point `RANGKA_AI_DIR` at a checkout),
`run-demo` also starts its scripted sidecar (`serve --demo`, no LLM key) and Raka answers on ⌘J.

Sign in with `ceo@saga.co.id` / `rangka-demo`. Add more people with
`RANGKA_USER_PASSWORD=… rangka user add <email> --org kopi-senja --role sales_rep` (`--db` defaults to `$DATABASE_URL`).

## Test

```bash
node --test web/app/test/*.test.js
python3 web/app/test_app.py [--width 1280|400] [--require-all] [--shots DIR] [--keep] [-k NAME]
```

`test_app.py` builds the kernel, starts a throwaway Postgres, migrates and seeds the kernel's v0 catalogue
(`kernel/testdata/catalogue-v0`, the one `make test` uses; only `rangka-connectors` is read from next to this repo), adds a `sales_manager` and a
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

`RANGKA_AI_DIR=<built rangka-ai> python3 web/app/test_app.py -k test_ai_live` runs the assistant end to end: the
harness starts the real sidecar (`serve --demo`) with a random secret and the kernel with `--ai-url`; the scenario asks
"berapa faktur jatuh tempo bulan ini?" and "tambah field Nomor PO di faktur" and opens the previewed change set. Without
`RANGKA_AI_DIR` it returns at once; the other Raka scenarios mock `POST /api/v1/ai/chat`.
