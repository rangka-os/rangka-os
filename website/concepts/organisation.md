# Organisation and overlays

An organisation's choices live in a few site files. Its people and records live in the database and are managed in
[Rangka Console](/tools/console) and with `rangka user` ([CLI](/reference/cli)).

## config.rk

```yaml
apps: [crm]                       # installed apps, in menu order; must be allowed by the plan
plan: tumbuh
locale: id-ID
currency: IDR
timezone: Asia/Jakarta
tax: {ppn_rate: 12, dpp_ratio: 11/12, pkp: true}
numbering:
  invoice: {pattern: "INV-{YYYY}-{#####}", reset: yearly}
theme: kopi_senja
layout: crm_first
sso: {domains: [kopisenja.id], required: false}
ai: {enabled: true, auto_apply: never, approvers: [owner, sales_manager]}
```

The kernel installs the modules each app lists. Reference: [config.rk](/reference/schema/config).

### Several organisations in one site

One site can hold several legal entities, for example two PTs that share a team. `orgs:` lists them; each one's
`config:` overrides the site's keys for that organisation ([contract 0010](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0010-multi-org-and-row-scope.md)):

```yaml
# examples/sites/demo-jasa/config.rk (shortened)
orgs:
  pt_contoh_inspeksi:
    name: PT Contoh Inspeksi
    code: CIN                      # {ORG} in number patterns
    config:
      org: {legal_name: PT Contoh Inspeksi, npwp: '00.000.000.0-000.001'}
      tax: {pkp: true}
  cv_contoh_pelatihan:
    name: CV Contoh Pelatihan
    code: CPL
    config:
      org: {legal_name: CV Contoh Pelatihan, npwp: '00.000.000.0-000.002'}
      tax: {pkp: false}
```

Records are kept per organisation (Postgres row-level security). A person who belongs to several switches between
them in the app. A role with `scope:` limits its members to the records of their own outlets.

## features.rk

Plans and what each allows: apps, capabilities and limits. Plans can `extends` another. Rangka Cloud maps plans to
prices; a self-hosted installation can list one plan with everything. Reference: [features.rk](/reference/schema/features).

```yaml
plans:
  - name: mulai
    label: {id: Mulai}
    apps: [crm]
    capabilities: [realtime, queue, scheduler, files, search, notify_email, api]
    limits: {users: 3, outlets: 1, storage_mb: 1024, ai_credits_month: 200}
  - name: tumbuh
    label: {id: Tumbuh}
    extends: mulai
    apps: [crm]
    capabilities: [realtime, queue, scheduler, files, search, notify_email, notify_whatsapp, api, mcp, ai, sso]
    limits: {users: 15, outlets: 5, storage_mb: 10240, ai_credits_month: 2000}
```

## Overlays: the company layer

An organisation's own fields, rules, approvals and form placements live in `custom/<model>.rk`, on top of the module's
model, without touching the module's files. Reference: [overlay.rk](/reference/schema/overlay).

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
- The kernel merges overlays onto module models at load, including through links (`customer.x_customer_group` reads the
  customer's overlay field).
- Overlay values are stored in the record's `x` JSONB column, so adding one needs no table change.
- Module and app upgrades never touch overlays, so customisations survive every upgrade.

## The company's own tables and automations

A table or automation the company owns outright lives in the site itself: `models/x_<name>.rk` and
`flows/<name>.rk` ([contract 0016](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0016-site-company-module.md)).
The kernel loads them as one unit named `site`. A company model's name starts with `x_`, so a module upgrade can never
clash with it, and its fields are ordinary columns of its own table. Ubah's "Tambah tabel" and "Tambah otomasi" write
here, through a change set.
