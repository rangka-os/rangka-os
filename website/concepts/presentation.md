# Themes and layouts

Look and structure are metadata: a theme or layout `extends` a template and overrides only what differs, so every
organisation starts from a tested standard. Light and dark are always both produced.

## Themes

```yaml
# themes/kopi_senja.rk
name: kopi_senja
extends: rangka/standard     # rangka/laut, rangka/daun, rangka/senja, rangka/anggur, rangka/arang
mode: system                 # light | dark | system
accent: '#7a4520'            # one colour is enough: the kernel derives the rest
button: accent               # ink (default) | accent
neutral: stone               # stone (default) | slate | gray
font: {sans: plus-jakarta-sans}
color:                       # optional, per token, applied last
  brand: '#9a5b2e'           # a string sets light and derives dark
  chart-1: {light: '#0c66e4', dark: '#579dff'}   # or set both modes yourself
radius: standard             # sharp | standard | round
density: standard            # compact | standard | comfortable
```

- **`rangka/standard`** is the Rangka base: Ink (#1C1917) primary buttons, deep Bata (#A33B23) links and selection on a
  Kapur (#F6EDE6) tint, a Bata Terang (#E0623F) focus ring, warm stone neutrals; danger stays red. A company theme
  overrides only what it needs, like the coffee `brand` and the first chart colour above.
- **One colour is enough** (contract 0067): `accent` derives `brand`, `selected`, `row-selected`, `selected-ink`,
  `link`, `focus` and `chart-1`, light and dark, each moved in 1 % lightness steps until it passes the contrast floors.
  `button: accent` also derives the primary button; `neutral` picks warm stone, cool slate or plain gray.
- **Bundled themes**: `standard` (Standar), `laut`, `daun` (was `clinic`), `senja` (was `retail`), `anggur`, `arang`.
  The old names still load with a `renamed_template` warning.
- **Locked tokens**: status colours (`ok`, `warn`, `bad`, `info`, `discovery` and their variants), `raka*`, `spark`,
  `helmet`, `scrim` and `mascot-ink` keep their meaning in every theme (`locked_token`).
- **Fonts**: `font.sans` and `font.display` take a bundled font: `system`, `inter`, `plus-jakarta-sans`,
  `nunito-sans`, `ibm-plex-sans`, `source-serif-4` (`unknown_font`).
- **Tokens** are the design system's names: `primary`, `selected`, `selected-ink`, `brand`, `link`, `chart-1` … `chart-6`.
- **Dark derivation**: a colour given once keeps its hue; backgrounds and tints go to lightness 100 − L·0.6, accents
  gain 15 lightness, then contrast floors apply.
- **Contrast is enforced**: the validator refuses a theme whose text drops below 4.5:1 or whose controls drop below 3:1
  in either mode (`low_contrast`).

Reference: [theme.rk](/reference/schema/theme).

## Layouts

```yaml
# layouts/crm_first.rk
name: crm_first
extends: rangka/top-nav
shell: top-nav               # side-only (default) | top-nav | pos | portal
home: dashboard:crm_overview
nav:
  top:
    - {to: 'dashboard:crm_overview', label: {id: Dasbor}}
    - {to: 'model:deal', label: {id: Peluang}}
  create: [deal, lead, sales_invoice]
  app_switcher: true
pages:
  - name: pagi_ini
    label: {id: Pagi ini}
    blocks:
      - {block: page_header, props: {title: Pagi ini}}
      - block: columns
        children:
          - {block: number, span: 4, props: {dashboard: crm_overview, widget: pipeline}}
          - {block: list, span: 8, props: {dashboard: crm_overview, widget: activities_due}}
```

The default shell is `side-only`, a Frappe-style sidebar; a site without a layout gets it too
([contract 0019](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0019-frappe-shell-default.md)).
`top-nav` puts the app switcher and search in a bar above the sidebar. From 1024px wide the bar also carries the menu:
the layout's `nav.top` links in file order (up to 6; a `model:` or `dashboard:` target the site lacks is left out), or,
without `nav.top`, one item per sidebar group. A layout with `nav.top` and no `shell` gets `top-nav`; say
`shell: side-only` to keep only the sidebar. The `nav.top` links are also in ⌘K. `pos` and `portal` show the sidebar
until those shells are built.

A page is a tree of **UI blocks** — `page_header`, `list`, `board`, `form`, `dashboard`, `number`, `chart`, `table`,
`markdown`, `filter_bar`, `tabs`, `columns`, `empty_state`, `embed` — on a 12-column grid. Each block is a design-system
component; its props come from the metadata. Reference: [layout.rk](/reference/schema/layout).

Templates ship in `rangka-os/templates/themes` and `templates/layouts`.
