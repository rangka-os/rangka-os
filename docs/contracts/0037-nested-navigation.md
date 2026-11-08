# 0037 Nested navigation: sub-levels in menus and nav.side

Status: proposed (8 Nov 2026) · Owner of the change: rangka-os (schema 0.5, both validators, web app, design system)
· Affects: rangka-apps (apps and modules may group their menus) · Builds on 0001, 0019, 0032

## Context
- A menu is one level today: `menu: [{group, items: [model]}]` in `app.json` and `module.json`, and
  `nav.side: [{group, items: [link]}]` in `layout.json`. A business with stock, purchasing and books (the Mogu pilot)
  gets a sidebar of 25+ links in six groups, every link at the same level.
- The web app now folds groups (design system `Sidebar`, remembered per person, a long navigation starts with only
  the current page's group open). That shortens the list but cannot express "Transfer stok" and "Penerimaan
  transfer" belonging under one "Transfer" entry.

## Decision

### 1. Schema (0.5, additive)
- `menu[].items[]` in `app.json` and `module.json`: an entry is a model name, as now, or a sub-menu
  `{label, icon?, items: [model]}`. Sub-menus do not nest further (two levels under a group).
- `menu[]` gains `collapsed: true | false` (optional): the group's starting state before the person folds or opens
  it. Without it the renderer decides (today: folded when the navigation has more than 15 links).
- `layout.json` `nav.side[].items[]`: an entry is a link `{to, label?, icon?}`, as now, or
  `{label, icon?, items: [link]}`; `nav.side[]` gains the same `collapsed`.
- A sub-menu needs at least one item and at most 12.

### 2. Validators (Go and Python, identical reports)
- `menu_depth`: a sub-menu inside a sub-menu.
- `menu_empty`: a sub-menu with no item that resolves (all models missing or not installed is a warning, as for
  groups today).
- Existing `unknown_model` and link checks apply to sub-menu items.

### 3. Rendering
- Design system `Sidebar`: an item may carry `items` (children). The parent is a row with a chevron; it is open when
  it holds the current page or the person opened it (same storage as group folds). Children are indented 24px,
  44px tall on touch.
- Icon rail (collapsed sidebar, tablet portrait): the parent shows its icon; a tap opens a flyout with the children.
- Command menu (⌘K) lists children as pages with the parent as their subtitle.
- Phone drawer: same tree as the sidebar.

### 4. Compatibility
- Old files stay valid. A file using sub-menus needs `requires: {rangka: '>=0.5'}`; older kernels reject it with
  their schema error.

## Consequences
- rangka-apps can regroup long modules (stock, purchasing, books) without new models.
- The fold behaviour shipped with the top-nav fix (8 Nov 2026) stays the default for groups without `collapsed`.

## Open
- Whether `collapsed` belongs to the app (author) or the site layout (company) only; this draft allows both, the
  layout wins.
