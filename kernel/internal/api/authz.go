package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/changeset"
)

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email, Password, Org string
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "bad_json", err.Error())
		return
	}
	org := body.Org
	if org == "" {
		org = s.Org
	}
	tok, u, err := s.Auth.Login(r.Context(), body.Email, body.Password, org)
	if errors.Is(err, auth.ErrBadLogin) {
		writeError(w, 401, "bad_login", err.Error())
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	http.SetCookie(w, s.Auth.SessionCookie(tok, int(s.Auth.SessionTTL.Seconds())))
	writeJSON(w, 200, map[string]any{"user": u})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.CookieName); err == nil {
		s.Auth.Logout(r.Context(), c.Value)
	}
	http.SetCookie(w, s.Auth.SessionCookie("", -1))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, c *caller) {
	approver := false
	for _, role := range c.p.User.Roles {
		approver = approver || s.Changes.Approvers()[role]
	}
	writeJSON(w, 200, map[string]any{"user": c.p.User, "via": c.p.Via, "approver": approver, "human": c.p.Human()})
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request, c *caller) {
	var body struct {
		Name          string `json:"name"`
		ExpiresInDays int    `json:"expires_in_days"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "bad_json", err.Error())
		return
	}
	if c.p.User.ID == 0 {
		writeError(w, 403, "no_user", "the dev-open demo user cannot create tokens; seed the database first")
		return
	}
	if body.Name == "" {
		body.Name = "api"
	}
	tok, id, err := s.Auth.CreateToken(r.Context(), c.p.User, body.Name, time.Duration(body.ExpiresInDays)*24*time.Hour)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": body.Name, "token": tok, "note": "shown once; send as Authorization: Bearer " + tok[:6] + "…"})
}

// ---------- change sets

func csFail(w http.ResponseWriter, err error) {
	var ce *changeset.Error
	if errors.As(err, &ce) {
		if ce.Status == 422 {
			if d, ok := ce.Detail.(map[string]any); ok {
				writeJSON(w, 422, d)
				return
			}
		}
		body := map[string]any{"error": map[string]any{"code": ce.Code, "message": ce.Msg}}
		if cs, ok := ce.Detail.(*changeset.ChangeSet); ok {
			body["changeset"] = cs
		}
		writeJSON(w, ce.Status, body)
		return
	}
	fail(w, err)
}

func (s *Server) listChangesets(w http.ResponseWriter, r *http.Request, c *caller) {
	list, err := s.Changes.List(r.Context(), c.actor.Org, r.URL.Query().Get("status"), intParam(r, "limit", 50))
	if err != nil {
		csFail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": list})
}

func (s *Server) proposeChangeset(w http.ResponseWriter, r *http.Request, c *caller) {
	body, ok := readObject(w, r)
	if !ok {
		return
	}
	cs, err := s.Changes.Propose(r.Context(), ChangeActor(c.p), body)
	if err != nil {
		csFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, cs)
}

func (s *Server) getChangeset(w http.ResponseWriter, r *http.Request, c *caller) {
	cs, err := s.Changes.Get(r.Context(), c.actor.Org, r.PathValue("id"))
	if err != nil {
		csFail(w, err)
		return
	}
	writeJSON(w, 200, cs)
}

func (s *Server) changesetVerb(w http.ResponseWriter, r *http.Request, c *caller) {
	var body struct {
		Note string `json:"note"`
	}
	readJSON(r, &body)
	a := ChangeActor(c.p)
	id := r.PathValue("id")
	var cs *changeset.ChangeSet
	var err error
	switch r.PathValue("verb") {
	case "validate":
		cs, err = s.Changes.Validate(r.Context(), a, id)
	case "preview":
		cs, err = s.Changes.Preview(r.Context(), a, id)
	case "approve":
		cs, err = s.Changes.Approve(r.Context(), a, id, body.Note)
	case "reject":
		cs, err = s.Changes.Reject(r.Context(), a, id, body.Note)
	case "apply":
		cs, err = s.Changes.Apply(r.Context(), a, id)
	case "revert":
		cs, err = s.Changes.Revert(r.Context(), a, id)
	default:
		writeError(w, 404, "not_found", "unknown change set operation "+r.PathValue("verb"))
		return
	}
	if err != nil {
		csFail(w, err)
		return
	}
	writeJSON(w, 200, cs)
}
