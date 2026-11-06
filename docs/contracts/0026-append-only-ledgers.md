# 0026 Append-only ledgers

Status: accepted (6 Nov 2026) · Owner of the change: rangka-os (kernel, schema 0.5) · Affects: rangka-apps ledger
models (`project_ledger`, `stock_adjustment_value`) once they set `append_only: true`

## Context

Apps keep money and stock history in ledger models: one row per posting, written by a flow when the source
document is submitted, and a reversing row when it is cancelled (`project_ledger` in rangka-apps #21,
`stock_adjustment_value` in #14). The rows must match their documents. Rules guard most hand-written rows, but the
owner holds every grant, so through the list screen the owner could edit or delete a ledger row, and delete runs no
rule at all. A margin report could then disagree with the invoices behind it. The audits of #21 accepted this as a
kernel limitation (DECISIONS 6 Nov).

## Decision

1. **Model key.** A model may set `append_only: true` (schema 0.5, default `false`).
2. **Create.** Rows are created only by flows, hooks and seed. A create through the API, the MCP tools or an import
   is refused with 409 `append_only`, for every role, the owner included. A flow still needs the role's `create`
   grant, as for any model (it runs as the person whose action started it).
3. **No edit, no delete.** Update and delete of a row are refused with 409 `append_only` on every path: the API, the
   MCP tools, flows (`update` step) and hooks. A flow that tries rolls back the whole write that started it.
4. **Correction.** The only correction is a new row. Apps write the reversing row in the flow on the source
   document's `cancelled` event (amount negated, pointing at the row it reverses), then the amended document posts
   again.
5. **Screens.** `GET /api/v1/meta` reports `can.create`, `can.write`, `can.delete` and `can.import` as `false` for an
   append-only model, so the web app shows the rows without new, edit or delete buttons. `read`, `export` and
   `print` follow the grants as usual.
6. **Validation.** `append_only` with `is_submittable: true` is an error (`append_only_submittable`): a ledger row
   is not a document with a draft and a cancel of its own.

The message shown on refusal: "Baris <label> dicatat otomatis dari dokumen sumbernya dan tidak bisa dibuat, diubah,
atau dihapus manual. Untuk koreksi, batalkan dokumen sumbernya."

## Consequences

- An owner cannot quietly change history; restore from backup and direct database access remain outside the
  kernel's reach (as for every model).
- A wrong row caused by a wrong document is fixed by cancelling and amending the document, which leaves both the
  original and the reversal in the ledger.
- Existing apps keep working until they opt in: rangka-apps sets `append_only: true` on `project_ledger` and
  `stock_adjustment_value` in its own change after this kernel ships, and may then drop the hand-written-row rules
  that only existed to fence off manual edits.
