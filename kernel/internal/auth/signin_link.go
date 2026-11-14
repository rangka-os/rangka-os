package auth

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Sign-in links (contract 0055 §4): an owner adds a person in Pengaturan → Tim; a new account gets a link, shown once,
// valid SigninLinkTTL and usable once, with which the person sets a password and signs in.

// SigninLinkTTL is how long a sign-in link stays valid.
const SigninLinkTTL = 72 * time.Hour

// SessionLink is rk_sessions.method for a session opened with a sign-in link.
const SessionLink = "link"

// MinPasswordLength is the shortest password a sign-in link accepts (counted in characters).
const MinPasswordLength = 8

var (
	// ErrLinkInvalid covers an unknown, used or expired link, the link of a person no longer a member there, and
	// the link of an account that has a password, an identity link or a membership in another organisation.
	ErrLinkInvalid = errors.New("Tautan masuk ini tidak berlaku. Minta tautan baru ke pemilik usaha.")
	// ErrWeakPassword is a password shorter than MinPasswordLength.
	ErrWeakPassword = errors.New("Kata sandi minimal 8 karakter.")
)

// CreateSigninLink stores a new link for userID in org, inside the caller's transaction, and returns the token
// (32 random bytes, base64url). Only its sha256 is stored.
func (s *Service) CreateSigninLink(ctx context.Context, q pg.Querier, userID int64, org, createdBy string) (string, error) {
	tok := RandomToken("", 32)
	_, err := q.Query(ctx, "insert into rk_signin_links (token_hash, user_id, org_id, created_by, created_at, expires_at) values ($1, $2, $3, $4, $5, $6)",
		HashToken(tok), userID, org, createdBy, s.now(), s.now().Add(SigninLinkTTL))
	return tok, err
}

// DropSigninLinks ends the unused links of userID in org (the person was removed there).
func DropSigninLinks(ctx context.Context, q pg.Querier, userID int64, org string) error {
	_, err := q.Query(ctx, "delete from rk_signin_links where user_id = $1 and org_id = $2 and used_at is null", userID, org)
	return err
}

// dropUnusedLinks ends every unused link of userID, in every organisation (the account has a way in).
func dropUnusedLinks(ctx context.Context, q pg.Querier, userID int64) error {
	_, err := q.Query(ctx, "delete from rk_signin_links where user_id = $1 and used_at is null", userID)
	return err
}

// UseSigninLink sets the password of the link's account, marks the link used and opens a session in the link's
// organisation (openSession, so the first sign-in turns the member record active). A refusal writes nothing:
// ErrLinkInvalid, ErrWeakPassword, or *ErrPasswordDisabled when auth.password does not let this member sign in
// with a password.
func (s *Service) UseSigninLink(ctx context.Context, token, password string) (string, *User, error) {
	if token == "" {
		return "", nil, ErrLinkInvalid
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback(ctx)
	h := HashToken(token)
	res, err := tx.Query(ctx, "select user_id, org_id from rk_signin_links where token_hash = $1 and used_at is null and expires_at > $2 for update",
		h, s.now())
	if err != nil {
		return "", nil, err
	}
	if len(res.Rows) == 0 {
		return "", nil, ErrLinkInvalid
	}
	uid, org := res.Rows[0][0].(int64), res.Rows[0][1].(string)
	// the link sets only the first password of an account this organisation alone holds: an account that has a
	// password or an identity link already has a way in, and one another organisation holds is not this owner's
	// to open (the owner who saw the token would reach that organisation)
	if res, err = tx.Query(ctx, "select password_hash is null from rk_users where id = $1 for update", uid); err != nil {
		return "", nil, err
	}
	if len(res.Rows) == 0 || !res.Rows[0][0].(bool) {
		return "", nil, ErrLinkInvalid
	}
	if res, err = tx.Query(ctx, `select 1 from rk_members where user_id = $1 and org_id <> $2
		union all select 1 from rk_identities where user_id = $1 limit 1`, uid, org); err != nil {
		return "", nil, err
	}
	if len(res.Rows) > 0 {
		return "", nil, ErrLinkInvalid
	}
	u, err := LoadUser(ctx, tx, uid, org)
	if errors.Is(err, ErrNoMembership) {
		return "", nil, ErrLinkInvalid
	}
	if err != nil {
		return "", nil, err
	}
	if err := s.checkPasswordPolicy(u); err != nil {
		return "", nil, err
	}
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return "", nil, ErrWeakPassword
	}
	hash, err := HashPassword(password)
	if err != nil {
		return "", nil, err
	}
	if _, err := tx.Query(ctx, "update rk_users set password_hash = $1 where id = $2", hash, u.ID); err != nil {
		return "", nil, err
	}
	if _, err := tx.Query(ctx, "update rk_signin_links set used_at = $2 where token_hash = $1", h, s.now()); err != nil {
		return "", nil, err
	}
	if err := dropUnusedLinks(ctx, tx, u.ID); err != nil { // the account has a way in now: its other links end
		return "", nil, err
	}
	if err := store.Audit(ctx, tx, u.Org, u.Email, "auth.link_used", "user/"+u.Email, "password set with a sign-in link", nil); err != nil {
		return "", nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", nil, err
	}
	ttl := s.SessionTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	tok, err := s.openSession(ctx, u, ttl, SessionLink, false)
	if err != nil {
		return "", nil, err
	}
	return tok, u, nil
}
