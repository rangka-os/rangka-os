# Frappe UI Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every Rangka screen (design system, `/app/`, `/console/`) moves from the Atlassian-style structure to Frappe UI's
compact structure in Rangka colours. It also gets Frappe's sidebar, ⌘K, and the list / kanban / group-by / report views,
backed by one new read-only kernel aggregate endpoint.

**Architecture:**
- Token *values* change and token *names* stay, so company `theme.rk` overrides keep working.
- The design system (`web/design-system/bundle.{css,js}`, plain React UMD, no build step) is restyled in place, and a
  few new components are added.
- `web/app` and `web/console` move onto those components.
- Two contract proposals, 0017 (default shell) and 0018 (list aggregates), gate the only schema and kernel changes.

**Tech Stack:**
- Go 1.24 kernel (`net/http` mux).
- Plain React 18.3.1 UMD (vendored) with the `window.Rangka` design system.
- `node --test` for pure JS, Playwright Python suites (`test_app.py`, `test_console.py`).
- JSON Schema 0.4.

**Spec:** `docs/superpowers/specs/2026-11-04-frappe-ui-redesign-design.md`

## Global Constraints

- Direction B: Frappe UI structure, Rangka colours.
  - Ink `#1C1917` primary.
  - Deep Bata `#A33B23` links, selected nav and checked boxes.
  - Kapur `#F6EDE6` selected background.
  - Focus Bata Terang `#E0623F`.
  - Warm stone neutrals.
  - Danger stays red (`bad-solid`).
  - Raka teal for the AI only.
- Token names in `web/design-system/tokens.json` never change or disappear. New tokens may be added.
  `kernel/internal/theme/tokens.json` must stay byte-equal (`make -C kernel sync-schemas`; `TestTokensInSync`).
- Desktop metrics:
  - Body 13/20, small 12/16, header title 15px semibold.
  - Controls 28px (sm 24, lg 32), radius 6px.
  - Sidebar 216px, collapsed 48px.
  - Header 48px.
  - List header row 32px, rows 36px.
- Phone (≤ 600px):
  - The sidebar is a drawer.
  - Controls are at least 40px tall.
  - No sideways page scroll (`test_no_horizontal_scroll`).
- No build step, no new framework, no CDN in `/app/`. Remix Icon only.
- UI copy:
  - Bahasa Indonesia first, sentence case, "kamu".
  - `Rp 1.250.000`, `27 Okt 2026`.
  - No emoji, no uppercase status text.
- Change sets stay the only way metadata changes. "Jadikan bawaan untuk semua" goes through Ubah.
- Feature detection stays: no entry or button for something the kernel lacks.
- One task = one branch = one worktree (`git worktree add ../rangka-os-<task> -b ui/<task>`). Small commits. Each commit
  message ends with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Out of scope:
  - Project 2 module screens (helpdesk inbox, Insights builder, calendar view, HR, lending).
  - New apps and connectors.
  - The landing page.
  - `web/prototype`.

## Review Focus

1. **Empty group values.**
   - Records with an empty group-by field form one group labelled "(kosong)".
   - Opening that group lists exactly those records.
   - Test: Task 2 `TestAggregateEmptyGroup`; Task 8 `groupFilter null`.
2. **Link-field groups.**
   - Grouping invoices by `customer` shows customer titles ("CV Maju Jaya"), not names (`CUST-0012`).
   - Opening a group still filters by the name.
   - Test: Task 2 `TestAggregateOverHTTP` checks `label`.
3. **Stale saved list settings.**
   - A column, sort or group field saved in the browser that the model no longer has (field removed, or a different org's
     overlay) is dropped quietly. It must never break the list.
   - Test: Task 8 `loadListPrefs drops unknown fields`.
4. **Many groups.**
   - A group field with hundreds of values returns the 50 largest groups plus one "Lainnya" bucket.
   - The page never renders hundreds of collapsible headers.
   - Test: Task 2 `TestAggregateGroupCap`.
5. **Row scope.**
   - Aggregate counts and sums never include rows the person cannot see in the list.
   - For any query, `count` equals the list's `total`.
   - Test: Task 2 `TestAggregateMatchesListUnderRowScope`.

---

### Task 1: Contract proposals 0017 and 0018

**Files:**
- Create: `docs/contracts/0017-frappe-shell-default.md`
- Create: `docs/contracts/0018-list-aggregates.md`
- Modify: `docs/contracts.md` (index line for each, if the file lists contracts)

**Interfaces:**
- Produces: the accepted wording that Tasks 2 and 3 implement.

- [ ] **Step 1: Write 0017.** Follow `0000-how-contracts-change.md`. Status: proposed. It decides:
  - `schema/0.4/layout.json` `shell` default changes from `top-nav` to `side-only`;
  - the description changes from "top-nav (Atlassian style)" to "side-only (Frappe-style sidebar, default)";
  - `top-nav` stays valid;
  - the kernel's no-layout fallback returns `{"name": "side_only", "shell": "side-only"}` (today it returns `"side"`, which
    is not in the enum);
  - `web/app` renders `layout.shell`: `side-only` and `top-nav`; `pos` and `portal` fall back to `side-only` until project 2.
- [ ] **Step 2: Write 0018.** Status: proposed. Endpoint:
  - `GET /api/v1/aggregate/{model}?q=&filter=&group=<field>&fn=<kind>:<field>[,…]`
  - `kind` is one of `count|sum|avg|min|max`. `count` needs no field.
  - It matches the same rows as `GET /api/v1/r/{model}` with the same `q` / `filter`, read grant and row scope. No paging.
  - Response:
    `{"count": 142, "values": {"sum:total": 51250000}, "groups": [{"key": "CUST-0012", "label": "CV Maju Jaya", "count": 4, "values": {"sum:total": 3900000}}], "other": {"count": 0, "values": {}}}`
  - Group rules:
    - the 50 largest groups by count, ties by key;
    - the rest summed into `other`;
    - empty values share `key: null, label: "(kosong)"`;
    - a link field's `label` is the linked record's title.
  - Errors:
    - `400` when `group` is not a field of the model, or when `sum|avg|min|max` names a non-number field (currency, int,
      float, percent are numbers);
    - `400` on a bad `filter`;
    - `404` for an unknown model;
    - `403` without a read grant.
  - No preview variant. In preview mode the app hides totals.
- [ ] **Step 3: Commit and open one PR** titled "Contracts 0017 and 0018 (proposed)". Stop here until Dede accepts both. Tasks
  2 and 3 merge only after acceptance. Tasks 4–7 may start in parallel.

```bash
git add docs/contracts/0017-frappe-shell-default.md docs/contracts/0018-list-aggregates.md docs/contracts.md
git commit -m "contracts: 0017 default shell, 0018 list aggregates (proposed)"
```

---

### Task 2: Kernel list aggregates (contract 0018)

**Files:**
- Modify: `kernel/internal/engine/query.go` (extract the matching loop of `List` into `matching`)
- Create: `kernel/internal/engine/aggregate.go`
- Modify: `kernel/internal/engine/dashboard.go` (`aggregate` stays and is reused, not copied)
- Modify: `kernel/internal/api/records.go`, `kernel/internal/api/server.go:116` (route)
- Test: `kernel/internal/engine/aggregate_test.go`, `kernel/internal/server/server_test.go`
- Docs: `website` REST reference page for records (add the endpoint)

**Interfaces:**
- Consumes: `engine.ListOpts{Filter, Q}` (Sort/Limit/Offset ignored).
- Produces:
  - `type AggFn struct{ Kind, Field string }` and `func ParseAggFns(s string) ([]AggFn, error)`, which parses
    `"count,sum:total"`.
  - `type AggGroup struct{ Key any; Label string; Count int64; Values map[string]any }` with JSON tags `key,label,count,values`.
  - `type AggResult struct{ Count int64; Values map[string]any; Groups []AggGroup; Other *AggGroup }` with JSON tags
    `count,values,groups,other`. `Other` is omitted when nothing overflowed.
  - `func (e *Engine) Aggregate(ctx context.Context, a Actor, model string, opts ListOpts, group string, fns []AggFn) (AggResult, error)`
  - `func (e *Engine) matching(ctx context.Context, a Actor, m *meta.Model, opts ListOpts) ([]store.Record, error)`:
    every row `List` would return before sort and paging.

- [ ] **Step 1: Write failing engine tests** in `aggregate_test.go`, on the engine testdata that `engine_test.go` uses:
  - `TestAggregateCountEqualsList`: for filters `""`, `"docstatus == 1"` and `q: "kopi"`, `Aggregate(...).Count == total`
    from `List` with the same opts.
  - `TestAggregateSumByGroup`: group `status`, fn `sum:grand_total`. The group sums add up to `Values["sum:grand_total"]`.
  - `TestAggregateEmptyGroup`: a record with empty group field → one group `Key == nil`, `Label == "(kosong)"`.
  - `TestAggregateGroupCap`: 60 distinct values → `len(Groups) == 50`, `Other.Count` equals the rest.
  - `TestAggregateRejects`: unknown group field → 400 `StatusError`; `sum:customer` (link) → 400.
  - `TestAggregateMatchesListUnderRowScope`: with the row-scoped actor from `rowscope_test.go`, `Count` equals that actor's
    `List` total and is less than the owner's.
- [ ] **Step 2: Run them and see them fail.**
  Run: `cd kernel && go test ./internal/engine -run Aggregate -v`. Expected: FAIL (undefined: `Aggregate`).
- [ ] **Step 3: Implement.**
  - Extract `matching` from `List` so `List` = `matching` → sort → page, with identical behaviour.
  - `Aggregate` = `matching` → group → `aggregate(rows, kind, field)` from `dashboard.go` per fn.
  - Values are keyed `"<kind>:<field>"` and plain `"count"` for count.
  - Link labels come from the same title lookup `List` uses (`matchLinkTitle`'s `titles` cache or the existing title helper).
  - Sort groups by count desc, then key asc. `nil` sorts last within ties.
- [ ] **Step 4: Run engine tests.** `go test ./internal/engine -v`. Expected: PASS, including the existing `List` tests.
- [ ] **Step 5: HTTP.**
  - Register `mux.HandleFunc("GET /api/v1/aggregate/{model}", a(s.aggregateRecords))` in `server.go`.
  - In `records.go`, `aggregateRecords` reads `q`, `filter`, `group` and `fn` (default `count`) and writes `AggResult` as JSON.
  - Add `TestAggregateOverHTTP` in `server_test.go`, next to `TestREST`:
    - `GET /api/v1/aggregate/sales_invoice?group=customer&fn=count,sum:grand_total` → 200;
    - `count` equals the `total` of `GET /api/v1/r/sales_invoice`;
    - a group with a `CUST-` key has a non-empty `label` different from its key;
    - `?group=nope` → 400; `/aggregate/nope` → 404.
- [ ] **Step 6: Run.** `cd kernel && make test-unit && make test`. Expected: PASS.
- [ ] **Step 7: Document and commit.** Add the endpoint to the REST reference page. Commit:
  `kernel: list aggregates endpoint (contract 0018)`.

---

### Task 3: Default shell (contract 0017)

**Files:**
- Modify: `schema/0.4/layout.json` (`shell.default`, top-level `description`, `extends.default` → `rangka/side-only`)
- Modify: `kernel/internal/api/meta.go:384`
- Modify: `templates/layouts/top-nav.rk`, `templates/layouts/side-only.rk` (comment lines only)
- Test: `kernel/internal/server/server_test.go`

- [ ] **Step 1: Failing test.** In `TestCapabilities` (or a new `TestDefaultLayout`): with no `config.layout`,
  `GET /api/v1/meta` has `layout.shell == "side-only"`. Run `go test ./internal/server -run Layout`. Expected: FAIL (`"side"`).
- [ ] **Step 2: Fix** the fallback to `"side-only"`. Edit the schema default and description. Then run
  `make -C kernel sync-schemas` so the embedded schema matches.
- [ ] **Step 3: Validate.** `make -C kernel test-unit && make -C kernel compare-python`, then
  `python3 tools/rangka_validate.py ../rangka-apps/apps --site ../rangka-apps/sites/kopi-senja --connectors ../rangka-connectors`.
  Expected: PASS, identical reports, 0 errors.
- [ ] **Step 4: Commit** `schema+kernel: side-only is the default shell (contract 0017)`.

---

### Task 4: Tokens and one tokens.css compiler

**Files:**
- Modify: `web/design-system/tokens.json`
- Create: `web/design-system/tokens_css.py` (compiler and `--check`)
- Modify: `web/console/build_demo.py:208-268` (import the compiler instead of its own copy)
- Regenerate: `web/app/tokens.css`, `web/console/tokens.css`, `kernel/internal/theme/tokens.json`
- Test: `web/design-system/test_tokens.py`

**Interfaces:**
- Produces:
  - `compile_css(tokens: dict) -> str`, with the same output format as today's `tokens.css`;
  - CLI `python3 web/design-system/tokens_css.py [--check]`, which writes or checks both `tokens.css` files;
  - new CSS variables: `--sidebar-collapsed`, `--header`, `--row`, `--row-head`, `--row-selected`, `--font-size`,
    `--line-height`, `--font-size-sm`.

- [ ] **Step 1: Failing test** `test_tokens.py` (plain `python3`, asserts, `sys.exit(1)` on failure):
  - every token name in `git show HEAD:web/design-system/tokens.json` is still present;
  - `control == "28px"`, `control-sm == "24px"`, `control-lg == "32px"`, `field == "28px"`;
  - `r-btn == r == r-md == "6px"`, `r-lg == "8px"`, `r-dialog == "12px"`;
  - `topbar == "48px"` and the new `header == "48px"`;
  - `sidebar == "216px"`, `sidebar-collapsed == "48px"`, `row == "36px"`, `row-head == "32px"`;
  - `compile_css` output for the repo tokens equals `web/app/tokens.css` and `web/console/tokens.css`.
- [ ] **Step 2: Run** `python3 web/design-system/test_tokens.py`. Expected: FAIL.
- [ ] **Step 3: Edit values.**
  - Sizes as in the test.
  - Body type: 13/20, small 12/16.
  - Status tints from the approved mockup:
    - `ok` `#2B7A4B` on `ok-tint` `#E6F4EA`;
    - `warn` `#8F6400` on `warn-tint` `#FDF3D7`;
    - `bad` `#B42318` on `bad-tint` `#FDECEA`.
    - Keep each pair at 4.5:1 or better.
  - `row-selected` `#FBF6F2` (dark `#2A1D18`).
  - Shadows: `shadow-pop` / `overlay` / `toast` → `0 4px 16px rgba(28,25,23,.10), 0 0 0 1px rgba(28,25,23,.06)`.
  - Rewrite the "Atlassian" wording in `usage` strings to say what the token is for in Frappe terms.
  - Dark values: keep, except new tokens.
- [ ] **Step 4: Write `tokens_css.py`** by moving `css_block` and friends out of `build_demo.py`, then regenerate:
  `python3 web/design-system/tokens_css.py && make -C kernel sync-schemas`.
- [ ] **Step 5: Run** `python3 web/design-system/test_tokens.py && (cd kernel && go test ./internal/theme)`. Expected: PASS.
- [ ] **Step 6: Commit** `design-system: Frappe-density token values; one tokens.css compiler`.

---

### Task 5: Design system foundation restyle and gallery

**Files:**
- Modify: `web/design-system/bundle.css` (whole file), `web/design-system/bundle.js` (`Lozenge`, `StatusChip`, `Toaster`
  position, density)
- Create: `web/design-system/gallery.html` (every component, light and dark, `?theme=dark`, `?density=comfortable`;
  loads `../app/vendor` React)
- Create: `web/design-system/test_gallery.py` (Playwright: open the gallery at 1280 and 400, no console errors,
  no horizontal scroll, screenshot to `--shots DIR`)
- Modify: `web/design-system/README.md` (rewrite "Visual foundations" and "Layout" for Frappe structure)

**Interfaces:**
- Produces:
  - `Lozenge` / `StatusChip` keep their props (`appearance`, `children`) and render
    `<span class="rk-pill <appearance>">` with a 6px dot. Text keeps its case.
  - Toasts float bottom-right.
  - `document.documentElement.dataset.density` of `compact|standard|comfortable` switches metrics through CSS variables:
    - `standard` is this design;
    - `comfortable` restores 32px controls and 40px rows;
    - `compact` uses 24px controls and 32px rows.

- [ ] **Step 1: Failing check.** `python3 web/design-system/test_gallery.py` fails because the gallery does not exist.
- [ ] **Step 2: Build `gallery.html`.** One section per exported component, in the order of the `@ds-bundle` header list.
  Use real Indonesian content (Kopi Senja data). Add a theme and density toggle at the top.
- [ ] **Step 3: Restyle `bundle.css`.**
  - Every rule reads the token variables. No hard-coded px for control heights or radii.
  - Remove `text-transform: uppercase` from lozenges, nav group labels and menu headings.
  - Secondary buttons: grey fill, no border.
  - Cards: 1px `line`, 8px radius, no shadow.
  - Dialog radius `--r-dialog`.
  - `.rk-toasts.float` moves to `right: 16px; bottom: 16px`.
- [ ] **Step 4: Run** `python3 web/design-system/test_gallery.py --shots /tmp/ds`. Expected: PASS. Look at the shots in
  light and dark next to the approved mockup (option B) before committing.
- [ ] **Step 5: Run the app and Console suites** (`python3 web/app/test_app.py`, `--width 400`,
  `python3 web/console/test_console.py --react-dir web/app/vendor`). Expected: PASS, or failures only in selectors that
  matched removed uppercase text. Fix those selectors in the tests.
- [ ] **Step 6: Commit** `design-system: Frappe UI foundation restyle, pills, gallery`.

---

### Task 6: New design-system components

**Files:**
- Modify: `web/design-system/bundle.js`, `bundle.css`, `index.d.ts`, `gallery.html`, `README.md`
- Create: `web/design-system/test/components.test.js` (`node --test`, loads `bundle.js` in `vm` with a stub React that
  records `createElement` calls, like `web/app/test/*.test.js` load their files)

**Interfaces (all on `window.Rangka`; add to the `@ds-bundle` header list):**
- `Sidebar` (rewritten; old `app` prop still accepted and mapped to `switcher`). Props:
  - `switcher {title, subtitle, mark, onClick}`;
  - `search {label, onOpen}`;
  - `pinned [{id, label, icon, count}]`;
  - `groups [{label, items: [{id, label, icon, count}]}]`;
  - `current`, `onNavigate(id)`;
  - `collapsed`, `onToggleCollapse()`;
  - `footer` (node).
  - Collapsed: icons only, `title` tooltips, width `--sidebar-collapsed`.
- `PageHeader` (slim, 48px). Props:
  - `breadcrumbs [{label, onClick}]`, `title`, `status` (node), `actions` (node), `tabs` (node), `onBack`.
  - The old props `subTitle` / `tags` / `extra` keep working as aliases.
- `ViewSwitcher({value, options: [{id: "list"|"kanban"|"group"|"report", label, icon}], onChange})`.
- `BulkBar({count, onClear, children})`: shown when `count > 0`, "x dipilih".
- `GroupList({groups: [{key, label, count, values, open, rows: node}], valueFormat(key, v), onToggle(key)})`.
- `ReportTable({columns: [{field, label, align, render}], rows, totals: {label, values}, onColumnsChange})`: header 32px,
  rows 36px, sticky footer total row, horizontal scroll inside its own container.
- `DocPanel({sections: [{title, content}]})`: 280px right panel. On ≤ 1100px it moves below the form.
- `CommandMenu` keeps its API and gets the new look.

- [ ] **Step 1: Failing tests** in `components.test.js`:
  - `Sidebar collapsed renders no labels`: every item's label appears only as `title`;
  - `Sidebar maps legacy app prop to switcher`;
  - `ViewSwitcher marks value as aria-pressed`;
  - `BulkBar renders nothing at 0`;
  - `GroupList shows (kosong) for null key label`;
  - `ReportTable renders totals row`.
- [ ] **Step 2: Run** `node --test web/design-system/test/*.test.js`. Expected: FAIL.
- [ ] **Step 3: Implement** the components and CSS. Add each one to `gallery.html` and `index.d.ts`.
- [ ] **Step 4: Run** the node tests and `test_gallery.py` at 1280 and 400. Expected: PASS.
- [ ] **Step 5: Commit** `design-system: Sidebar, slim PageHeader, ViewSwitcher, BulkBar, GroupList, ReportTable, DocPanel`.

---

### Task 7: App shell (sidebar, layout.shell, ⌘K, density)

**Files:**
- Modify: `web/app/shell.js` (≈ lines 600–660 render the shell), `web/app/index.html` (`ap-*` CSS), `web/app/README.md`
- Test: `web/app/test/shell.test.js`, `web/app/test_app.py`

**Interfaces:**
- Consumes: the `Sidebar`, `PageHeader` and `CommandMenu` props from Task 6.
- Produces on `window.RangkaApp`:
  - `shellKind(meta) -> "side-only" | "top-nav"`: reads `meta.layout.shell`; `side` and missing become `side-only`;
    `pos` and `portal` become `side-only`;
  - `commandGroups(meta, features, nav) -> [{label, items: [{id, label, sub, hash}]}]`, with the groups "Halaman",
    "Buat baru" and "Data" (records via `api.list(model, {q})`, first 5 per model in nav);
  - `densityOf(meta) -> "compact"|"standard"|"comfortable"` from `meta.theme.density`, default `standard`.

- [ ] **Step 1: Failing unit tests** in `shell.test.js`:
  - `shellKind`: `{shell: "side"}` → `side-only`, `{shell: "top-nav"}` → `top-nav`, `{}` → `side-only`,
    `{shell: "pos"}` → `side-only`;
  - `densityOf`;
  - `commandGroups` lists every nav item once and a "Buat baru" entry per `layout.nav.create` model.
- [ ] **Step 2: Run** `node --test web/app/test/shell.test.js`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - `side-only` renders `Sidebar` with:
    - `switcher`: the Sendi tile, company name and current outlet. Clicking opens a menu with the app switcher (the entries `TopNav`'s `AppSwitcher` shows today), the org switch (when
      `features.orgs`), theme, help, and the account / sign-out entries that used to live in TopNav;
    - `search` opening ⌘K;
    - `pinned`: Beranda, Persetujuan with count, Proses bisnis, and Impor data when present;
    - `groups` from `navGroups`;
    - `footer`: the Pakai/Ubah toggle when allowed, and collapse.
  - Collapse state lives in `localStorage["rk.sidebar.collapsed"]`, read and written in try/catch.
  - ⌘K / Ctrl+K opens `CommandMenu`.
  - A "Tanya Raka" group appears only when `meta.features.ai` is true. Today it is never true.
  - `top-nav` keeps `TopNav`, restyled.
  - Set `document.documentElement.dataset.density = densityOf(meta)`.
  - At ≤ 600px the sidebar is a drawer opened from a 48px header menu button.
- [ ] **Step 4: Update `test_app.py`** scenarios that click the TopNav. Add `test_sidebar_collapse` (collapse, reload,
  still collapsed) and `test_command_menu` (⌘K, type "Maju", Enter opens the customer).
- [ ] **Step 5: Run** `node --test web/app/test/*.test.js && python3 web/app/test_app.py && python3 web/app/test_app.py --width 400`.
  Expected: PASS.
- [ ] **Step 6: Commit** `app: Frappe sidebar shell, layout.shell, ⌘K, density`.

---

### Task 8: List views: list, kanban, group-by, report

**Files:**
- Modify: `web/app/api.js` (`api.aggregate`), `web/app/list.js`, `web/app/ubah.js` (reuse `RA.opsListColumns`)
- Test: `web/app/test/api.test.js`, `web/app/test/list.test.js`, `web/app/test_app.py`

**Interfaces:**
- Consumes:
  - `GET /api/v1/aggregate/{model}` (Task 2);
  - `ViewSwitcher`, `BulkBar`, `GroupList`, `ReportTable` (Task 6);
  - the existing `RA.buildFilter`, `RA.inClause`, `RA.celQuote` and `RA.opsListColumns(model, columns, existing)`.
- Produces on `window.RangkaApp`:
  - `api.aggregate(model, {q, filter, group, fns}) -> Promise<AggResult|null>`. `fns` is an array like `["count", "sum:grand_total"]`.
    It resolves `null` in preview mode.
  - `loadListPrefs(storage, org, model, meta) -> {view, sort, columns, group}`, using key `rk.list.<org>.<model>`.
    It drops fields the model lacks and returns defaults from `views.default` / `views.list` when nothing valid is saved.
  - `saveListPrefs(storage, org, model, prefs) -> void`
  - `groupFilter(field, key) -> string`: CEL `field == 'v'` via `celQuote`; `key === null` gives `field == null`.
  - `sumFields(meta, model) -> string[]`: currency, int, float and percent fields that are in `views.list` columns.
  - `availableViews(meta, model) -> string[]`: always `list`, `group` and `report`; `kanban` only when `views.kanban` exists.

- [ ] **Step 1: Failing unit tests.**
  - `api.test.js`: `aggregate builds the query string` (`/api/v1/aggregate/sales_invoice?q=kopi&group=status&fn=count%2Csum%3Agrand_total`);
    `aggregate resolves null in preview`.
  - `list.test.js`:
    - `loadListPrefs drops unknown fields`;
    - `loadListPrefs falls back to views.default`;
    - `groupFilter quotes and escapes` (`name == 'O\'Neil'`);
    - `groupFilter null` → `status == null`;
    - `availableViews without kanban`;
    - `sumFields picks currency columns only`.
- [ ] **Step 2: Run** `node --test web/app/test/api.test.js web/app/test/list.test.js`. Expected: FAIL.
- [ ] **Step 3: Kernel check for `== null`.** Add an engine test that `List` with filter `status == null` returns the rows
  with an empty status. If the CEL subset lacks `null`, make it pass in `kernel/internal/cel` within this task.
- [ ] **Step 4: Implement `list.js`.**
  - Toolbar: search, Filter (existing FilterBar), Urutkan, Kolom, `ViewSwitcher`.
  - The view, sort, columns and group are saved per person with `saveListPrefs`.
  - Kolom has a "Jadikan bawaan untuk semua" item, shown only in Ubah when allowed. It proposes
    `RA.opsListColumns(model, columns, existing)` through the existing Ubah flow.
  - **Group-by view.** Pick a group field (select, link, status and check fields of the model). `api.aggregate` with
    `group` and `count` plus `sumFields` gives the headers. Opening a group loads its rows with
    `api.list(model, {filter: and(currentFilter, groupFilter(field, key)), limit: 20})`, with "Muat lagi" for more.
    The "Lainnya" bucket shows its count and cannot be opened.
  - **Report view.** `ReportTable` with the chosen columns (any model field may be added), the current page of rows, and a
    footer from `api.aggregate` labelled "Total semua <count> baris", with sums formatted by `rupiah`. In preview the
    footer is hidden.
  - Selection shows `BulkBar` with the existing bulk actions.
  - Rows are 36px; document numbers use `--link`.
- [ ] **Step 5: Add Playwright scenarios.**
  - `test_list_group_by`: group sales invoices by status; header counts add up to the list total; open "Lunas"; rows are
    all Lunas.
  - `test_report_totals`: report view; the footer sum equals `GET /api/v1/aggregate/...` `sum:grand_total` formatted as
    rupiah.
  - `test_list_prefs_persist`: switch to report, reload, still report.
- [ ] **Step 6: Run** the node tests and `test_app.py` at 1280 and 400. Expected: PASS.
- [ ] **Step 7: Commit** `app: list, kanban, group-by and report views with per-person settings`.

---

### Task 9: Document page and side panel

**Files:**
- Modify: `web/app/form.js`, `web/app/docs.js` (attachments render inside the panel), `web/app/approvals.js` (the
  "Menunggu persetujuan" banner becomes a panel section plus header pill), `web/app/index.html`
- Test: `web/app/test/form.test.js`, `web/app/test_app.py`

**Interfaces:**
- Consumes: `PageHeader`, `DocPanel`, `Lozenge` pill (Tasks 5–6).
- Produces: `RA.docPanelSections(record, features, model) -> [{id, title}]` with the ids `info` (created/modified by and
  at), `attachments` and `approvals` (each gated by the app's existing feature detection, see the README's Feature detection table), `print` (when the model has
  prints), in that order.

- [ ] **Step 1: Failing unit test.** `docPanelSections` order and feature gating.
- [ ] **Step 2: Implement.**
  - The header shows the breadcrumb (app › model), document number as title, status pill, and actions: the primary
    lifecycle verb plus a ⋯ menu.
  - Tabs come from the form layout's tabs when present.
  - Sections are titled groups divided by `line`, not cards.
  - Two-column field grid on ≥ 900px.
  - Child tables inline.
  - `DocPanel` on the right.
- [ ] **Step 3: Update `test_app.py`** selectors for the lifecycle bar, attachments and approval banner. Run at 1280 and 400.
  Expected: PASS.
- [ ] **Step 4: Commit** `app: Frappe-style document page with side panel`.

---

### Task 10: Remaining app screens

**Files:**
- Modify: `web/app/dashboard.js`, `web/app/approvals.js` (inbox), `web/app/import.js`, `web/app/process.js`,
  `web/app/ubah.js` (mode bar, change-set card, preview banner), `web/app/index.html`

- [ ] **Step 1: Move each screen onto the slim `PageHeader`, flat sections and pills.**
  - Dashboards keep `DashGrid` with 16px gaps and 8px cards.
  - The Ubah bar becomes a 40px strip under the header in `selected`/`selected-ink`, with "Urungkan" toasts bottom-right.
- [ ] **Step 2: Run** `node --test web/app/test/*.test.js`, then `test_app.py` at 1280 and 400 with `--shots /tmp/app`.
  Expected: PASS. Compare the shots with the mockup. No screen may still show the 56px top nav, uppercase lozenges or
  24px titles.
- [ ] **Step 3: Commit** `app: dashboards, approvals, import, process and Ubah in the new look`.

---

### Task 11: Console

**Files:**
- Modify: `web/console/console.js:1030-1065` (shell), `web/console/index.html` (`cn-*` CSS), `web/console/README.md`
- Test: `web/console/test_console.py`, `web/console/test_live.py`

- [ ] **Step 1: Replace `TopNav` + `Sidebar` with the new `Sidebar`.**
  - Switcher title "Rangka Console", subtitle the site name and version, no org switch.
  - Ringkasan, Change set and Audit become `pinned`; the current groups stay `groups`.
  - ⌘K keeps its groups.
  - Regenerate `tokens.css` via `python3 web/design-system/tokens_css.py`.
- [ ] **Step 2: Run** `python3 web/console/build_demo.py && python3 web/console/test_console.py --react-dir web/app/vendor`
  and `test_live.py`. Expected: PASS.
- [ ] **Step 3: Commit** `console: Frappe sidebar shell`.

---

### Task 12: Docs, screenshots, full run

**Files:**
- Modify: `templates/themes/standard.rk` (comment), `website` pages describing the UI and Console, `website/public/screens/*.webp`
- Modify: `CLAUDE.md` "Design" line in the workspace root, which currently says "Atlassian-grade structure"

- [ ] **Step 1: Update the wording.** Replace "Atlassian" structure wording with "Frappe UI structure, Rangka colours"
  wherever it describes the product UI: `grep -rn "Atlassian" web website templates schema docs --include=*.md --include=*.rk --include=*.json`.
  Leave historical contract text alone.
- [ ] **Step 2: Recapture the screenshots** the landing and docs use, from `make -C kernel run-demo`, light and dark.
- [ ] **Step 3: Full run.**
  `make -C kernel test-unit test compare-python && node --test web/app/test/*.test.js web/design-system/test/*.test.js && python3 web/design-system/test_tokens.py && python3 web/app/test_app.py --require-all && python3 web/app/test_app.py --width 400 && python3 web/console/test_console.py --react-dir web/app/vendor && (cd website && npm ci && npm run build)`.
  Expected: everything passes.
- [ ] **Step 4: Commit** `docs: Frappe UI structure; recaptured screenshots`.
