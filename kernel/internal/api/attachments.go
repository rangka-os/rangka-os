package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/files"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Attachments (contract 0011): files on records, stored in the files capability and listed in rk_attachments.

const defaultMaxUploadMB = 20

var errTooLarge = errors.New("attachment too large")

func (s *Server) maxUploadMB() int {
	if s.MaxUploadMB > 0 {
		return s.MaxUploadMB
	}
	return defaultMaxUploadMB
}

func (s *Server) filesOn(w http.ResponseWriter) bool {
	if s.Files == nil {
		writeError(w, http.StatusNotImplemented, "files_disabled", "Lampiran tidak aktif di instalasi ini")
		return false
	}
	return true
}

func attachmentItem(a *store.Attachment) map[string]any {
	return map[string]any{"id": a.ID, "filename": a.Filename, "content_type": a.ContentType, "size": a.Size, "sha256": a.SHA256,
		"created_by": a.CreatedBy, "created_at": a.CreatedAt.UTC().Format(time.RFC3339), "url": "/api/v1/attachments/" + a.ID}
}

func attachmentNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "not_found", "lampiran tidak ditemukan")
}

// countingReader passes bytes through, keeps the first 512 for sniffing and fails once more than max were read.
type countingReader struct {
	r    io.Reader
	n    int64
	max  int64
	head []byte
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if k := 512 - len(c.head); k > 0 {
		c.head = append(c.head, p[:min(n, k)]...)
	}
	c.n += int64(n)
	if c.n > c.max {
		return n, errTooLarge
	}
	return n, err
}

func isTooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.Is(err, errTooLarge) || errors.As(err, &mbe)
}

// attachmentRecord loads the record an attachment belongs to through engine.Get, which checks read (403 with
// action read); any 404 becomes "lampiran tidak ditemukan".
func (s *Server) attachmentRecord(w http.ResponseWriter, r *http.Request, c *caller) (*store.Attachment, store.Record, bool) {
	ctx := r.Context()
	row, err := store.GetAttachment(ctx, s.DB, c.actor.Org, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		attachmentNotFound(w)
		return nil, nil, false
	}
	if err != nil {
		fail(w, err)
		return nil, nil, false
	}
	rec, err := s.Engine.Get(ctx, c.actor, row.Model, row.Record)
	if err != nil {
		var se *engine.StatusError
		if errors.As(err, &se) && se.Status == http.StatusNotFound {
			attachmentNotFound(w)
		} else {
			fail(w, err)
		}
		return nil, nil, false
	}
	return row, rec, true
}

// dropBlobIfUnused removes a blob that no row of the org points at. Callers hold blobMu.
func (s *Server) dropBlobIfUnused(ctx context.Context, org, sum string) error {
	n, err := store.CountBlobRefs(ctx, s.DB, org, sum)
	if err != nil || n > 0 {
		return err
	}
	return s.Files.Delete(org, sum)
}

func (s *Server) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}

// POST /api/v1/r/{model}/{name}/attachments (multipart, part "file"), behind recordGuard("write").
func (s *Server) uploadAttachment(w http.ResponseWriter, r *http.Request, c *caller, _ store.Record) {
	if !s.filesOn(w) {
		return
	}
	ctx := r.Context()
	model, name := r.PathValue("model"), r.PathValue("name")
	mb := s.maxUploadMB()
	limit := int64(mb) << 20
	tooLarge := func() {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("Ukuran file melebihi batas %d MB", mb))
	}
	badUpload := func() { writeError(w, http.StatusBadRequest, "bad_upload", "Pilih file untuk diunggah") }
	r.Body = http.MaxBytesReader(w, r.Body, limit+1<<20)
	mr, err := r.MultipartReader()
	if err != nil {
		badUpload()
		return
	}
	var part *multipart.Part
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			if isTooLarge(err) {
				tooLarge()
			} else {
				badUpload()
			}
			return
		}
		if p.FormName() == "file" {
			part = p
			break
		}
		p.Close()
	}
	if part == nil {
		badUpload()
		return
	}
	defer part.Close()
	filename := files.SafeFilename(part.FileName())
	cr := &countingReader{r: part, max: limit}
	sum, size, err := s.Files.Put(c.actor.Org, cr)
	if err != nil {
		if isTooLarge(err) {
			tooLarge() // Put removed its temp file, so no blob was stored
			return
		}
		fail(w, err)
		return
	}
	a := &store.Attachment{Org: c.actor.Org, Model: model, Record: name, Filename: filename,
		ContentType: files.SniffType(cr.head, filename), Size: size, SHA256: sum, CreatedBy: c.actor.ID}
	s.blobMu.Lock()
	// a delete of another row with the same bytes may have removed the blob between Put and here
	f, err := s.Files.Open(c.actor.Org, sum)
	if err != nil {
		s.blobMu.Unlock()
		fail(w, fmt.Errorf("blob vanished during upload, try again: %w", err))
		return
	}
	f.Close()
	err = store.InsertAttachment(ctx, s.DB, a)
	if err != nil {
		if derr := s.dropBlobIfUnused(ctx, a.Org, sum); derr != nil {
			s.logf("attachments: cleanup %s: %v", sum, derr)
		}
	}
	s.blobMu.Unlock()
	if err != nil {
		fail(w, err)
		return
	}
	if err := store.Audit(ctx, s.DB, a.Org, c.actor.ID, "attachment.added", model+"/"+name, filename,
		map[string]any{"id": a.ID, "filename": filename, "size": size, "sha256": sum}); err != nil {
		s.logf("attachments: audit %s: %v", a.ID, err)
	}
	writeJSON(w, http.StatusCreated, attachmentItem(a))
}

// GET /api/v1/r/{model}/{name}/attachments, behind recordGuard("read").
func (s *Server) listAttachments(w http.ResponseWriter, r *http.Request, c *caller, _ store.Record) {
	if !s.filesOn(w) {
		return
	}
	ctx := r.Context()
	model, name := r.PathValue("model"), r.PathValue("name")
	rows, err := store.ListAttachments(ctx, s.DB, c.actor.Org, model, name)
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]any, 0, len(rows))
	for i := range rows {
		out = append(out, attachmentItem(&rows[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// contentDisposition builds `inline|attachment; filename*=UTF-8` plus two apostrophes and the percent-escaped
// name (RFC 6266 / 5987).
func contentDisposition(disp, filename string) string {
	const attrChar = "!#$&+-.^_`|~"
	var b strings.Builder
	for i := 0; i < len(filename); i++ {
		ch := filename[i]
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.IndexByte(attrChar, ch) >= 0 {
			b.WriteByte(ch)
		} else {
			fmt.Fprintf(&b, "%%%02X", ch)
		}
	}
	return disp + "; filename*=UTF-8''" + b.String()
}

// GET /api/v1/attachments/{id}
func (s *Server) getAttachment(w http.ResponseWriter, r *http.Request, c *caller) {
	if !s.filesOn(w) {
		return
	}
	row, _, ok := s.attachmentRecord(w, r, c)
	if !ok {
		return
	}
	f, err := s.Files.Open(row.Org, row.SHA256)
	if errors.Is(err, os.ErrNotExist) {
		attachmentNotFound(w)
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	defer f.Close()
	disp := "attachment"
	if files.Inline(row.ContentType) {
		disp = "inline"
	}
	h := w.Header()
	h.Set("Content-Type", row.ContentType)
	h.Set("X-Content-Type-Options", "nosniff")
	if mt, _, _ := mime.ParseMediaType(row.ContentType); mt != "application/pdf" {
		h.Set("Content-Security-Policy", "sandbox") // Chromium's PDF viewer refuses sandboxed documents
	}
	h.Set("Cache-Control", "private, max-age=0")
	h.Set("Content-Disposition", contentDisposition(disp, row.Filename))
	http.ServeContent(w, r, "", row.CreatedAt, f) // sets Content-Length, honours Range
}

func docstatus(rec store.Record) int64 {
	switch v := rec["docstatus"].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}

// DELETE /api/v1/attachments/{id}
func (s *Server) deleteAttachment(w http.ResponseWriter, r *http.Request, c *caller) {
	if !s.filesOn(w) {
		return
	}
	ctx := r.Context()
	row, rec, ok := s.attachmentRecord(w, r, c)
	if !ok {
		return
	}
	if err := s.Engine.Authorize(ctx, c.actor, row.Model, "write", rec); err != nil {
		fail(w, err)
		return
	}
	if m := s.Engine.Reg().Model(row.Model); m != nil && m.IsSubmittable && docstatus(rec) != 0 {
		writeError(w, http.StatusConflict, "submitted", "Lampiran dokumen yang sudah diajukan tidak bisa dihapus")
		return
	}
	if s.Engine.HoldsAttachment(row.Model, rec, row.ID) { // an image or signature field points at it (0030 §4)
		writeError(w, http.StatusConflict, "in_use", "Lampiran ini dipakai sebagai foto atau tanda tangan catatan; kosongkan field itu dulu")
		return
	}
	s.blobMu.Lock()
	err := store.DeleteAttachment(ctx, s.DB, row.Org, row.ID)
	if err == nil {
		if derr := s.dropBlobIfUnused(ctx, row.Org, row.SHA256); derr != nil {
			s.logf("attachments: remove blob %s: %v", row.SHA256, derr)
		}
	}
	s.blobMu.Unlock()
	if err != nil {
		fail(w, err)
		return
	}
	if err := store.Audit(ctx, s.DB, row.Org, c.actor.ID, "attachment.deleted", row.Model+"/"+row.Record, row.Filename,
		map[string]any{"id": row.ID, "filename": row.Filename, "size": row.Size, "sha256": row.SHA256}); err != nil {
		s.logf("attachments: audit %s: %v", row.ID, err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// removeRecordAttachments runs after a record delete committed: rows go, then blobs nothing else uses.
// Errors are logged, the record is already gone.
func (s *Server) removeRecordAttachments(ctx context.Context, org, model, name string) {
	s.blobMu.Lock()
	defer s.blobMu.Unlock()
	rows, err := store.DeleteRecordAttachments(ctx, s.DB, org, model, name)
	if err != nil {
		s.logf("attachments: delete rows of %s/%s: %v", model, name, err)
		return
	}
	if s.Files == nil {
		return
	}
	done := map[string]bool{}
	for _, a := range rows {
		if done[a.SHA256] {
			continue
		}
		done[a.SHA256] = true
		if err := s.dropBlobIfUnused(ctx, org, a.SHA256); err != nil {
			s.logf("attachments: remove blob %s: %v", a.SHA256, err)
		}
	}
}
