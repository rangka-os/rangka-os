# Responsive coverage: phone, tablet, desktop

Status of every rangka-os UI surface at three widths, the rules each pattern follows, and what is left.
Audited 8 Nov 2026 on `ui/responsive` (from main `c87f46f`) with Playwright + Chromium against `make run-demo`
(Kopi Senja, top-nav shell) and `kernel/testdata/screens` (work screens, counter). Reference design: `rangka.pen`
frame "Rangka POS · Tablet & HP" (`sL99a`); tablet 1194×834 / 834×1194, phone 390×844.

## Breakpoints (`tokens.json` → `breakpoint`)

| Name | Width | Pointer | Layout |
| --- | --- | --- | --- |
| phone | ≤ 767px | any | one column, drawer navigation, sheets, 44px targets |
| tablet portrait | 768–1023px | coarse | icon rail (48px; POS rail), one-column forms, 44px targets |
| tablet landscape | 1024–1279px | coarse | desktop layout, 44px targets |
| desktop | ≥ 1280px | fine | dense Frappe layout (28px controls) |

CSS cannot read custom properties inside `@media`, so stylesheets write the numbers: `(max-width: 767px)`,
`(min-width: 768px) and (max-width: 1023px)`, `(max-width: 1279px) and (pointer: coarse)`, `(min-width: 1280px)`.
Scripts use `Rangka.breakpoint()` / `Rangka.useBreakpoint()` (to add). A narrow desktop window with a mouse keeps
the dense controls; only phones and touch screens get the 44px `touch` size.

Today the CSS uses 489, 600, 640, 760, 860, 900 and 1100px. Each moves to the nearest rule above: 489–760 → 767,
860/900 → 1023, 1100 → 1279.

## Rules per pattern

| Pattern | Phone | Tablet | Desktop |
| --- | --- | --- | --- |
| Page | no sideways page scroll ever; 16px side padding | 20px | 20px |
| Side navigation | drawer from a 48px bar (menu, company, search) | icon rail (portrait), full sidebar (landscape) | full 216px sidebar, collapsible |
| Work shell (desk: false) | bottom tab bar, ≤ 5 items + Lainnya, icons + label | rail on the left (88px, icon + label), like the POS design | rail |
| Tables / lists | card list (title, number, amount, status) | table; it scrolls sideways inside its box with the first column sticky | table |
| Forms | one column, actions full width | one column below 1024, two above | grid as declared |
| Document panel | below the form | below the form | beside the form (≥ 1280) |
| Dialogs | bottom sheet, full width, max 90vh, actions stacked | centred | centred |
| Drawers, chat | full screen | 560px / 420px | same |
| Command menu | full screen sheet | centred | centred |
| Dashboards | 1 column | 2 columns | 12-column grid as declared |
| Tap targets | 44px both ways | 44px with a touch pointer | dense |
| POS counter | catalogue + order bar + order sheet + bottom tabs | portrait: rail + catalogue + order bar + sheet; landscape: rail + catalogue + 384px order panel | same as landscape |

## Coverage (before fixes)

`ok` = right layout and tap targets · `touch` = layout fine, controls under 44px · `gap` = layout differs from the
rules · `—` not audited yet. No screen scrolled sideways at any width (checked automatically on all 94 screen ×
width pairs in light; dark only differs in colour).

### Web app (`web/app`)

| Screen | Phone 375 | Tablet 768 | Desktop 1440 |
| --- | --- | --- | --- |
| Shell, top-nav (Kopi Senja) | touch (40px) | gap: no sidebar or rail, menu opens a drawer; touch (28–32px) | ok |
| Shell, side-only | touch | gap: full 216px sidebar at 768 | ok |
| Dashboards (crm_overview, sales_overview) | touch | touch | ok |
| Lists × 8 models | touch (card list ok) | touch: 14px checkboxes, 16px sort buttons | ok |
| New form × 8 | touch | touch | ok |
| Document view × 8 | touch | touch | ok |
| Approvals, Proses, Ubah | touch | touch | ok |
| Dialogs (confirm, form) | gap: centred box, not a sheet | ok | ok |
| Command menu, chat drawer | ok (full width) | ok | ok |
| Kanban, report, group views | — | — | — |
| Sign-in | ok | ok | ok |

### Work screens and POS (`web/app` work shell, `counter.js`)

| Screen | Phone 375 | Tablet portrait 834 | Tablet landscape 1194 | Desktop 1280 |
| --- | --- | --- | --- | --- |
| Work shell navigation | gap: tab tiles have no icon | gap: bottom tab bar instead of the rail | gap: rail tiles have no icon, labels cut ("Kasir tan…") | same as landscape |
| Counter: open session | touch | touch | touch | ok |
| Counter: catalogue | touch: search 40, order bar button 40, account 40 | touch | touch: Tutup sesi 24px, steppers 32px, account 32px | ok |
| Counter: order sheet / panel | touch: steppers 32×40, close 36 | touch | touch | ok |
| Counter: pay | touch: Kembali, tender inputs 40 | touch: Kembali 32, Uang pas 40 | touch: Kembali 32 | ok |

### Console (`web/console`, 22 screens)

| Screen | Phone 375 | Tablet 768 | Desktop 1440 |
| --- | --- | --- | --- |
| Shell | touch | gap: full 216px sidebar at 768 leaves 552px; touch | ok |
| Ringkasan, system, connectors, orgs, apps, processes, appearance, audit | touch | touch | ok |
| Model detail tables (fields, views, rules) | touch | gap: table cut at the right edge inside its box, no sticky first column | ok |
| Change set detail | gap: lifecycle steps cut at the right ("D…") | touch | ok |

### Design system gallery (`web/design-system/gallery.html`)

Already checked by `test_gallery.py` at several widths (40px rule on phones). Moves to 44px with the fixes below.

## Proposed order

1. **Design system, shared (most screens at once).** `touch` size on phones and coarse tablets (buttons, icon
   buttons, fields, nav links, sort headers, checkbox hit areas, steppers); breakpoints consolidated to 767 / 1023 /
   1279; `Rangka.breakpoint()` + `useBreakpoint()`; Dialog as a bottom sheet on phones; Table sticky first column
   when it scrolls; Sidebar icon rail at tablet portrait. Gallery test moves to 44px.
2. **Renderer (`web/app`).** Shells use the rail at tablet portrait; work shell: rail on tablets, icons on rail and
   tab tiles (fallback icon per screen type), labels that fit; counter: tablet portrait gets the rail, 44px steppers,
   Tutup sesi, Kembali, account; kanban/report/group views audited.
3. **Console.** Rail at tablet portrait, change-set lifecycle steps wrap on phones, model tables with a sticky
   first column.
4. **Metadata (needs a contract).** `layout.rk` could declare `responsive: {tablet: rail | sidebar | drawer, phone:
   drawer | tabs}`. That changes `schema/0.4/layout.json` (both validators, rangka-apps), so it starts as a proposal
   in `docs/contracts/`; the defaults above hold until then.
5. **Verify.** Re-run the audit at 375 / 768 / 1440 in light and dark; `test_console.py` gains 768; `test_screens.py`
   `test_counter_fits_phone_and_tablet` (added, failing until step 2) passes; `make -C kernel test`.
