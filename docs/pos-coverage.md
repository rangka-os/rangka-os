# POS coverage matrix

Status: draft for review (8 Nov 2026). Reference design: `rangka.pen`, frame `sL99a` "Rangka POS · Tablet & HP"
(32 screens, sample data Kopi Teras, outlet Kemang). Measured against rangka-os `main` at a5d5f89 (contract 0030,
WS1–WS3 merged) and rangka-apps `main` at cd7c624 (`modules/pos`, app `kasir`).

## What already exists

- **Contract 0030 work screens.** Kind `screen`, app `work:`, home per role, work shell, `Idempotency-Key` on every
  write, `_occurred_at`, IndexedDB outbox (`offline: queue`), cached screen and catalogue.
- **`counter` renderer** (`web/app/counter.js`, WS3): catalogue grid with search, barcode on Enter and group tabs;
  cart with qty and a note per line; hold bills (browser `localStorage`); tender with quick cash, full or partial QRIS
  and change; session open and blind close (`MiniForm`); receipt print through the print format; "Kirim ulang" for a
  sale whose answer was lost.
- **rangka-apps `modules/pos`:** `pos_sale` (per-outlet numbering, line and bill discount, cash/QRIS payments,
  computed change, stock moves on submit, reverse on cancel), `pos_shift`, `pos_shift_close` (blind count),
  `pos_receipt` print format, screen `kasir`. `outlet_sales` has `pos_refund`. `stock` has `recipe`.
- **Design system:** `PosKeypad` (keypad, offline pill, QRIS pay) is in `web/design-system`, but the web app's
  counter does not use it.
- **Kernel:** approvals (0007) for supervisor sign-off, print formats with a receipt width (0008), per-outlet number
  series (0009), row scope per outlet (0010), stock moves (0021), list aggregates (0018), dashboards.
- **Edge:** contracts 0003 and 0006 (`edge-sync/v1`), `rangka-edge` with queue and sync; printer, drawer and PIN are
  planned, not built.
- **Not used for POS:** `templates/layouts/pos.rk` (`shell: pos`) is retired by 0030 §3. The old `web/prototype`
  renderer is no longer on `main`.

## Legend

- **Have:** works today and matches the design closely enough.
- **Part:** something exists, but the design needs more.
- **Gap:** nothing yet.
- **Where:** DS = design system, R = web app renderer, S = schema / `.rk` (needs a contract), K = kernel,
  M = rangka-apps module (other owner), E = rangka-edge, C = connector.

## Matrix

| # | Design screen / interaction | Today | Status | Gap | Where |
|---|---|---|---|---|---|
| **Shell** | | | | | |
| 1 | Rail (tablet): Kasir, Meja, Pesanan, Online, Dapur, Shift, Produk, Laporan + badges | Work shell: bottom bar with up to 5 screens + "Lainnya" | Part | Left rail on tablet, badge counts per screen (open orders, kitchen) | R, S (badge source) |
| 2 | Status bar HP: outlet, online/offline, cashier | Header shows "N antre" | Part | Outlet + sync pill + cashier chip in the header | R |
| 3 | Tab bar HP (Kasir, Pesanan, Meja, Laporan, Lainnya) | Bottom bar | Have | — | — |
| **Sign-in and shift** | | | | | |
| 4 | Masuk PIN: pick a staff member, 4-digit PIN | Normal sign-in per user (0030 §8, phase 1) | Gap | PIN unlock needs a paired device (0006 §2) or a kernel "device session + staff PIN" | K, E, S |
| 5 | Buka shift: opening cash with keypad, presets (Rp 300.000…), last shift's handover | `MiniForm` with `opening_cash` | Part | Money keypad (`PosKeypad`), presets, "previous shift" panel (read of the last closed shift) | DS, R, S (slot for presets / previous) |
| 6 | Ringkasan shift: sales, per method, refunds, expected cash, cash in/out | `pos_shift` form (hidden from cashier) | Part | Cash in/out model (`pos_cash_move`), shift summary panel on the screen | M, R |
| 7 | Tutup shift: count per denomination, difference, tolerance, where the cash goes (left / deposited) | Blind close, one `counted_cash` field | Part | Denomination counter widget that fills `counted_cash`; tolerance and split "ditinggal / disetor" fields | DS, R, M |
| **Selling** | | | | | |
| 8 | Catalogue: category chips, item cards with image/colour, price, qty badge in cart | Grid of text buttons, group tabs | Part | Card look, image slot, in-cart count badge | DS, R, S (`catalogue.image` is in 0030 but not rendered) |
| 9 | Stock badges "Sisa 4", "Habis" (item disabled) | None | Gap | Catalogue slot for available qty (from stock / recipe "bisa dibuat") | S, R, K (aggregate), M |
| 10 | Search + scan barcode | Search, Enter matches barcode | Have | Camera scan is WS5 | — |
| 11 | Pilih varian (modal): size (required, +price), sugar, ice, add-ons (multi, +price), note, qty | `item.variant_group/variant_label`, line `note` | Gap | Modifier groups on items and modifier lines on the sale line, priced by the model | M, S, R, DS |
| 12 | Order panel: order no., type (makan di sini / bawa pulang / online), table + guests, customer + points | Free-text "Pembeli atau meja" | Part | Order type select, table link, customer link with lookup | S (slots), R, M |
| 13 | Cart lines: qty, modifiers, note, amount | qty, note, amount | Part | Modifier lines under the item; swipe/delete | R |
| 14 | Totals: subtotal, promo, PB1 10 %, rounding, total — live before paying | Screen sums `qty × price` itself ("estimate") | Gap | Server-side dry compute so the screen shows the model's numbers without saving (screens never compute tax, 0030 §2) | K, S, R |
| 15 | Simpan (open bill) | Hold in `localStorage` per browser | Part | Open bills as server records (draft sale), visible on other devices and in "Pesanan" | R, S |
| 16 | Tablet portrait, phone catalogue + cart sheet | One layout | Gap | Responsive counter: portrait = cart as bottom bar; phone = catalogue + "Lihat pesanan" sheet | R |
| **Payment and receipt** | | | | | |
| 17 | Bayar tunai: amount keypad, "Uang pas", quick notes, change | Quick-cash buttons + input | Part | Keypad (`PosKeypad`), layout per design | DS, R |
| 18 | QRIS dynamic: QR on screen, countdown, auto status, "tampilkan di layar pelanggan" | QRIS = cashier types amount; no QR | Gap | Payment connector (Xendit/Midtrans), payment intent record, status webhook; static QRIS image as phase 1 | C, K (0002 runtime), M |
| 19 | Card (EDC), bank transfer | Only `cash`, `qris` | Gap | More `method` options in `pos_payment`; renderer offers every option of `methods` | M, R |
| 20 | Bayar terpisah: per item, equal split, per amount; parties paid one by one | Cash + QRIS split of one total | Gap | Split bill = several payments per sale with party labels, or child sales; design decision needed | S, M, R |
| 21 | Pembayaran berhasil: change, points earned, send receipt (print / WhatsApp / email / none) | Change + "Cetak struk" | Part | WhatsApp/email send (0002 notify), points | R, C, M |
| 22 | Struk 58 mm preview | `pos_receipt` print format | Have | ESC/POS through Edge later | E |
| 23 | Digital receipt (customer's phone, public link) | None | Gap | Public signed link to a print format (portal, 0019 Open) | K |
| **Tables and orders** | | | | | |
| 24 | Denah meja: areas, table states (empty, occupied + time + amount, bill requested, reserved, cleaning), side panel, move/merge/split | None | Gap | `table` model + area, sale links a table; new screen type `floor` (0030 says types are a closed set → contract change) | S, R, M |
| 25 | Riwayat pesanan: filters (open/paid/refund), detail with trail, reprint, send | Generic list of `pos_sale` | Part | A screen of type `queue` or a "history" view on the counter (`orders` slot) | R, S |
| 26 | Refund with supervisor PIN: pick items, reason, to the original method, stock not returned | `pos_refund` (outlet_sales), approvals (0007) | Part | Refund from the screen; supervisor approves on own phone (phase 1) or PIN on device (phase 2) | R, M, E |
| **Online and kitchen** | | | | | |
| 27 | Pesanan online: GoFood/GrabFood/ShopeeFood, accept/decline timer, pause all, auto-disable sold-out items | None | Gap | Marketplace connectors (0002), order inbox = `queue` screen | C, K, M |
| 28 | KDS: stations, tickets with age, done per item | `queue` type designed in 0030 (WS4, not built) | Gap | WS4 renderer; ticket per station via flow | R, M |
| 29 | Self-order QR (customer phone) | None | Gap | Customer-facing; 0030 keeps it out (portal) | K, R |
| **Back office on the device** | | | | | |
| 30 | Produk & stok: list with "bisa dibuat", price per order type, modifiers, recipe | `item`, `price_list`, `recipe` forms | Part | Price per order type (price list per channel), recipe-based availability | M |
| 31 | Pelanggan & poin: segments, member tiers, points, redeem | `contacts` only | Gap | Loyalty module | M |
| 32 | Promo: time-boxed discounts, bundles, buy-2-get-1, member discount, scheduled | Line % and bill discount by hand | Gap | Promo module evaluated by the model (CEL), not the screen | M, K? |
| 33 | Stok opname (HP): system vs counted, scan, save & next | `stock` count models; `roll` type designed (WS7) | Part | WS7 `roll` renderer | R |
| 34 | Laporan (tablet) and owner report (phone, all outlets) | Dashboards (`kasir_ringkas`), list aggregates | Part | Hour histogram, by order type; phone dashboard cards exist | M |
| **Devices and offline** | | | | | |
| 35 | Pengaturan perangkat: printers (BT/LAN), drawer, customer display, kitchen screens | Browser print dialog | Gap | Edge device connectors (0003 build order) | E, C |
| 36 | Tax & receipt settings: PB1 10 %, service 5 % dine-in only, rounding to Rp 100 | PPN only (`config.tax`) | Gap | PB1/PBJT and service charge as module computes; rounding field | M, S? |
| 37 | Security: supervisor PIN for refund/void and manual discount > 10 % | Approvals by role | Part | Rule "discount > 10 % needs approval" is a model rule + 0007; PIN is phase 2 | M, E |
| 38 | Offline banner: "Internet putus sejak 12.31", N transactions stored, dynamic QRIS off | Outbox + "N antre" + "data terakhir" | Part | Banner per design; hide QRIS dynamic when offline | R |

## Reading the matrix

- **The kernel side is mostly in place.** Idempotent writes, the outbox, sessions, numbering, stock and approvals all
  work. The two real kernel gaps for the cashier are #14 (a dry compute, so the screen shows tax, promo and
  rounding without computing them) and #4 (PIN on a shared device).
- **The renderer is the biggest gap.** It works, but it is plain. The design asks for a rail, cards, a keypad, a
  variant sheet, a breakdown, phone and portrait layouts, and a proper success screen (#1, 5, 8, 13, 16, 17, 21, 38).
- **Restaurant features are module work in rangka-apps** (other owner): modifiers, tables, PB1/service, loyalty,
  promo, cash in/out (#6, 11, 24, 30–32, 36).
- **Integrations come last:** dynamic QRIS, food delivery apps, WhatsApp receipts, printers and the drawer (#18, 21,
  27, 35). They depend on the connector runtime (0002) and Edge (0003/0006).

## Proposed slices

| Slice | Content | Contract | Repos |
|---|---|---|---|
| **A. Counter to design** (proposed first) | (1) Kernel: `POST /api/v1/r/{model}/_compute` runs defaults, computes and save rules on an unsaved record and writes nothing. (2) Screen slot `document.summary: [fields]`: the cart shows those fields from the dry compute, debounced, so the breakdown is the model's own numbers. (3) Renderer per the design: tablet rail, item cards with image and in-cart count, order panel with breakdown, tender with `PosKeypad` and quick cash, success panel, open-shift keypad, portrait and phone layouts, offline banner. (4) Denomination counter for the blind close. | New 0034 (compute endpoint + `summary` slot), both validators | rangka-os only |
| B. Modifiers | `item_modifier_group` / `item_modifier` models, a modifier child table on the sale line, slot `line.options`, variant sheet | 0034 or its own | rangka-apps (models) + rangka-os (slot, sheet) |
| C. Orders and tables | Open bills as server draft sales, `orders` view on the counter, `floor` screen type | New contract (adds a type) | both |
| D. Restaurant money | PB1/PBJT, service charge, rounding, more payment methods, split bill | Module work; split bill needs a decision | rangka-apps mostly |
| E. Kitchen | WS4 `queue` renderer + ticket flow | 0030 (already planned) | rangka-os + rangka-apps |
| F. Devices | ESC/POS print, drawer, PIN unlock through Edge | 0003, 0006 | rangka-edge + kernel |
| G. Integrations | Dynamic QRIS, GoFood/GrabFood/ShopeeFood, WhatsApp receipts | 0002 | rangka-connectors + kernel |

Slice A stays inside rangka-os, changes no money rule (the model still computes everything) and makes every later
slice look right, because B–E render inside the same counter.
