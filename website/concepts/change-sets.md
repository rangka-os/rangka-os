# Change sets

A change set is the only way a running installation changes. The Console, the web app's **Ubah** mode, Rangka AI and
any REST or MCP client all propose the same thing — a diff of `.rk` files — and only a person can approve it. Reference: [changeset.rk](/reference/schema/changeset).

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
   `delete` on paths such as `custom/<m>.rk`, `models/x_<m>.rk`, `flows/<f>.rk`, `dashboards/<d>.rk`,
   `themes/<t>.rk`, `layouts/<l>.rk`, `config.rk`, `features.rk`, `system.rk`, `modules/**` or `apps/**`. The kernel computes the layers touched and the risk.
2. **Validate** — on a temporary copy, with the same checks as `rangka validate`. Failure ends as `invalid`.
3. **Preview** — a dry run: the SQL that would run, affected record counts, and the full before/after text of every file.
4. **Approve or reject** — only from `previewed`, by a person (a browser session) whose role is in
   `config.ai.approvers` (the owner always is). High risk needs the owner (403 `owner_required`). API tokens and AI
   tokens can never approve.
5. **Apply** — write the files, migrate, reload metadata live, audit, push a `changeset` event. Low-risk changes may
   auto-apply when `config.ai.auto_apply: low_risk`.
6. **Revert** — restore the previous file versions the same way.

## Risk

| Risk | When |
| --- | --- |
| High — owner only | `system.rk`, roles and permissions, `config.tax`, `config.ai`, money or stock fields, existing fields of document models, edits to existing module or app files, any delete |
| Medium | New overlay fields and rules, overlay approvals, overlay select `options` (high on a document model), new tables, flows, `features.rk`, `config.apps` |
| Low | Themes, layouts, labels, dashboards (app and site `dashboards/<d>.rk`), overlays that only change `views` (form layout, list columns, kanban order) |

A new table (model) without `permissions` stays medium but its reasons say so: every role has full access to it.
Ubah's "Tambah tabel" copies `permissions` from a sibling model to avoid that.

A company's own tables and automations live in the site company module (contract 0016): `models/x_<name>.rk` and
`flows/<name>.rk` in the site folder, layer 4. Ubah writes them there, never into `apps/` or a catalogue module, so a
module upgrade never touches them. Changing a company table is judged field by field, like an overlay.

Every transition is written to the audit log and shown in the Console with the diff.
