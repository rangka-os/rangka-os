package api

import (
	"errors"
	"io"
	"net/http"

	"github.com/rangka-os/rangka-os/kernel/internal/imports"
)

// previewImport reads an uploaded file, maps it and stores a previewed import log entry (contract 0012 §3).
func (s *Server) previewImport(w http.ResponseWriter, r *http.Request, c *caller) {
	if s.planStorageRefused(w, false) { // db_mb full (contract 0057 §1)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, imports.MaxBytes+1<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		uploadError(w, err)
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file_required", "Pilih file dulu")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		uploadError(w, err)
		return
	}
	e, err := s.Imports.Preview(r.Context(), c.actor, r.PathValue("import"), hdr.Filename, data, r.FormValue("outlet"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// uploadError answers a failed multipart read: 413 when the body is over the MaxBytesReader limit, else 400.
func uploadError(w http.ResponseWriter, err error) {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", imports.ErrTooBig.Error())
		return
	}
	writeError(w, http.StatusBadRequest, "file_required", "Pilih file dulu")
}

// commitImport creates the records of a previewed entry. Only people may run it.
func (s *Server) commitImport(w http.ResponseWriter, r *http.Request, c *caller) {
	if im := s.Engine.Reg().Imports[r.PathValue("import")]; im != nil && !c.p.Human() {
		fail(w, s.Engine.Deny(im.Model, "import")) // the one 403 shape; an unknown import is 404 below
		return
	}
	if s.planStorageRefused(w, false) {
		return
	}
	e, err := s.Imports.Commit(r.Context(), c.actor, r.PathValue("import"), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) getImport(w http.ResponseWriter, r *http.Request, c *caller) {
	e, err := s.Imports.Get(r.Context(), c.actor, r.PathValue("import"), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) listImports(w http.ResponseWriter, r *http.Request, c *caller) {
	list, total, err := s.Imports.List(r.Context(), c.actor, r.PathValue("import"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": list, "total": total})
}
