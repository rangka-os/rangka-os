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
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
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
	msgRecordTaken          = "Data karyawan ini sudah terhubung ke akun lain, atau anggota ini sudah punya data karyawan."
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

// memberToken admits an API token with members:write whose owner is still an owner of its organisation. It
// answers when it refuses.
func memberToken(w http.ResponseWriter, c *caller) bool {
	if c.p.Via != "token" || !c.p.HasScope(auth.ScopeMembersWrite) || !isOwner(c) || c.actor.Org == "" {
		writeError(w, http.StatusForbidden, "forbidden", msgMembersForbidden)
		return false
	}
	return true
}

// memberGuard admits an API token with members:write whose owner is still an owner of its organisation, and
// resolves {upstream}. It answers when it refuses.
func (s *Server) memberGuard(w http.ResponseWriter, r *http.Request, c *caller) (*auth.Upstream, bool) {
	if !memberToken(w, c) {
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
		Record  string    `json:"record"` // an existing user record to link (contract 0055 §3)
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
	in := memberInput{Email: email, Name: body.Name, Role: body.Role, Outlets: body.Outlets, Record: body.Record,
		actor: c.p.User.Email, via: "token", note: "member set by API token",
		link: &memberLink{issuer: up.Issuer, upstream: up.ID, subject: subject}}
	var userID int64
	var created bool
	err := s.memberTx(ctx, func(tx *pg.Tx) error {
		var err error
		userID, created, err = s.writeMember(ctx, tx, org, in)
		return err
	})
	if err != nil {
		memberFail(w, err)
		return
	}
	s.syncMemberRecord(ctx, org, userID)
	writeJSON(w, http.StatusOK, map[string]any{"user_id": userID, "created": created})
}

// checkRole refuses (422 unknown_role) a role missing from the registry, and owner: owner is never given, changed or
// removed through the member routes.
func (s *Server) checkRole(role string) error {
	if role == "" || role == "owner" || s.Engine.Reg().Roles[role] == nil {
		return &memberError{http.StatusUnprocessableEntity, "unknown_role", fmt.Sprintf(`Peran "%s" tidak ada di usaha ini.`, role)}
	}
	return nil
}

// memberInput is one member write: the Member API's PUT (by upstream subject) and the Tim routes (by e-mail, or by
// account for a change) share it.
type memberInput struct {
	Email, Name, Role string    // Email valid and lowercase; Role known and not owner (checked by the caller)
	Outlets           *[]string // nil keeps an existing member's outlets (none for a new one)
	Record            string    // an existing user record to link (contract 0055 §3)

	userID int64       // the account, resolved by the caller (Tim PATCH); 0 resolves by link or e-mail
	link   *memberLink // the upstream subject (Member API): resolve by it, and link it to the account
	actor  string      // audit actor (the owner's e-mail)
	via    string      // audit via: token or team
	note   string      // audit message
}

// memberLink is the upstream subject a Member API call is keyed by.
type memberLink struct{ issuer, upstream, subject string }

// writeMember is the checked member write of contract 0043 §2 inside the caller's transaction: it validates the
// outlets, resolves the target writing nothing yet, refuses an owner (here or anywhere), an account another
// organisation holds, an e-mail collision and a new person past the plan's limit, claims the record, upserts the
// membership, links the subject, revokes on a real change and audits. The record sync runs after the commit
// (syncMemberRecord). created reports a new account.
func (s *Server) writeMember(ctx context.Context, tx *pg.Tx, org string, in memberInput) (userID int64, created bool, err error) {
	reg := s.Engine.Reg()
	if in.Outlets != nil {
		for _, o := range *in.Outlets {
			found := false
			if reg.Model("outlet") != nil {
				if found, err = store.Exists(ctx, tx, org, "outlet", o); err != nil {
					return 0, false, err
				}
			}
			if !found {
				return 0, false, &memberError{http.StatusUnprocessableEntity, "unknown_outlet", fmt.Sprintf(`Outlet "%s" tidak ditemukan.`, o)}
			}
		}
	}

	// resolve the target, writing nothing yet
	email := in.Email
	target, linked := in.userID, in.userID != 0
	byEmail := func() error {
		res, err := tx.Query(ctx, "select id from rk_users where email = $1", email)
		if err != nil || len(res.Rows) == 0 {
			return err
		}
		target = res.Rows[0][0].(int64)
		if in.link != nil {
			res, err = tx.Query(ctx, "select 1 from rk_identities where issuer = $1 and user_id = $2 and subject <> $3 limit 1", in.link.issuer, target, in.link.subject)
			if err != nil {
				return err
			}
			if len(res.Rows) > 0 {
				return &memberError{http.StatusConflict, "identity_conflict", msgIdentityConflict}
			}
		}
		// an account found by e-mail is taken only when no other organisation holds it: linking a subject (or
		// handing a sign-in) to another organisation's person would hand that person's access there over
		if err := ownerAnywhere(ctx, tx, target); err != nil {
			return err
		}
		return s.accountOnlyHere(ctx, tx, target, org)
	}
	switch {
	case linked:
	case in.link != nil:
		uid, err := auth.UserByIdentity(ctx, tx, in.link.issuer, in.link.subject)
		switch {
		case err == nil:
			target, linked = uid, true
		case errors.Is(err, store.ErrNotFound):
			if err := byEmail(); err != nil {
				return 0, false, err
			}
		default:
			return 0, false, err
		}
	default:
		if err := byEmail(); err != nil {
			return 0, false, err
		}
	}
	targetEmail, m := email, membership{}
	if target != 0 {
		res, err := tx.Query(ctx, "select email from rk_users where id = $1", target)
		if err != nil {
			return 0, false, err
		}
		if len(res.Rows) == 0 {
			return 0, false, store.ErrNotFound
		}
		targetEmail = res.Rows[0][0].(string)
		if m, err = loadMembership(ctx, tx, org, target); err != nil {
			return 0, false, err
		}
	}

	// refuse before writing: an owner here or in any organisation (a linked subject included)
	if target != 0 {
		if err := ownerAnywhere(ctx, tx, target); err != nil {
			return 0, false, err
		}
	}
	if linked && targetEmail != email {
		// the e-mail is the account's, not this membership's: changed only for a person no other organisation holds
		if err := s.accountOnlyHere(ctx, tx, target, org); err != nil {
			return 0, false, err
		}
		res, err := tx.Query(ctx, "select 1 from rk_users where email = $1 and id <> $2", email, target)
		if err != nil {
			return 0, false, err
		}
		if len(res.Rows) > 0 {
			return 0, false, &memberError{http.StatusConflict, "email_taken", msgEmailTaken}
		}
	}
	if err := plan.CheckNewPerson(ctx, tx, org, targetEmail); err != nil { // an existing member is not a new person
		return 0, false, err
	}
	if in.Record != "" {
		if err := s.claimMemberRecord(ctx, tx, org, in.Record, target); err != nil {
			return 0, false, err
		}
	}

	// write
	if linked && targetEmail != email {
		if _, err := tx.Query(ctx, "update rk_users set email = $1 where id = $2", email, target); err != nil {
			return 0, false, err
		}
	}
	outlets := []string{}
	switch {
	case in.Outlets != nil:
		outlets = dedupe(*in.Outlets)
	case m.member:
		outlets = m.outlets
	}
	userID, created, err = auth.UpsertMember(ctx, tx, email, strings.TrimSpace(in.Name), org, in.Role, outlets)
	if errors.Is(err, auth.ErrOwnerProtected) {
		return 0, false, &memberError{http.StatusConflict, "owner_protected", msgOwnerProtected}
	}
	if err != nil {
		return 0, false, err
	}
	if target != 0 && userID != target {
		return 0, false, fmt.Errorf("member write: upsert wrote user %d, resolved %d", userID, target)
	}
	if in.Record != "" {
		// the sync looks rk_members.record up first, so it links this record instead of making one
		if _, err := tx.Query(ctx, "update rk_members set record = $3 where org_id = $1 and user_id = $2", org, userID, in.Record); err != nil {
			return 0, false, err
		}
	}
	if in.link != nil && !linked {
		if err := auth.LinkIdentity(ctx, tx, in.link.issuer, in.link.subject, userID); errors.Is(err, auth.ErrConflict) {
			return 0, false, &memberError{http.StatusConflict, "identity_conflict", msgIdentityConflict}
		} else if err != nil {
			return 0, false, err
		}
	}
	// a real change revokes; so does joining again, since a session or token left from an earlier membership
	// (ended without revocation, e.g. rangka user remove) must not come back to life with the new one
	revoked := (m.member && (m.role != in.Role || !sameSet(m.outlets, outlets))) || (!m.member && target != 0)
	if revoked {
		if err := s.Auth.RevokeMember(ctx, tx, userID, org); err != nil {
			return 0, false, err
		}
	}
	data := map[string]any{"via": in.via, "role": in.Role, "outlets": outlets, "record": in.Record, "created": created, "revoked": revoked}
	if in.link != nil {
		data["upstream"], data["subject"] = in.link.upstream, in.link.subject
	}
	return userID, created, store.Audit(ctx, tx, org, in.actor, "member.upsert", "user/"+email, in.note, data)
}

// claimMemberRecord refuses a record that is not an existing user record of org (unknown_record; also when the
// registry has no member record model), that is another account's, or that would move a member off the record it
// already has (record_taken). userID 0 is an account not created yet.
func (s *Server) claimMemberRecord(ctx context.Context, q pg.Querier, org, record string, userID int64) error {
	err := engine.ErrUnknownRecord
	if s.Engine.HasMemberRecord() {
		err = engine.MemberRecordClaim(ctx, q, org, record, userID)
	}
	switch {
	case errors.Is(err, engine.ErrUnknownRecord):
		return &memberError{http.StatusUnprocessableEntity, "unknown_record", fmt.Sprintf(`Data karyawan "%s" tidak ditemukan.`, record)}
	case errors.Is(err, engine.ErrRecordTaken):
		return &memberError{http.StatusUnprocessableEntity, "record_taken", msgRecordTaken}
	}
	return err
}

// memberOptions is GET /api/v1/members/options (contract 0055 §3): what the host's Tim page picks from. Roles that
// can be given (owner never), active outlets (every outlet when the model has no active field), and employee records
// with access none that can be invited. Without the outlet model or the member record model, that list is empty.
func (s *Server) memberOptions(w http.ResponseWriter, r *http.Request, c *caller) {
	if !memberToken(w, c) {
		return
	}
	org, reg := c.actor.Org, s.Engine.Reg()
	ctx := pg.WithOrg(r.Context(), org)

	roles := []map[string]any{}
	names := make([]string, 0, len(reg.Roles))
	for n := range reg.Roles {
		if n != "owner" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		label := meta.LabelOf(meta.AsMap(reg.Roles[n].Data)["label"]).Text()
		if label == "" {
			label = n
		}
		roles = append(roles, map[string]any{"name": n, "label": label})
	}

	outlets := []map[string]any{}
	if m := reg.Model("outlet"); m != nil {
		title, code, where := "name", "''", ""
		if m.TitleField != "" && m.Field(m.TitleField) != nil {
			title = "coalesce(" + pg.QuoteIdent(m.TitleField) + "::text, '')"
		}
		if m.Field("code") != nil {
			code = "coalesce(code::text, '')"
		}
		if f := m.Field("active"); f != nil && f.Type == "check" {
			where = " and active is not false" // unset reads as the field's default, active
		}
		res, err := s.DB.Query(ctx, fmt.Sprintf("select name, %s, %s from %s where org_id = $1%s order by name",
			title, code, pg.QuoteIdent(store.TableName(m.Name)), where), org)
		if err != nil {
			fail(w, err)
			return
		}
		for _, row := range res.Rows {
			label := row[1].(string)
			if label == "" {
				label = row[0].(string)
			}
			outlets = append(outlets, map[string]any{"name": row[0], "label": label, "code": row[2]})
		}
	}

	employees := []map[string]any{}
	// apps from before the access field have a user model that cannot list employees without access
	if um := s.Engine.Reg().Model("user"); s.Engine.HasMemberRecord() && um.Field("access") != nil && um.Field("full_name") != nil {
		res, err := s.DB.Query(ctx, fmt.Sprintf("select name, coalesce(full_name, ''), access from %s where org_id = $1 and access = 'none' order by name",
			pg.QuoteIdent(store.TableName("user"))), org)
		if err != nil {
			fail(w, err)
			return
		}
		for _, row := range res.Rows {
			employees = append(employees, map[string]any{"name": row[0], "full_name": row[1], "access": row[2]})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": roles, "outlets": outlets, "employees": employees})
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
		ok, err := s.removeMember(ctx, tx, org, uid, c.p.User.Email, "member removed by API token",
			map[string]any{"via": "token", "upstream": up.ID, "subject": subject})
		if ok {
			removed = uid
		}
		return err
	})
	if err != nil {
		memberFail(w, err)
		return
	}
	s.syncMemberRecord(ctx, org, removed) // the record stays, marked removed
	w.WriteHeader(http.StatusNoContent)
}

// removeMember ends userID's membership in org inside the caller's transaction (contract 0043 §2, §4): an owner is
// refused (owner_protected), the person's sessions and tokens there are revoked, unused sign-in links there end, and
// the removal is audited with data plus the old role and outlets. removed is false when there was no membership.
func (s *Server) removeMember(ctx context.Context, tx *pg.Tx, org string, userID int64, actor, note string, data map[string]any) (removed bool, err error) {
	m, err := loadMembership(ctx, tx, org, userID)
	if err != nil || !m.member {
		return false, err
	}
	if m.role == "owner" {
		return false, &memberError{http.StatusConflict, "owner_protected", msgOwnerProtected}
	}
	if err := auth.RemoveMember(ctx, tx, org, userID); err != nil {
		return false, err
	}
	if err := s.Auth.RevokeMember(ctx, tx, userID, org); err != nil {
		return false, err
	}
	if err := auth.DropSigninLinks(ctx, tx, userID, org); err != nil {
		return false, err
	}
	res, err := tx.Query(ctx, "select email from rk_users where id = $1", userID)
	if err != nil {
		return false, err
	}
	data["role"], data["outlets"] = m.role, m.outlets
	return true, store.Audit(ctx, tx, org, actor, "member.remove", "user/"+res.Rows[0][0].(string), note, data)
}
