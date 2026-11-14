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
	// Upstreams are the external OpenID Providers of system.rk auth.upstream, ${ENV} resolved at start (contract 0042).
	Upstreams []Upstream
	// Password reads system.rk auth.password (true, false or "owners"); nil means true.
	Password func() any
	// UpstreamHTTP supplies the transport for calls to upstream providers (tests); nil uses the default transport.
	UpstreamHTTP *http.Client
	// Log receives operator messages; it never gets a token, code, secret or claim value.
	Log func(format string, args ...any)

	upMu    sync.Mutex
	upCache map[string]*issuerCache // discovery and JWKS per issuer (upstream.go)

	keyOnce sync.Once
	key     *rsa.PrivateKey
	kid     string
	keyErr  error
}

// Principal is an authenticated caller.
type Principal struct {
	User  *User
	Via   string // session, token, oidc, dev, ai (assistant token minted by MintAssistantToken)
	Scope string // OAuth scopes for oidc and ai tokens; for API tokens the scopes they were minted with (secrets:write, members:write)
	// Chat marks the token the chat relay minted for one chat (MintAssistantToken, contract 0020 §2): the kernel
	// admitted that chat and counted its credit. An API token with the assistant scope is Via "ai" too, but not Chat.
	Chat bool
	// System marks a system session (contract 0055 §2): nothing it does counts as the member's activity.
	System bool
}

// Kind names the principal for a client that must know what it was given (MCP _meta "rangka/principal"): chat
// (the relay's per-chat token), assistant (an API token with the assistant scope), else Via (session, token,
// oidc, dev).
func (p *Principal) Kind() string {
	switch {
	case p.Chat:
		return "chat"
	case p.Via == "ai":
		return "assistant"
	}
	return p.Via
}

// AIForbiddenMessage is the message of the ai_forbidden error: an assistant asked for something only a
// person may do (approve, apply, revert, write records).
const AIForbiddenMessage = "Raka tidak boleh melakukan ini; minta orang yang berwenang."

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
	return s.LoginAs(ctx, email, password, org, false)
}

// LoginAs is Login that may open a system session (contract 0055 §2): the same session, except that nothing it does
// counts as activity. Signing in with a person's session counts as their activity.
func (s *Service) LoginAs(ctx context.Context, email, password, org string, system bool) (string, *User, error) {
	id, err := Authenticate(ctx, s.DB, email, password)
	if err != nil {
		store.Audit(ctx, s.DB, org, strings.ToLower(email), "auth.login_failed", "user/"+strings.ToLower(email), "wrong email or password", nil)
		return "", nil, err
	}
	u, err := LoadUser(ctx, s.DB, id, org)
	if errors.Is(err, ErrNoMembership) && org != "" {
		u, err = LoadUser(ctx, s.DB, id, "")
	}
	if err != nil {
		return "", nil, err
	}
	if err := s.checkPasswordPolicy(u); err != nil {
		return "", nil, err
	}
	ttl := s.SessionTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	tok, err := s.openSession(ctx, u, ttl, SessionPassword, system)
	if err != nil {
		return "", nil, err
	}
	var data map[string]any
	if system {
		data = map[string]any{"system": true}
	}
	store.Audit(ctx, s.DB, u.Org, u.Email, "auth.login", "user/"+u.Email, "signed in", data)
	return tok, u, nil
}

// How a session was opened (rk_sessions.method).
const (
	SessionPassword = "password"
	SessionUpstream = "upstream"
)

// openSession stores a session for u in its organisation and returns the cookie value. Password sessions last
// auth.session_ttl; upstream sessions UpstreamSessionTTL. method is SessionPassword or SessionUpstream. A session
// that is not a system one counts as the member's activity (contract 0055 §1).
func (s *Service) openSession(ctx context.Context, u *User, ttl time.Duration, method string, system bool) (string, error) {
	tok := RandomToken("", 32)
	if _, err := s.DB.Query(ctx, "insert into rk_sessions (id, user_id, org_id, expires_at, method, system) values ($1, $2, $3, $4, $5, $6)",
		HashToken(tok), u.ID, u.Org, s.now().Add(ttl), method, system); err != nil {
		return "", err
	}
	if !system {
		s.touch(ctx, u.ID, u.Org)
	}
	return tok, nil
}

// activityEvery is how stale rk_members.last_active_at must be before a request writes it again (contract 0055 §1).
const activityEvery = 5 * time.Minute

// touch records that the member used the organisation now (contract 0055 §1). It writes only when the stored value
// is older than activityEvery, and a failure never fails the request.
func (s *Service) touch(ctx context.Context, userID int64, org string) {
	if userID == 0 || org == "" {
		return
	}
	now := s.now()
	s.DB.Query(ctx, `update rk_members set last_active_at = $3 where org_id = $1 and user_id = $2
		and (last_active_at is null or last_active_at < $4)`, org, userID, now, now.Add(-activityEvery))
}

// SwitchOrg moves a live session to another organisation the user belongs to. A session opened with a password
// moves only into an organisation where auth.password lets this member sign in with one (*ErrPasswordDisabled
// otherwise); an upstream session is not limited.
func (s *Service) SwitchOrg(ctx context.Context, cookie, org string) (*User, error) {
	id := HashToken(cookie)
	res, err := s.DB.Query(ctx, "select user_id, method from rk_sessions where id = $1 and expires_at > $2", id, s.now())
	if err != nil {
		return nil, err
	}
	if len(res.Rows) == 0 {
		return nil, ErrUnauthenticated
	}
	u, err := LoadUser(ctx, s.DB, res.Rows[0][0].(int64), org)
	if err != nil {
		return nil, err
	}
	if org == "" || u.Org != org {
		return nil, ErrNoMembership
	}
	if res.Rows[0][1].(string) != SessionUpstream {
		if err := s.checkPasswordPolicy(u); err != nil {
			return nil, err
		}
	}
	if _, err := s.DB.Query(ctx, "update rk_sessions set org_id = $1 where id = $2", u.Org, id); err != nil {
		return nil, err
	}
	store.Audit(ctx, s.DB, u.Org, u.Email, "auth.org_switched", "org/"+u.Org, "switched organisation", nil)
	return u, nil
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
func (s *Service) CreateToken(ctx context.Context, u *User, name string, ttl time.Duration, scopes ...string) (string, int64, error) {
	tok := RandomToken("rk_", 32)
	var exp any
	if ttl > 0 {
		exp = s.now().Add(ttl)
	}
	if scopes == nil {
		scopes = []string{}
	}
	res, err := s.DB.Query(ctx, "insert into rk_tokens (user_id, org_id, name, token_hash, expires_at, scopes) values ($1, $2, $3, $4, $5, $6) returning id",
		u.ID, u.Org, name, HashToken(tok), exp, scopes)
	if err != nil {
		return "", 0, err
	}
	var data any
	if len(scopes) > 0 {
		data = map[string]any{"scopes": scopes}
	}
	store.Audit(ctx, s.DB, u.Org, u.Email, "auth.token_created", "token/"+name, "API token created", data)
	return tok, res.Rows[0][0].(int64), nil
}

// ScopeSecretsWrite lets an API token set and remove connector secrets (contract 0017 §3), for Rangka Cloud
// provisioning. Only a person who may manage secrets can mint it; Omilo helper and Rangka AI tokens never carry it.
const ScopeSecretsWrite = "secrets:write"

// ScopeMembersWrite lets an API token create, change and remove the non-owner members of its organisation through
// the member API (contract 0043), for Rangka Cloud. Only an owner with a browser session can mint it.
const ScopeMembersWrite = "members:write"

// ScopeAssistant marks an API token as the assistant's (contract 0045 §3): it resolves with Via "ai", so the kernel
// treats its holder as Raka acting for the person who minted it.
const ScopeAssistant = "assistant"

// TokenScopes are the scopes an API token may carry.
var TokenScopes = map[string]bool{ScopeSecretsWrite: true, ScopeMembersWrite: true, ScopeAssistant: true}

// RevokeMember deletes a person's sessions and API tokens in one organisation (contract 0043 §4); their sessions and
// tokens for other organisations stay. q may be the transaction that changes the membership.
func (s *Service) RevokeMember(ctx context.Context, q pg.Querier, userID int64, org string) error {
	if _, err := q.Query(ctx, "delete from rk_sessions where user_id = $1 and org_id = $2", userID, org); err != nil {
		return err
	}
	_, err := q.Query(ctx, "delete from rk_tokens where user_id = $1 and org_id = $2", userID, org)
	return err
}

// HasScope reports whether the principal's scopes include scope.
func (p *Principal) HasScope(scope string) bool {
	for _, s := range strings.Fields(p.Scope) {
		if s == scope {
			return true
		}
	}
	return false
}

// ErrUnauthenticated means no valid credentials were sent.
var ErrUnauthenticated = errors.New("sign in first (session cookie or Authorization: Bearer token)")

// Resolve authenticates a request from the session cookie or a bearer token (API token or OIDC access
// token). It returns nil, nil for anonymous requests. A real principal that is not a system session counts as the
// member's activity (contract 0055 §1).
func (s *Service) Resolve(r *http.Request) (*Principal, error) {
	p, err := s.resolve(r)
	if err == nil && p != nil && !p.System && p.Via != "dev" && p.User != nil {
		s.touch(r.Context(), p.User.ID, p.User.Org)
	}
	return p, err
}

func (s *Service) resolve(r *http.Request) (*Principal, error) {
	ctx := r.Context()
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		if strings.HasPrefix(tok, "rk_") {
			res, err := s.DB.Query(ctx, `update rk_tokens set last_used_at = now() where token_hash = $1 and (expires_at is null or expires_at > $2)
				returning user_id, org_id, array_to_string(scopes, ' ')`, HashToken(tok), s.now())
			if err != nil {
				return nil, err
			}
			if len(res.Rows) == 0 {
				return nil, ErrUnauthenticated
			}
			if res.Rows[0][1].(string) == "" {
				return nil, ErrUnauthenticated
			}
			u, err := LoadUser(ctx, s.DB, res.Rows[0][0].(int64), res.Rows[0][1].(string))
			if errors.Is(err, ErrNoMembership) {
				return nil, ErrUnauthenticated
			}
			if err != nil {
				return nil, err
			}
			scope := res.Rows[0][2].(string)
			p := &Principal{User: u, Via: "token", Scope: scope}
			if p.HasScope(ScopeAssistant) {
				p.Via = "ai"
			}
			return p, nil
		}
		claims, err := s.VerifyJWT(tok)
		if err != nil {
			return nil, ErrUnauthenticated
		}
		uid, _ := claims["uid"].(float64)
		org, _ := claims["org"].(string)
		if org == "" {
			return nil, ErrUnauthenticated
		}
		u, err := LoadUser(ctx, s.DB, int64(uid), org)
		if err != nil {
			return nil, ErrUnauthenticated
		}
		scope, _ := claims["scope"].(string)
		if ai, _ := claims["ai"].(bool); ai {
			return &Principal{User: u, Via: "ai", Scope: scope, Chat: true}, nil
		}
		return &Principal{User: u, Via: "oidc", Scope: scope}, nil
	}
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		res, err := s.DB.Query(ctx, "select user_id, org_id, system from rk_sessions where id = $1 and expires_at > $2", HashToken(c.Value), s.now())
		if err != nil {
			return nil, err
		}
		if len(res.Rows) > 0 {
			if res.Rows[0][1].(string) == "" {
				return nil, ErrUnauthenticated
			}
			u, err := LoadUser(ctx, s.DB, res.Rows[0][0].(int64), res.Rows[0][1].(string))
			if errors.Is(err, ErrNoMembership) {
				return nil, ErrUnauthenticated
			}
			if err != nil {
				return nil, err
			}
			system, _ := res.Rows[0][2].(bool)
			return &Principal{User: u, Via: "session", System: system}, nil
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
