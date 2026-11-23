---
name: rangka-themes
description: "How to change how a Rangka business looks: pick a bundled theme, make one from the owner's colour or logo, choose the button style, neutral family and font, and what a theme may never change. Use whenever the owner asks about colours, theme, logo colours, font, dark mode or 'tampilan' (contract 0067)."
metadata: {rangka: {version: 0.5.0, writes: [config, themes]}}
---

# Rangka themes

A business has one theme (`config.rk` `theme:`), a file `themes/<name>.rk` or a bundled template. Every theme has a
light and a dark version; light or dark is each person's own pick (their account menu), not the business's.

## Bundled themes

`standard` (Standar: ink buttons, Bata selection), `laut` (blue, cool slate), `daun` (green, for clinics),
`senja` (orange, for shops), `anggur` (purple selection, ink buttons), `arang` (black and white). Switching is one
patch on `config.rk`: `{op: replace, path: /theme, value: laut}` (`add` when config.rk has no theme). Every plan may
switch.

## A theme of the business's own

Write the owner's colour, not tokens. The kernel derives selection, links, focus, charts and the dark version from
it and moves each until it is readable (4.5:1 text, 3:1 controls). Never compute colours yourself.

```yaml
# themes/kopi_senja.rk
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/theme.json
name: kopi_senja
label: {id: Kopi Senja}
extends: rangka/standard
accent: '#7a4520'        # quoted: one colour is enough
button: accent           # ink (default) | accent
neutral: stone           # stone (warm, default) | slate (cool) | gray
font: {sans: plus-jakarta-sans}   # system | inter | plus-jakarta-sans | nunito-sans | ibm-plex-sans
```

Then point `config.rk` at it in the same change set. Colours from a logo: the owner uploads the logo in
Pengaturan › Tampilan (the kernel computes them, `POST /api/v1/themes/from-image`); when they give you a hex, use it.

- Only on plans that allow it: `plan_limit` with "mulai paket Zafdoz Pro" means the plan has `custom_theme: false`.
  Offer the bundled themes instead and say why. Never edit `features.rk`.
- `color:` (one token at a time) is for designers who ask for a specific token; it is checked, not adjusted.
- **Locked:** status colours (`ok`, `warn`, `bad`, `info`, `discovery` and their variants), `raka*`, `spark`,
  `helmet`, `scrim`, `mascot-ink`. The validator refuses them (`locked_token`): they keep their meaning in every theme.

## Tell the owner

A theme change is low risk. Say it is proposed and waiting for them; they see it in the preview, and it applies for
everyone when they approve. Their team's light/dark pick stays theirs.
