# Organisation and overlays

An organisation's choices live in three kinds of files. Its people and records live in the database and are managed in
[Rangka Console](/tools/console).

## config.rk

```yaml
apps: [core, crm, sales]          # installed apps, in menu order; must be allowed by the plan
plan: tumbuh
locale: id-ID
currency: IDR
timezone: Asia/Jakarta
tax: {ppn_rate: 12, dpp_ratio: 11/12, pkp: true}
numbering: {sales_invoice: INV-{YYYY}-{#####}}
theme: kopi_senja
layout: crm_first
sso: {domains: [kopisenja.id], required: false}
ai: {enabled: true, auto_apply: never, approvers: [owner, sales_manager]}
```

Reference: [config.rk](/reference/schema/config).

## features.rk

Plans and what each allows: apps, capabilities and limits. Plans can `extends` another. Rangka Cloud maps plans to
prices; a self-hosted installation can list one plan with everything. Reference: [features.rk](/reference/schema/features).

```yaml
plans:
  - name: mulai
    label: {id: Mulai}
    apps: [core, crm]
    capabilities: [realtime, queue, scheduler, files, search, notify_email, api]
    limits: {users: 3, outlets: 1, storage_mb: 1024, ai_credits_month: 200}
  - name: tumbuh
    label: {id: Tumbuh}
    extends: mulai
    apps: [core, crm, sales]
    limits: {users: 15, outlets: 5}
```

## Overlays: the company layer

An organisation's own fields, rules and form placements live in `custom/<model>.rk`, on top of the app model, without
touching the app's files. Reference: [overlay.rk](/reference/schema/overlay).

```yaml
# custom/sales_invoice.rk
model: sales_invoice
fields:
  - {name: x_po_number, type: data, label: {id: Nomor PO}}
rules:
  - name: po_for_wholesale
    on: submit
    check: customer.x_customer_group != 'grosir' || x_po_number != ''
    message: {id: Nomor PO wajib untuk pelanggan grosir}
views: {form: {add: [{field: x_po_number, after: customer}]}}
```

- Overlay field names always start with `x_`.
- The kernel merges overlays onto app models at load, including through links (`customer.x_customer_group` reads the
  customer's overlay field).
- Overlay values are stored in the record's `x` JSONB column, so adding one needs no table change.
- App upgrades never touch overlays, so customisations survive every upgrade.
