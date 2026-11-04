# 0017 Default shell: side-only

Status: proposed (4 Nov 2026) · Owner of the change: rangka-os (schema, kernel, web app) · Affects: nothing in rangka-apps

## Context

The Rangka UI redesign moves the app to a Frappe-style sidebar layout. Today the layout schema defaults `shell` to
`top-nav` (described as "Atlassian style"), and the kernel's no-layout fallback in `/api/v1/meta` returns
`{"name": "side_only", "shell": "side"}`. `side` is not in the schema's `shell` enum, so the fallback is not a valid
layout. The web app does not yet render `layout.shell`.

## Decision

1. **Schema default.** In `layout.json`, `shell` defaults to `side-only` instead of `top-nav`, and `extends` defaults to
   `rangka/side-only` instead of `rangka/top-nav`. The enum is unchanged: `top-nav`, `side-only`, `pos`, `portal`.
   The live schema is `schema/0.5` (the kernel embeds it); `schema/0.4` keeps only `app.json` for legacy `app.rk`
   and a `layout.json` copy. The same edit is made in both.
2. **Description.** The layout description changes from "top-nav (Atlassian style)" to "side-only (Frappe-style
   sidebar, default)". The other templates keep their descriptions.
3. **`top-nav` stays valid.** Layouts that name it keep working and render as before.
4. **Kernel fallback.** When a site has no layout, `/api/v1/meta` returns `{"name": "side_only", "shell": "side-only"}`
   (today `"side"`, which is not in the enum).
5. **Web app.** `web/app` renders `layout.shell`: `side-only` and `top-nav`. `pos` and `portal` fall back to
   `side-only` until project 2.

## Contract changes (rangka-os)

- `schema/0.5/layout.json` and `schema/0.4/layout.json`: `shell.default`, `extends.default` and the top-level
  description, identical in both. `make sync-schemas` copies `schema/0.5` into `kernel/internal/schema/0.5`.
- `kernel/internal/api/meta.go`: the no-layout fallback value.
- `web/app`: shell selection from `layout.shell`.
- Validator (Go and Python): no new codes; reports stay identical (`make compare-python`).

## Open

- The `pos` and `portal` shells (project 2).
