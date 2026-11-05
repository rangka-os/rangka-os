Rangka is the business-apps side of the Omilo family: records, stock and money for Indonesian small and medium businesses (CRM, sales and invoices, POS, stock). Rangka screens take their **structure from Frappe UI** (compact 13px text, 28px controls, a side navigation on soft grey, flat grey secondary buttons, filled inputs, status pills with a dot, line borders and shadows only on floating things) and their **colours from the Rangka brand**: Ink for actions, deep Bata for selection, links and checked boxes, Kapur for selected backgrounds, Bata Terang for focus, warm stone neutrals. Every app (Rangka CRM, Penjualan, POS, Stok) and Omilo share one account. The Rangka logo (Sendi) is the mark; Raka is the Rangka AI agent with its own teal. There is a dark theme for everything, three densities, plus the components a business app needs: tables, filters, documents, money, dashboards and the point of sale.

## Content fundamentals

- **Bahasa Indonesia first**, English second. Write the Indonesian string first and keep the English one just as short.
- **Sentence case** everywhere, including status pills, nav group labels and menu headings: "Faktur penjualan", never "Faktur Penjualan" or "FAKTUR PENJUALAN". CSS never uppercases text.
- **Verbs on actions**, nouns on pages: the button says "Ajukan faktur", the page is "Faktur".
- **Speak to the owner as "kamu"**, plainly, the way a trusted bookkeeper would. Use "Anda" only in customer-facing documents (invoices, receipts, portals).
- **Name things the way the business does**: "pelanggan", "barang", "stok", "faktur", "kasir", "outlet", "lunas", "jatuh tempo". Use model, record and workflow only in developer docs.
- **Errors say how to fix them**: "NPWP harus 16 angka", not "Input tidak valid".
- **No emoji** in the product UI.

### Numbers, money and dates

- Rupiah has no decimals: `Rp 1.250.000`. Use a dot for thousands, a non-breaking space after "Rp", and a true minus sign (`−Rp 75.000`). Use `Amount` or `formatRupiah()`.
- Dashboards may shorten big figures with `compactNumber()`: `Rp 51,1 jt`, `Rp 2,3 M`. Put the full amount in the tooltip.
- Write percentages with a comma: `11%`, `2,5%`.
- Dates read `27 Okt 2026` in the UI and `27/10/2026` in tables and exports. Times use 24 hours: `14.30`.
- Document numbers read `INV-2026-00042`; in tables they are link-coloured (`rk-doc-no`, deep Bata) and open the record.
- **Numbers use tabular figures in the sans font** (`font-variant-numeric: tabular-nums`), right-aligned in columns. No monospace numbers: mono is only for code, IDs in developer screens and CEL expressions.

## Visual foundations

Open `gallery.html` (served over http from `web/`) to see every component in light and dark and in the three densities.

### Layout (Frappe-style)

- **The shell is `AppShell`**: a side navigation on the left and the page on the right. No full-width top bar in the default shell; `TopNav` remains for layouts that ask for it.
- **Side navigation (`Sidebar`)**, 216px (`sidebar`), on `soft` grey with a 1px `line` on its right: the business switcher on top (Ink mark with a Bata corner, name, outlet), search (⌘K), pinned entries, then nav groups. Group labels are 12px `muted`, sentence case. Links are 28px tall with 6px corners; the current one is Kapur `selected` with deep Bata text, no bar. Counts are plain `muted` figures on the right. `collapsed` shrinks it to 48px (`sidebar-collapsed`): icons only, labels as tooltips, counts as a dot, group labels as dividers, the footer compact (the avatar only, or the custom `footer` centred and clipped, so give it icon-only content); `onToggleCollapse` adds the fold button at the bottom. Below 860px the shell hides it and the app opens it as a drawer.
- **Page header (`PageHeader`)**: one 48px row (`header`) with a `line` under it: muted parent breadcrumbs and the title at 15px semibold ("Penjualan / Faktur / INV-2026-00042"), the status pill, actions on the right with one primary. Tabs sit underneath; a subtitle, when there is one, is a muted line below the row.
- **List views**: `ViewSwitcher` (Daftar, Kanban, Kelompok, Laporan) sits in the list toolbar. Selecting rows shows `BulkBar`, a floating bar at the bottom of the list. `GroupList` shows one 36px header per group (count pill, sums on the right, caller-formatted); `ReportTable` scrolls inside its own bordered box with a sticky total row on `soft`.
- **Document page**: the form on the left, `DocPanel` (280px, `line` on its left, titled sections divided by `line`) on the right; wrap both in `.rk-docpage` and the panel moves below the form at 1100px and narrower. **Content** is padded 16px 20px (12px 16px on phones), sections 16px apart.
- **The page is white (`app`) and flat.** Tables, toolbars and forms sit straight on the page. Use `Card` (1px `line`, 8px corners, no shadow) for grouped content such as dashboard widgets, settings sections and stat cards. Form sections are titled groups divided by a `line`, not cards.
- **Settings** use `SettingsLayout`: a settings nav on `soft`, then sections of label/description on the left and the control on the right.
- **Dashboards** use `DashGrid`: 12 columns, 12px gaps, number cards on top, chart and list cards under them.
- **Density.** `<html data-density="compact|standard|comfortable">` swaps the metric tokens; `standard` (the default) is the design below. `comfortable` gives 32px controls and 40px rows, `compact` 24px controls and 32px rows. On phones (≤ 600px) every control is 40px tall whatever the density.

### Colour

- **Grounds.** `app` and `surface` are white (dark: warm `#1a1715` / `#221e1b`). `soft` is the sunken grey of the side navigation, read-only areas, code and footers. `neutral` is the grey fill of secondary buttons, inputs, tags, count badges and default pills; `neutral-hover` darkens it on hover.
- **Text.** `ink` (#1C1917, the logo's Ink) for primary text, `ink-2` for nav items and secondary text, `muted` for labels, column headers, metadata and breadcrumbs. `faint` is for placeholders and disabled labels only, because it fails 4.5:1.
- **Borders.** `line` draws card edges, row dividers, the side navigation edge and tab rules; `line-strong` marks totals and dashed drop targets. `border` outlines inputs, selects, checkboxes, radios and the off switch.
- **Ink is the action colour.** `primary` (#1C1917) fills the one primary button per view, the selected date and the POS pay key; in dark it flips to warm off-white with dark text. Secondary buttons are grey with no border. Danger buttons are red text on `bad-tint`, or `bad-solid` for the final destructive step, so a destructive action never looks like the main one.
- **Bata is the selection and link colour.** Selection is the `selected` Kapur background (#F6EDE6) with `selected-ink` deep Bata text (#A33B23): the current nav link, a selected option, active filters, the bulk bar. Checked checkboxes, radios and the on switch are deep Bata. Selected and open table rows take the lighter `row-selected` tint. The current tab gets a 2px Bata underline. `link` and document numbers are the same deep Bata.
- **Focus** is `focus` (Bata Terang #E0623F): a 2px outline on buttons and controls; inputs turn white with a `focus` border and a Kapur ring.
- **Status colours always come with a word.** `ok`, `warn`, `bad`, `info` and `discovery` each have a text colour and a `-tint` background, used by pills, section messages and toasts. `-solid` versions fill bold pills and unread dots; `warn-solid` (yellow) takes dark `on-warn` text and also fills the warning `Banner`. `discovery` (purple) marks new things.
- **Charts** use `chart-1` to `chart-6`: Bata first, then teal-blue, purple, amber, green and blue; single series use `chart-1`. Legends and labels always name each series.
- **Bata is the brand.** `brand` (Bata #B4432A, Bata Terang #E0623F on dark), `brand-deep` (Ink) and `brand-soft` (Kapur) carry the logo, the cover, sign-in, marketing, the landing page, the app icon and `chart-1`. Inside the interface Bata appears only as `selected-ink`/`link` and as `focus`, never as a filled button and never in error UI, which keeps `bad` red unmistakable.
- **Teal belongs to Raka.** `raka`, `raka-deep` and `raka-soft` colour Raka, the Rangka AI agent, and Rangka AI surfaces only (the "Tanya Raka" command item).
- **Amber is shared.** `spark` is Omilo's spark; `helmet` is an alias for Raka's hat. A mark colour, never text.
- **Dark theme.** Every token has a dark value: warm near-black grounds, off-white primary buttons, Bata Terang (#F08A6B) for links, selection, checked boxes and focus, lighter status hues. Every text pair holds 4.5:1 or more.

### Type

- **The system font stack** (`ui-sans-serif, -apple-system, Segoe UI, …`) for everything in the product. **Bricolage Grotesque** only for the brand name and marketing display. **Mono** only for code and CEL.
- `font-size`/`line-height` 13/20 for text, inputs, menus and cells; `font-size-sm` 12/16 for labels, help text, column headers and captions.
- **Headings:** the header title 15px semibold; page and dialog titles 16/24 semibold; card, form-section, settings-section and drawer-section titles 14/20 semibold.
- **No uppercase.** Pills, nav group labels, menu headings, command-menu groups and calendar weekdays are sentence case, 12px `muted` where they are labels.
- **Figures:** 22/28 semibold on number cards; 13px tabular figures in tables and totals, right aligned.

### Spacing, radius, size

- A 4px grid: 6–8 between buttons, 12 between cards and inside small cards, 16 between page sections, 20 inside dialogs, 16px 20px page padding.
- **Radii:** `r-sm` 3px for checkboxes, tags and keyboard hints; `r` / `r-btn` / `r-md` 6px for buttons, inputs, selects, nav links, menu items and options; `r-lg` 8px for cards, menus, popovers, section messages and toasts; `r-dialog` 12px for dialogs and the command menu; `r-pill` for status pills and counts.
- **Heights:** controls `control` 28px (`control-sm` 24px, `control-lg` 32px); fields `field` 28px; nav links and menu items 28px; table header rows `row-head` 32px and rows `row` 36px; header `header` 48px; side navigation `sidebar` 216px, collapsed `sidebar-collapsed` 48px. `pos-key` stays 64px for anything a cashier taps.

### Borders, shadows, states

- **Flat by default.** Cards and tables use a 1px `line`, never a shadow. Only floating things (menus, selects, date pickers, popovers, dialogs, drawers, the command menu, toasts) use the soft overlay shadows (`shadow-pop`, `shadow-overlay`, `shadow-toast`).
- **Inputs are filled and outlined.** Text fields, selects, link fields and search boxes have a `neutral` grey fill inside a 1px `border` outline, so a field reads as a field (3:1 or more against the page); hover darkens the fill; focus turns them white with a `focus` border and a Kapur ring. Error fields turn white with a `bad-solid` border.
- **Hover:** `hover` (a 5% Ink wash) on nav links, menu items, ghost buttons and table rows; secondary buttons and inputs go to `neutral-hover`.
- **Focus:** a 2px `focus` outline with 2px offset on buttons and controls.
- **Disabled:** `disabled-bg` fill with a `faint` label and a not-allowed cursor.
- **Loading:** `Spinner` for short waits and inside buttons (`loading`); `Skeleton` shapes where content will appear (tables with `loading`, number and chart cards with `loading`).
- **Motion:** 0.1–0.2s background and border transitions; toasts rise 8px. Spinners slow down and skeletons stop pulsing under reduced motion.

### Records and documents

- **Submitted documents are immutable.** `DocumentHeader` shows the lifecycle as `Steps` (Draf → Diajukan → Lunas or Dibatalkan), says the record is locked, and offers only Batalkan and Ubah (amend).
- **Lists are flat `Table`s** (Frappe list view): a toolbar above (search, filters, actions right), a 32px header row in 12px `muted`, 36px rows with `line` dividers, a checkbox column, document numbers in deep Bata, numbers right aligned, the page footer under it. Checked rows take `row-selected`; a Kapur bulk bar appears while rows are checked.
- **Filters:** `FilterBar` for quick filters as grey dropdown buttons (Kapur when active); `FilterBuilder` for saved views with field / operator / value rules (it shows the CEL expression the engine runs).
- **Statuses** use `StatusChip` or `Lozenge`: a rounded pill with a 6px dot and the word in sentence case, from the fixed vocabulary Draf, Diajukan, Lunas, Dibayar sebagian, Jatuh tempo, Dibatalkan. Both render `<span class="rk-pill <tone>">`; the older `rk-chip` class stays on the element for existing selectors.
- **Links between records use `LinkField`.** Never a plain select for customers, items, suppliers or outlets: people need search, quick-create and the peek card.
- **Line items use `ChildTable`.** Totals and PPN are computed in the table footer; submitted documents show it read-only.
- **Dates use `DatePicker`; periods use `DateRangePicker`.** Monday-first, Indonesian month names, national holidays marked.
- **The POS is offline-tolerant.** `PosKeypad` always shows the sync pill: Online, or Offline with the number of queued sales.

### Overlays and feedback

- **One layer at a time.** A `Dialog`, `Drawer`, `CommandMenu` or image viewer sits on the `scrim`. Don't stack a dialog on a drawer; close the drawer first or confirm inline.
- **Pick by job:**
  - `Dialog` confirm (440px): a yes/no with consequences.
  - `Dialog` form (560px): up to five fields.
  - `Drawer`: a record to read or edit beside its list.
  - `CommandMenu` (⌘K): jumping, commands and asking.
  - `Menu`: the same actions from right-click and the ⋯ button.
- **Feedback by time:**
  - `Toast` (bottom-right, 16px from the edges): something just happened.
  - `Alert` (section message, tinted, no border): something is true on this page.
  - `Banner`: something is true across the app, like offline mode.
  - `EmptyState`: nothing here yet.

## Iconography

- Use **Remix Icon**, line style, the same set as Omilo (`remixicon` 4.9.1, Apache-2.0).
- Size icons at 16px in text, 12px in lozenges and 22px on POS keys. They take the text colour, usually `ink-2`.
- The `Icon` component carries the icons Rangka uses (see the Icon card). To add another, copy its `-line` path from Remix Icon.
- Product tiles (top nav, side nav, app switcher) are an icon in white on the app's colour (CRM purple #5E4DB2, Penjualan blue, POS orange, Stok green); the default is Ink; Rangka's own tools (Console) use Ink with the Rangka symbol.
- Use no emoji and no other icon sets.

## Logo

- **The Rangka logo is Sendi** (logo kit v1.0, Saga Tekno Studio, 27 Oct 2026): a lowercase r made of two beams meeting in a mitred joint, the arm cut at 45° like a roof eave. Files: `assets/Logos/rangka-symbol.svg`, `rangka-horizontal.svg`, `rangka-horizontal-reversed.svg`, `rangka-app-icon-bata-rounded.svg`.
- **Colours:** Ink #1C1917 on light, white reversed on dark or on Bata. The app icon is white on Bata #B4432A. Use the kit's files; never redraw, recolour beyond the kit's one-colour versions, or stretch.
- **Clear space** is one beam width (about a quarter of the logo height). Minimum sizes: horizontal 96px, stacked 64px, symbol 32px, the small-cut symbol 16px.
- **Product names** sit next to the logo as text ("Rangka OS", "Rangka CRM"), never inside it.

## Raka

- **What it is.** Raka (`assets/Logos/raka-mark.svg`, the `Raka` component) is the Rangka AI agent, not the Rangka logo. It is drawn on the Omilo mascot grid: 64 units, an 18-unit rounded tile at 16% of the body colour, and Omilo's eyes, cheeks and smile. It has a teal (`raka`) `frame` body with a seam and four bolts, and wears an amber hard hat.
- **Where it goes.** Wherever the AI speaks or acts: the ⌘K "Tanya Raka" answers, Rangka AI change sets, onboarding chat, and empty states that offer AI help. Use the Rangka logo, not Raka, for the product, sign-in and the side nav header.
- **Colours are fixed.** Never recolour, stretch or animate Raka beyond Omilo's eye-follow and hop.
- **Search asks Raka** ("Cari atau tanya Raka", "Tanya Raka: …"). Omilo helpers that appear in Rangka keep their own Omilo looks.
- **Raka in the app.** `AskBar` (⌘J) floats 24px above the bottom, centred, 560px wide, with a removable "Halaman ini" chip so Raka knows the page; Enter sends, Esc closes. `ChatDrawer` (420px, right) holds the conversation and leaves the page usable: no scrim on desktop, full screen on phones. Raka's mark sits next to its turns, tool activity shows as short muted lines, an escalation is an info `Alert`, and a proposed change set is a card with a risk pill ("Risiko rendah/sedang/tinggi", or neutral "Risiko belum diketahui"), "Lihat pratinjau" and "Buka di Ubah". Raka never approves or applies; the card says so, and a person approves in Ubah.
- **The drawing is a draft** made from the project brief.
