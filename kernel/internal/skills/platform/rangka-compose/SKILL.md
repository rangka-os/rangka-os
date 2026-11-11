---
name: rangka-compose
description: "Find out what Rangka can do for a business and add or remove apps on a site: read the live catalogue (installed, available, not in the plan), explain which data a new app shares with the apps already there, and propose the config.rk change. Use when an owner asks for a capability (\"mau pakai kasir\", \"bisa buat sewa alat?\", \"tambahin POS\"), asks what their system can do, or when nothing they ask for seems to exist."
metadata: {rangka: {version: 0.5.0, writes: [config], requires: [rangka-concepts, rangka-changesets]}}
---

# Composing a Rangka site

## 1. Read the catalogue first, every time

Never name an app, module or connector from memory: the catalogue changes with every release.

- MCP: `rangka_catalogue`.
- In a checkout: `rangka catalogue --json --apps <apps dir> --site <site dir>` (without `--site`, nothing counts as
  installed: that is the view before a business exists).

It returns:

- `apps[]`: `name`, `title`, `summary`, `state`, `modules`, `shares`, `adds`.
  - `state` is `installed`, `available`, or `not_in_plan` (the site's plan does not allow it).
  - `shares`: modules the app uses that are already installed. Their data is shared with the apps already there.
  - `adds`: modules installing it would add.
- `modules[]`: `name`, `title`, `state`, `installed_by` (the installed apps that bring it in).
- `connectors[]` and `connectors_run`. When `connectors_run` is false, this kernel does not run connectors yet: a
  connector can be named, not used. Say so.

## 2. Match the request

Match by the app's title and summary and by the owner's words (see `rangka-concepts`, "Words owners use"). Then:

| Catalogue says | Do |
| --- | --- |
| an `installed` app already covers it | Say where it is in their system (menu or screen). Change nothing. |
| an `available` app covers it | Explain it (step 3), then propose adding it (step 4). |
| a `not_in_plan` app covers it | Say it exists and their plan does not include it. Do not propose it. Tell them to change the plan with Juragan (an operator does it). |
| nothing covers it, in the studio | Say which part nothing covers, then design it as a `custom` piece on the map (`rangka-consult`). Money and stock records stay in core modules. |
| no app covers it, but the modules it needs are installed or available, in the console | Compose a custom app from those modules (step 4, "Custom app"). Do not send the owner to an operator. |
| nothing covers it, in the console | Say which part nothing covers. Draft company pieces (site `models/x_*`, `flows/`, overlays; 0016, 0033) only once contract 0033 is accepted; until then name the gap plainly and change nothing. |

Only two gaps are real in either place: a system with no usable API, and edge hardware with no edge module. Never
draft a fake module or model, and never name an app that is neither in the catalogue nor created in the same change
set. A custom app made only of real modules is not fake.

## 3. Explain what it shares before proposing

Owners fear a second, separate system. Use `shares` and `adds` to answer that in plain words:

> Rangka Kasir pakai barang, stok, outlet dan pengguna yang sama dengan F&B Cabang. Yang baru cuma modul kasir
> (sesi kas, struk, pembayaran tunai dan QRIS). Jadi penjualan di kasir langsung mengurangi stok yang sama.

Translate module names into the owner's words; do not list raw module names unless asked.

If the app has its own skill (`rangka-app-<name>`, for example `rangka-app-kasir`), load it now: it says what to ask
and what to set up after adding the app.

## 4. Propose the change

Adding or removing an app is one patch on `config.rk`, judged medium risk by the kernel. Follow `rangka-changesets`.

1. Read `config.rk` (`rangka_get_file {path: "config.rk"}`). Note the position of `apps`.
2. Propose:

   ```json
   {"title": "Tambah Rangka Kasir", "reason": "Pemilik minta kasir untuk outlet",
    "ops": [{"op": "patch", "path": "config.rk", "patch": [{"op": "add", "path": "/apps/-", "value": "kasir"}]}]}
   ```

   If `config.rk` has no `apps` key, the site runs its plan's apps: add `/apps` with the plan's apps plus the new one,
   so nothing that runs today disappears.
3. Validate and preview. `app_not_in_plan` means the catalogue's `not_in_plan` was right: drop the change set and
   tell the owner (step 2's table).
4. Tell the owner what you prepared, never that it is done (`rangka-changesets`, "Telling the owner").

Custom app (step 2: no catalogue app covers it, the modules do). An app is only `apps/<name>/app.rk` composing modules,
so one change set creates it and lists it:

```json
{"title": "Tambah aplikasi Kasir", "reason": "Pemilik minta kasir; katalog tidak punya aplikasinya",
 "ops": [
  {"op": "create", "path": "apps/kasir_toko/app.rk", "content": "name: kasir_toko\nversion: 1.0.0\ntitle: {id: Kasir, en: Cashier}\nmodules: {pos: ^1.0, stock: ^1.3}\nmenu:\n  - group: {id: Kasir, en: Cashier}\n    items: [pos_sale, pos_shift]\nhome: {model: pos_sale}\n"},
  {"op": "patch", "path": "config.rk", "patch": [{"op": "add", "path": "/apps/-", "value": "kasir_toko"}]}]}
```

- Use a snake_case name that no catalogue app has.
- List only real modules, each with `^major.minor` read from its `modules/<m>/module.rk`. Menu items and `home` are
  models of those modules.
- `unknown_app` means the app.rk is missing from the change set. `app_not_in_plan` works as above.

Removing an app: only when the owner asks explicitly. Modules other installed apps still use stay installed; data is
never deleted, but the owner must hear that its screens disappear.

## 5. When nothing fits

Say what exists instead and what you cannot do yet. Examples of honest answers:

- Orders from GoFood, GrabFood or ShopeeFood: no connector in the catalogue covers them. Say so; if `outlet_sales` (outlet
  sales) is installed, offer recording each day's sales in it, or one of its sales-file imports; check which imports
  exist with `rangka_meta` before naming one.
- A device at the outlet: rangka-edge runs offline at the outlet (0003); check `rangka_catalogue` for an edge module
  before offering it.

Never promise a date for a missing capability.
