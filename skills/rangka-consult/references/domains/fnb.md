# F&B (`industry: fnb`)

Food and drink businesses: kedai kopi, kafe, warung and rumah makan, bakery, frozen food and catering kitchens,
multi-branch restaurant chains, cloud kitchens selling mostly through food delivery apps. Money moves every day in
small amounts; the risks are stock (ingredients that spoil or go missing), cash at the counter and food cost (HPP).

## How it usually runs

| Stage | What happens | Who | Documents the owner names |
| --- | --- | --- | --- |
| Purchasing | Buy ingredients from suppliers or the market, often daily | Owner, kepala dapur, purchasing | Nota belanja, PO, faktur pemasok |
| Central kitchen (multi-branch) | Prepare semi-finished goods, send to outlets | Dapur pusat | Surat kirim, transfer stok |
| Selling at the counter | Orders rung up, paid cash or QRIS | Kasir | Struk, catatan kasir |
| Kitchen | Orders go to bar or hot kitchen; marked ready | Dapur, barista | Tiket pesanan |
| Delivery apps | Orders from GoFood, GrabFood, ShopeeFood on a separate tablet | Kasir | Laporan mitra, pencairan |
| Closing | Cash counted, takings deposited, daily sales recap | Kasir, kepala outlet | Laporan harian, setoran |
| Stock | Count (opname) weekly or monthly; waste noted | Kepala outlet | Form opname |
| Staff | Shift attendance, pay, kasbon | Kepala outlet, owner | Absen, slip gaji |
| Reporting | Sales per outlet, HPP, best sellers | Owner | Rekap harian, laba rugi |

**Size bands:** single kedai: `staff: to_5`, `scale: one_location`. Growing chains: `scale: branches_2_5` or
`branches_over_5`, `staff: to_20` to `to_100`, `customers: over_1000`. Count outlets exactly: it drives the plan
(contract 0054 §5).

## Words owners use → Rangka

| Owner says | Rangka concept |
| --- | --- |
| outlet, cabang, gerai | `outlet` |
| menu, produk, bahan baku | `item` (catalogue), with `uom_conversion` for gram, karung, pack |
| resep, takaran | `recipe` (stock): selling a menu item takes ingredients out |
| kasir, POS | `pos_sale`, `pos_shift`, `pos_shift_close` (app `kasir`, screen `kasir`) |
| tutup kasir, setoran | `pos_shift_close`; daily `outlet_takings` |
| rekap penjualan harian | `outlet_sales` (from the cashier, a daily form, a POS file, Moka or Loyverse) |
| refund, retur di kasir | `pos_refund`, `outlet_return` |
| stok, opname, kirim ke cabang | `stock_balance`, `stock_count`, `stock_transfer` + `stock_transfer_receipt` |
| waste, bahan rusak | `stock_adjustment` |
| belanja, PO, faktur pemasok | `purchase_request`, `purchase_order`, `goods_receipt`, `vendor_bill` |
| tiket dapur, bar | `kitchen_ticket`, `kitchen_station`, `kitchen_route` (app `dapur`) |
| promo, voucher, beli 1 gratis 1 | `promotion`, `promo_code` (module `promotions`) |
| absen karyawan outlet | `staff_attendance` (app `absen_karyawan`, selfie and GPS) |

## Common pains and the question that uncovers them

| Pain (owner's words) | 0061 code | Question to ask |
| --- | --- | --- |
| "Rekap penjualan dan setoran tiap outlet masih manual" | `sales_daily` | "Laporan harian dari outlet sampai ke kamu lewat apa?" |
| "Stok di catatan beda dengan di rak" | `product_stock` | "Opname dilakukan seberapa sering, dan selisihnya diapakan?" |
| "Kiriman bahan ke cabang tidak tercatat" | `product_transfer` | "Dari dapur pusat ke cabang, ada surat kirim atau cuma chat?" |
| "Pemakaian bahan baku tidak ketahuan" | `product_recipe` | "Satu porsi menu andalanmu pakai bahan apa saja, ditakar?" |
| "Harga menu beda-beda antar cabang" | `product_prices` | "Harga menu sama di semua cabang, atau ada yang beda?" |
| "Hitung gaji dan kasbon manual" | `staff_pay`, `staff_advance` | "Karyawan outlet digaji bulanan, mingguan, atau harian?" |
| "Karyawan titip absen" | none | "Absen karyawan sekarang pakai apa?" |
| "Pesanan ojol tidak masuk ke laporan" | none | "Pesanan dari aplikasi ojol direkap ke laporan harian, atau terpisah?" |

## What covers it

- **One app per role** (consult §4): `kasir` at the counter (load `rangka-app-kasir` first: it decides whether the
  cashier goes on the map), `dapur` for kitchen tickets, `fnb_cabang` for head office (daily sales, stock, transfers,
  opname, recipes, purchasing, books), `absen_karyawan` for outlet attendance. Smaller cuts: `penjualan_harian` (daily
  sales only), `stok_outlet` (stock only), `pembelian_pemasok` (purchasing only). Demo sites: `demo-fnb`
  (`fnb_cabang` + `absen_karyawan`), `demo-kasir`.
- **Owner keeps their cashier:** sales come in through `outlet_sales` from Moka (connector `moka`), Loyverse
  (connector `loyverse`), a POS file, or the daily form. Do not place `kasir` then.
- **Add-ons:** `promotions`; `whatsapp_receipt` (receipt to the buyer's WhatsApp) and `whatsapp_order_ready` (pickup
  message when the kitchen marks the order ready); `crm` only when the story shows members or regular customers.
- **Payroll for outlet staff:** `payroll` needs `workforce`, `projects` and `invoicing` (0061 §1). Mention that it
  brings more than the owner asked for, or design a lighter custom piece when they only need a monthly wage plus kasbon.
- **Usually custom:** catering orders with DP and delivery date; central kitchen production batches; waste log with
  reasons; supplier price comparison; table reservations.
- **Delivery apps:** no connector exists for GoFood, GrabFood or ShopeeFood. Their sales go into the daily sales recap
  (form or file). Say: "Pesanan dari ojol belum bisa masuk otomatis, jadi direkap harian dulu."

## KPIs the owner watches

- Sales per outlet per day, average ticket, best and worst sellers.
- Food cost (HPP) vs. menu price; recipe-based usage vs. counted stock (selisih opname).
- Cash differences at shift close; takings deposited vs. sales.
- Stock value and waste per outlet; purchases per supplier.
- Staff cost vs. sales, lateness.

## Money, stock and compliance hotspots

- **Stock is moved only by documents**: sale, transfer, receipt, count, adjustment. Never by editing a balance.
  Negative stock alerts exist (`stock_negative_alert`); how negative sales are handled is the owner's call.
- **Cash at the counter**: shifts open and close per outlet; differences are recorded at close, not hidden.
- **QRIS is recorded, not processed** by the cashier: it records the payment and its reference.
- **Recipes drive HPP**: wrong takaran gives wrong food cost. Ask for one real recipe before mapping recipes.
- **Taxes on restaurants** (regional restaurant tax vs. PPN) depend on the region and status: verify with a tax
  consultant; never state a rate.

## Red flags: not a fit yet

- Outlets with unstable internet that must keep selling offline: the cashier runs online only; offline selling,
  receipt printers and cash drawers come later with Rangka Edge (`edge` piece without `ref`, "belum tersedia").
- Businesses where most orders come from delivery apps and the owner expects them to flow in order by order: no
  connector yet; only a daily recap.
- Large franchise networks with royalty billing to franchisees: royalties are custom; check volume before mapping.
- Table service with split bills, course firing and table maps as the main need: confirm with the kasir skill what the
  cashier does before promising it.
