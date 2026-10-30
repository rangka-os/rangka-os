# Relations

Models connect to each other, inside one app and across apps.

| Relation | How you write it | Example |
| --- | --- | --- |
| Many to one | `type: link, to: customer` | Invoice → customer, deal → customer |
| One to many, owned by the parent | `type: table, of: sales_invoice_item` (the child has `is_child: true`) | Invoice → its line items |
| To one of several models | `type: dynamic_link` | Supported in schema and tables; not yet used by the shipped apps |
| Read through a relation | CEL `customer.payment_days` | Due date from the customer's payment terms |
| Peek at the related record | `peek: [outstanding, payment_days]` on the link field | Customer card on the invoice form |

```yaml
fields:
  - name: customer
    type: link
    to: customer
    required: true
    peek: [outstanding, payment_days]
  - name: due_date
    type: date
    default: posting_date + duration(customer.payment_days)
  - {name: items, type: table, of: sales_invoice_item}
```

## Across apps

Relations follow app dependencies:

- An app may link to its own models and to models of apps it `requires` (`crm` → `core`).
- Models from an `optional` app need a guard: `if: app_installed('sales')`.
- Never the other way: `core` does not link to `crm`, so the core apps install on their own.
- Company overlays may add link fields, and overlay fields are readable through links
  (`customer.x_customer_group`).

The validator checks all of this, with suggestions for typos.

## Planned

These are designed but not built yet (see [roadmap](/roadmap)):

- **Related lists**: the kernel derives reverse relations from every link, so a customer shows its invoices, deals
  and activities with counts, without writing anything.
- **On delete**: `on_delete: restrict | set_empty | cascade` per link, `restrict` by default; submitted documents are
  never deleted.
- **Many to many**: `type: link_multi, to: item`, with the junction table made by the kernel. When the relation
  carries its own data (quantity, role), use a child model instead.
- **Trees**: a link to the same model, with cycle checks, for categories and chart of accounts.
- **Relations through events**: "invoice paid → update the deal" as a flow instead of a direct link.
