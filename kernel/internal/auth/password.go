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
	"fmt"
	"strconv"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// Iterations of PBKDF2-HMAC-SHA256 (bcrypt/argon2 live in x/crypto, which was not reachable at build time;
// the hash format names its algorithm so it can be upgraded on next login).
var Iterations = 310000

// HashPassword returns pbkdf2-sha256$<iter>$<salt>$<hash>.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, Iterations, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", Iterations, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// CheckPassword verifies a password against a stored hash.
func CheckPassword(hash, pw string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
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

// Authenticate checks email and password.
func Authenticate(ctx context.Context, q pg.Querier, email, password string) (int64, error) {
	res, err := q.Query(ctx, "select id, coalesce(password_hash, '') from rk_users where email = $1", strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		return 0, err
	}
	if len(res.Rows) == 0 {
		CheckPassword("pbkdf2-sha256$1000$AAAA$AAAA", password) // similar work for unknown emails
		return 0, ErrBadLogin
	}
	if !CheckPassword(res.Rows[0][1].(string), password) {
		return 0, ErrBadLogin
	}
	return res.Rows[0][0].(int64), nil
}
