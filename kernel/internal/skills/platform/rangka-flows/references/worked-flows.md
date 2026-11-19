# Worked flows

Each file below passes `rangka validate` (kernel 0.5, rangka-apps main) on the example site named, with the file
added under the site directory. Businesses and numbers are made up. Each starts from the same question: does
something already do this?

## 1. Overdue invoice reminder (site with `faktur_piutang`, e.g. `demo-kepingan-keuangan`)

Owner: "Kalau faktur sudah telat dua minggu, salesman-nya yang harus nagih, bukan aku."

**Already there?** `invoicing/invoice_due_digest` tells owner and finance every morning about invoices due today and
newly overdue. It does not tell the salesman. So the site flow adds only that, and does not repeat the digest.

Choices:

- Scheduled, not event: nothing happens to the invoice on day 14; time passes.
- A window of 14 to 20 days plus a stable notice `id` per invoice: a missed morning is caught up, a rerun tells
  nobody twice.
- The salesman is a user record (`sales_invoice.salesman` links to `user`): `find` it into a variable and notify the
  variable. Notifying the field name `salesman` directly would collect the salesman of every invoice in every list
  variable.
- `channel: in_app`: a per-invoice notice; mail stays a personal setting.
- No customer phone or address in the text; the link opens the invoice.
- Nothing is written on the invoice: it is submitted and immutable.

`flows/x_tagih_telat_14.rk`:

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: x_tagih_telat_14
label: {id: Faktur telat 2 minggu ke salesman, en: Invoices two weeks overdue to the salesman}
description: {id: 'Tiap pagi Senin sampai Sabtu jam 08.30, salesman dikabari faktur pelanggannya yang sudah 14 hari lewat jatuh tempo dan belum lunas. Faktur tanpa salesman dikabarkan ke bagian keuangan. Sekali per faktur.'}
schedule: '30 8 * * 1-6'
steps:
  - find_all: sales_invoice
    where: "docstatus == 1 && outstanding > 0 && due_date != '' && days_between(due_date, today()) >= 14 && days_between(due_date, today()) <= 20"
    sort: due_date
    limit: 200
    as: late
  - for_each: late
    as: inv
    do:
      - {find: user, where: name == inv.salesman, as: rep}
      - notify: rep
        if: rep != null
        channel: in_app
        record: inv
        id: "'telat14:' + inv.name"
        title: 'Faktur {inv.name} telat 14 hari'
        template: 'Faktur {inv.name} sudah {days_between(inv.due_date, today())} hari lewat jatuh tempo dan belum lunas. Buka fakturnya untuk menagih pelanggan.'
      - notify: finance_manager
        if: rep == null
        channel: in_app
        record: inv
        id: "'telat14:' + inv.name"
        title: 'Faktur {inv.name} telat 14 hari, tanpa salesman'
        template: 'Faktur {inv.name} sudah {days_between(inv.due_date, today())} hari lewat jatuh tempo dan belum lunas, dan tidak ada salesman-nya.'
```

Tell the owner: "Aku siapkan usulan: tiap pagi jam 08.30, salesman dikabari faktur pelanggannya yang sudah telat 14
hari. Daftar tagih pagi untukmu dan bagian keuangan tetap jalan seperti biasa. Pesan WA ke pelanggan tetap dikirim
salesman sendiri dari faktur."

## 2. Purchase approval above Rp 5.000.000 (site with `pembelian_pemasok`)

Owner: "PO di atas 5 juta harus aku setujui dulu."

**Not a flow.** An approval on a document is a model policy in the company overlay: it holds the submit, binds to
what the approver saw (an edit while pending voids it) and runs the submit as the person who prepared it.

Choices:

- The threshold is the owner's. Ask when unsure: "5 juta itu sudah termasuk PPN?" (`grand_total` includes PPN).
- `approvers: [purchasing_manager]`, not the role that prepares POs: an approver who submits approves themself.
  When no member holds `purchasing_manager`, every PO above it waits for the owner. Say that.
- A foreign-currency PO always waits: its rate is typed by the maker.
- Read `custom/purchase_order.rk` first (`rangka_get_file`); if it exists, `patch` it (`/approvals/-`), do not
  replace it.

`custom/purchase_order.rk`:

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/overlay.json
model: purchase_order
approvals:
  - name: x_po_above_5m
    on: submit
    when: "grand_total > 5000000 || (currency != '' && currency != 'IDR')"
    approvers: [purchasing_manager]
    label: {id: 'Persetujuan pembelian di atas Rp 5.000.000', en: 'Approval for purchases above Rp 5,000,000'}
```

Quote labels that hold `Rp 5.000.000` or `5,000,000`: inside a `{...}` map a bare comma splits the value
(`duplicate_key` at validate).

The approvers hear about each request on their own (`kernel.approval.requested` notice); no flow is needed for that.

Tell the owner: "Aku siapkan usulan: PO di atas Rp 5.000.000 menunggu persetujuan sebelum diajukan. Saat ini belum
ada yang pegang peran manajer pembelian, jadi semua PO itu menunggu kamu. Notifikasinya masuk ke lonceng di atas."

## 3. Low stock alert to head office (site with `stok_outlet`, e.g. `demo-stok`)

Owner: "Kabari kantor pusat kalau bahan di outlet mau habis."

**Already there?** Yes: `stock/stock_low_alert` runs at 06:30 and tells head office once per episode, for items whose
`reorder_level` is filled in. First answer: "Isi batas minimum di tiap barang; tiap pagi kantor pusat dikabari." Only
when the owner wants it **right away**, write the event flow below. Then say both will tell head office (once at the
moment, once next morning), or leave the morning one as the daily list.

Choices:

- Event `stock_move.created`, not a cron every 10 minutes.
- Fire only on the move that crosses the level (before ≥ level, after < level), not on every sale below it.
- The flow runs inside the cashier's sale: read only `item` and `outlet` (every writer may read them), never
  `stock_balance`, and guard every value that may be missing. An error here would fail the sale.
- Notice per outlet (`outlet:`) so members limited to other outlets are not told; `id` per item, outlet and day.

`flows/x_stok_bahan_menipis.rk`:

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: x_stok_bahan_menipis
label: {id: Stok turun di bawah batas minimum, en: Stock fell below the reorder level}
description: {id: 'Begitu stok sebuah barang di outlet turun melewati batas minimumnya, kantor pusat langsung dikabari. Sekali per barang, outlet dan hari.'}
on: stock_move.created
if: event.qty < 0
steps:
  - {find: item, where: name == event.item, as: it}
  - {find: outlet, where: name == event.outlet, as: ou}
  - notify: head_office
    if: "it != null && it.reorder_level > 0 && event.balance_qty < it.reorder_level && event.balance_qty - event.qty >= it.reorder_level"
    channel: in_app
    outlet: event.outlet
    record: it
    id: "'x_low:' + event.outlet + ':' + event.item + ':' + today()"
    title: 'Stok {it.item_name} di bawah batas minimum'
    template: 'Stok {it.item_name} di {ou != null ? ou.outlet_name : event.outlet} tinggal {event.balance_qty}, batas minimumnya {it.reorder_level}.'
```

Drafting a PO from it is `procurement/reorder_draft_po`'s job (drafts only, a person submits). Do not create
purchase orders in this flow: that is a core-module draft (high risk) and doubles the shipped one.

## 4. A delay with a re-check (`wait`)

Owner: "Kalau 3 hari setelah faktur dikirim belum ada bayaran sama sekali, kabari keuangan."

`flows/x_cek_bayar_3_hari.rk`:

```yaml
# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: x_cek_bayar_3_hari
label: {id: Cek pembayaran 3 hari setelah faktur, en: Payment check 3 days after the invoice}
description: {id: 'Tiga hari setelah faktur diajukan, kalau belum ada pembayaran sama sekali, bagian keuangan dikabari.'}
on: sales_invoice.submitted
steps:
  - wait: 3d
  - {find: sales_invoice, where: name == event.name && docstatus == 1, as: inv}
  - notify: finance_manager
    if: inv != null && inv.paid_amount == 0 && inv.outstanding > 0
    channel: in_app
    record: inv
    id: "'bayar3:' + inv.name"
    title: 'Faktur {inv.name} belum dibayar'
    template: 'Sudah 3 hari sejak faktur {inv.name} diajukan dan belum ada pembayaran.'
```

- After a `wait`, `event` is the record as it was three days ago: re-read it (it may be paid or cancelled).
- `docstatus == 1` in the `find` drops a cancelled invoice.
- The wait needs the queue (`503 no_queue` without it): check `rangka_system_status`.
