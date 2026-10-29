package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// CookieName is the session cookie.
const CookieName = "rk_session"

// Client is an OAuth client from system.rk auth.clients.
type Client struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	RedirectURIs []string `json:"redirect_uris"`
	Public       bool     `json:"public"`
	Scopes       []string `json:"scopes"`
}

// Service is Rangka ID.
type Service struct {
	DB         *pg.Pool
	Issuer     string
	SessionTTL time.Duration
	Clients    func() []Client
	DevOpen    bool
	DefaultOrg string
	Now        func() time.Time

	keyOnce sync.Once
	key     *rsa.PrivateKey
	kid     string
	keyErr  error
}

// Principal is an authenticated caller.
type Principal struct {
	User  *User
	Via   string // session, token, oidc, dev
	Scope string // OAuth scopes for oidc tokens
}

// Human reports a person signed in with a browser session (or the dev-open demo user).
func (p *Principal) Human() bool { return p.Via == "session" || p.Via == "dev" }

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Login checks credentials and opens a session; it returns the cookie value.
func (s *Service) Login(ctx context.Context, email, password, org string) (string, *User, error) {
	id, err := Authenticate(ctx, s.DB, email, password)
	if err != nil {
		store.Audit(ctx, s.DB, org, strings.ToLower(email), "auth.login_failed", "user/"+strings.ToLower(email), "wrong email or password", nil)
		return "", nil, err
	}
	u, err := LoadUser(ctx, s.DB, id, org)
	if err != nil {
		return "", nil, err
	}
	if u.Org == "" {
		if u, err = LoadUser(ctx, s.DB, id, ""); err != nil {
			return "", nil, err
		}
	}
	tok := RandomToken("", 32)
	ttl := s.SessionTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if _, err := s.DB.Query(ctx, "insert into rk_sessions (id, user_id, org_id, expires_at) values ($1, $2, $3, $4)", HashToken(tok), u.ID, u.Org, s.now().Add(ttl)); err != nil {
		return "", nil, err
	}
	store.Audit(ctx, s.DB, u.Org, u.Email, "auth.login", "user/"+u.Email, "signed in", nil)
	return tok, u, nil
}

// Logout ends a session.
func (s *Service) Logout(ctx context.Context, cookie string) error {
	_, err := s.DB.Query(ctx, "delete from rk_sessions where id = $1", HashToken(cookie))
	return err
}

// SessionCookie builds the cookie for a session token.
func (s *Service) SessionCookie(tok string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: CookieName, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: strings.HasPrefix(s.Issuer, "https://"), MaxAge: maxAge}
}

// CreateToken issues an API token (rk_…) for a user in an org.
func (s *Service) CreateToken(ctx context.Context, u *User, name string, ttl time.Duration) (string, int64, error) {
	tok := RandomToken("rk_", 32)
	var exp any
	if ttl > 0 {
		exp = s.now().Add(ttl)
	}
	res, err := s.DB.Query(ctx, "insert into rk_tokens (user_id, org_id, name, token_hash, expires_at) values ($1, $2, $3, $4, $5) returning id",
		u.ID, u.Org, name, HashToken(tok), exp)
	if err != nil {
		return "", 0, err
	}
	store.Audit(ctx, s.DB, u.Org, u.Email, "auth.token_created", "token/"+name, "API token created", nil)
	return tok, res.Rows[0][0].(int64), nil
}

// ErrUnauthenticated means no valid credentials were sent.
var ErrUnauthenticated = errors.New("sign in first (session cookie or Authorization: Bearer token)")

// Resolve authenticates a request from the session cookie or a bearer token (API token or OIDC access
// token). It returns nil, nil for anonymous requests.
func (s *Service) Resolve(r *http.Request) (*Principal, error) {
	ctx := r.Context()
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		if strings.HasPrefix(tok, "rk_") {
			res, err := s.DB.Query(ctx, `update rk_tokens set last_used_at = now() where token_hash = $1 and (expires_at is null or expires_at > now())
				returning user_id, org_id`, HashToken(tok))
			if err != nil {
				return nil, err
			}
			if len(res.Rows) == 0 {
				return nil, ErrUnauthenticated
			}
			u, err := LoadUser(ctx, s.DB, res.Rows[0][0].(int64), res.Rows[0][1].(string))
			if err != nil {
				return nil, err
			}
			return &Principal{User: u, Via: "token"}, nil
		}
		claims, err := s.VerifyJWT(tok)
		if err != nil {
			return nil, ErrUnauthenticated
		}
		uid, _ := claims["uid"].(float64)
		org, _ := claims["org"].(string)
		u, err := LoadUser(ctx, s.DB, int64(uid), org)
		if err != nil {
			return nil, ErrUnauthenticated
		}
		scope, _ := claims["scope"].(string)
		return &Principal{User: u, Via: "oidc", Scope: scope}, nil
	}
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		res, err := s.DB.Query(ctx, "select user_id, org_id from rk_sessions where id = $1 and expires_at > now()", HashToken(c.Value))
		if err != nil {
			return nil, err
		}
		if len(res.Rows) > 0 {
			u, err := LoadUser(ctx, s.DB, res.Rows[0][0].(int64), res.Rows[0][1].(string))
			if err != nil {
				return nil, err
			}
			return &Principal{User: u, Via: "session"}, nil
		}
	}
	if s.DevOpen {
		return s.devPrincipal(ctx)
	}
	return nil, nil
}

// devPrincipal is the owner of the default org (dev-open demos run as that person).
func (s *Service) devPrincipal(ctx context.Context) (*Principal, error) {
	res, err := s.DB.Query(ctx, `select user_id from rk_members where org_id = $1 order by (role = 'owner') desc, user_id limit 1`, s.DefaultOrg)
	if err == nil && len(res.Rows) > 0 {
		if u, err := LoadUser(ctx, s.DB, res.Rows[0][0].(int64), s.DefaultOrg); err == nil {
			return &Principal{User: u, Via: "dev"}, nil
		}
	}
	return &Principal{User: &User{Email: "dev@localhost", FullName: "Developer", Org: s.DefaultOrg, Roles: []string{"owner"}}, Via: "dev"}, nil
}

// signingKey loads the RS256 key from rk_keys, generating and storing one on first use.
func (s *Service) signingKey(ctx context.Context) (*rsa.PrivateKey, string, error) {
	s.keyOnce.Do(func() {
		res, err := s.DB.Query(ctx, "select kid, private_pem from rk_keys where alg = 'RS256' order by created_at limit 1")
		if err != nil {
			s.keyErr = err
			return
		}
		if len(res.Rows) > 0 {
			blk, _ := pem.Decode([]byte(res.Rows[0][1].(string)))
			if blk == nil {
				s.keyErr = errors.New("rk_keys: bad PEM")
				return
			}
			k, err := x509.ParsePKCS1PrivateKey(blk.Bytes)
			s.key, s.kid, s.keyErr = k, res.Rows[0][0].(string), err
			return
		}
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			s.keyErr = err
			return
		}
		kid := RandomToken("", 9)
		p := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
		if _, err := s.DB.Query(ctx, "insert into rk_keys (kid, alg, private_pem) values ($1, 'RS256', $2)", kid, string(p)); err != nil {
			s.keyErr = err
			return
		}
		s.key, s.kid = k, kid
	})
	return s.key, s.kid, s.keyErr
}

// Client returns a configured OAuth client.
func (s *Service) Client(id string) *Client {
	if s.Clients == nil {
		return nil
	}
	for _, c := range s.Clients() {
		if c.ID == id {
			cc := c
			return &cc
		}
	}
	return nil
}
