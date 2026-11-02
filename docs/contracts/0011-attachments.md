# 0011: Attachments

Status: proposed (3 Nov 2026) · Owner: rangka-os (kernel) · Affects: web app (S0), backup (0005 §2), rangka-apps (none) · Stream: S6 of the pilot program

## Context
- Both pilots need files on records: a signed BAST or PO from the customer on a project or order, a supplier's
  invoice photo on a goods receipt, proof of transfer on a payment.
- `system.rk` already has a `files:` capability (`driver: local|s3`, `path`, `max_upload_mb`), but nothing uses it.
- `/api/v1/files` already means the `.rk` metadata files (Console, MCP `rangka_file`). Uploads are called
  **attachments** everywhere (API, table, UI) so the two never mix.

## Decision

### 1. Storage (`files` capability, local driver)
- The kernel stores attachment bytes through a small `Store` interface (`Put`, `Open`, `Delete`) with one driver
  for now, `local`. `driver: s3` is accepted by the schema but refused at start-up with a clear error until a
  driver exists; `files: {enabled: false}` turns the endpoints off (501 `files_disabled`).
- Root directory, first match wins: `rangka serve --files-dir` / `$RANGKA_FILES_DIR`, then `system.rk`
  `files.path`, then `./data/files`. A relative path is relative to the working directory of `rangka serve`
  (never the site directory, which is a git checkout).
- Layout: `<root>/<org>/<sha256[0:2]>/<sha256>`. Content-addressed per org: the same bytes uploaded twice in one
  org are stored once; the blob is removed when its last attachment row is deleted. Orgs never share blobs.
- Writes go to a temp file in `<root>/<org>/tmp/` and are renamed into place, so a crash never leaves half a file
  under its hash.

### 2. Table `rk_attachments` (kernel table, `store/system.sql`)
`id text primary key` (`att_` + 20 random hex), `org_id`, `model`, `record` (the record's `name`), `filename`,
`content_type`, `size bigint`, `sha256`, `created_by` (actor id), `created_at`. Index `(org_id, model, record)`.
Nothing in the model's own table changes, so no `.rk` key and no schema 0.5 change is needed.

### 3. REST (`/api/v1`, error shape as today)
| Method and path | Result |
| --- | --- |
| `POST /r/{model}/{name}/attachments` (multipart, part `file`) | 201 `{id, filename, content_type, size, sha256, created_by, created_at, url}` |
| `GET /r/{model}/{name}/attachments` | 200 `{data: [item…]}`, newest first |
| `GET /attachments/{id}` | 200, the bytes |
| `DELETE /attachments/{id}` | 204 |

- `url` is `/api/v1/attachments/{id}`.
- **Size limit:** `files.max_upload_mb`, default **20**. Larger → 413 `too_large` (message in Bahasa Indonesia).
  The body is read through `http.MaxBytesReader`, so an oversized upload is never buffered whole.
- **Content type** is sniffed from the bytes (`http.DetectContentType`), falling back to the extension; the
  client's header is not trusted. Downloads send `X-Content-Type-Options: nosniff` and
  `Content-Security-Policy: sandbox`. `Content-Disposition` is `inline` for raster images (`image/png|jpeg|gif|webp`)
  and `application/pdf` (the web app previews these), `attachment` for everything else, SVG and HTML included.
- **Filename:** base name only, control characters and path separators removed, at most 200 bytes; empty →
  `lampiran`.
- Missing multipart part `file` → 400 `bad_upload`. Unknown attachment id, or one of another org → 404
  `not_found` ("lampiran tidak ditemukan"), never 403, so ids cannot be probed across orgs.

### 4. Rules
- **Read** (list, download): the caller must be able to read the record. The handlers load it through
  `Engine.Get`, so org scope applies today and the permission and row-scope checks of 0005 §4 / 0010 apply as soon
  as they land in `Get`. Download checks the record again: holding an id is not enough.
- **Upload and delete** need `write` on the model under its `permissions:` (see §5).
- **Upload is allowed in any docstatus.** Attachments are evidence (signed BAST, proof of transfer) that often
  arrives after submit; adding one does not change the document.
- **Delete is refused unless the record is a draft** (docstatus 0, or the model is not submittable):
  409 `submitted`, "Lampiran dokumen yang sudah diajukan tidak bisa dihapus". Cancel + amend gives a new draft;
  attachments are not copied to the amended document.
- Attachments follow the record when submit renames it (`DRAFT-…` → final number): `store.RenameRecordAttachments`
  runs in the same transaction as the submit (`Engine.Submit`).
- Deleting a record deletes its attachments (rows and unreferenced blobs) after the record delete commits.
- Every upload and delete writes `rk_audit` (`attachment.added`, `attachment.deleted`, target `<model>/<name>`,
  data `{id, filename, size, sha256}`).

### 5. Permission check used until 0005 §4 lands
`Engine.Permits(ctx, actor, model, record, action)` (kernel, `engine/permits.go`):
- a model without `permissions:` allows everything (today's behaviour);
- role `owner` and the internal actors (`system`, `seed`, `flow:*`) are allowed;
- otherwise some entry whose `role` is one of the actor's roles must have `<action>: true` and, if it has `if:`,
  the CEL must be true on the record (same scope as action `if:`).
- A refusal is 403 `permission_denied` `{model, action}` as fixed in 0005 §4.

S1 (0005 §4) may replace the body of `Permits` with the general enforcement; the attachment handlers only call it.

### 6. Status and backup
- `/system/status` `capabilities.files` reports `{driver, ok, path (resolved root), max_upload_mb, attachments, bytes}`.
- Backup (0005 §2) archives the resolved root; `rk_attachments` is in the database dump.

## Not in this contract
- An `s3` driver, virus scanning, image thumbnails, MCP tools for attachments, attachment fields on models
  (`type: attach` in a form). Propose separately when a pilot asks.

## Consequences
- The web app (S0) can show an attachment panel on any record with no metadata change.
- A file is reachable only through its record, so later permission and row-scope work covers attachments for free.
