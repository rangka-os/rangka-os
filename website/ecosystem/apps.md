# Apps and templates

Apps live in [rangka-os/rangka-apps](https://github.com/rangka-os/rangka-apps), written entirely as `.rk` files.

| App | Depends on | Models | Highlights |
| --- | --- | --- | --- |
| `core` | — | customer, item, outlet, user | Master data every other app links to |
| `sales` | core | sales_invoice (document), sales_invoice_item | PPN 12% × DPP 11/12, payment status, dashboard |
| `crm` | core, sales (optional) | lead, deal, activity | Kanban pipeline with win probability, convert lead, won deal → draft invoice, WhatsApp → lead flow, morning follow-up, dashboard |

## Templates

A **template** packages apps, a starting configuration and onboarding questions for one kind of business. The first is
**Rangka CRM** (`templates/crm/template.rk`). Partners build templates for their industry on the shared core apps, so a
clinic and a coffee chain use the same customers and items.

## Roadmap

POS (cashier, shifts, offline sales, QRIS), Stock, Ledger, Payments, HR basics; industry packs for F&B, clinics and
services. See [Your first app](/guide/first-app) to build one.
