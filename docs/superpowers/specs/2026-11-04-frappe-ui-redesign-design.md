# Rangka UI redesign: Frappe UI structure, Rangka colours

Date: 4 Nov 2026 · Owner: Dede Kurniawan · Status: design, awaiting approval

## Why

Dede wants Rangka to feel **compact, lean, easy and professional**, the way Frappe's newer apps feel
(Frappe CRM, Helpdesk, Insights, HR, Lending, and the ERPNext desk). The current design system takes its structure
from the Atlassian Design System (56px top nav across the page, 240px side nav, 24px headings, uppercase lozenges,
32px controls). It works, but it reads heavy and generic next to Frappe UI ("Espresso").

Frappe's UI is good and may be followed closely. **Rangka's architecture is different**, and the redesign must express
Rangka's own concepts, not Frappe's: metadata in `.rk` files, change sets (Ubah mode, approvals, preview, revert),
company overlays, themes and layouts as layer-6 metadata, Raka as the AI agent, and features detected from the kernel.

This is project 1 of 3. Project 2 (module screens: helpdesk ticket inbox, Insights-style report/chart builder,
CRM deal page with activity timeline, HR employee profile and leave calendar, lending schedule, ERPNext-style module
workspaces) and project 3 (the actual apps and connectors in `rangka-apps` / `rangka-connectors`) come after and are
not designed here. This redesign must leave room for them.

## Decided

- **Direction B (chosen from mockups):** Frappe UI structure and density, Rangka colours. Warm stone neutrals,
  Ink `#1C1917` primary buttons, deep Bata `#A33B23` for links, the selected sidebar item, checked checkboxes and
  selected rows, Kapur `#F6EDE6` selected background, focus Bata Terang `#E0623F`, danger stays red. Status pills keep
  soft green / amber / red / grey fills. Reference mockup:
  `../../../.superpowers/brainstorm/40220-1793787479/content/direction.html` (option B; workspace folder, not in git).
- **Frappe's interactions come too**, not only the restyle:
  - Sidebar that collapses to icons.
  - ⌘K command bar ("Cari atau tanya Raka").
  - List views that switch between list, kanban, group-by and report.
- **Everything moves.** No screen is left in the old Atlassian style: design system, end-user app, Console.

## Target look (Frappe UI in Rangka colours)

Numbers below are the target; the builder may adjust by a pixel or two after seeing it rendered.

- **Type:** Inter (system fallback). Body 13/20, small 12/16, page title 15–16px semibold in the header,
  form/section titles 14px semibold. Tabular figures for numbers, right aligned in columns.
- **Shell:**
  - Sidebar: about 208–220px on `soft` with a right `line` border. From top to bottom:
    - business switcher (Sendi tile + company name + chevron; opens org/outlet switch, the app switcher and the
      account menu);
    - search / ⌘K entry;
    - fixed entries (Beranda, Kotak masuk or Persetujuan with count, Proses bisnis when the kernel has them);
    - nav groups from the layout (`nav.side`) or `meta.apps[].menu`;
    - footer: Ubah toggle, help, collapse.
  - Collapsed sidebar: about 48px, icons with tooltips.
  - Header: 48px, breadcrumb with title, actions on the right (one primary).
  - No full-width top nav in the default shell.
- **Controls:** 28px buttons and inputs on desktop (sm 24px, lg 32px), 6px radius, no border on secondary buttons
  (grey fill), Ink primary, subtle/ghost variant for toolbars. Sidebar items 28px with 6px radius.
- **Lists:**
  - Toolbar (search, Filter, Urutkan, Kolom, view switcher).
  - Header row 32px in `muted` 12px.
  - Rows 36px with `line` dividers.
  - Checkbox column; selected rows tinted.
  - Document numbers in deep Bata.
  - Footer with "x dari y" and page size.
  - Bulk bar when rows are selected.
- **Status:** rounded pill with a dot and sentence-case text, replacing uppercase lozenges.
- **Forms:**
  - Frappe-style document page: header with status pill and actions, tabs, sections as titled groups with no heavy
    cards, two-column field grid.
  - Child tables inline.
  - A right-hand side panel for meta: created/modified, attachments, approvals, print.
    (Change sets change metadata, not records, so they have no place here; they stay in Ubah and the Console.)
- **Surfaces:** flat, `line` borders, small shadows only on floating things (menus, dialogs, toasts).
  Dialogs 8–12px radius. Toasts bottom-right, Frappe style.
- **Phones (≤ 600px):** sidebar becomes a drawer; controls stay at least 40px tall (the existing
  `test_no_horizontal_scroll` rule stands); no sideways scroll.
- **Dark theme:** redone in the same style from the existing dark token values.

## Rangka concepts the UI must show (where it differs from Frappe)

| Frappe has | Rangka shows instead |
| --- | --- |
| Customize Form / edit DocType directly | **Ubah mode**: every edit is a change set; low risk + approver applies with "Urungkan" toast, medium/high stop at `previewed` with the risk card. Keep this flow; restyle it |
| Desk workspace pages | App **dashboards** (`dashboards/*.rk`) as the home of each app, plus the layout's `nav.side` |
| Theme set in code | **theme.rk / layout.rk** (layer 6). The redesign is the new look of `rangka/standard` and of the default shell. Company theme overrides by token name must keep working |
| Assistant-less | **Raka** (teal, AI only) answers in ⌘K and proposes change sets; never the product logo |
| One site = one company | Org switcher in the business switcher; multi-org via `/auth/org` |
| Approve anything | Approvals inbox; "Setujui & terapkan" only for approvers, high risk owner only |
| Features always present | **Feature detection** stays: entries and buttons appear only when the kernel has them |

## Constraints

- **Token names stay.** `theme.rk` `color:` overrides address tokens by name (`primary`, `selected`, `brand`,
  `chart-1`…), and `kernel/internal/theme/tokens.json` must equal `web/design-system/tokens.json` (test enforced,
  `make sync-schemas`). Values and usage notes change, names don't; new tokens may be added.
- **`theme.rk` `density` and `radius`** (`compact|standard|comfortable`, `sharp|standard|round`) keep working.
  Frappe density becomes `standard`; `comfortable` stays usable for people who want the roomier feel.
- **Default shell is a schema matter.** `schema/0.4/layout.json` has `shell: top-nav | side-only | pos | portal`
  with default `top-nav` and describes top-nav as "Atlassian style". Making the Frappe sidebar the default
  changes the schema default and description, so it starts as a contract proposal
  (`docs/contracts/0017-…`, see `0000-how-contracts-change.md`). `top-nav` keeps rendering, restyled.
- **No build step, no new framework.** `web/app` is plain React 18.3.1 (UMD, vendored) + `window.Rangka` design
  system, hash routes, no CDN. Stay with that. Remix Icon stays the only icon set.
- **Content rules unchanged:** Bahasa Indonesia first, sentence case, "kamu", `Rp 1.250.000`, `27 Okt 2026`,
  no emoji.
- **Brand unchanged:** Sendi logo, Raka teal for the AI only, landing page marketing style untouched.
- **Tests stay green and get updated, not deleted:**
  - `node --test web/app/test/*.test.js`
  - `python3 web/app/test_app.py` at 1280 and 400
  - `web/console/test_console.py`
  - kernel token sync test

## Scope (what moves)

1. `web/design-system`:
   - `tokens.json` values/usage, plus new tokens if needed;
   - `bundle.css` / `bundle.js` / `index.d.ts`. The components the app and Console use most come first: Button,
     Lozenge (becomes the pill), Card, Alert, PageHeader, Icon, Descriptions, Field, IconButton, Dialog,
     NativeSelect, EmptyState, Drawer, Table, Skeleton, NumberCard, Tabs, Steps, FilterBar, Pagination, Sidebar,
     TopNav, CommandMenu, Toast;
   - README rewritten from "Atlassian structure" to "Frappe UI structure, Rangka colours".
2. New pieces:
   - collapsible Sidebar with business switcher;
   - ViewSwitcher (list / kanban / group-by / report);
   - GroupBy list;
   - Report view (a dense, column-configurable table with totals);
   - document side panel;
   - pill status.
3. `web/app`: shell, list, form, dashboard, approvals, import, process, Ubah UI moved onto the new pieces.
4. `web/console`: same shell and components; `build_demo.py` regenerates `tokens.css`.
5. `kernel/internal/theme/tokens.json` via `make sync-schemas`.
6. Contract 0017 for the default shell; `templates/layouts/*.rk` and `templates/themes/standard.rk` comments updated.
7. Docs: the design-system README, `website` pages that describe the UI, and recaptured screenshots.
8. After the code: the claude.ai Design System artifact (Dede updates or asks for it); the old `web/prototype` is not
   updated.

## Goals / non-goals / anti-goals

- **Goal:** `make run-demo` → Kopi Senja in `/app/` and `/console/` feels like Frappe CRM in Rangka colours, on
  desktop and phone, light and dark, with every existing flow (Ubah, approvals, print, import, multi-org) working.
- **Non-goals:**
  - The module screens of project 2.
  - New apps or connectors (project 3).
  - Kernel API changes, apart from the one aggregate endpoint below.
  - Landing page.

## The one kernel addition: list aggregates (decided by Dede)

- Group-by and report views need totals across all matching rows, not just the loaded page.
- The kernel gets one read-only endpoint for this: count, sum, average, min and max over the rows a list query matches.
  It takes the list's search, filter and row scope, and can group by one field.
- Permissions and row scope work as they do for `GET /api/v1/r/{model}`.
- It starts as its own contract proposal, `docs/contracts/0018-…`, next to 0017.
- Its exact shape is left to the builder. It may reuse the dashboard aggregate code.
- **Anti-goals:**
  - Copying Frappe's concepts where Rangka differs (no direct "Customize Form" that bypasses change sets).
  - Losing token-name compatibility for company themes.
  - Bringing back blue/teal as UI colour.
  - A heavy look: big headings, thick cards, uppercase labels.

## Calls made while writing (Claude's, for Dede to check)

- **View, sort and columns are personal by default.** The chosen view (list / kanban / group-by / report), sort and
  visible columns are kept per person in the browser, like Frappe's list settings. "Jadikan bawaan untuk semua"
  goes through Ubah as a change set on `views.*`, the way list columns already do.
- **Report view** uses `views.list` as its starting columns and lets the person add any model field. It gets no metadata
  of its own. The calendar view (`views.default: calendar`) is not part of this project; it belongs to project 2.
- **Contract 0017** covers three things:
  - make the Frappe sidebar the default shell;
  - make `web/app` honour `layout.shell`, which it ignores today because it always renders `top`;
  - fix the kernel's fallback layout, which returns `shell: "side"`, a value that is not in the schema enum.
  The contract is proposed in the first PR. Shell work merges after Dede accepts it.
- **Console** moves to the same sidebar. Its switcher shows "Rangka Console" and the site name, with no org switch.

## Left to the builder

- Exact pixel values within the ranges above, shadow recipes, and the dark-theme fine tuning.
- Component API shape for the new pieces, as long as existing call sites keep working or are updated in the same change.
- Order of work and how the change is split into PRs (one branch/worktree per task, per the workspace rules).
- Whether `comfortable` density maps to the old metrics or a new set.
