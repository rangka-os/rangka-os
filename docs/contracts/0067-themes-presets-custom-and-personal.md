# 0067 Themes: presets, custom colours, font, and a personal pick

Status: proposed (18 Nov 2026) · Owner of the change: rangka-os (theme schema, `theme` package, templates, `/meta`,
new theme endpoints, web app and Console) · Affects: rangka-cloud (Konsol › Tampilan, account appearance, plan
flag), rangka-ai (none: a platform skill), rangka-apps (sites that name `rangka/clinic` or `rangka/retail`) ·
Builds on: contracts.md decisions 12 (dark derivation) and 13 (`themes/<t>.rk` paths), the `low_contrast` check,
0045 §2 (`features.rk` is not proposable by an assistant), 0057 (plans in `features.rk`)

Design: FigCraft `design/figcraft/Rangka OS.figcraft` › page "Rangka Cloud" › board "Tema & warna · usulan"
(sections A–F). Decisions by Dede on 18 Nov 2026 are in section F of the board and below.

## Context

- **The kernel already computes themes.** `site/themes/<t>.rk` extends a template (`rangka/standard`, `rangka/retail`,
  `rangka/clinic`), overrides tokens by name, gets dark values derived (decision 12) and is contrast-checked.
  `GET /api/v1/meta` returns `theme: {name, mode, tokens: {light, dark}, overrides, radius, density, font, logo}`.
  A theme change is a low-risk change set and Cloud may approve overlay paths (`themes/*.rk`) for the owner.
- **The web app ignores the colours.** `web/app/shell.js` reads only `theme.mode` and `theme.density`. Colours come
  from the static `tokens.css` plus `brand-juragan.css` (`<html data-brand="juragan">`). A site theme changes the
  Console swatches but not what people see.
- **Overriding a theme means knowing token names.** There is no way to say "my colour is #7a4520" and get a
  consistent, readable set, and no gallery to pick from.
- **Light/dark is a personal pick, colours are not.** The app keeps the mode in `localStorage` (`rk.app.theme`);
  Cloud keeps it in `users.theme` (`PUT /api/akun/tampilan`). Neither knows a colour theme.
- **`theme.font` exists in the schema** but nothing loads a font, and arbitrary family names would need fonts that are
  not installed (and the PWA must work offline).
- **Plans have `flags`** (schema 0.5 `features.json`), unused so far.

## Decision

Theme (the colours, font, corners, density) and mode (light, dark, follow the system) are two separate choices.
Every theme has a light and a dark version.

### 1. Theme schema 0.5: one colour is enough

`theme.json` gains three optional keys:

| Key | Values | Meaning |
| --- | --- | --- |
| `accent` | `#rrggbb` | The business colour. Derives `brand`, `selected`, `row-selected`, `selected-ink`, `link`, `focus`, `chart-1` (§2) |
| `button` | `ink` (default) \| `accent` | Primary buttons stay ink, or take the accent (derived `primary`, `primary-hover`, `primary-ink`) |
| `neutral` | `stone` (default) \| `slate` \| `gray` | The neutral family: `app`, `surface`, `soft`, `ink`, `ink-2`, `muted`, `faint`, `line`, `line-strong`, `border`, `hover`, `neutral*`, `disabled-bg` |

- Order of application: template (`extends`) → `neutral` → `accent`/`button` → `color` (per token, as today). So
  `color` still wins for the designer who wants one token different.
- `font.sans` and `font.display` take a name from the bundled list (§5); `font.mono` stays as is.
- **Locked tokens.** `color` may not set `ok*`, `warn*`, `bad*`, `on-bad`, `on-warn`, `info*`, `discovery*`, `raka*`,
  `spark`, `helmet`, `scrim`, `mascot-ink`: status colours keep their meaning and Zafdoz keeps the look its base gives it.
  The validator reports `locked_token` (error). Danger buttons stay `bad-solid`.

### 2. Derivation (code, not an LLM)

`theme.Derive(accent, neutral, button) → (light, dark map[string]string)` in Go, with the same arithmetic in
`tools/rangka_validate.py` (`make compare-python` must stay identical):

- Light: `brand` = accent darkened until 3:1 on `surface` (so a very light logo colour still reads; the logo image
  itself is untouched); `selected` = HLS(h, 94 %, min(s, 60 %)); `row-selected` = HLS(h, 97.5 %, min(s, 50 %));
  `selected-ink` = accent darkened in 1 % lightness steps until 4.5:1 on `selected`; `link` likewise on `surface`;
  `focus` and `chart-1` until 3:1 on `surface`; `primary` = `ink` or accent darkened until `primary-ink` (white)
  reaches 4.5:1.
- Dark: `brand` = lightness + 15 %, lightened until 3:1 on `surface`; `selected` = HLS(h, 18 %, min(s, 45 %)); `row-selected` = HLS(h, 14 %,
  min(s, 35 %)); `selected-ink`, `link` start at 60 % lightness and lighten until 4.5:1 on their background; `focus`,
  `chart-1` start at 55 % until 3:1; `primary` = dark `ink`, or the accent lightened until 3:1 on `surface`, with
  `primary-ink` = dark `app`.
- Derived values always pass the contrast floors (kernel test: 300 random accents × both buttons × three neutrals). Hand-set `color` values that fail stay `low_contrast` errors
  (unless `contrast_check: false`, as today).

### 3. Bundled themes

`templates/themes/` ships six, all passing every floor in light and dark:

| Name | Label | accent | button | neutral |
| --- | --- | --- | --- | --- |
| `standard` | Standar | (today's tokens, unchanged) | ink | stone |
| `laut` | Laut | `#0c66e4` | accent | slate |
| `daun` | Daun | `#1f845a` | accent | gray |
| `senja` | Senja | `#e56910` | accent | stone |
| `anggur` | Anggur | `#6e5dc6` | ink | gray |
| `arang` | Arang | `#44403c` | ink | gray |

`rangka/clinic` and `rangka/retail` stay loadable as aliases of `rangka/daun` and `rangka/senja` (validator warning
`renamed_template`); rangka-apps moves its sites over in its own PR.

### 4. One business theme, plus a personal pick

- **Business.** `config.theme` names the business theme, as today. One per business (per kernel); outlets have none.
  It changes through a change set on `themes/<t>.rk` and/or `config.rk` (`theme:` key): low risk (contracts.md
  decision 3), approved from previewed by `config.ai.approvers` (default the owner). Cloud Konsol › Tampilan is for the
  owner. Zafdoz may propose, never approve or apply.
- **Saved themes.** A business may keep several `themes/*.rk` (e.g. its own "Kopi Senja" next to the presets); only
  `config.theme` is the default.
- **Person.** Each member may pick, for themselves only, a theme from the list (§6: bundled + the site's saved themes)
  or "follow the business" (default), and a mode (`system`, `light`, `dark`). No approval, no change set.
  Stored in the kernel per user: table `rk_user_appearance (user_id pk, theme text null, mode text not null default
  'system', updated_at)`. `/meta.theme.personal.mode` is null until the person saves one, so the device's own pick
  and the business default still apply. The app caches it in `localStorage` for the first paint and drops `rk.app.theme` after
  reading it once (migration).
- A person cannot create or edit a theme; that stays the owner's business theme work.

### 5. Fonts

A fixed list: `system` (default, today's stack), `inter`, `plus-jakarta-sans`, `nunito-sans`, `ibm-plex-sans`,
`source-serif-4` (display only). The app loads a theme's font the way it loads its own fonts today (Google Fonts,
cached for offline use by `sw.js`); only the families a theme uses are loaded. `font.sans` / `font.display` outside
the list: validator error `unknown_font`.

### 6. API

| Method | Path | Body / result |
| --- | --- | --- |
| GET | `/api/v1/themes` | `{themes: [{name, label, source: bundled\|site, accent, button, neutral, swatches: {light: {...}, dark: {...}}, custom: bool}], current, fonts: [names], allowed: {custom: bool}}`. `swatches` holds `brand primary selected selected-ink link chart-1 app soft surface ink line-strong` |
| POST | `/api/v1/themes/derive` | `{accent, button?, neutral?, color?}` → `{light, dark, checks: [{pair, light, dark, floor, ok}]}`. Nothing is stored. Used by the editor and by Zafdoz |
| POST | `/api/v1/themes/from-image` | multipart image (logo, ≤ 2 MB) → `{colours: [#rrggbb × up to 5], suggested}`: k-means in Lab space on opaque pixels, near-white/near-black dropped unless they are all there is |
| GET | `/api/v1/me/appearance` | `{theme: name\|null, mode}` |
| PUT | `/api/v1/me/appearance` | same shape; `theme` must be in `/themes` or null. 400 `unknown_theme` |
| DELETE | `/api/v1/me/appearance` | forget the person's pick (204) |

- `/api/v1/meta.theme` adds `personal: {theme, mode}` for the signed-in user and `effective` = the theme actually
  shown (personal pick, else business), with its `tokens` and `overrides`. Existing keys keep their meaning
  (the business theme).
- Open apps already get `event: changeset {id, status}` on `/api/v1/events`. On `applied` or `reverted` they reload
  `/meta` and repaint the theme without a page reload; the same on `PUT /me/appearance` in another tab
  (`event: appearance {user_id}`, new, sent to that user's connections only).

### 7. Plan flag

`features.rk` plan flag `custom_theme` gates **creating or changing** a site theme of the business's own: one with
`accent`, `color`, or a font other than `system` (the "Buat tema sendiri" editor, logo colours, per-token edits).
Only a plan whose resolved flags say `custom_theme: false` refuses it; a site without `features.rk`, without a chosen
plan, or whose plans never name the flag allows it (self-host and existing sites keep working). Refused at validate:
`plan_limit` on the theme file, message "Tema buatan sendiri … mulai paket Zafdoz Pro". Pointing `config.theme` at
any theme, a theme that only `extends` a bundled one, and `neutral`, `button`, `mode`, `radius`, `density`, `logo`
are always allowed. Downgrade never deletes: an existing custom theme keeps working and can be switched away from,
not edited. Personal picks are never gated. Cloud writes `custom_theme: false` on `cloud_mulai` and `cloud_juragan`;
`cloud_pro` ("Zafdoz Pro") and Grup leave it on. `GET /api/v1/plan` reports the plan's `flags`.

### 8. Web app and Console

- The app applies `meta.theme.effective` on top of the base look: each overridden token becomes an inline custom
  property on `<html>` for the current mode (inline beats `:root[data-brand=…]`), so tokens a theme leaves alone keep
  the Juragan or Rangka base. Font, `radius` (`data-radius`) and `density` follow.
- Account menu › Tampilan: "Tema" (follow the business + list) and "Mode" (system, light, dark), saved through
  `/me/appearance`.
- ⚙ Pengaturan sistem › Tampilan (owner): the gallery, try-before-apply preview, the custom editor (gated),
  mode default, corners, density, font, logo. The same screen as Cloud Konsol › Tampilan.

### 9. Rangka Cloud

- Konsol › Sistem › Tampilan (owner): `GET/PUT /api/konsol/businesses/{id}/appearance` and `POST …/appearance/derive`
  call the business kernel (`/themes`, `/themes/derive`, files and change sets) with the owner's kernel session.
  `PUT {theme}` switches, `PUT {custom: {label, accent, button, neutral, font}}` writes `themes/<slug>.rk`; both are
  one change set, approved and applied with the owner's session, kept in the business's Riwayat. A kernel without
  this contract answers 404: the Konsol says the business needs an update. Cloud stores no copy of a theme.
- Package tiers gain `custom_theme` (true from Zafdoz Pro, like `custom_domain`); without it the `cloud_<tier>` plan in
  `features.rk` carries `flags: {custom_theme: false}`.
- Cloud's own screens keep the Juragan look; the account's light/dark pick (`users.theme`) stays as it is. A person's
  own colour theme lives in each business kernel (§4). Business tiles in Bisnismu taking the business `brand` is left
  for later.

### 10. Rangka AI

No new tool. Zafdoz changes a theme with `draft_changeset` like any other layer-6 change, guided by the platform skill
`rangka-themes` (bundled themes, `accent`/`button`/`neutral`/`font`, locked tokens, the plan note). The kernel does the
colour work and the checks: `locked_token`, `unknown_font`, `low_contrast` and `plan_limit` come back as validator
errors Zafdoz already repairs from or explains. Zafdoz never approves or applies.

## Consequences

- The colours an owner picks finally reach the screen; every page and Zafdoz's panel follow one set of tokens.
- Owners on any plan get six readable themes and a personal pick; Zafdoz Pro and up can match their own brand
  without knowing token names, and contrast stays guaranteed by code.
- New kernel table and endpoints; Go/Python parity for `Derive`.
- rangka-apps renames two template references (aliases keep old sites loading meanwhile).

## Implementation order

1. rangka-os: schema + `Derive` + Python parity + templates + validator codes (`locked_token`, `unknown_font`,
   `renamed_template`, `plan_limit/custom_theme`).
2. rangka-os: endpoints, `rk_user_appearance`, `/meta` additions, SSE repaint.
3. rangka-os: web app applies tokens, account Tampilan, ⚙ Pengaturan sistem › Tampilan; fonts.
4. rangka-cloud: Konsol › Tampilan and the `custom_theme` tier flag.
5. rangka-os: the `rangka-themes` platform skill.
