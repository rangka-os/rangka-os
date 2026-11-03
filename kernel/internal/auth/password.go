// Package auth is Rangka ID, built into the kernel: passwords, cookie sessions, API tokens and a minimal
// OpenID Connect provider (discovery, authorize with code + PKCE S256, token, userinfo, JWKS with RS256).
package auth

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// BcryptCost is the bcrypt work factor for new hashes (tests set bcrypt.MinCost).
var BcryptCost = 12

// Iterations is the PBKDF2-HMAC-SHA256 count used only to verify legacy pbkdf2-sha256$ hashes.
var Iterations = 310000

const legacyPrefix = "pbkdf2-sha256$"

// HashPassword returns a bcrypt hash at BcryptCost. bcrypt only reads 72 bytes, so longer passwords are refused.
func HashPassword(pw string) (string, error) {
	if len(pw) > 72 {
		return "", errors.New("password longer than 72 bytes")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), BcryptCost)
	return string(h), err
}

// CheckPassword verifies a password against a bcrypt hash or a legacy pbkdf2-sha256$<iter>$<salt>$<hash>.
func CheckPassword(hash, pw string) bool {
	if strings.HasPrefix(hash, legacyPrefix) {
		return checkLegacy(hash, pw)
	}
	if !strings.HasPrefix(hash, "$2") {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// NeedsRehash reports whether a stored hash should be replaced: legacy PBKDF2, or bcrypt below BcryptCost.
func NeedsRehash(hash string) bool {
	if strings.HasPrefix(hash, legacyPrefix) {
		return true
	}
	cost, err := bcrypt.Cost([]byte(hash))
	return err != nil || cost < BcryptCost
}

func checkLegacy(hash, pw string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[2])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// RandomToken returns a URL-safe random token with a prefix.
func RandomToken(prefix string, n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// HashToken is how sessions, API tokens and codes are stored (never in clear).
func HashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// User is a Rangka ID account with its membership in the current org.
type User struct {
	ID       int64    `json:"id"`
	Email    string   `json:"email"`
	FullName string   `json:"full_name"`
	Org      string   `json:"org"`
	Roles    []string `json:"roles"`
	Record   string   `json:"record,omitempty"` // core user record (USR-001)
}

// EnsureOrg creates an organisation when missing.
func EnsureOrg(ctx context.Context, q pg.Querier, id, name string) error {
	_, err := q.Query(ctx, "insert into rk_orgs (id, name) values ($1, $2) on conflict (id) do nothing", id, name)
	return err
}

// EnsureUser creates or updates a user and its membership (the password is set only for new users).
func EnsureUser(ctx context.Context, q pg.Querier, email, fullName, password, org, role, record string) (int64, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	hash, err := HashPassword(password)
	if err != nil {
		return 0, err
	}
	res, err := q.Query(ctx, `insert into rk_users (email, full_name, password_hash) values ($1, $2, $3)
		on conflict (email) do update set full_name = case when rk_users.full_name = '' then excluded.full_name else rk_users.full_name end
		returning id`, email, fullName, hash)
	if err != nil {
		return 0, err
	}
	id := res.Rows[0][0].(int64)
	_, err = q.Query(ctx, `insert into rk_members (org_id, user_id, role, record) values ($1, $2, $3, $4)
		on conflict (org_id, user_id) do update set role = excluded.role, record = coalesce(excluded.record, rk_members.record)`, org, id, role, nullIf(record))
	return id, err
}

func nullIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ErrBadLogin is returned for a wrong email or password.
var ErrBadLogin = errors.New("email atau kata sandi salah")

// LoadUser loads a user and its membership in org (the first membership when org is empty).
func LoadUser(ctx context.Context, q pg.Querier, userID int64, org string) (*User, error) {
	res, err := q.Query(ctx, `select u.id, u.email, u.full_name, coalesce(m.org_id, ''), coalesce(m.role, ''), coalesce(m.record, '')
		from rk_users u left join rk_members m on m.user_id = u.id and ($2 = '' or m.org_id = $2)
		where u.id = $1 order by m.org_id limit 1`, userID, org)
	if err != nil {
		return nil, err
	}
	if len(res.Rows) == 0 {
		return nil, ErrBadLogin
	}
	r := res.Rows[0]
	u := &User{ID: r[0].(int64), Email: r[1].(string), FullName: r[2].(string), Org: r[3].(string), Record: r[5].(string)}
	if role := r[4].(string); role != "" {
		u.Roles = []string{role}
	} else {
		u.Roles = []string{}
	}
	return u, nil
}

var (
	dummyOnce sync.Once
	dummyHash []byte
)

// Authenticate checks email and password and upgrades the stored hash when NeedsRehash.
func Authenticate(ctx context.Context, q pg.Querier, email, password string) (int64, error) {
	res, err := q.Query(ctx, "select id, coalesce(password_hash, '') from rk_users where email = $1", strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		return 0, err
	}
	if len(res.Rows) == 0 {
		dummyOnce.Do(func() { dummyHash, _ = bcrypt.GenerateFromPassword([]byte("rangka"), BcryptCost) })
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password)) // similar work for unknown emails
		return 0, ErrBadLogin
	}
	id, hash := res.Rows[0][0].(int64), res.Rows[0][1].(string)
	if !CheckPassword(hash, password) {
		return 0, ErrBadLogin
	}
	if NeedsRehash(hash) {
		if nh, err := HashPassword(password); err == nil {
			q.Query(ctx, "update rk_users set password_hash = $1 where id = $2", nh, id) // best effort; login still succeeds
		}
	}
	return id, nil
}
