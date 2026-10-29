Rangka is the business-apps side of the Omilo family: records, stock and money for Indonesian small and medium businesses (CRM, sales and invoices, POS, stock). Rangka screens follow the **Atlassian Design System look**: blue for actions and selection, slate neutrals, flat surfaces, uppercase status lozenges, a top navigation across the full width with a side navigation under it. Every app (Rangka CRM, Penjualan, POS, Stok) and Omilo share one account and switch through the app switcher. Rangka keeps `brand-*` teal for Raka and marketing only, has a dark theme for everything, and adds the components a business app needs: tables, filters, documents, money, dashboards and the point of sale.

## Content fundamentals

- **Bahasa Indonesia first**, English second. Write the Indonesian string first and keep the English one just as short.
- **Sentence case** everywhere: "Faktur penjualan", never "Faktur Penjualan". Lozenges, side-nav group labels and menu headings are set uppercase by CSS; write their text in sentence case anyway ("Jatuh tempo") so screen readers and exports read it normally.
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
- Document numbers read `INV-2026-00042`; in tables they are link-blue (`rk-doc-no`) and open the record.
- **Numbers use tabular figures in the sans font** (`font-variant-numeric: tabular-nums`), right-aligned in columns. No monospace numbers: mono is only for code, IDs in developer screens and CEL expressions.

## Visual foundations

### Layout (Atlassian navigation)

- **The shell is `AppShell`** with `topNav`, `sider`, `pageHeader` and the content.
- **Top navigation (`TopNav`)**, 56px, across the full width: the app switcher (grid icon) listing every Rangka app and Omilo on the same account (single sign-on), the product name with its coloured tile, the app's primary items (the current one gets a 3px blue bar under it), the blue **Buat** create button, then on the right the search "Cari atau tanya Raka" (⌘K), notifications, help and the avatar that opens `AccountMenu` (company switch, profile, theme, sign out).
- **Side navigation (`Sidebar`)**, 240px, white with a right border: an app header (32px coloured tile, name, type), the company/outlet switcher, grouped links, the signed-in person. Collapse it with `collapsed`; below 860px it hides.
- **Page header (`PageHeader`)**: breadcrumbs, a 24px title with lozenges, actions on the right (one primary), tabs underneath. **Content** is padded 24px 40px (16px on phones), sections 24px apart.
- **The page is white (`app`) and mostly flat.** Tables, filter bars and page headers sit straight on the page. Use `Card` (1px `line-strong` border, 6px corners, no shadow) for grouped content such as dashboard widgets, form sections, settings sections and stat cards.
- **Settings** use `SettingsLayout`: a 240px settings nav, then sections of label/description on the left and the control on the right.
- **Dashboards** use `DashGrid`: 12 columns, 16px gaps, number cards on top, chart and list cards under them.

### Colour

- **Grounds.** `app` and `surface` are white (dark: `#1d2125` / `#22272b`). `soft` is the sunken grey for input hover, read-only areas, code and footers. `neutral` is the subtle grey fill of default buttons, tags, count badges and default lozenges.
- **Text.** `ink` (#172B4D) for primary text, `ink-2` for labels, nav items and button text, `muted` for metadata, column headers, captions and breadcrumbs. `faint` is for placeholders and disabled labels only, because it fails 4.5:1.
- **Borders.** `border` (#8590A2) outlines inputs, selects, checkboxes and radios so fields read as fields. `line-strong` outlines cards and draws table rules and nav edges; `line` divides rows inside cards.
- **Blue is the action and selection colour.** `primary` (#0C66E4) fills the one primary button per view, checked checkboxes and radios, the selected date and progress. `link` is the same blue.
- **Selection** is the `selected` light-blue background with `selected-ink` blue text, plus a 2–3px blue bar on the left of a selected nav link, menu item, option or table row. Active tabs and top-nav items get a blue bar underneath.
- **Focus** is `focus` (#388BFF): a 2px outline on buttons and controls, a 2px border on inputs.
- **Status colours always come with a word.** `ok`, `warn`, `bad`, `info` and `discovery` each have a text colour and a `-tint` background, used by lozenges, section messages and flags. `-solid` versions fill bold lozenges, the switch (green when on), danger buttons and unread dots; `warn-solid` (yellow) takes dark `on-warn` text and also fills the warning `Banner`. `discovery` (purple) marks new things and suggestions by Raka.
- **Charts** use `chart-1` to `chart-6` (Atlassian data-viz order); single series use `chart-1`. Legends and labels always name each series.
- **Teal is the brand and stays out of the interface.** `brand`, `brand-deep` and `brand-soft` are for Raka, the logo, the cover, sign-in, marketing and the app icon. Never for selection, focus, links, actions or status.
- **Amber is shared.** `spark` is Omilo's spark; `helmet` is an alias for Raka's hat. A mark colour, never text.
- **Dark theme.** Every token has a dark value: dark slate grounds, lighter blue (`#579DFF`) for primary and selection, lighter status hues. Every text pair holds 4.5:1 or more.

### Type

- **The system font stack** (`ui-sans-serif, -apple-system, Segoe UI, …`) for everything in the product. **Bricolage Grotesque** only for the brand name and marketing display. **Mono** only for code and CEL.
- `body` 14/20 for text, inputs, menus and cells; `small` 12/16 for help text and captions; `label` 12/16 semibold in `ink-2` above controls.
- **Headings:** `h1` 24/28 semibold for the page title; `h2` 20/24 for modal and drawer titles; `h3` 16/20 for card, form-section and settings-section titles.
- **Uppercase 11/16 bold** (`eyebrow`, `group-label`) for lozenges, side-nav groups, menu headings and calendar weekdays only.
- **Table headers** are 12/16 semibold in `ink-2`.
- **Figures:** `num-stat` 28/32 semibold on number cards; `num` 14/20 with tabular figures in tables and totals.

### Spacing, radius, size

- An 8px grid: 8 between buttons, 16 between cards and inside cards, 24 between page sections and around dialogs, 24px 40px page padding.
- **Radii:** `r-xs` 2px for lozenges and checkboxes; `r` / `r-btn` / `r-sm` 3px for buttons, inputs, selects, tags and nav items; `r-md` 4px for menus, dropdowns, popovers and flags; `r-lg` 6px for cards and section messages; `r-dialog` 8px for modals and the command menu; `r-pill` for count badges.
- **Heights:** buttons `control` 32px (`control-sm` 24px, `control-lg` 40px); form fields `field` 36px; nav links and menu items 36px; top nav `topbar` 56px; side nav `sidebar` 240px. `pos-key` stays 64px for anything a cashier taps.

### Borders, shadows, states

- **Flat by default.** Cards use a border, not a shadow. Everything that floats (menus, selects, date pickers, popovers, modals, drawers, the command menu, flags) uses the Atlassian overlay shadow (`shadow-pop`, `shadow-overlay`, `shadow-toast`). Clickable number cards lift with `shadow-float` on hover.
- **Hover:** `hover` (a 6% slate wash) on nav links, menu items, subtle buttons and table rows; default buttons go to `neutral-hover`; inputs fill with `soft`.
- **Pressed:** default buttons turn `selected` with blue text while pressed.
- **Focus:** a 2px `focus` outline with 2px offset on buttons and controls; inputs turn their border `focus` and add `focus-soft` so the border reads 2px.
- **Disabled:** `disabled-bg` fill with a `faint` label and a not-allowed cursor.
- **Loading:** `Spinner` for short waits and inside buttons (`loading`); `Skeleton` shapes where content will appear (tables with `loading`, number and chart cards with `loading`).
- **Motion:** 0.1–0.2s background and border transitions; flags slide up. Spinners slow down and skeletons stop pulsing under reduced motion.

### Records and documents

- **Submitted documents are immutable.** `DocumentHeader` shows the lifecycle as a progress tracker (`Steps`: Draf → Diajukan → Lunas or Dibatalkan), says the record is locked, and offers only Batalkan and Ubah (amend).
- **Lists are flat `Table`s** (Atlassian dynamic table): no outer card, a toolbar above (search, `FilterBar`, actions right), 12px semibold headers over a 2px rule, 1px row rules, no zebra stripes, numbers right-aligned, pagination under it. Checked rows turn `selected`; the open row also gets a 2px blue bar on the left.
- **Filters:** `FilterBar` for quick filters as dropdown buttons; `FilterBuilder` for saved views with field / operator / value rules (it shows the CEL expression the engine runs).
- **Statuses** use `StatusChip` (a lozenge) with the fixed vocabulary: Draf, Diajukan, Lunas, Dibayar sebagian, Jatuh tempo, Dibatalkan.
- **Links between records use `LinkField`.** Never a plain select for customers, items, suppliers or outlets: people need search, quick-create and the peek card.
- **Line items use `ChildTable`.** Totals and PPN are computed in the table footer; submitted documents show it read-only.
- **Dates use `DatePicker`; periods use `DateRangePicker`.** Monday-first, Indonesian month names, national holidays marked.
- **The POS is offline-tolerant.** `PosKeypad` always shows the sync lozenge: Online, or Offline with the number of queued sales.

### Overlays and feedback

- **One layer at a time.** A `Dialog`, `Drawer`, `CommandMenu` or image viewer sits on the `scrim`. Don't stack a dialog on a drawer; close the drawer first or confirm inline.
- **Pick by job:**
  - `Dialog` confirm (440px): a yes/no with consequences.
  - `Dialog` form (560px): up to five fields.
  - `Drawer`: a record to read or edit beside its list.
  - `CommandMenu` (⌘K): jumping, commands and asking.
  - `Menu`: the same actions from right-click and the ⋯ button.
- **Feedback by time:**
  - `Toast` (Atlassian flag, 400px, bottom-left): something just happened.
  - `Alert` (section message, tinted, no border): something is true on this page.
  - `Banner`: something is true across the app, like offline mode.
  - `EmptyState`: nothing here yet.

## Iconography

- Use **Remix Icon**, line style, the same set as Omilo (`remixicon` 4.9.1, Apache-2.0).
- Size icons at 16px in text, 12px in lozenges and 22px on POS keys. They take the text colour, usually `ink-2`.
- The `Icon` component carries the icons Rangka uses (see the Icon card). To add another, copy its `-line` path from Remix Icon.
- Product tiles (top nav, side nav, app switcher) are an icon in white on the app's colour (CRM purple #5E4DB2, Penjualan blue, POS orange, Stok green); the default is `primary` blue, never brand teal.
- Use no emoji and no other icon sets.

## Raka

- **What it is.** Raka (`assets/Logos/raka-mark.svg`, the `Raka` component) is the Rangka mark. It is drawn on the Omilo mascot grid: 64 units, an 18-unit rounded tile at 16% of the body colour, and Omilo's eyes, cheeks and smile. It has a teal (`brand`) `frame` body with a seam and four bolts, and wears an amber hard hat.
- **Where it goes.** On empty states, the sign-in screen, the cover and marketing, and in the side nav header when the app passes no `app` tile.
- **Colours are fixed.** Never recolour, stretch or animate Raka beyond Omilo's eye-follow and hop.
- **Raka is the product.** Search asks Raka ("Cari atau tanya Raka", "Tanya Raka: …"). Omilo helpers that appear in Rangka keep their own Omilo looks.
- **The drawing is a draft** made from the project brief.
