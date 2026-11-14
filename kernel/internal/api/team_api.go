package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// Pengaturan → Tim (contract 0055 §4): the owner of a self-hosted kernel manages the team with their browser
// session. The writes share the Member API's checks, transaction, revocations and record sync (writeMember,
// removeMember); a new account gets a one-time sign-in link. With system.rk auth.team: host the hosting side owns the
// team and every write here is 403 team_managed_by_host; the list stays readable.

const (
	msgTeamForbidden = "Hanya pemilik usaha yang bisa mengatur tim, lewat browser."
	msgTeamHost      = "Tim usaha ini diatur di Rangka Cloud."
	msgTeamNotMember = "Anggota tidak ditemukan di usaha ini."
)

// teamManagedBy is system.rk auth.team: host or kernel (the default).
func (s *Server) teamManagedBy() string {
	if v, _ := s.Engine.Reg().SystemConfig("auth.team").(string); v == "host" {
		return "host"
	}
	return "kernel"
}

// teamGuard admits an owner signed in with a browser session; write also needs auth.team: kernel. It answers when
// it refuses.
func (s *Server) teamGuard(w http.ResponseWriter, c *caller, write bool) bool {
	if !c.p.Human() || !isOwner(c) || c.actor.Org == "" {
		writeError(w, http.StatusForbidden, "forbidden", msgTeamForbidden)
		return false
	}
	if write && s.teamManagedBy() == "host" {
		writeError(w, http.StatusForbidden, "team_managed_by_host", msgTeamHost)
		return false
	}
	return true
}

// getTeam is GET /api/v1/team: every member of the organisation, owner first. access is pending until the account
// has signed in once, then active (as on the member record).
func (s *Server) getTeam(w http.ResponseWriter, r *http.Request, c *caller) {
	if !s.teamGuard(w, c, false) {
		return
	}
	res, err := s.DB.Query(r.Context(), `select u.id, u.email, u.full_name, m.role, m.outlets, coalesce(m.record, ''), u.signed_in_at is not null
		from rk_members m join rk_users u on u.id = m.user_id where m.org_id = $1 order by (m.role = 'owner') desc, u.email`, c.actor.Org)
	if err != nil {
		fail(w, err)
		return
	}
	members := []map[string]any{}
	for _, row := range res.Rows {
		access := "pending"
		if row[6].(bool) {
			access = "active"
		}
		members = append(members, map[string]any{"user_id": row[0], "email": row[1], "full_name": row[2], "role": row[3],
			"outlets": textList(row[4]), "record": row[5], "access": access})
	}
	writeJSON(w, http.StatusOK, map[string]any{"managed_by": s.teamManagedBy(), "members": members})
}

// postTeam is POST /api/v1/team: adds a person (or changes the member with that e-mail). A new account gets no
// password and a sign-in link, shown only in this answer; an existing account (a member nowhere else) is linked
// without one.
func (s *Server) postTeam(w http.ResponseWriter, r *http.Request, c *caller) {
	if !s.teamGuard(w, c, true) {
		return
	}
	var body struct {
		Email    string    `json:"email"`
		FullName string    `json:"full_name"`
		Role     string    `json:"role"`
		Outlets  *[]string `json:"outlets"`
		Record   string    `json:"record"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "bad_request", "Isi permintaan harus JSON yang valid.")
		return
	}
	email, ok := validEmail(body.Email)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "bad_request", "E-mail wajib diisi dengan alamat yang valid.")
		return
	}
	if err := s.checkRole(body.Role); err != nil {
		memberFail(w, err)
		return
	}
	org, ctx := c.actor.Org, r.Context()
	in := memberInput{Email: email, Name: body.FullName, Role: body.Role, Outlets: body.Outlets, Record: body.Record,
		actor: c.p.User.Email, via: "team", note: "member set in Tim"}
	var userID int64
	var link string
	err := s.memberTx(ctx, func(tx *pg.Tx) error {
		id, created, err := s.writeMember(ctx, tx, org, in)
		if err != nil || !created {
			userID = id
			return err
		}
		userID = id
		link, err = s.Auth.CreateSigninLink(ctx, tx, id, org, c.p.User.Email)
		return err
	})
	if err != nil {
		memberFail(w, err)
		return
	}
	s.syncMemberRecord(ctx, org, userID)
	record := ""
	if res, err := s.DB.Query(ctx, "select coalesce(record, '') from rk_members where org_id = $1 and user_id = $2", org, userID); err == nil && len(res.Rows) > 0 {
		record = res.Rows[0][0].(string)
	}
	out := map[string]any{"user_id": userID, "record": record}
	if link != "" {
		out["signin_link"] = strings.TrimSuffix(s.Auth.Issuer, "/") + "/#/masuk/tautan?t=" + link
	}
	writeJSON(w, http.StatusCreated, out)
}

// teamTarget reads {user_id}; a malformed id answers 404.
func teamTarget(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("user_id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not_found", msgTeamNotMember)
		return 0, false
	}
	return id, true
}

// patchTeam is PATCH /api/v1/team/{user_id}: changes a member's role and/or outlets (an omitted one is kept). A real
// change revokes the person's sessions and tokens here (contract 0043 §4).
func (s *Server) patchTeam(w http.ResponseWriter, r *http.Request, c *caller) {
	if !s.teamGuard(w, c, true) {
		return
	}
	id, ok := teamTarget(w, r)
	if !ok {
		return
	}
	var body struct {
		Role    *string   `json:"role"`
		Outlets *[]string `json:"outlets"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "bad_request", "Isi permintaan harus JSON yang valid.")
		return
	}
	org, ctx := c.actor.Org, r.Context()
	err := s.memberTx(ctx, func(tx *pg.Tx) error {
		m, err := loadMembership(ctx, tx, org, id)
		if err != nil {
			return err
		}
		if !m.member {
			return &memberError{http.StatusNotFound, "not_found", msgTeamNotMember}
		}
		if m.role == "owner" {
			return &memberError{http.StatusConflict, "owner_protected", msgOwnerProtected}
		}
		role := m.role
		if body.Role != nil {
			role = *body.Role
		}
		if err := s.checkRole(role); err != nil {
			return err
		}
		res, err := tx.Query(ctx, "select email from rk_users where id = $1", id)
		if err != nil {
			return err
		}
		_, _, err = s.writeMember(ctx, tx, org, memberInput{Email: res.Rows[0][0].(string), Role: role, Outlets: body.Outlets,
			userID: id, actor: c.p.User.Email, via: "team", note: "member changed in Tim"})
		return err
	})
	if err != nil {
		memberFail(w, err)
		return
	}
	s.syncMemberRecord(ctx, org, id)
	writeJSON(w, http.StatusOK, map[string]any{"user_id": id})
}

// deleteTeam is DELETE /api/v1/team/{user_id}: removes the membership here and revokes the person here. The account
// and its record stay (the record is marked removed). Idempotent; the owner is 409 owner_protected.
func (s *Server) deleteTeam(w http.ResponseWriter, r *http.Request, c *caller) {
	if !s.teamGuard(w, c, true) {
		return
	}
	id, ok := teamTarget(w, r)
	if !ok {
		return
	}
	org, ctx := c.actor.Org, r.Context()
	var removed bool
	err := s.memberTx(ctx, func(tx *pg.Tx) (err error) {
		removed, err = s.removeMember(ctx, tx, org, id, c.p.User.Email, "member removed in Tim", map[string]any{"via": "team"})
		return err
	})
	if err != nil {
		memberFail(w, err)
		return
	}
	if removed {
		s.syncMemberRecord(ctx, org, id)
	}
	w.WriteHeader(http.StatusNoContent)
}

// useSigninLink is POST /auth/link {token, password}: sets the password of a new member once and signs them in.
// Unknown, used and expired links get one answer (400 link_invalid). Shaped like POST /auth/login (shape.go).
func (s *Server) useSigninLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	tok, u, err := s.Auth.UseSigninLink(r.Context(), body.Token, body.Password)
	var pd *auth.ErrPasswordDisabled
	switch {
	case errors.Is(err, auth.ErrLinkInvalid):
		writeError(w, http.StatusBadRequest, "link_invalid", err.Error())
		return
	case errors.Is(err, auth.ErrWeakPassword):
		writeError(w, http.StatusUnprocessableEntity, "weak_password", err.Error())
		return
	case errors.As(err, &pd):
		writeError(w, http.StatusForbidden, "password_disabled", pd.Message)
		return
	case err != nil:
		fail(w, err)
		return
	}
	http.SetCookie(w, s.Auth.SessionCookie(tok, int(s.Auth.SessionTTL.Seconds())))
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}
