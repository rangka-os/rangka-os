# Change sets

A change set is the only way a running installation changes. Console, CLI and Rangka AI all propose the same thing — a
diff of `.rk` files — and only a person can approve it. Reference: [changeset.rk](/reference/schema/changeset).

```json
{
  "title": "Nomor PO wajib untuk pelanggan grosir",
  "reason": "Pelanggan grosir mengirim purchase order",
  "ops": [
    {"op": "create", "path": "custom/customer.rk", "content": "model: customer\nfields: …"},
    {"op": "create", "path": "custom/sales_invoice.rk", "content": "model: sales_invoice\nfields: …"}
  ]
}
```

## Lifecycle

1. **Propose** — `POST /api/v1/changesets`. Ops are `create`, `replace`, `patch` (JSON Patch on the parsed file) or
   `delete` on paths such as `custom/<m>.rk`, `themes/<t>.rk`, `layouts/<l>.rk`, `config.rk`, `features.rk`,
   `system.rk` or `apps/**`. The kernel computes the layers touched and the risk.
2. **Validate** — on a temporary copy, with the same checks as `rangka validate`. Failure ends as `invalid`.
3. **Preview** — a dry run: the SQL that would run, affected record counts, and the full before/after text of every file.
4. **Approve or reject** — only from `previewed`, by a person in `config.ai.approvers`. High risk needs the owner
   (403 `owner_required`). API tokens used by AI can never approve.
5. **Apply** — write the files, migrate, reload metadata live, audit, push a `changeset` event. Low-risk changes may
   auto-apply when `config.ai.auto_apply: low_risk`.
6. **Revert** — restore the previous file versions the same way.

## Risk

| Risk | When |
| --- | --- |
| High — owner only | `system.rk`, roles and permissions, `config.tax`, money or stock fields, existing fields of document models, edits to existing app files, any delete |
| Medium | New overlay fields and rules, flows, `features.rk`, `config.apps` |
| Low | Themes, layouts, labels, dashboards |

Every transition is written to the audit log and shown in the Console with the diff.
