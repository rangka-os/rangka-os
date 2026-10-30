# Themes and layouts

Look and structure are metadata: a theme or layout `extends` a template and overrides only what differs, so every
organisation starts from a tested standard. Light and dark are always both produced.

## Themes

```yaml
# themes/kopi_senja.rk
name: kopi_senja
extends: rangka/standard     # rangka/retail, rangka/clinic
mode: system                 # light | dark | system
color:
  brand: '#9a5b2e'           # a string sets light and derives dark
  primary: {light: '#0c66e4', dark: '#579dff'}
radius: standard             # sharp | standard | round
density: standard            # compact | standard | comfortable
```

- **`rangka/standard`** is the Rangka base: Ink (#1C1917) primary buttons, Bata (#A33B23) links, selection and focus on a Kapur tint, warm stone neutrals. A company theme overrides only what it needs, like the blue `primary` above.
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
shell: top-nav               # top-nav | side-only | pos | portal
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

A page is a tree of **UI blocks** — `page_header`, `list`, `board`, `form`, `dashboard`, `number`, `chart`, `table`,
`markdown`, `filter_bar`, `tabs`, `columns`, `empty_state` — on a 12-column grid. Each block is a design-system
component; its props come from the metadata. Reference: [layout.rk](/reference/schema/layout).

Templates ship in `rangka-os/templates/themes` and `templates/layouts`.
