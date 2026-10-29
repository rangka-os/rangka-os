package api

import (
	"net/http"
	"strconv"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
)

func (s *Server) listRecords(w http.ResponseWriter, r *http.Request, c *caller) {
	q := r.URL.Query()
	rows, total, err := s.Engine.List(r.Context(), c.actor, r.PathValue("model"), engine.ListOpts{
		Filter: q.Get("filter"), Sort: q.Get("sort"), Limit: intParam(r, "limit", 50), Offset: intParam(r, "offset", 0), Q: q.Get("q"),
		Children: q.Get("children") == "1" || q.Get("children") == "true",
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows, "total": total})
}

func (s *Server) createRecord(w http.ResponseWriter, r *http.Request, c *caller) {
	body, ok := readObject(w, r)
	if !ok {
		return
	}
	res, err := s.Engine.Create(r.Context(), c.actor, r.PathValue("model"), body)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/r/"+r.PathValue("model")+"/"+res.Record["name"].(string))
	writeJSON(w, http.StatusCreated, recordOut(res))
}

func (s *Server) getRecord(w http.ResponseWriter, r *http.Request, c *caller) {
	rec, err := s.Engine.Get(r.Context(), c.actor, r.PathValue("model"), r.PathValue("name"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, rec)
}

func (s *Server) patchRecord(w http.ResponseWriter, r *http.Request, c *caller) {
	body, ok := readObject(w, r)
	if !ok {
		return
	}
	res, err := s.Engine.Update(r.Context(), c.actor, r.PathValue("model"), r.PathValue("name"), body)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, recordOut(res))
}

func (s *Server) deleteRecord(w http.ResponseWriter, r *http.Request, c *caller) {
	if err := s.Engine.Delete(r.Context(), c.actor, r.PathValue("model"), r.PathValue("name")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) lifecycle(w http.ResponseWriter, r *http.Request, c *caller) {
	model, name := r.PathValue("model"), r.PathValue("name")
	var res *engine.Result
	var err error
	switch r.PathValue("verb") {
	case "submit":
		body, ok := readObject(w, r)
		if !ok {
			return
		}
		force, _ := body["force"].(bool)
		res, err = s.Engine.Submit(r.Context(), c.actor, model, name, force)
	case "cancel":
		res, err = s.Engine.Cancel(r.Context(), c.actor, model, name)
	case "amend":
		res, err = s.Engine.Amend(r.Context(), c.actor, model, name)
		if err == nil {
			writeJSON(w, http.StatusCreated, recordOut(res))
			return
		}
	default:
		writeError(w, 404, "not_found", "unknown operation "+r.PathValue("verb")+" (submit, cancel, amend, actions/<name>)")
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, recordOut(res))
}

func (s *Server) runAction(w http.ResponseWriter, r *http.Request, c *caller) {
	body, ok := readObject(w, r)
	if !ok {
		return
	}
	out, err := s.Engine.RunAction(r.Context(), c.actor, r.PathValue("model"), r.PathValue("name"), r.PathValue("action"), body, engine.ActionOpts{})
	if err != nil {
		fail(w, err)
		return
	}
	if out.Queued {
		w.Header().Set("Location", "/api/v1/system/jobs/"+strconv.FormatInt(out.JobID, 10))
		writeJSON(w, http.StatusAccepted, map[string]any{"job_id": out.JobID, "status": "queued"})
		return
	}
	writeJSON(w, 200, out.Result)
}

func (s *Server) getDashboard(w http.ResponseWriter, r *http.Request, c *caller) {
	d, cached, err := s.Engine.Dashboard(r.Context(), c.actor, r.PathValue("name"), intParam(r, "period", 0))
	if err != nil {
		fail(w, err)
		return
	}
	if cached {
		w.Header().Set("X-Cache", "hit")
	} else {
		w.Header().Set("X-Cache", "miss")
	}
	writeJSON(w, 200, d)
}

// runFlow runs a flow now (an event flow with the request body as its event).
func (s *Server) runFlow(w http.ResponseWriter, r *http.Request, c *caller) {
	body, ok := readObject(w, r)
	if !ok {
		return
	}
	event, _ := body["event"].(map[string]any)
	if event == nil {
		event = body
	}
	res, err := s.Engine.RunFlow(r.Context(), c.actor, r.PathValue("name"), event)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, res)
}
