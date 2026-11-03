# S6 Attachments Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Files attached to any record through `/api/v1`, stored by the `files` capability's local driver, with a size limit, permission checks and no delete on submitted records.

**Architecture:** A new package `kernel/internal/files` holds the content-addressed local blob store. A kernel table `rk_attachments` holds metadata. Handlers in `kernel/internal/api/attachments.go` load the record through `Engine.Get` (org scope, later S1 permissions), check `Engine.Permits` for writes, and write `rk_audit`.

**Tech Stack:** Go 1.24 stdlib (`net/http` multipart, `crypto/sha256`, `os`), PostgreSQL 16, existing kernel packages.

**Spec:** `docs/contracts/0011-attachments.md` (the decisions), `docs/superpowers/plans/2026-11-03-pilot-program.md` stream S6 and Fixed interfaces, `docs/superpowers/specs/2026-11-03-first-pilots-design.md`.

## Global Constraints

- Code, comments, docs in English; user-facing messages in Bahasa Indonesia, sentence case, "kamu".
- Error JSON shape as today: `{"error":{"code","message"}}` via `writeError` in `kernel/internal/api/http.go`.
- Default size limit **20 MB** (`files.max_upload_mb` overrides); 413 code `too_large`.
- Delete on a submitted record: 409 code `submitted` (S0 relies on this exact code).
- Unknown or other-org attachment id: 404 `not_found`.
- Refusal: 403 `permission_denied` with `model` and `action` in the body.
- `/api/v1/files` keeps meaning `.rk` files. Never touch those routes.
- Keep `make -C kernel test`, `make -C kernel vet` and `make -C kernel compare-python` green. No schema or validator change in this stream.
- Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Path traversal in the filename** (`../../etc/passwd`, `a\b`, NUL bytes): stored name is a safe base name, blob path never derives from it. Test in Task 1 (`TestSafeFilename`).
2. **HTML or SVG uploaded then opened in the browser** (stored XSS on the kernel origin): served as `attachment` with `nosniff` and `CSP: sandbox`. Test in Task 3 (`TestAttachmentDownloadHeaders`).
3. **Attachment id from another org**: 404, bytes never served. Test in Task 3 (`TestAttachmentsOrgIsolation`).
4. **Same file uploaded twice, one copy deleted**: the other attachment still downloads (blob kept until last reference). Test in Task 3 (`TestAttachmentsDedupe`).
5. **Upload larger than the limit**: 413 without the kernel reading the whole body into memory. Test in Task 3 (`TestAttachmentTooLarge`, limit set to 1 MB via `system.rk`).

---

## File structure
- Create `kernel/internal/files/files.go`: `Store` interface, `Local` driver, `SafeFilename`, `SniffType`, `Inline`.
- Create `kernel/internal/files/files_test.go`.
- Modify `kernel/internal/store/system.sql`: table `rk_attachments`.
- Create `kernel/internal/store/attachments.go`: row CRUD.
- Create `kernel/internal/engine/permits.go` + `permits_test.go`: `Engine.Permits`.
- Create `kernel/internal/api/attachments.go`: handlers. Modify `api/server.go` (routes, `Files` field), `api/system.go` (status), `api/records.go` (delete cascade).
- Modify `kernel/internal/server/server.go` (`Config.FilesDir`, wiring) and `kernel/cmd/rangka/serve.go` (`--files-dir`).
- Create `kernel/internal/server/attachments_test.go`.
- Modify `kernel/README.md` (one section on attachments) and `docs/superpowers/plans/2026-11-03-pilot-program.md` Fixed interfaces (response shapes).

### Task 1: Local blob store (`internal/files`)

**Files:** Create `kernel/internal/files/files.go`, `kernel/internal/files/files_test.go`

**Interfaces — Produces:**
```go
type Store interface {
    // Put streams r into the org's area, returns hex sha256 and size. Writes to <root>/<org>/tmp then renames.
    Put(org string, r io.Reader) (sum string, size int64, err error)
    Open(org, sum string) (io.ReadSeekCloser, error) // os.ErrNotExist when missing
    Delete(org, sum string) error                      // missing is not an error
    Root() string
}
func NewLocal(root string) (*Local, error) // MkdirAll(root, 0o750); root made absolute
func SafeFilename(name string) string      // base name, strip control chars and / \ , max 200 bytes (cut on rune boundary), "" or "."/".." → "lampiran"
func SniffType(head []byte, filename string) string // http.DetectContentType; if result is application/octet-stream or text/plain, use mime.TypeByExtension(ext) when non-empty
func Inline(contentType string) bool       // true only for image/png, image/jpeg, image/gif, image/webp, application/pdf (params ignored)
```
Blob path: `<root>/<org>/<sum[0:2]>/<sum>`; `org` must match `^[a-z0-9][a-z0-9_-]*$` else error (org ids are directory names). Open/Delete validate `sum` is 64 hex chars.

- [ ] **Step 1: Write failing tests** in `files_test.go`:
  - `TestLocalPutOpenDelete`: Put "halo" → sum `sha256("halo")`, size 4; file exists at `root/kopi-senja/<sum[:2]>/<sum>`; Open reads "halo"; Put same bytes again → same sum, no error; Delete → Open returns `errors.Is(err, os.ErrNotExist)`; Delete again → nil; no files left in `root/kopi-senja/tmp`.
  - `TestLocalRejectsBadOrgAndSum`: Put with org `"../x"` errors; Open with sum `"../../etc/passwd"` errors.
  - `TestSafeFilename`: `"../../etc/passwd"`→`"passwd"`, `"a\\b.pdf"`→`"b.pdf"`, `"x\x00y.png"`→`"xy.png"`, `""`→`"lampiran"`, `".."`→`"lampiran"`, 300×`"é"` → len ≤ 200 and `utf8.ValidString`.
  - `TestSniffAndInline`: PNG magic bytes → `image/png`, `Inline` true; `"%PDF-1.4"` → `application/pdf`, true; `"<svg xmlns=…>"` with `x.svg` → not inline; `"<html>"` → `text/html; charset=utf-8`, not inline; plain bytes with `a.csv` → `text/csv…`.
- [ ] **Step 2:** `cd kernel && go test ./internal/files/` → FAIL (package missing).
- [ ] **Step 3:** Implement. Put hashes while copying into `os.CreateTemp(<root>/<org>/tmp, "up-*")`, then `os.Rename`; remove temp on any error.
- [ ] **Step 4:** `go test ./internal/files/` → PASS; `go vet ./internal/files/`.
- [ ] **Step 5:** Commit `kernel: files capability local driver for attachments`.

### Task 2: Table, row store and `Engine.Permits`

**Files:** Modify `kernel/internal/store/system.sql`; create `kernel/internal/store/attachments.go`, `kernel/internal/engine/permits.go`, `kernel/internal/engine/permits_test.go`; add a DB test to `kernel/internal/store/` following its existing test pattern (`testdb.New(t)`).

**Interfaces — Produces:**
```go
// store
type Attachment struct {
    ID, Org, Model, Record, Filename, ContentType, SHA256, CreatedBy string
    Size int64
    CreatedAt time.Time
}
func InsertAttachment(ctx context.Context, q pg.Querier, a *Attachment) error // fills ID ("att_"+20 hex from crypto/rand) and CreatedAt
func GetAttachment(ctx context.Context, q pg.Querier, org, id string) (*Attachment, error) // ErrNotFound when missing or other org
func ListAttachments(ctx context.Context, q pg.Querier, org, model, record string) ([]Attachment, error) // created_at desc, id desc
func DeleteAttachment(ctx context.Context, q pg.Querier, org, id string) error
func DeleteRecordAttachments(ctx context.Context, q pg.Querier, org, model, record string) ([]Attachment, error) // returns deleted rows
func CountBlobRefs(ctx context.Context, q pg.Querier, org, sum string) (int64, error)
func AttachmentTotals(ctx context.Context, q pg.Querier) (count, bytes int64, err error)
// engine
func (e *Engine) Permits(ctx context.Context, a Actor, model string, rec store.Record, action string) bool
```
SQL (append to `system.sql`, idempotent):
```sql
create table if not exists rk_attachments (
  id           text primary key,
  org_id       text not null,
  model        text not null,
  record       text not null,
  filename     text not null,
  content_type text not null,
  size         bigint not null,
  sha256       text not null,
  created_by   text not null default '',
  created_at   timestamptz not null default now()
);
create index if not exists rk_attachments_record_idx on rk_attachments (org_id, model, record);
create index if not exists rk_attachments_blob_idx on rk_attachments (org_id, sha256);
```
`Permits` rules (contract §5): unknown model → false; `m.Raw["permissions"]` empty → true; actor role `owner` or `system`, or actor ID `system`/`seed`/prefix `flow:` → true; else true iff some entry has `role` ∈ `a.Roles`, `<action>` == true and (no `if` or `e.newScope(ctx, e.DB, a, m, rec, nil).eval(if) == true`, same as `ActionAllowed`).

- [ ] **Step 1: Failing tests.** Store DB test `TestAttachmentRows`: insert two rows for `sales_invoice/X` in org `a`, one in org `b` with the same sha; `ListAttachments(a, sales_invoice, X)` → 2 newest first; `GetAttachment(b, idOfA)` → `ErrNotFound`; `CountBlobRefs(a, sha)` → 2 (and 1 for `b`); `DeleteRecordAttachments` returns 2 rows and leaves `b`'s. Engine test `TestPermits` (uses the engine package's existing DB test setup with the workspace apps): on model `deal` (permissions: sales_rep read/create/write with `if: owner == user.name`; sales_manager write): sales_rep with `UserName` equal to the deal's owner → `write` true; with another UserName → false; sales_manager → true; role `owner` → true; role `viewer` → false; a model without `permissions:` → true for role `viewer`.
- [ ] **Step 2:** `make -C kernel test` (or `RANGKA_TEST_DATABASE_URL=… go test ./internal/store ./internal/engine -run 'Attachment|Permits'`) → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Same command → PASS; existing store DDL tests still pass.
- [ ] **Step 5:** Commit `kernel: rk_attachments table and Engine.Permits`.

### Task 3: REST endpoints, wiring, status

**Files:** Create `kernel/internal/api/attachments.go`, `kernel/internal/server/attachments_test.go`; modify `kernel/internal/api/server.go`, `api/system.go`, `api/records.go`, `kernel/internal/server/server.go`, `kernel/cmd/rangka/serve.go`.

**Interfaces — Consumes:** Task 1 `files.Store`, `SafeFilename`, `SniffType`, `Inline`; Task 2 store functions and `Engine.Permits`.
**Produces:** `api.Server` fields `Files files.Store` (nil = disabled) and `MaxUploadMB int`; `server.Config.FilesDir string`; routes:
```
POST   /api/v1/r/{model}/{name}/attachments
GET    /api/v1/r/{model}/{name}/attachments
GET    /api/v1/attachments/{id}
DELETE /api/v1/attachments/{id}
```
Route note: `POST /api/v1/r/{model}/{name}/{verb}` exists; the literal `attachments` pattern is more specific so Go's mux picks it. Add a `GET /api/v1/r/{model}/{name}/attachments` route explicitly.

Item JSON: `{id, filename, content_type, size, sha256, created_by, created_at (RFC3339), url: "/api/v1/attachments/<id>"}`; list → `{data: [...]}`.

Behaviour (contract §3–§4):
- Root: `cfg.FilesDir` → `$RANGKA_FILES_DIR` (via the serve flag default) → `system.rk files.path` → `data/files`. `files.enabled: false` → `Files` nil → endpoints 501 `files_disabled`. `files.driver: s3` → `server.New` returns error `files: driver s3 is not available yet; use local`.
- Upload: `Engine.Get` the record (404 passes through `fail`); `Permits(…, "write")` else 403 `permission_denied` with body `{"error":{"code":"permission_denied","message":"Kamu tidak punya izin write pada <model>","model":…,"action":"write"}}`; `r.Body = http.MaxBytesReader(w, r.Body, limit+1<<20)`; read part `file` with `r.MultipartReader()` streaming (not `ParseMultipartForm`) through a counting reader into `Files.Put`; if the part exceeds `limit` → 413 `too_large` "Ukuran file melebihi batas <N> MB" and delete the blob if no row references it; sniff from the first 512 bytes; insert row; `store.Audit(… "attachment.added", model+"/"+name, filename, {id, filename, size, sha256})`; 201 item. Missing part → 400 `bad_upload` "Pilih file untuk diunggah".
- List: `Engine.Get` then `ListAttachments`.
- Download: `GetAttachment` (404 `not_found` "lampiran tidak ditemukan"), then `Engine.Get(row.Model, row.Record)` (404 when not visible); headers `Content-Type`, `Content-Length`, `X-Content-Type-Options: nosniff`, `Content-Security-Policy: sandbox`, `Cache-Control: private, max-age=0`, `Content-Disposition: inline|attachment; filename*=UTF-8''<pct-escaped>` (use `mime.FormatMediaType`); `http.ServeContent`.
- Delete: row → record via `Engine.Get`; `Permits(…, "write")`; if the model `IsSubmittable` and `docstatus` ≠ 0 → 409 `submitted` "Lampiran dokumen yang sudah diajukan tidak bisa dihapus"; delete row; `CountBlobRefs`==0 → `Files.Delete`; audit `attachment.deleted`; 204.
- `deleteRecord` in `api/records.go`: after a successful engine delete, `DeleteRecordAttachments` and remove unreferenced blobs (errors logged with `s.Log`, not returned).
- Status: `caps["files"]` = `{driver, ok: Files != nil, path: Files.Root(), max_upload_mb, attachments, bytes}`.

- [ ] **Step 1: Failing tests** in `server/attachments_test.go` using `newEnv`, `e.login()`, and a new helper `func (e *env) upload(path, filename string, data []byte) *resp` (multipart part `file`, same cookie/token handling as `do`). Set `cfg.FilesDir` to `t.TempDir()` inside `newEnv` so tests never write to the working directory.
  - `TestAttachmentsCRUD`: create draft `sales_invoice` (as `TestREST` does); upload `"bast.pdf"` with `%PDF-1.4…` → 201, `content_type` `application/pdf`, `size` matches, `url` set; list → 1 item; GET url → 200, body equal, `Content-Disposition` starts `inline`; DELETE → 204; list → 0; GET → 404; `rk_audit` has `attachment.added` and `attachment.deleted` with target `sales_invoice/<name>`.
  - `TestAttachmentDeleteSubmitted`: upload to draft, submit, upload again → 201 (allowed after submit), DELETE either → 409 code `submitted`; cancel then DELETE → 409 still (docstatus 2).
  - `TestAttachmentPermissions`: `auth.EnsureUser(…, "rep@example.com", …, "kopi-senja", "sales_rep", <a user record that does not own the target deal>)`, log in as rep; upload to a deal owned by someone else → 403 `permission_denied` with `model` `deal`, `action` `write`; upload to a deal whose `owner` is the rep's record → 201; DELETE of the ceo's attachment on the other deal → 403. Pick deal fixtures from `rangka-apps/fixtures`; if none is owned by a non-owner user record, create one as ceo with `owner` set.
  - `TestAttachmentsOrgIsolation`: insert an `rk_attachments` row for org `other` directly; GET/DELETE `/api/v1/attachments/<id>` as ceo → 404.
  - `TestAttachmentsDedupe`: upload the same bytes to two records; one blob file on disk under `FilesDir/kopi-senja`; delete the first → second still downloads; delete second → blob gone.
  - `TestAttachmentTooLarge`: `newEnv` mutate sets `system.rk` `max_upload_mb: 1`; upload 1 MiB + 1 byte → 413 `too_large`; nothing listed; no blob left.
  - `TestAttachmentDownloadHeaders`: upload `x.svg` (`<svg xmlns="http://www.w3.org/2000/svg"/>`) and `x.html` → GET has `Content-Disposition: attachment…`, `X-Content-Type-Options: nosniff`, `Content-Security-Policy: sandbox`; filename `"../../a b.png"` stored as `"a b.png"`.
  - `TestAttachmentsMissingRecordAndPart`: upload to `sales_invoice/NOPE` → 404; POST without part `file` → 400 `bad_upload`; `GET /api/v1/files` still lists `.rk` files (regression).
  - `TestAttachmentsStatus`: after one upload, `/api/v1/system/status` `capabilities.files.attachments` == 1, `bytes` > 0, `max_upload_mb` == 25 (kopi-senja sets 25).
  - `TestDeleteRecordRemovesAttachments`: upload to a draft, DELETE the record → 204; `rk_attachments` has no row for it; blob gone.
- [ ] **Step 2:** `make -C kernel test` → new tests FAIL (404 routes).
- [ ] **Step 3:** Implement handlers, wiring, `--files-dir` flag (`envOr("RANGKA_FILES_DIR", "")`).
- [ ] **Step 4:** `make -C kernel test` → all PASS; `make -C kernel vet`.
- [ ] **Step 5:** Commit `kernel: attachments REST endpoints (contract 0011)`.

### Task 4: Docs and fixed interfaces

**Files:** Modify `kernel/README.md`, `docs/superpowers/plans/2026-11-03-pilot-program.md` (Fixed interfaces → REST additions: add the item shape, 201/204, 409 `submitted`, 413 `too_large`, 501 `files_disabled`, `--files-dir`).

- [ ] **Step 1:** Add a short "Attachments" section to `kernel/README.md` (endpoints, storage root order, limit) matching the README's existing tone.
- [ ] **Step 2:** `make -C kernel test && make -C kernel compare-python` → green.
- [ ] **Step 3:** Commit `docs: attachments in kernel README and pilot fixed interfaces`.
