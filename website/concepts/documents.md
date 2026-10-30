# Documents and lifecycle

A model with `is_submittable: true` is a **document**: an invoice, an order, a stock entry. Documents follow one
lifecycle, enforced by the kernel.

| State | `docstatus` | What can happen |
| --- | --- | --- |
| Draft | 0 | Edit freely; named `DRAFT-xxxxx`; rules run on save |
| Submitted | 1 | Gets its final number (`INV-2026-00047`); **immutable** — edits return 409; hooks post stock and ledger moves |
| Cancelled | 2 | Moves are reversed; the record stays for audit |
| Amended | new draft | `amend` copies a cancelled document into a new draft with `amended_from`, numbered `-1`, `-2` |

```bash
POST /api/v1/r/sales_invoice                       # create a draft
POST /api/v1/r/sales_invoice/DRAFT-ML5G8/submit    # rules with level warn need {"force": true}
POST /api/v1/r/sales_invoice/INV-2026-00047/cancel
POST /api/v1/r/sales_invoice/INV-2026-00047/amend
```

Derived statuses for invoices: draft, submitted, partly paid, paid, overdue, cancelled.

**What never depends on an AI:** totals, tax, stock and ledger postings are computed by the kernel from rules and
hooks. An AI may propose a change to the rules through a change set; it never computes money.
