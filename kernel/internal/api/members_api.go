package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/plan"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Member API (contract 0043): the host that decides who is a member (Rangka Cloud) creates, changes and removes the
// non-owner members of one organisation, keyed by the upstream provider's subject. Only an API token with
// members:write reaches it, and it acts in the organisation the token was minted in. Each call resolves its target
// and refuses before it writes; the whole call is one transaction, so a refusal or an error leaves nothing written.

const (
	msgMembersForbidden     = "Anggota hanya bisa diatur dengan token API pemilik usaha yang punya izin members:write."
	msgMembersMintForbidden = "Token members:write hanya bisa dibuat oleh pemilik usaha yang masuk lewat browser."
	msgOwnerProtected       = "Pemilik usaha tidak bisa dibuat, diubah, atau dikeluarkan lewat API anggota."
	msgIdentityConflict     = "E-mail ini milik akun yang sudah terhubung ke akun lain di penyedia ini."
	msgEmailTaken           = "E-mail ini sudah dipakai akun lain."
	msgUnknownUpstream      = "Penyedia akun tidak ditemukan."
	msgAccountElsewhere     = "Akun dengan e-mail ini juga dipakai di usaha lain, jadi tidak bisa diatur lewat API anggota usaha ini."
)

// memberError is a refusal of the member API: status, code and copy.
type memberError struct {
	status        int
	code, message string
}

func (e *memberError) Error() string { return e.code + ": " + e.message }

func isOwner(c *caller) bool {
	for _, r := range c.actor.Roles {
		if r == "owner" {
			return true
		}
	}
	return false
}

// memberGuard admits an API token with members:write whose owner is still an owner of its organisation, and
// resolves {upstream}. It answers when it refuses.
func (s *Server) memberGuard(w http.ResponseWriter, r *http.Request, c *caller) (*auth.Upstream, bool) {
	if c.p.Via != "token" || !c.p.HasScope(auth.ScopeMembersWrite) || !isOwner(c) || c.actor.Org == "" {
		writeError(w, http.StatusForbidden, "forbidden", msgMembersForbidden)
		return nil, false
	}
	up := s.Auth.Upstream(r.PathValue("upstream"))
	if up == nil {
		writeError(w, http.StatusNotFound, "unknown_upstream", msgUnknownUpstream)
		return nil, false
	}
	return up, true
}

// memberTx runs fn in one transaction, serialised with every other member API call (so two pushes cannot both
// take the plan's last place or race on one subject), and commits only when fn succeeds.
func (s *Server) memberTx(ctx context.Context, fn func(tx *pg.Tx) error) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Query(ctx, "select pg_advisory_xact_lock(hashtext('rk_member_api'))"); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func memberFail(w http.ResponseWriter, err error) {
	var me *memberError
	var pe *plan.Error
	switch {
	case errors.As(err, &me):
		writeError(w, me.status, me.code, me.message)
	case errors.As(err, &pe):
		writeError(w, http.StatusForbidden, pe.Code, pe.Msg)
	default:
		fail(w, err)
	}
}

// validEmail is a bare address (no display name), lowercased and trimmed.
func validEmail(raw string) (string, bool) {
	e := strings.ToLower(strings.TrimSpace(raw))
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e || !strings.Contains(e, "@") {
		return "", false
	}
	return e, true
}

// membership is a user's row in rk_members for one organisation.
type membership struct {
	member  bool
	role    string
	outlets []string
}

func loadMembership(ctx context.Context, q pg.Querier, org string, userID int64) (membership, error) {
	res, err := q.Query(ctx, "select role, outlets from rk_members where org_id = $1 and user_id = $2", org, userID)
	if err != nil || len(res.Rows) == 0 {
		return membership{}, err
	}
	return membership{member: true, role: res.Rows[0][0].(string), outlets: textList(res.Rows[0][1])}, nil
}

func textList(v any) []string {
	out := []string{}
	switch a := v.(type) {
	case []any:
		for _, x := range a {
			out = append(out, fmt.Sprint(x))
		}
	case []string:
		out = append(out, a...)
	}
	return out
}

// sameSet reports whether a and b hold the same values (order and repeats aside).
func sameSet(a, b []string) bool {
	norm := func(x []string) string {
		m := map[string]bool{}
		for _, v := range x {
			m[v] = true
		}
		out := make([]string, 0, len(m))
		for v := range m {
			out = append(out, v)
		}
		sort.Strings(out)
		return strings.Join(out, "\x00")
	}
	return norm(a) == norm(b)
}

// ownerAnywhere refuses an account that is an owner of any organisation: the owner is never created, changed or
// removed through this API, and an owner's account is never linked or edited from another organisation.
func ownerAnywhere(ctx context.Context, q pg.Querier, userID int64) error {
	res, err := q.Query(ctx, "select 1 from rk_members where user_id = $1 and role = 'owner' limit 1", userID)
	if err != nil {
		return err
	}
	if len(res.Rows) > 0 {
		return &memberError{http.StatusConflict, "owner_protected", msgOwnerProtected}
	}
	return nil
}

// accountOnlyHere refuses a change to the account itself (its e-mail) when the account has a membership outside org
// (an owner elsewhere included): the account is not this organisation's alone.
func (s *Server) accountOnlyHere(ctx context.Context, q pg.Querier, userID int64, org string) error {
	res, err := q.Query(ctx, "select 1 from rk_members where user_id = $1 and org_id <> $2 limit 1", userID, org)
	if err != nil {
		return err
	}
	if len(res.Rows) > 0 {
		return &memberError{http.StatusConflict, "account_elsewhere", msgAccountElsewhere}
	}
	return nil
}

// dedupe keeps the first of each value, in order.
func dedupe(in []string) []string {
	seen, out := map[string]bool{}, []string{}
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// putMember is PUT /api/v1/members/{upstream}/{subject} (contract 0043 §2).
func (s *Server) putMember(w http.ResponseWriter, r *http.Request, c *caller) {
	up, ok := s.memberGuard(w, r, c)
	if !ok {
		return
	}
	subject := r.PathValue("subject")
	var body struct {
		Email   string    `json:"email"`
		Name    string    `json:"name"`
		Role    string    `json:"role"`
		Outlets *[]string `json:"outlets"`
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
	reg := s.Engine.Reg()
	if body.Role == "" || body.Role == "owner" || reg.Roles[body.Role] == nil {
		writeError(w, http.StatusUnprocessableEntity, "unknown_role", fmt.Sprintf(`Peran "%s" tidak ada di usaha ini.`, body.Role))
		return
	}
	org := c.actor.Org
	ctx := r.Context()
	var userID int64
	var created bool
	err := s.memberTx(ctx, func(tx *pg.Tx) error {
		if body.Outlets != nil {
			for _, o := range *body.Outlets {
				found := false
				if reg.Model("outlet") != nil {
					var err error
					if found, err = store.Exists(ctx, tx, org, "outlet", o); err != nil {
						return err
					}
				}
				if !found {
					return &memberError{http.StatusUnprocessableEntity, "unknown_outlet", fmt.Sprintf(`Outlet "%s" tidak ditemukan.`, o)}
				}
			}
		}

		// resolve the target, writing nothing yet
		target, linked := int64(0), false
		uid, err := auth.UserByIdentity(ctx, tx, up.Issuer, subject)
		switch {
		case err == nil:
			target, linked = uid, true
		case errors.Is(err, store.ErrNotFound):
			res, err := tx.Query(ctx, "select id from rk_users where email = $1", email)
			if err != nil {
				return err
			}
			if len(res.Rows) > 0 {
				target = res.Rows[0][0].(int64)
				res, err = tx.Query(ctx, "select 1 from rk_identities where issuer = $1 and user_id = $2 and subject <> $3 limit 1", up.Issuer, target, subject)
				if err != nil {
					return err
				}
				if len(res.Rows) > 0 {
					return &memberError{http.StatusConflict, "identity_conflict", msgIdentityConflict}
				}
				// an account found by e-mail is linked only when no other organisation holds it: linking a subject to
				// another organisation's person would hand that person's access there to whoever holds the subject
				if err := ownerAnywhere(ctx, tx, target); err != nil {
					return err
				}
				if err := s.accountOnlyHere(ctx, tx, target, org); err != nil {
					return err
				}
			}
		default:
			return err
		}
		targetEmail, m := email, membership{}
		if target != 0 {
			res, err := tx.Query(ctx, "select email from rk_users where id = $1", target)
			if err != nil {
				return err
			}
			targetEmail = res.Rows[0][0].(string)
			if m, err = loadMembership(ctx, tx, org, target); err != nil {
				return err
			}
		}

		// refuse before writing: an owner here or in any organisation (a linked subject included)
		if target != 0 {
			if err := ownerAnywhere(ctx, tx, target); err != nil {
				return err
			}
		}
		if linked && targetEmail != email {
			// the e-mail is the account's, not this membership's: changed only for a person no other organisation holds
			if err := s.accountOnlyHere(ctx, tx, target, org); err != nil {
				return err
			}
			res, err := tx.Query(ctx, "select 1 from rk_users where email = $1 and id <> $2", email, target)
			if err != nil {
				return err
			}
			if len(res.Rows) > 0 {
				return &memberError{http.StatusConflict, "email_taken", msgEmailTaken}
			}
		}
		if err := plan.CheckNewPerson(ctx, tx, org, targetEmail); err != nil { // an existing member is not a new person
			return err
		}

		// write
		if linked && targetEmail != email {
			if _, err := tx.Query(ctx, "update rk_users set email = $1 where id = $2", email, target); err != nil {
				return err
			}
		}
		outlets := []string{}
		switch {
		case body.Outlets != nil:
			outlets = dedupe(*body.Outlets)
		case m.member:
			outlets = m.outlets
		}
		userID, created, err = auth.UpsertMember(ctx, tx, email, strings.TrimSpace(body.Name), org, body.Role, outlets)
		if errors.Is(err, auth.ErrOwnerProtected) {
			return &memberError{http.StatusConflict, "owner_protected", msgOwnerProtected}
		}
		if err != nil {
			return err
		}
		if target != 0 && userID != target {
			return fmt.Errorf("member api: upsert wrote user %d, resolved %d", userID, target)
		}
		if !linked {
			if err := auth.LinkIdentity(ctx, tx, up.Issuer, subject, userID); errors.Is(err, auth.ErrConflict) {
				return &memberError{http.StatusConflict, "identity_conflict", msgIdentityConflict}
			} else if err != nil {
				return err
			}
		}
		// a real change revokes; so does joining again, since a session or token left from an earlier membership
		// (ended without revocation, e.g. rangka user remove) must not come back to life with the new one
		revoked := (m.member && (m.role != body.Role || !sameSet(m.outlets, outlets))) || (!m.member && target != 0)
		if revoked {
			if err := s.Auth.RevokeMember(ctx, tx, userID, org); err != nil {
				return err
			}
		}
		return store.Audit(ctx, tx, org, c.p.User.Email, "member.upsert", "user/"+email, "member set by API token",
			map[string]any{"via": "token", "upstream": up.ID, "subject": subject, "role": body.Role, "outlets": outlets, "created": created, "revoked": revoked})
	})
	if err != nil {
		memberFail(w, err)
		return
	}
	s.syncMemberRecord(ctx, org, userID)
	writeJSON(w, http.StatusOK, map[string]any{"user_id": userID, "created": created})
}

// syncMemberRecord keeps the member's user record in step after the membership committed (contract 0055 §2, §5).
// The member write stands either way: a failure is logged and the repair loop syncs the record later.
func (s *Server) syncMemberRecord(ctx context.Context, org string, userID int64) {
	if s.Engine == nil || userID == 0 {
		return
	}
	if _, err := s.Engine.SyncMemberRecord(ctx, org, userID); err != nil && s.Log != nil {
		s.Log("member record: sync of account %d in %s failed (the repair retries): %v", userID, org, err)
	}
}

// deleteMember is DELETE /api/v1/members/{upstream}/{subject} (contract 0043 §2): removes the membership in the
// token's organisation and revokes the person there. The account and its identity link stay. Idempotent.
func (s *Server) deleteMember(w http.ResponseWriter, r *http.Request, c *caller) {
	up, ok := s.memberGuard(w, r, c)
	if !ok {
		return
	}
	subject, org, ctx := r.PathValue("subject"), c.actor.Org, r.Context()
	var removed int64
	err := s.memberTx(ctx, func(tx *pg.Tx) error {
		uid, err := auth.UserByIdentity(ctx, tx, up.Issuer, subject)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		m, err := loadMembership(ctx, tx, org, uid)
		if err != nil || !m.member {
			return err
		}
		if m.role == "owner" {
			return &memberError{http.StatusConflict, "owner_protected", msgOwnerProtected}
		}
		if err := auth.RemoveMember(ctx, tx, org, uid); err != nil {
			return err
		}
		removed = uid
		if err := s.Auth.RevokeMember(ctx, tx, uid, org); err != nil {
			return err
		}
		res, err := tx.Query(ctx, "select email from rk_users where id = $1", uid)
		if err != nil {
			return err
		}
		email := res.Rows[0][0].(string)
		return store.Audit(ctx, tx, org, c.p.User.Email, "member.remove", "user/"+email, "member removed by API token",
			map[string]any{"via": "token", "upstream": up.ID, "subject": subject, "role": m.role, "outlets": m.outlets})
	})
	if err != nil {
		memberFail(w, err)
		return
	}
	s.syncMemberRecord(ctx, org, removed) // the record stays, marked removed
	w.WriteHeader(http.StatusNoContent)
}
