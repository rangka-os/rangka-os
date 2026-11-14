package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/plan"
)

// planStorageRefused answers 403 plan_limit when an upload (files) or an import may not start because the plan's
// storage is full (contract 0057 §1). It reads the cached plan snapshot only.
func (s *Server) planStorageRefused(w http.ResponseWriter, files bool) bool {
	if s.DB == nil || s.Engine == nil {
		return false
	}
	reg := s.Engine.Reg()
	snap := s.Engine.Plan.Peek(s.DB, reg, s.Engine.Now(), s.logf)
	if pe := snap.CheckUpload(reg, files); pe != nil {
		writeError(w, http.StatusForbidden, pe.Code, pe.Msg)
		return true
	}
	return false
}

// writePlanError answers a plan refusal: 429 with Retry-After for pdf_slowed, else 403.
func writePlanError(w http.ResponseWriter, pe *plan.Error) {
	if pe.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(pe.RetryAfter))
		writeError(w, http.StatusTooManyRequests, pe.Code, pe.Msg)
		return
	}
	writeError(w, http.StatusForbidden, pe.Code, pe.Msg)
}

// takePDF counts one PDF render against pdf_month (no database: not counted).
func (s *Server) takePDF(ctx context.Context, reg *meta.Registry) error {
	if s.DB == nil {
		return nil
	}
	return plan.TakePDF(ctx, s.DB, reg, s.Engine.Now())
}

func planKeys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
