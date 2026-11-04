# Modules and apps

[rangka-os/rangka-apps](https://github.com/rangka-os/rangka-apps) holds two levels of `.rk` files
([contract 0001](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0001-modules-and-apps.md)):

- **Modules** (`modules/<name>/module.rk`): models, rules, flows and roles, reusable by every app.
- **Apps** (`apps/<name>/app.rk`): what a business installs. An app lists the modules it uses and adds the menu,
  dashboards, home page, onboarding questions and connectors. It never defines models.

## Apps

| App | Modules | For |
| --- | --- | --- |
| `crm` (Rangka CRM) | contacts, catalogue, pipeline, invoicing, outlets, users | Leads from WhatsApp to closing, kanban pipeline, won deal → draft invoice |
| `jasa_proyek` (Rangka Jasa Proyek) | contacts, catalogue, users, outlets, tax_id, currency, invoicing, quotation, orders, workforce, projects, payments, procurement | Quotation and staffing through invoice, payment and receivables, for one or more companies |
| `fnb_cabang` (Rangka F&B Cabang) | contacts, catalogue, outlets, users, stock, outlet_sales, procurement, payments, invoicing | Daily outlet sales, raw-material stock, transfers, stock counts and purchasing across outlets |

## Modules

| Module | Models |
| --- | --- |
| `users` | user (and the shared roles in `modules/users/roles/`) |
| `contacts` | customer, vendor, contact_person |
| `catalogue` | item, uom, uom_conversion, price_list, price_list_item |
| `outlets` | outlet |
| `invoicing` | sales_invoice (document), sales_invoice_item; PPN 12% × DPP 11/12 |
| `payments` | payment, payment_allocation, withholding |
| `pipeline` | lead, deal, activity; WhatsApp → lead and morning follow-up flows |
| `quotation`, `orders` | quotation, customer_order and their lines |
| `procurement` | purchase_request, purchase_order, goods_receipt, vendor_bill and their lines |
| `stock` | stock_move, stock_balance, transfers, counts, adjustments, recipes |
| `outlet_sales` | outlet_sales, outlet_sales_line; imports from a daily form and Moka |
| `projects`, `workforce` | project, assignment, timesheet, acceptance; worker, certification |
| `tax_id`, `currency` | tax_code; currency, exchange_rate |

## Example sites

`examples/sites/kopi-senja` (CRM), `examples/sites/demo-jasa` and `examples/sites/demo-fnb`, with their demo data in
`examples/fixtures/`. Partners build apps for their industry on the shared modules, so a clinic and a coffee chain use
the same customers and items. See [Your first app](/guide/first-app) to build one.
