package api

import (
	"net/http"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
)

// maxSubmitMany bounds one POST /api/v1/r/{model}/submit (each document is its own transaction).
const maxSubmitMany = 100

// submitMany submits several drafts of one model: POST /api/v1/r/{model}/submit {names: [...], force}. Each draft
// is submitted on its own, exactly as POST .../{name}/submit would (rules, approvals, numbering, hooks, flows), so
// one refused draft does not stop the others. The answer is 200 with one result per name in the order given:
// status 200 (submitted; new_name is its number), 202 (held for approval; approval is the pending request) or the
// error status and body the single submit would answer. A retry is safe without an Idempotency-Key (the header
// is not used here): a submitted draft got its number, so its draft name answers 404 not_found and its number
// 409 not_draft; a held draft reuses its pending request. Card t_35afb0bc.
func (s *Server) submitMany(w http.ResponseWriter, r *http.Request, c *caller) {
	body, ok := readObject(w, r)
	if !ok {
		return
	}
	list, _ := body["names"].([]any)
	var names []string
	seen := map[string]bool{}
	for _, x := range list {
		n, ok := x.(string)
		if !ok || n == "" {
			writeError(w, http.StatusBadRequest, "bad_names", "names must be a list of record names")
			return
		}
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	if len(names) == 0 || len(names) > maxSubmitMany {
		writeError(w, http.StatusBadRequest, "bad_names", "names must hold 1 to 100 record names")
		return
	}
	force, _ := body["force"].(bool)
	model := r.PathValue("model")
	results := make([]map[string]any, 0, len(names))
	counts := map[string]int{"submitted": 0, "held": 0, "failed": 0}
	for _, n := range names {
		if r.Context().Err() != nil {
			break // the caller went away: the rest stay drafts
		}
		res, err := s.Engine.Submit(r.Context(), c.actor, model, n, force)
		item := map[string]any{"name": n}
		switch {
		case err != nil:
			st, b := failBody(err)
			item["status"], item["error"] = st, errorOf(b)
			counts["failed"]++
		case res.Held:
			item["status"] = http.StatusAccepted
			for i := range res.Approvals {
				if res.Approvals[i].State == "pending" {
					item["approval"] = approvalItem(c.actor, &res.Approvals[i])
					break
				}
			}
			counts["held"]++
		default:
			item["status"], item["new_name"] = http.StatusOK, res.Record["name"]
			if len(res.Warnings) > 0 {
				item["warnings"] = res.Warnings
			}
			counts["submitted"]++
		}
		results = append(results, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "submitted": counts["submitted"], "held": counts["held"], "failed": counts["failed"]})
}

// errorOf is the "error" object of a failBody body (a validation error is kept whole).
func errorOf(b any) any {
	switch v := b.(type) {
	case errorBody:
		return v.Error
	case map[string]any:
		if e, ok := v["error"]; ok {
			return e
		}
	case *engine.ValidationError:
		return v
	}
	return b
}
