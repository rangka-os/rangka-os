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
2. **Create.** Rows are created only by flows the kernel starts (record events, schedules, a resumed `wait` or an
   approved `approve` step of such a flow), hooks and seed. A create through the API or an import is refused with
   409 `append_only`, for every role, the owner included. So is a create by a flow a caller starts by hand
   (`POST /api/v1/flows/{name}/run`), with or without `on:`, since its event is whatever the caller wrote; the mark
   stays with that run across `wait` and `approve`. A flow still needs the role's `create` grant, as for any model
   (it runs as the person whose action started it). Connector events reach flows today only through that endpoint,
   so a connector flow cannot post to a ledger until connectors have an authenticated inbound route.
3. **No edit, no delete.** Update and delete of a row are refused with 409 `append_only` on every path: the API, flows
   (`update` step), hooks (`Update`, `SetSubmitted`), submit, cancel and amend. A flow that tries rolls back the whole
   write that started it. Attachments of a row cannot be added or removed either (the write check refuses).
   One exception: when a draft another model links is submitted, the link value in an append-only row is rewritten
   to the draft's number (0009 §4). The row keeps pointing at the same document; no other field changes.
4. **Correction.** The only correction is a new row. Apps write the reversing row in the flow on the source
   document's `cancelled` event (amount negated, pointing at the row it reverses), then the amended document posts
   again.
5. **Screens.** `GET /api/v1/meta` reports `can.create`, `can.write`, `can.delete` and `can.import` as `false` for an
   append-only model, so the web app shows the rows without new, edit or delete buttons. `read`, `export` and
   `print` follow the grants as usual.
6. **Validation.** `append_only` with `is_submittable: true` (`append_only_submittable`: a ledger row is not a
   document with a draft and a cancel of its own) or with `is_child: true` (`append_only_child`: child rows are saved
   through their parent) is an error.
7. **Metadata changes.** Turning `append_only` on or off in a company model is a high-risk changeset (contract
   0015), never applied without a person approving it.

The message shown on refusal: "Baris <label> dicatat otomatis dari dokumen sumbernya dan tidak bisa dibuat, diubah,
atau dihapus manual. Untuk koreksi, batalkan dokumen sumbernya."

## Consequences

- Nobody changes a posted row through the running system. What remains is metadata: an owner may approve a changeset
  that adds a flow writing rows with any amount, or that turns `append_only` off, edits, and turns it back on. Both
  are recorded in the changeset history (who, when, what) and the second is high risk. Restore from backup and
  direct database access stay outside the kernel's reach, as for every model.
- A ledger flow that copies amounts from its event (`amount: event.dpp`) is safe because only real events start it
  with the power to create; apps should still keep the rules that tie a row to its source document and the unique
  `entry_key`, which also stop a second posting of the same document.
- A wrong row caused by a wrong document is fixed by cancelling and amending the document, which leaves both the
  original and the reversal in the ledger.
- Existing apps keep working until they opt in: rangka-apps sets `append_only: true` on `project_ledger` and
  `stock_adjustment_value` in its own change after this kernel ships; their 11 ledger flows only create rows. A
  backfill flow without `on:` (`adjustment_value_backfill`) keeps posting from its schedule; running it by hand,
  as a test script does today, is refused after the opt-in, and the script must wait for the schedule instead.
- Deleting a source document (allowed for drafts and for cancelled documents without an amended copy) leaves its
  ledger rows pointing at a name that no longer exists; a submitted document cannot be deleted, so a posted amount
  always has its reversal before its source can go.
- An import whose target is append-only passes metadata validation but is refused with `append_only` when someone
  runs it (the import permission check); a validation check for it is a follow-up.
