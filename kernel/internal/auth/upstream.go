package auth

// The kernel as an OpenID Connect relying party of an external provider (contract 0042): system.rk
// auth.upstream, the code flow with PKCE S256, id_token verification against the provider's JWKS, and the
// identity link that ties (issuer, sub) to a kernel user. The provider says who a person is; the kernel's
// membership says what they may do.

import (
	"context"
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Upstream is an external OpenID Provider from system.rk auth.upstream, with ${ENV} values already resolved.
// ClientSecret is empty for a public client (PKCE only).
type Upstream struct {
	ID, Label, Issuer, ClientID, ClientSecret string
}

const (
	// UpstreamSessionTTL is how long a session opened through an upstream lasts (contract 0042 §7). It is
	// independent of auth.session_ttl: the kernel cannot learn about a provider-side sign-out.
	UpstreamSessionTTL = 12 * time.Hour
	upstreamStateTTL   = 10 * time.Minute
	upstreamCacheTTL   = time.Hour
	jwksRefetchEvery   = time.Minute
	upstreamTimeout    = 10 * time.Second
	upstreamMaxBody    = 1 << 20
	clockSkew          = 60 * time.Second
	maxIATAhead        = 5 * time.Minute
	// orgCookieName is the web app's last-used organisation (api.orgCookie): a preference, never an authority.
	orgCookieName = "rk_org"
	// upstreamDefaultTo is where a sign-in lands without a usable ?to=.
	upstreamDefaultTo = "/app/"
	// bindCookieName ties a sign-in to the browser that started it (login CSRF): set at start with
	// Path=/auth/upstream/<id>/, its hash stored with the state, required and cleared at the callback.
	bindCookieName = "rk_upstream"
)

// SecureURL reports whether a provider URL may be used: https, or http on a loopback host (development, tests).
func SecureURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		h := u.Hostname()
		return h == "localhost" || h == "127.0.0.1" || h == "::1"
	}
	return false
}

// bindCookie builds the browser-binding cookie of an upstream flow (value "" with maxAge -1 clears it). Secure
// follows the session cookie: set when the kernel's issuer is https, and also when this request came over TLS.
func (s *Service) bindCookie(r *http.Request, up *Upstream, value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: bindCookieName, Value: value, Path: "/auth/upstream/" + up.ID + "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: strings.HasPrefix(s.Issuer, "https://") || r.TLS != nil, MaxAge: maxAge}
}

// upstreamError is a refused upstream sign-in: the status, code and copy shown to the person (contract 0042 §8),
// and a reason for the operator's log that never carries a token, code, secret or claim value.
type upstreamError struct {
	status        int
	code, message string
	reason        string
}

func (e *upstreamError) Error() string { return e.code + ": " + e.reason }

func errUnknownUpstream() *upstreamError {
	return &upstreamError{404, "unknown_upstream", "Penyedia akun tidak ditemukan.", "unknown upstream id"}
}

func errInvalidState(reason string) *upstreamError {
	return &upstreamError{400, "invalid_state", "Sesi masuk sudah habis. Mulai lagi.", reason}
}

func errUpstreamInvalid(up *Upstream, reason string) *upstreamError {
	return &upstreamError{401, "upstream_invalid", "Masuk dengan " + up.Label + " belum berhasil. Coba lagi.", reason}
}

func errUpstreamUnavailable(up *Upstream, reason string) *upstreamError {
	return &upstreamError{502, "upstream_unavailable", up.Label + " sedang tidak bisa dihubungi. Coba lagi sebentar.", reason}
}

func errMembershipPending(reason string) *upstreamError {
	return &upstreamError{403, "membership_pending", "Aksesmu sedang disiapkan. Coba lagi sebentar.", reason}
}

func errNoMembership() *upstreamError {
	return &upstreamError{403, "no_membership", ErrNoMembership.Error(), "identity has no membership"}
}

// Upstream returns a configured upstream by id, nil when none.
func (s *Service) Upstream(id string) *Upstream {
	for i := range s.Upstreams {
		if s.Upstreams[i].ID == id {
			u := s.Upstreams[i]
			return &u
		}
	}
	return nil
}

// PasswordSetting is auth.password as configured: true, false or "owners" (anything else reads as true).
func (s *Service) PasswordSetting() any {
	if s.Password == nil {
		return true
	}
	switch v := s.Password().(type) {
	case bool:
		return v
	case string:
		if v == "owners" {
			return v
		}
	}
	return true
}

// ErrPasswordDisabled is returned by Login when auth.password does not let this member sign in with a password
// (contract 0042 §6). Message is the copy to show.
type ErrPasswordDisabled struct{ Message string }

func (e *ErrPasswordDisabled) Error() string { return e.Message }

// checkPasswordPolicy applies auth.password to a member whose password has verified: false refuses everyone,
// "owners" refuses every role but owner.
func (s *Service) checkPasswordPolicy(u *User) error {
	switch s.PasswordSetting() {
	case false:
	case "owners":
		for _, r := range u.Roles {
			if r == "owner" {
				return nil
			}
		}
	default:
		return nil
	}
	msg := "Masuk lewat penyedia akun organisasimu."
	if len(s.Upstreams) > 0 {
		msg = "Masuk dengan " + s.Upstreams[0].Label + "."
	}
	return &ErrPasswordDisabled{Message: msg}
}

// Providers serves GET /auth/providers: how a person may sign in. It reveals no issuer, client id or secret.
func (s *Service) Providers(w http.ResponseWriter, r *http.Request) {
	ups := []map[string]string{}
	for _, u := range s.Upstreams {
		ups = append(ups, map[string]string{"id": u.ID, "label": u.Label})
	}
	writeJSON(w, 200, map[string]any{"password": s.PasswordSetting(), "upstream": ups})
}

// SafePath keeps a post-sign-in target on this site: a path that starts with one "/", with no scheme, no
// backslash and no control character. Anything else becomes /app/.
func SafePath(to string) string {
	if to == "" || to[0] != '/' || strings.HasPrefix(to, "//") || strings.ContainsAny(to, "\\") {
		return upstreamDefaultTo
	}
	for _, c := range to {
		if c < 0x20 || c == 0x7f {
			return upstreamDefaultTo
		}
	}
	u, err := url.Parse(to)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return upstreamDefaultTo
	}
	return to
}

// UpstreamStart serves GET /auth/upstream/{id}/start?to=<path>: it stores state, nonce and the PKCE verifier
// server-side (10 minutes, one use) and sends the browser to the provider.
func (s *Service) UpstreamStart(w http.ResponseWriter, r *http.Request) {
	up := s.Upstream(r.PathValue("id"))
	if up == nil {
		s.upstreamFail(w, r, nil, errUnknownUpstream())
		return
	}
	ctx := r.Context()
	disc, uerr := s.discovery(ctx, up)
	if uerr != nil {
		s.upstreamFail(w, r, up, uerr)
		return
	}
	state, nonce, verifier, browser := RandomToken("", 32), RandomToken("", 32), RandomToken("", 32), RandomToken("", 32)
	now := s.now()
	// expired rows are swept here so the table stays small; a row is otherwise removed by its callback
	if _, err := s.DB.Query(ctx, "delete from rk_upstream_states where expires_at < $1", now); err != nil {
		s.upstreamFail(w, r, up, &upstreamError{500, "internal", "Terjadi kesalahan. Coba lagi.", "sweep states: " + err.Error()})
		return
	}
	if _, err := s.DB.Query(ctx, `insert into rk_upstream_states (state_hash, upstream, browser_hash, nonce_hash, verifier, to_path, expires_at)
		values ($1, $2, $3, $4, $5, $6, $7)`, HashToken(state), up.ID, HashToken(browser), HashToken(nonce), verifier, SafePath(r.URL.Query().Get("to")), now.Add(upstreamStateTTL)); err != nil {
		s.upstreamFail(w, r, up, &upstreamError{500, "internal", "Terjadi kesalahan. Coba lagi.", "store state: " + err.Error()})
		return
	}
	sum := sha256.Sum256([]byte(verifier))
	au, err := url.Parse(disc.AuthorizationEndpoint)
	if err != nil {
		s.upstreamFail(w, r, up, errUpstreamUnavailable(up, "authorization_endpoint is not a URL"))
		return
	}
	q := au.Query()
	q.Set("response_type", "code")
	q.Set("client_id", up.ClientID)
	q.Set("redirect_uri", s.upstreamRedirectURI(up))
	q.Set("scope", "openid email profile")
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
	q.Set("code_challenge_method", "S256")
	au.RawQuery = q.Encode()
	http.SetCookie(w, s.bindCookie(r, up, browser, int(upstreamStateTTL.Seconds())))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, au.String(), http.StatusFound)
}

// UpstreamCallback serves GET /auth/upstream/{id}/callback: one-use state, code exchange with the PKCE verifier,
// id_token checks, identity link, membership, a 12-hour session, then back to the stored path.
func (s *Service) UpstreamCallback(w http.ResponseWriter, r *http.Request) {
	up := s.Upstream(r.PathValue("id"))
	if up == nil {
		s.upstreamFail(w, r, nil, errUnknownUpstream())
		return
	}
	// the binding cookie is single use like the state: cleared whatever the outcome
	http.SetCookie(w, s.bindCookie(r, up, "", -1))
	u, to, uerr := s.upstreamCallback(r, up)
	if uerr != nil {
		s.upstreamFail(w, r, up, uerr)
		return
	}
	ctx := r.Context()
	tok, err := s.openSession(ctx, u, UpstreamSessionTTL)
	if err != nil {
		s.upstreamFail(w, r, up, &upstreamError{500, "internal", "Terjadi kesalahan. Coba lagi.", "open session: " + err.Error()})
		return
	}
	store.Audit(ctx, s.DB, u.Org, u.Email, "auth.login", "user/"+u.Email, "signed in with "+up.Label, map[string]any{"upstream": up.ID})
	http.SetCookie(w, s.SessionCookie(tok, int(UpstreamSessionTTL.Seconds())))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, to, http.StatusFound)
}

func (s *Service) upstreamCallback(r *http.Request, up *Upstream) (*User, string, *upstreamError) {
	ctx := r.Context()
	q := r.URL.Query()
	state := q.Get("state")
	if state == "" {
		return nil, "", errInvalidState("no state")
	}
	bind, err := r.Cookie(bindCookieName)
	if err != nil || bind.Value == "" {
		return nil, "", errInvalidState("no browser-binding cookie")
	}
	// one use: the row is deleted by the statement that reads it. It must match the upstream and the browser that
	// started the flow, so another upstream's or another browser's attempt neither consumes it nor signs anyone in.
	res, err := s.DB.Query(ctx, `delete from rk_upstream_states where state_hash = $1 and upstream = $2 and browser_hash = $3
		returning nonce_hash, verifier, to_path, expires_at`, HashToken(state), up.ID, HashToken(bind.Value))
	if err != nil {
		return nil, "", &upstreamError{500, "internal", "Terjadi kesalahan. Coba lagi.", "consume state: " + err.Error()}
	}
	if len(res.Rows) == 0 {
		return nil, "", errInvalidState("state unknown, used, or for another upstream or browser")
	}
	row := res.Rows[0]
	nonceHash, verifier, to := row[0].(string), row[1].(string), row[2].(string)
	if exp, _ := row[3].(time.Time); !s.now().Before(exp) {
		return nil, "", errInvalidState("state expired")
	}
	if e := q.Get("error"); e != "" {
		// the provider's error_description is never shown or logged; its error code only when it looks like one
		reason := "provider returned an error"
		if oauthErrCode.MatchString(e) {
			reason += " (" + e + ")"
		}
		return nil, "", errUpstreamInvalid(up, reason)
	}
	code := q.Get("code")
	if code == "" {
		return nil, "", errUpstreamInvalid(up, "no code")
	}
	disc, uerr := s.discovery(ctx, up)
	if uerr != nil {
		return nil, "", uerr
	}
	rawID, uerr := s.exchangeCode(ctx, up, disc, code, verifier)
	if uerr != nil {
		return nil, "", uerr
	}
	claims, uerr := s.verifyIDToken(ctx, up, rawID, nonceHash)
	if uerr != nil {
		return nil, "", uerr
	}
	u, uerr := s.resolveUpstreamUser(ctx, r, up, claims)
	if uerr != nil {
		return nil, "", uerr
	}
	return u, SafePath(to), nil
}

var oauthErrCode = regexp.MustCompile(`^[a-z_]{1,64}$`)

// upstreamRedirectURI is the callback registered at the provider.
func (s *Service) upstreamRedirectURI(up *Upstream) string {
	return strings.TrimSuffix(s.Issuer, "/") + "/auth/upstream/" + up.ID + "/callback"
}

// resolveUpstreamUser applies the identity link rule (contract 0042 §5) and finds the membership to enter.
func (s *Service) resolveUpstreamUser(ctx context.Context, r *http.Request, up *Upstream, claims map[string]any) (*User, *upstreamError) {
	sub, _ := claims["sub"].(string)
	prefer := s.DefaultOrg
	if c, err := r.Cookie(orgCookieName); err == nil && c.Value != "" {
		prefer = c.Value
	}
	uid, err := UserByIdentity(ctx, s.DB, up.Issuer, sub)
	var u *User
	switch {
	case err == nil:
		// 1. known pair: that user; the e-mail claim is ignored
		u, err = s.enterOrg(ctx, uid, prefer)
		if errors.Is(err, ErrNoMembership) || errors.Is(err, ErrBadLogin) {
			return nil, errNoMembership()
		}
		if err != nil {
			return nil, &upstreamError{500, "internal", "Terjadi kesalahan. Coba lagi.", "load user: " + err.Error()}
		}
	case errors.Is(err, store.ErrNotFound):
		// 2. first sign-in: link by e-mail only when the provider verified it and that person is already a member
		email, _ := claims["email"].(string)
		email = strings.ToLower(strings.TrimSpace(email))
		if !emailVerified(claims["email_verified"]) || email == "" {
			return nil, errMembershipPending("unknown identity without a verified e-mail")
		}
		res, err := s.DB.Query(ctx, "select id from rk_users where email = $1", email)
		if err != nil {
			return nil, &upstreamError{500, "internal", "Terjadi kesalahan. Coba lagi.", "find user: " + err.Error()}
		}
		if len(res.Rows) == 0 {
			return nil, errMembershipPending("no account with that e-mail yet")
		}
		uid = res.Rows[0][0].(int64)
		u, err = s.enterOrg(ctx, uid, prefer)
		if errors.Is(err, ErrNoMembership) {
			return nil, errMembershipPending("account has no membership yet")
		}
		if err != nil {
			return nil, &upstreamError{500, "internal", "Terjadi kesalahan. Coba lagi.", "load user: " + err.Error()}
		}
		// a person already linked at this issuer under another sub is not linked again by e-mail: an e-mail the
		// provider reassigned, or a second account claiming it, must not become a second way in
		res, err = s.DB.Query(ctx, "select 1 from rk_identities where issuer = $1 and user_id = $2 limit 1", up.Issuer, uid)
		if err != nil {
			return nil, &upstreamError{500, "internal", "Terjadi kesalahan. Coba lagi.", "find identity: " + err.Error()}
		}
		if len(res.Rows) > 0 {
			return nil, errMembershipPending("account already linked to another subject at this issuer")
		}
		if err := LinkIdentity(ctx, s.DB, up.Issuer, sub, uid); err != nil {
			// ErrConflict: the pair was linked to someone else meanwhile; it is never re-linked at sign-in
			return nil, errMembershipPending("link identity: " + err.Error())
		}
		store.Audit(ctx, s.DB, u.Org, u.Email, "auth.identity_linked", "user/"+u.Email, "linked to "+up.Label+" by verified e-mail", map[string]any{"upstream": up.ID})
	default:
		return nil, &upstreamError{500, "internal", "Terjadi kesalahan. Coba lagi.", "find identity: " + err.Error()}
	}
	if name, _ := claims["name"].(string); u.FullName == "" && strings.TrimSpace(name) != "" {
		name = strings.TrimSpace(name)
		if _, err := s.DB.Query(ctx, "update rk_users set full_name = $1 where id = $2 and full_name = ''", name, u.ID); err == nil {
			u.FullName = name
		}
	}
	return u, nil
}

// enterOrg loads the membership to enter: the preferred organisation, else the user's first one.
func (s *Service) enterOrg(ctx context.Context, uid int64, prefer string) (*User, error) {
	u, err := LoadUser(ctx, s.DB, uid, prefer)
	if errors.Is(err, ErrNoMembership) && prefer != "" {
		u, err = LoadUser(ctx, s.DB, uid, "")
	}
	return u, err
}

func emailVerified(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string: // some providers (Cognito) send the claim as a string
		return x == "true"
	}
	return false
}

// upstreamFail shows a refused sign-in: the sign-in page with the error copy for a browser, the error body for a
// JSON client. Nothing the provider sent is echoed. The reason goes to the operator's log only.
func (s *Service) upstreamFail(w http.ResponseWriter, r *http.Request, up *Upstream, e *upstreamError) {
	if s.Log != nil && e.code != "membership_pending" {
		id := ""
		if up != nil {
			id = up.ID
		}
		s.Log("upstream %s: %s (%s)", id, e.code, e.reason)
	}
	w.Header().Set("Cache-Control", "no-store")
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html") {
		writeJSON(w, e.status, map[string]any{"error": map[string]any{"code": e.code, "message": e.message}})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(e.status)
	loginPage.Execute(w, map[string]any{"Error": e.message, "Back": upstreamDefaultTo})
}

// ---- provider calls: discovery, JWKS, token endpoint ----

type upstreamDiscovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// issuerCache holds one issuer's discovery document and keys. mu is held across fetches so concurrent sign-ins
// do not fetch the same document twice.
type issuerCache struct {
	mu          sync.Mutex
	disc        *upstreamDiscovery
	discAt      time.Time
	keys        map[string]crypto.PublicKey
	keysAt      time.Time
	lastRefetch time.Time // last JWKS fetch caused by an unknown kid (at most one per minute)
}

func (s *Service) issuer(iss string) *issuerCache {
	s.upMu.Lock()
	defer s.upMu.Unlock()
	if s.upCache == nil {
		s.upCache = map[string]*issuerCache{}
	}
	c := s.upCache[iss]
	if c == nil {
		c = &issuerCache{}
		s.upCache[iss] = c
	}
	return c
}

// upstreamClient bounds every call to the provider: 10 s, no redirect off the issuer's host.
func (s *Service) upstreamClient(up *Upstream) *http.Client {
	var rt http.RoundTripper
	if s.UpstreamHTTP != nil {
		rt = s.UpstreamHTTP.Transport
	}
	return &http.Client{Transport: rt, Timeout: upstreamTimeout, CheckRedirect: checkUpstreamRedirect}
}

// checkUpstreamRedirect follows a redirect only on the host of the request being followed, with the same scheme
// or http to https (never a downgrade), and at most 5 times.
func checkUpstreamRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("redirect refused: too many")
	}
	prev := via[len(via)-1].URL
	if req.URL.Host != prev.Host {
		return errors.New("redirect refused: another host")
	}
	if req.URL.Scheme != prev.Scheme && !(prev.Scheme == "http" && req.URL.Scheme == "https") {
		return errors.New("redirect refused: scheme change")
	}
	return nil
}

// upstreamDo runs a request and reads at most 1 MiB of the answer.
func (s *Service) upstreamDo(up *Upstream, req *http.Request) (int, []byte, error) {
	resp, err := s.upstreamClient(up).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, upstreamMaxBody+1))
	if err != nil {
		return 0, nil, err
	}
	if len(b) > upstreamMaxBody {
		return 0, nil, errors.New("answer larger than 1 MiB")
	}
	return resp.StatusCode, b, nil
}

func (s *Service) upstreamGet(ctx context.Context, up *Upstream, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	status, b, err := s.upstreamDo(up, req)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("status %d", status)
	}
	return b, nil
}

// discovery returns the provider's configuration, cached for an hour. Its issuer must equal the configured one.
func (s *Service) discovery(ctx context.Context, up *Upstream) (*upstreamDiscovery, *upstreamError) {
	if !SecureURL(up.Issuer) {
		return nil, errUpstreamUnavailable(up, "issuer is not https")
	}
	c := s.issuer(up.Issuer)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.disc != nil && s.now().Sub(c.discAt) < upstreamCacheTTL {
		return c.disc, nil
	}
	b, err := s.upstreamGet(ctx, up, strings.TrimSuffix(up.Issuer, "/")+"/.well-known/openid-configuration")
	if err != nil {
		return nil, errUpstreamUnavailable(up, "discovery: "+err.Error())
	}
	var d upstreamDiscovery
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, errUpstreamUnavailable(up, "discovery is not JSON")
	}
	if d.Issuer != up.Issuer {
		return nil, errUpstreamUnavailable(up, "discovery issuer differs from the configured issuer")
	}
	if d.AuthorizationEndpoint == "" || d.TokenEndpoint == "" || d.JWKSURI == "" {
		return nil, errUpstreamUnavailable(up, "discovery lacks an endpoint")
	}
	// a plain-http JWKS or token endpoint would let the network substitute keys or read codes
	if !SecureURL(d.JWKSURI) || !SecureURL(d.TokenEndpoint) || !SecureURL(d.AuthorizationEndpoint) {
		return nil, errUpstreamUnavailable(up, "discovery names an endpoint that is not https")
	}
	c.disc, c.discAt = &d, s.now()
	return c.disc, nil
}

// upstreamKey returns the issuer's key for kid. The JWKS is cached for an hour; an unknown kid refetches it at
// most once a minute, so a rotation heals itself and a flood of bad kids does not reach the provider.
func (s *Service) upstreamKey(ctx context.Context, up *Upstream, kid string) (crypto.PublicKey, *upstreamError) {
	disc, uerr := s.discovery(ctx, up)
	if uerr != nil {
		return nil, uerr
	}
	c := s.issuer(up.Issuer)
	c.mu.Lock()
	defer c.mu.Unlock()
	now := s.now()
	if c.keys == nil || now.Sub(c.keysAt) >= upstreamCacheTTL {
		if uerr := s.fetchJWKS(ctx, up, disc, c); uerr != nil {
			return nil, uerr
		}
	}
	if k, ok := c.keys[kid]; ok {
		return k, nil
	}
	if now.Sub(c.lastRefetch) < jwksRefetchEvery {
		return nil, errUpstreamInvalid(up, "id_token kid is not in the JWKS")
	}
	c.lastRefetch = now
	if uerr := s.fetchJWKS(ctx, up, disc, c); uerr != nil {
		return nil, uerr
	}
	if k, ok := c.keys[kid]; ok {
		return k, nil
	}
	return nil, errUpstreamInvalid(up, "id_token kid is not in the JWKS")
}

func (s *Service) fetchJWKS(ctx context.Context, up *Upstream, disc *upstreamDiscovery, c *issuerCache) *upstreamError {
	b, err := s.upstreamGet(ctx, up, disc.JWKSURI)
	if err != nil {
		return errUpstreamUnavailable(up, "jwks: "+err.Error())
	}
	var set struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(b, &set); err != nil {
		return errUpstreamUnavailable(up, "jwks is not JSON")
	}
	keys := map[string]crypto.PublicKey{}
	for _, k := range set.Keys {
		if use, _ := k["use"].(string); use != "" && use != "sig" {
			continue
		}
		kid, _ := k["kid"].(string)
		if pk := parseJWK(k); pk != nil {
			keys[kid] = pk
		}
	}
	c.keys, c.keysAt = keys, s.now()
	return nil
}

// parseJWK reads an RSA (2048 bits or more) or P-256 public key; anything else is skipped.
func parseJWK(k map[string]any) crypto.PublicKey {
	b64 := func(name string) []byte {
		v, _ := k[name].(string)
		b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(v, "="))
		if err != nil {
			return nil
		}
		return b
	}
	switch k["kty"] {
	case "RSA":
		n, e := b64("n"), b64("e")
		if len(n) == 0 || len(e) == 0 || len(e) > 4 {
			return nil
		}
		pk := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		if pk.N.BitLen() < 2048 || pk.E < 3 {
			return nil
		}
		return pk
	case "EC":
		if k["crv"] != "P-256" {
			return nil
		}
		x, y := b64("x"), b64("y")
		if len(x) != 32 || len(y) != 32 {
			return nil
		}
		// ecdh refuses a point that is not on the curve
		if _, err := ecdh.P256().NewPublicKey(append(append([]byte{4}, x...), y...)); err != nil {
			return nil
		}
		return &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
	}
	return nil
}

// exchangeCode trades the code for tokens with the PKCE verifier: client_secret_basic for a confidential client,
// client_id in the body for a public one. It returns the raw id_token.
func (s *Service) exchangeCode(ctx context.Context, up *Upstream, disc *upstreamDiscovery, code, verifier string) (string, *upstreamError) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {s.upstreamRedirectURI(up)}, "code_verifier": {verifier}}
	if up.ClientSecret == "" {
		form.Set("client_id", up.ClientID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, disc.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", errUpstreamUnavailable(up, "token_endpoint is not a URL")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if up.ClientSecret != "" {
		req.SetBasicAuth(url.QueryEscape(up.ClientID), url.QueryEscape(up.ClientSecret)) // RFC 6749 §2.3.1
	}
	status, b, err := s.upstreamDo(up, req)
	if err != nil {
		return "", errUpstreamUnavailable(up, "token endpoint: "+err.Error())
	}
	if status >= 400 && status < 500 {
		return "", errUpstreamInvalid(up, fmt.Sprintf("token endpoint refused the code (status %d)", status))
	}
	if status != 200 {
		return "", errUpstreamUnavailable(up, fmt.Sprintf("token endpoint status %d", status))
	}
	var out struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(b, &out); err != nil || out.IDToken == "" {
		return "", errUpstreamUnavailable(up, "token answer has no id_token")
	}
	return out.IDToken, nil
}

// verifyIDToken applies every check of contract 0042 §4. Only after they pass are other claims read.
func (s *Service) verifyIDToken(ctx context.Context, up *Upstream, raw, nonceHash string) (map[string]any, *upstreamError) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errUpstreamInvalid(up, "id_token is not a JWS")
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(hb, &hdr) != nil {
		return nil, errUpstreamInvalid(up, "id_token header unreadable")
	}
	if hdr.Alg != "RS256" && hdr.Alg != "ES256" {
		return nil, errUpstreamInvalid(up, "id_token alg not allowed")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errUpstreamInvalid(up, "id_token signature unreadable")
	}
	key, uerr := s.upstreamKey(ctx, up, hdr.Kid)
	if uerr != nil {
		return nil, uerr
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	ok := false
	switch pk := key.(type) {
	case *rsa.PublicKey: // the key type must match alg: an RSA key never verifies an ES256 header, and back
		ok = hdr.Alg == "RS256" && rsa.VerifyPKCS1v15(pk, crypto.SHA256, sum[:], sig) == nil
	case *ecdsa.PublicKey:
		ok = hdr.Alg == "ES256" && len(sig) == 64 &&
			ecdsa.Verify(pk, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]))
	}
	if !ok {
		return nil, errUpstreamInvalid(up, "id_token signature does not verify")
	}
	cb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errUpstreamInvalid(up, "id_token claims unreadable")
	}
	var claims map[string]any
	if err := json.Unmarshal(cb, &claims); err != nil {
		return nil, errUpstreamInvalid(up, "id_token claims unreadable")
	}
	if iss, _ := claims["iss"].(string); iss != up.Issuer {
		return nil, errUpstreamInvalid(up, "iss differs from the configured issuer")
	}
	var aud []string
	switch a := claims["aud"].(type) {
	case string:
		aud = []string{a}
	case []any:
		for _, x := range a {
			if s, ok := x.(string); ok {
				aud = append(aud, s)
			}
		}
	}
	found := false
	for _, a := range aud {
		found = found || a == up.ClientID
	}
	if !found {
		return nil, errUpstreamInvalid(up, "aud does not contain client_id")
	}
	azp, hasAzp := claims["azp"].(string)
	if len(aud) > 1 && !hasAzp {
		return nil, errUpstreamInvalid(up, "several audiences and no azp")
	}
	if _, present := claims["azp"]; present && azp != up.ClientID {
		return nil, errUpstreamInvalid(up, "azp names another client")
	}
	now := s.now()
	exp, okExp := claims["exp"].(float64)
	if !okExp || !now.Before(time.Unix(int64(exp), 0).Add(clockSkew)) {
		return nil, errUpstreamInvalid(up, "id_token expired")
	}
	iat, okIat := claims["iat"].(float64)
	if !okIat || time.Unix(int64(iat), 0).After(now.Add(maxIATAhead)) {
		return nil, errUpstreamInvalid(up, "iat missing or too far ahead")
	}
	nonce, _ := claims["nonce"].(string)
	if nonce == "" || subtle.ConstantTimeCompare([]byte(HashToken(nonce)), []byte(nonceHash)) != 1 {
		return nil, errUpstreamInvalid(up, "nonce does not match")
	}
	if sub, _ := claims["sub"].(string); sub == "" {
		return nil, errUpstreamInvalid(up, "no sub")
	}
	return claims, nil
}
