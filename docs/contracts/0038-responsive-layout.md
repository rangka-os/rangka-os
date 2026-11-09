# 0038 Responsive behaviour in layout.rk

Status: accepted and implemented (8 Nov 2026) · Owner of the change: rangka-os (schema 0.5, both validators, web app) · Affects:
rangka-apps sites and templates (optional) · Builds on 0019, 0030 · Follows `docs/responsive-coverage.md`

## Context
- Since 8 Nov 2026 the web app has fixed rules per width (`tokens.json` `breakpoint`): phone below 768px (drawer,
  cards, sheets), tablet portrait 768–1023px (icon rail), tablet landscape and desktop (full sidebar), 44px touch
  targets on phones and touch tablets.
- A company cannot change them. Some want the full sidebar on a 10" tablet at the counter, or a bottom tab bar on
  phones for the owner (the Juragan brand already draws one for Beranda, Setujui, Cari, Menu).

## Decision

### 1. `layout.json` gains `responsive` (optional)
```yaml
responsive:
  tablet: rail          # rail | sidebar | drawer   (768–1023px; default rail)
  phone: drawer         # drawer | tabs             (below 768px; default drawer)
  tabs:                 # phone: tabs only; 2–5 links, the drawer stays behind a last "Menu" tab
    - {to: "dashboard:ringkasan_outlet", label: {id: Beranda}, icon: home}
    - {to: "model:sales_invoice", icon: invoice}
    - {to: "page:approvals", label: {id: Setujui}, icon: check}
```
- The breakpoints are not configurable; they come from the design tokens so every screen agrees.
- `tabs` links use the existing link shape (`model:`, `dashboard:`, `page:` of the layout, plus `page:approvals`,
  `page:search`).
- The work shell (0030 §3) keeps its own rail and bottom bar; `responsive` applies to the desk shells.

### 2. Validators (Go and Python)
- `responsive_value`: an unknown value for `tablet` or `phone`.
- `responsive_tabs`: `phone: tabs` without 2–5 `tabs`, or `tabs` without `phone: tabs`.
- Link checks as for `nav.top`.

### 3. Kernel and web app
- `/api/v1/meta` returns `layout.responsive` as written; the web app falls back to today's rules for a missing key.
- `tablet: sidebar` keeps the full 216px sidebar at 768–1023px; `drawer` hides it behind the menu button as on
  phones.

## Consequences
- Templates (`templates/layouts/*.rk`) can ship a default: `pos` with `tablet: sidebar`, `portal` with
  `phone: tabs`.
- Rangka AI may propose a `responsive` change like any other layout change (layer 6, change set).

## Implementation (8 Nov 2026)
- Schema 0.5 `layout.json` `responsive`; `responsive_tabs` and tab link checks in both validators.
- Web app: `tablet` sets the shell class `ap-tablet-sidebar` / `ap-tablet-drawer` (rail is the default);
  `phone: tabs` draws `.ap-tabbar-layout` from `RangkaApp.tabLinks` (model, dashboard, `page:approvals`,
  `page:search`) plus a Menu tab. Other `page:` names are left out until the app routes layout pages.
