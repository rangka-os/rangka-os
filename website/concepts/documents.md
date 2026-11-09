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

## Approvals

A model can ask for a person's approval before a document is submitted, cancelled or runs an action:

```yaml
approvals:
  - name: manager_over_10m
    on: submit                     # submit | cancel | <action name>
    when: grand_total > 10000000   # optional; absent means always
    approvers: [sales_manager]     # roles; the owner may always approve
    label: {id: Persetujuan manajer}
```

When a policy matches, the verb does not run: the kernel answers 202 and creates a pending request bound to the
record as it is now. Editing the record voids the request. When the last request is approved, the verb runs as the
person who asked. Approving needs a person; API and AI tokens cannot. A company can add its own thresholds in an
overlay, and a flow can wait for a decision with an `approve` step. The web app lists open requests under
**Persetujuan**. Full rules: [contract 0007](https://github.com/rangka-os/rangka-os/blob/main/docs/contracts/0007-document-approvals.md).

**What never depends on an AI:** totals, tax, stock and ledger postings are computed by the kernel from rules and
hooks. An AI may propose a change to the rules through a change set; it never computes money.
