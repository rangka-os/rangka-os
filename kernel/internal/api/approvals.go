package api

import (
	"encoding/json"
	"net/http"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// approvalItem is a request as the API shows it: the stored fields plus whether the caller may decide it now.
func approvalItem(a engine.Actor, ap *store.Approval) map[string]any {
	b, _ := json.Marshal(ap)
	out := map[string]any{}
	json.Unmarshal(b, &out)
	if ap.Label == nil {
		out["label"] = map[string]any{}
	}
	out["can_decide"] = engine.CanDecide(a, ap)
	return out
}

func approvalItems(a engine.Actor, aps []store.Approval) []map[string]any {
	out := make([]map[string]any, 0, len(aps))
	for i := range aps {
		out = append(out, approvalItem(a, &aps[i]))
	}
	return out
}

// writeHeld answers 202 for a verb that waits for approval: the first pending request, all of them, the record.
func writeHeld(w http.ResponseWriter, a engine.Actor, aps []store.Approval, rec store.Record) {
	first := map[string]any{}
	for i := range aps {
		if first = approvalItem(a, &aps[i]); aps[i].State == "pending" {
			break
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"approval": first, "approvals": approvalItems(a, aps), "record": rec})
}

func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request, c *caller) {
	q := r.URL.Query()
	rows, total, err := s.Engine.ListApprovals(r.Context(), c.actor, store.ApprovalFilter{
		State: q.Get("state"), Model: q.Get("model"), Name: q.Get("name"),
		Limit: intParam(r, "limit", 50), Offset: intParam(r, "offset", 0),
	})
	if err != nil {
		fail(w, err)
		return
	}
	items := approvalItems(c.actor, rows)
	for i := range rows {
		if sum := s.Engine.ApprovalSummary(r.Context(), c.actor, &rows[i]); sum != nil {
			items[i]["summary"] = sum
		}
	}
	writeJSON(w, 200, map[string]any{"data": items, "total": total})
}

func (s *Server) getApproval(w http.ResponseWriter, r *http.Request, c *caller) {
	ap, err := s.Engine.GetApproval(r.Context(), c.actor, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	item := approvalItem(c.actor, ap)
	if sum := s.Engine.ApprovalSummary(r.Context(), c.actor, ap); sum != nil {
		item["summary"] = sum
	}
	writeJSON(w, 200, item)
}

func (s *Server) decideApproval(approve bool) func(w http.ResponseWriter, r *http.Request, c *caller) {
	return func(w http.ResponseWriter, r *http.Request, c *caller) { s.decide(w, r, c, approve) }
}

func (s *Server) decide(w http.ResponseWriter, r *http.Request, c *caller, approve bool) {
	body, ok := readObject(w, r)
	if !ok {
		return
	}
	var d *engine.Decision
	var err error
	if approve {
		d, err = s.Engine.Approve(r.Context(), c.actor, r.PathValue("id"))
	} else {
		reason, _ := body["reason"].(string)
		d, err = s.Engine.Reject(r.Context(), c.actor, r.PathValue("id"), reason)
	}
	if err != nil {
		fail(w, err)
		return
	}
	out := map[string]any{"approval": approvalItem(c.actor, &d.Approval)}
	if d.Record != nil {
		out["record"] = d.Record
	}
	if d.Action != nil {
		out["action"] = d.Action
	}
	if d.Flow != nil {
		out["flow"] = d.Flow
	}
	writeJSON(w, 200, out)
}
