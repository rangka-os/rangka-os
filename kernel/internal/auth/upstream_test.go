package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// ---- a fake OpenID Provider: discovery, JWKS, token endpoint ----

type opKey struct {
	kid   string
	alg   string // RS256 or ES256
	rsa   *rsa.PrivateKey
	ecdsa *ecdsa.PrivateKey
}

func newRSAKey(t *testing.T, kid string) *opKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return &opKey{kid: kid, alg: "RS256", rsa: k}
}

func newECKey(t *testing.T, kid string) *opKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &opKey{kid: kid, alg: "ES256", ecdsa: k}
}

func (k *opKey) jwk() map[string]any {
	b := base64.RawURLEncoding.EncodeToString
	if k.rsa != nil {
		return map[string]any{"kty": "RSA", "kid": k.kid, "use": "sig", "alg": "RS256", "n": b(k.rsa.N.Bytes()), "e": b(big.NewInt(int64(k.rsa.E)).Bytes())}
	}
	pad := func(i *big.Int) []byte { return i.FillBytes(make([]byte, 32)) }
	return map[string]any{"kty": "EC", "kid": k.kid, "use": "sig", "alg": "ES256", "crv": "P-256", "x": b(pad(k.ecdsa.X)), "y": b(pad(k.ecdsa.Y))}
}

// sign makes a JWS with the key; alg overrides the header (to forge none/HS256 headers).
func (k *opKey) sign(t *testing.T, claims map[string]any, alg string) string {
	t.Helper()
	if alg == "" {
		alg = k.alg
	}
	h, _ := json.Marshal(map[string]any{"alg": alg, "kid": k.kid, "typ": "JWT"})
	c, _ := json.Marshal(claims)
	in := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	sum := sha256.Sum256([]byte(in))
	var sig []byte
	if k.rsa != nil {
		s, err := rsa.SignPKCS1v15(rand.Reader, k.rsa, crypto.SHA256, sum[:])
		if err != nil {
			t.Fatal(err)
		}
		sig = s
	} else {
		r, s, err := ecdsa.Sign(rand.Reader, k.ecdsa, sum[:])
		if err != nil {
			t.Fatal(err)
		}
		sig = append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	}
	return in + "." + base64.RawURLEncoding.EncodeToString(sig)
}

type opGrant struct {
	nonce, challenge, redirect string
	claims                     map[string]any
}

type fakeOP struct {
	t        *testing.T
	srv      *httptest.Server
	clientID string
	secret   string // "" = public client

	mu         sync.Mutex
	discIssuer string   // issuer in the discovery document ("" = srv.URL)
	published  []*opKey // in the JWKS
	signer     *opKey
	signAlg    string // header alg override
	grants     map[string]*opGrant
	jwksHits   int
	tokenHits  int
	tokenFail  int // status the token endpoint answers with instead of tokens (0 = normal)
	discBody   []byte
	discRedir  string
	jwksURI    string // jwks_uri in discovery ("" = srv.URL/jwks)
	authHeader string
	tokenForm  url.Values
}

func newFakeOP(t *testing.T, clientID, secret string, key *opKey) *fakeOP {
	op := &fakeOP{t: t, clientID: clientID, secret: secret, published: []*opKey{key}, signer: key, grants: map[string]*opGrant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		op.mu.Lock()
		defer op.mu.Unlock()
		if op.discRedir != "" {
			http.Redirect(w, r, op.discRedir, http.StatusFound)
			return
		}
		if op.discBody != nil {
			w.Write(op.discBody)
			return
		}
		iss := op.discIssuer
		if iss == "" {
			iss = op.srv.URL
		}
		jwks := op.jwksURI
		if jwks == "" {
			jwks = op.srv.URL + "/jwks"
		}
		json.NewEncoder(w).Encode(map[string]any{"issuer": iss, "authorization_endpoint": op.srv.URL + "/authorize?tenant=t1",
			"token_endpoint": op.srv.URL + "/token", "jwks_uri": jwks})
	})
	mux.HandleFunc("GET /jwks", op.serveJWKS)
	mux.HandleFunc("POST /token", op.token)
	op.srv = httptest.NewServer(mux)
	t.Cleanup(op.srv.Close)
	return op
}

func (op *fakeOP) serveJWKS(w http.ResponseWriter, r *http.Request) {
	op.mu.Lock()
	defer op.mu.Unlock()
	op.jwksHits++
	keys := []any{}
	for _, k := range op.published {
		keys = append(keys, k.jwk())
	}
	json.NewEncoder(w).Encode(map[string]any{"keys": keys})
}

func (op *fakeOP) token(w http.ResponseWriter, r *http.Request) {
	op.mu.Lock()
	defer op.mu.Unlock()
	op.tokenHits++
	r.ParseForm()
	op.authHeader, op.tokenForm = r.Header.Get("Authorization"), r.PostForm
	if op.tokenFail != 0 {
		w.WriteHeader(op.tokenFail)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"PROVIDER-SECRET-TEXT"}`))
		return
	}
	refuse := func(why string) {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant", "error_description": why})
	}
	if op.secret != "" {
		id, sec, ok := r.BasicAuth()
		if !ok || id != op.clientID || sec != op.secret || r.PostForm.Get("client_secret") != "" {
			refuse("client authentication")
			return
		}
	} else if r.Header.Get("Authorization") != "" || r.PostForm.Get("client_id") != op.clientID {
		refuse("public client")
		return
	}
	g := op.grants[r.PostForm.Get("code")]
	delete(op.grants, r.PostForm.Get("code"))
	if g == nil || r.PostForm.Get("grant_type") != "authorization_code" || r.PostForm.Get("redirect_uri") != g.redirect {
		refuse("code")
		return
	}
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
		refuse("pkce")
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"access_token": "AT-" + RandomToken("", 8), "token_type": "Bearer",
		"id_token": op.signer.sign(op.t, g.claims, op.signAlg)})
}

// authorize plays the provider's authorization endpoint for the redirect the kernel sent: it checks the request
// and returns a code bound to it, with the claims the id_token will carry (mutate edits them).
func (op *fakeOP) authorize(t *testing.T, location string, now time.Time, sub, email string, verified any, mutate func(map[string]any)) (code, state string) {
	t.Helper()
	u, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if !strings.HasPrefix(location, op.srv.URL+"/authorize?") || q.Get("tenant") != "t1" {
		t.Fatalf("redirect not to the authorization endpoint (with its own query kept): %s", location)
	}
	for k, want := range map[string]string{"response_type": "code", "scope": "openid email profile", "code_challenge_method": "S256", "client_id": op.clientID} {
		if q.Get(k) != want {
			t.Fatalf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	for _, k := range []string{"state", "nonce", "code_challenge", "redirect_uri"} {
		if q.Get(k) == "" {
			t.Fatalf("authorization request lacks %s", k)
		}
	}
	if b, _ := base64.RawURLEncoding.DecodeString(q.Get("state")); len(b) < 32 {
		t.Fatalf("state has %d bytes of entropy, want >= 32", len(b))
	}
	claims := map[string]any{"iss": op.srv.URL, "sub": sub, "aud": op.clientID, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		"nonce": q.Get("nonce"), "email": email, "name": "Dewi Lestari"}
	if verified != nil {
		claims["email_verified"] = verified
	}
	if mutate != nil {
		mutate(claims)
	}
	code = "CODE-" + RandomToken("", 16)
	op.mu.Lock()
	op.grants[code] = &opGrant{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri"), claims: claims}
	op.mu.Unlock()
	return code, q.Get("state")
}

// ---- the kernel side ----

type upEnv struct {
	t    *testing.T
	ctx  context.Context
	db   *pg.Pool
	s    *Service
	op   *fakeOP
	h    http.Handler
	now  time.Time
	logs *strings.Builder
	mu   sync.Mutex
	bind string // the browser-binding cookie of the last start (this test's "browser")
}

const testSecret = "client-secret-DO-NOT-LOG"

func newUpEnv(t *testing.T, secret string, key *opKey) *upEnv {
	t.Helper()
	ctx, db := identEnv(t)
	op := newFakeOP(t, "rangka-kopi", secret, key)
	e := &upEnv{t: t, ctx: ctx, db: db, op: op, now: time.Now().Truncate(time.Second), logs: &strings.Builder{}}
	e.s = &Service{DB: db, Issuer: "https://kopi.example", DefaultOrg: "pt_alpha", SessionTTL: 24 * time.Hour,
		Now: func() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now },
		Upstreams: []Upstream{
			{ID: "kantor", Label: "Kantor", Issuer: op.srv.URL, ClientID: "rangka-kopi", ClientSecret: secret},
			{ID: "lain", Label: "Lain", Issuer: op.srv.URL, ClientID: "rangka-kopi", ClientSecret: secret},
		},
		Log: func(f string, a ...any) { e.mu.Lock(); defer e.mu.Unlock(); fmt.Fprintf(e.logs, f+"\n", a...) }}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/providers", e.s.Providers)
	mux.HandleFunc("GET /auth/upstream/{id}/start", e.s.UpstreamStart)
	mux.HandleFunc("GET /auth/upstream/{id}/callback", e.s.UpstreamCallback)
	e.h = mux
	return e
}

func (e *upEnv) advance(d time.Duration) { e.mu.Lock(); e.now = e.now.Add(d); e.mu.Unlock() }

func (e *upEnv) get(path string, hdr ...string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.getWith("", path, hdr...)
}

// getWith sends the browser-binding cookie bind (none when "").
func (e *upEnv) getWith(bind, path string, hdr ...string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if bind != "" {
		req.AddCookie(&http.Cookie{Name: bindCookieName, Value: bind})
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	return w
}

// start begins a sign-in and returns the provider redirect.
func (e *upEnv) start(id, to string) string {
	e.t.Helper()
	w := e.get("/auth/upstream/" + id + "/start?to=" + url.QueryEscape(to))
	if w.Code != 302 {
		e.t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	var ck *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == bindCookieName {
			ck = c
		}
	}
	if ck == nil || ck.Value == "" || !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/auth/upstream/"+id+"/" || ck.MaxAge != 600 {
		e.t.Fatalf("browser-binding cookie: %+v", ck)
	}
	e.bind = ck.Value
	return w.Header().Get("Location")
}

// callback finishes a flow in the browser that started it (it sends the binding cookie).
func (e *upEnv) callback(id, code, state string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.getWith(e.bind, "/auth/upstream/"+id+"/callback?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(state))
}

// signIn runs start → provider → callback.
func (e *upEnv) signIn(sub, email string, verified any, mutate func(map[string]any)) *httptest.ResponseRecorder {
	e.t.Helper()
	code, state := e.op.authorize(e.t, e.start("kantor", "/app/#/r/sales_order"), e.now, sub, email, verified, mutate)
	return e.callback("kantor", code, state)
}

func (e *upEnv) sessions() int {
	e.t.Helper()
	res, err := e.db.Query(e.ctx, "select count(*) from rk_sessions")
	if err != nil {
		e.t.Fatal(err)
	}
	return int(res.Rows[0][0].(int64))
}

// member creates a passwordless member of pt_alpha, linked to sub at the provider when sub is not "".
func (e *upEnv) member(email, sub string) int64 {
	e.t.Helper()
	id, _, err := UpsertMember(e.ctx, e.db, email, "", "pt_alpha", "cashier", []string{"JKT"})
	if err != nil {
		e.t.Fatal(err)
	}
	if sub != "" {
		if err := LinkIdentity(e.ctx, e.db, e.op.srv.URL, sub, id); err != nil {
			e.t.Fatal(err)
		}
	}
	return id
}

func wantRefused(t *testing.T, w *httptest.ResponseRecorder, status int, code, copy string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d (%s): %s", w.Code, status, code, w.Body)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") || !strings.Contains(w.Body.String(), copy) {
		t.Fatalf("want the sign-in page with %q, got %s %s", copy, w.Header().Get("Content-Type"), w.Body)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == CookieName || c.MaxAge >= 0 {
			t.Fatalf("a refused sign-in set a cookie: %v", c)
		}
	}
}

// assertLogsClean checks that none of the given values reached the operator's log.
func (e *upEnv) assertLogsClean(values ...string) {
	e.t.Helper()
	e.mu.Lock()
	logs := e.logs.String()
	e.mu.Unlock()
	for _, v := range append(values, testSecret) {
		if v != "" && strings.Contains(logs, v) {
			e.t.Fatalf("log contains a secret value %q:\n%s", v, logs)
		}
	}
}

// ---- tests ----

func TestUpstreamHappyPathConfidentialRSA(t *testing.T) {
	e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
	uid := e.member("dewi@kopi.id", "sub-dewi")
	loc := e.start("kantor", "/app/#/r/sales_order")
	if q, _ := url.Parse(loc); q.Query().Get("redirect_uri") != "https://kopi.example/auth/upstream/kantor/callback" {
		t.Fatalf("redirect_uri = %s", q.Query().Get("redirect_uri"))
	}
	code, state := e.op.authorize(t, loc, e.now, "sub-dewi", "other@mail.id", nil, nil)
	w := e.callback("kantor", code, state)
	if w.Code != 302 || w.Header().Get("Location") != "/app/#/r/sales_order" {
		t.Fatalf("callback: %d %q %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	// client_secret_basic and the PKCE verifier reached the token endpoint
	if !strings.HasPrefix(e.op.authHeader, "Basic ") || e.op.tokenForm.Get("code_verifier") == "" || e.op.tokenForm.Get("client_secret") != "" {
		t.Fatalf("token request: auth %q form %v", e.op.authHeader, e.op.tokenForm)
	}
	var ck *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == CookieName {
			ck = c
		}
	}
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.MaxAge != int(UpstreamSessionTTL.Seconds()) || !ck.Secure {
		t.Fatalf("session cookie: %+v", ck)
	}
	// the cookie resolves to the member (what /auth/me shows), and the session lasts 12 hours, not session_ttl
	req := httptest.NewRequest("GET", "/auth/me", nil)
	req.AddCookie(ck)
	p, err := e.s.Resolve(req)
	if err != nil || p == nil || p.User.ID != uid || p.User.Org != "pt_alpha" || p.User.Roles[0] != "cashier" || p.User.Outlets[0] != "JKT" || p.Via != "session" {
		t.Fatalf("principal %+v, %v", p, err)
	}
	if p.User.FullName != "Dewi Lestari" {
		t.Fatalf("empty name not filled from the id_token: %q", p.User.FullName)
	}
	res, _ := e.db.Query(e.ctx, "select expires_at from rk_sessions")
	if exp := res.Rows[0][0].(time.Time); !exp.Equal(e.now.Add(12 * time.Hour)) {
		t.Fatalf("session expires %v, want now+12h", exp)
	}
	// a known pair ignores the e-mail claim: no link was made by e-mail and the account's e-mail is unchanged
	if p.User.Email != "dewi@kopi.id" {
		t.Fatalf("email changed to %s", p.User.Email)
	}
	e.assertLogsClean(code, state, ck.Value, e.op.tokenForm.Get("code_verifier"))
}

func TestUpstreamPublicClientES256(t *testing.T) {
	e := newUpEnv(t, "", newECKey(t, "ec-1"))
	e.member("dewi@kopi.id", "sub-dewi")
	w := e.signIn("sub-dewi", "", nil, nil)
	if w.Code != 302 {
		t.Fatalf("public client sign-in: %d %s", w.Code, w.Body)
	}
	if e.op.authHeader != "" || e.op.tokenForm.Get("client_id") != "rangka-kopi" || e.op.tokenForm.Get("code_verifier") == "" {
		t.Fatalf("public client token request: auth %q form %v", e.op.authHeader, e.op.tokenForm)
	}
}

func TestUpstreamLinksByVerifiedEmail(t *testing.T) {
	e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
	uid := e.member("dewi@kopi.id", "")
	w := e.signIn("sub-new", "Dewi@Kopi.id", true, nil)
	if w.Code != 302 {
		t.Fatalf("verified e-mail of a member: %d %s", w.Code, w.Body)
	}
	if got, err := UserByIdentity(e.ctx, e.db, e.op.srv.URL, "sub-new"); err != nil || got != uid {
		t.Fatalf("link not stored: %d %v", got, err)
	}
}

func TestUpstreamUnverifiedEmailGetsNoLink(t *testing.T) {
	e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
	e.member("victim@kopi.id", "")
	for _, verified := range []any{false, nil, "false", 1} {
		w := e.signIn("attacker-sub", "victim@kopi.id", verified, nil)
		wantRefused(t, w, 403, "membership_pending", "Aksesmu sedang disiapkan. Coba lagi sebentar.")
	}
	if _, err := UserByIdentity(e.ctx, e.db, e.op.srv.URL, "attacker-sub"); err == nil {
		t.Fatal("an unverified e-mail linked the identity")
	}
	if e.sessions() != 0 {
		t.Fatal("session opened")
	}
	// membership_pending is expected traffic, not an operator warning
	if strings.Contains(e.logs.String(), "membership_pending") {
		t.Fatalf("membership_pending was logged: %s", e.logs)
	}
}

func TestUpstreamUnknownPersonIsPending(t *testing.T) {
	e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
	w := e.signIn("sub-x", "nobody@kopi.id", true, nil)
	wantRefused(t, w, 403, "membership_pending", "Aksesmu sedang disiapkan.")
	// an account with no membership is also pending; sign-in never creates a user or a membership
	if _, err := e.db.Query(e.ctx, "insert into rk_users (email, full_name) values ('lone@kopi.id', '')"); err != nil {
		t.Fatal(err)
	}
	wantRefused(t, e.signIn("sub-y", "lone@kopi.id", true, nil), 403, "membership_pending", "Aksesmu sedang disiapkan.")
	res, _ := e.db.Query(e.ctx, "select count(*) from rk_members")
	if n := res.Rows[0][0].(int64); n != 0 {
		t.Fatalf("sign-in created %d memberships", n)
	}
}

func TestUpstreamKnownIdentityWithoutMembership(t *testing.T) {
	e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
	uid := e.member("dewi@kopi.id", "sub-dewi")
	if err := RemoveMember(e.ctx, e.db, "pt_alpha", uid); err != nil {
		t.Fatal(err)
	}
	w := e.signIn("sub-dewi", "dewi@kopi.id", true, nil)
	wantRefused(t, w, 403, "no_membership", ErrNoMembership.Error())
	if e.sessions() != 0 {
		t.Fatal("session opened")
	}
}

func TestUpstreamIDTokenChecks(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(c map[string]any)
		alg    string
	}{
		{"aud is another client", func(c map[string]any) { c["aud"] = "other-client" }, ""},
		{"azp names another client", func(c map[string]any) { c["aud"] = []any{"rangka-kopi", "other"}; c["azp"] = "other" }, ""},
		{"several aud without azp", func(c map[string]any) { c["aud"] = []any{"rangka-kopi", "other"} }, ""},
		{"azp other with single aud", func(c map[string]any) { c["azp"] = "other" }, ""},
		{"wrong iss", func(c map[string]any) { c["iss"] = "https://evil.example" }, ""},
		{"iss with trailing slash", func(c map[string]any) { c["iss"] = c["iss"].(string) + "/" }, ""},
		{"expired beyond skew", func(c map[string]any) { c["exp"] = time.Now().Add(-2 * time.Minute).Unix() }, ""},
		{"iat far ahead", func(c map[string]any) { c["iat"] = time.Now().Add(10 * time.Minute).Unix() }, ""},
		{"no iat", func(c map[string]any) { delete(c, "iat") }, ""},
		{"bad nonce", func(c map[string]any) { c["nonce"] = "not-the-nonce" }, ""},
		{"no nonce", func(c map[string]any) { delete(c, "nonce") }, ""},
		{"no sub", func(c map[string]any) { c["sub"] = "" }, ""},
		{"alg none", nil, "none"},
		{"alg HS256", nil, "HS256"},
		{"alg ES256 on an RSA key", nil, "ES256"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
			e.member("dewi@kopi.id", "sub-dewi")
			e.op.signAlg = tc.alg
			w := e.signIn("sub-dewi", "dewi@kopi.id", true, tc.mutate)
			wantRefused(t, w, 401, "upstream_invalid", "Masuk dengan Kantor belum berhasil. Coba lagi.")
			if e.sessions() != 0 {
				t.Fatal("session opened")
			}
		})
	}
	t.Run("expiry within skew passes", func(t *testing.T) {
		e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
		e.member("dewi@kopi.id", "sub-dewi")
		w := e.signIn("sub-dewi", "", nil, func(c map[string]any) { c["exp"] = e.now.Add(-30 * time.Second).Unix() })
		if w.Code != 302 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	})
	t.Run("signature by a key the provider does not publish", func(t *testing.T) {
		e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
		e.member("dewi@kopi.id", "sub-dewi")
		e.op.signer = newRSAKey(t, "rsa-1") // same kid, other key
		wantRefused(t, e.signIn("sub-dewi", "", nil, nil), 401, "upstream_invalid", "belum berhasil")
	})
}

func TestUpstreamReplayedState(t *testing.T) {
	e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
	e.member("dewi@kopi.id", "sub-dewi")
	code, state := e.op.authorize(t, e.start("kantor", "/app/"), e.now, "sub-dewi", "", nil, nil)
	if w := e.callback("kantor", code, state); w.Code != 302 {
		t.Fatalf("first callback: %d %s", w.Code, w.Body)
	}
	w := e.callback("kantor", code, state)
	wantRefused(t, w, 400, "invalid_state", "Sesi masuk sudah habis. Mulai lagi.")
	if e.op.tokenHits != 1 || e.sessions() != 1 {
		t.Fatalf("replay reached the token endpoint (%d) or opened a session (%d)", e.op.tokenHits, e.sessions())
	}
	// a state the kernel never issued (another browser's forged callback)
	wantRefused(t, e.callback("kantor", code, "forged"), 400, "invalid_state", "Sesi masuk sudah habis.")
	wantRefused(t, e.get("/auth/upstream/kantor/callback?code=x"), 400, "invalid_state", "Sesi masuk sudah habis.")
	e.assertLogsClean(code, state)
}

func TestUpstreamStateForAnotherUpstream(t *testing.T) {
	e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
	e.member("dewi@kopi.id", "sub-dewi")
	code, state := e.op.authorize(t, e.start("kantor", "/app/"), e.now, "sub-dewi", "", nil, nil)
	wantRefused(t, e.callback("lain", code, state), 400, "invalid_state", "Sesi masuk sudah habis.")
	if e.op.tokenHits != 0 {
		t.Fatal("token endpoint called for another upstream's state")
	}
	// the misdirected callback did not burn the state of the flow it was started for
	if w := e.callback("kantor", code, state); w.Code != 302 {
		t.Fatalf("original flow: %d %s", w.Code, w.Body)
	}
}

func TestUpstreamStateExpires(t *testing.T) {
	e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
	e.member("dewi@kopi.id", "sub-dewi")
	code, state := e.op.authorize(t, e.start("kantor", "/app/"), e.now, "sub-dewi", "", nil, nil)
	e.advance(10*time.Minute + time.Second)
	wantRefused(t, e.callback("kantor", code, state), 400, "invalid_state", "Sesi masuk sudah habis.")
	if e.op.tokenHits != 0 {
		t.Fatal("token endpoint called for an expired state")
	}
}

func TestUpstreamUnknownKidRefetchAndRotation(t *testing.T) {
	a := newRSAKey(t, "key-a")
	e := newUpEnv(t, testSecret, a)
	e.member("dewi@kopi.id", "sub-dewi")
	if w := e.signIn("sub-dewi", "", nil, nil); w.Code != 302 {
		t.Fatalf("key a: %d %s", w.Code, w.Body)
	}
	if e.op.jwksHits != 1 {
		t.Fatalf("jwks fetched %d times, want 1", e.op.jwksHits)
	}
	// the provider rotates to an EC key b and drops a: the unknown kid refetches once, then succeeds
	b := newECKey(t, "key-b")
	e.op.mu.Lock()
	e.op.published, e.op.signer = []*opKey{b}, b
	e.op.mu.Unlock()
	if w := e.signIn("sub-dewi", "", nil, nil); w.Code != 302 {
		t.Fatalf("after rotation: %d %s", w.Code, w.Body)
	}
	if e.op.jwksHits != 2 {
		t.Fatalf("jwks fetched %d times, want 2", e.op.jwksHits)
	}
	// a token still signed with the dropped key is refused, and within the minute no refetch happens
	e.op.signer = a
	wantRefused(t, e.signIn("sub-dewi", "", nil, nil), 401, "upstream_invalid", "belum berhasil")
	e.op.signer = newRSAKey(t, "key-zzz")
	wantRefused(t, e.signIn("sub-dewi", "", nil, nil), 401, "upstream_invalid", "belum berhasil")
	if e.op.jwksHits != 2 {
		t.Fatalf("unknown kids within a minute fetched the JWKS %d times, want 2", e.op.jwksHits)
	}
	// a minute later an unknown kid may refetch again
	e.advance(61 * time.Second)
	wantRefused(t, e.signIn("sub-dewi", "", nil, nil), 401, "upstream_invalid", "belum berhasil")
	if e.op.jwksHits != 3 {
		t.Fatalf("jwks fetched %d times, want 3", e.op.jwksHits)
	}
	// after an hour the cached JWKS is refreshed even for a known kid
	e.op.signer = b
	e.advance(time.Hour)
	if w := e.signIn("sub-dewi", "", nil, nil); w.Code != 302 {
		t.Fatalf("after an hour: %d %s", w.Code, w.Body)
	}
	if e.op.jwksHits != 4 {
		t.Fatalf("jwks fetched %d times, want 4", e.op.jwksHits)
	}
}

func TestUpstreamProviderErrorNotReflected(t *testing.T) {
	e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
	_, state := e.op.authorize(t, e.start("kantor", "/app/"), e.now, "s", "", nil, nil)
	w := e.getWith(e.bind, "/auth/upstream/kantor/callback?state="+url.QueryEscape(state)+
		"&error=access_denied&error_description="+url.QueryEscape("<script>alert('EVIL-PROVIDER-TEXT')</script>"))
	wantRefused(t, w, 401, "upstream_invalid", "Masuk dengan Kantor belum berhasil. Coba lagi.")
	if strings.Contains(w.Body.String(), "EVIL-PROVIDER-TEXT") || strings.Contains(w.Body.String(), "access_denied") {
		t.Fatalf("provider text reflected: %s", w.Body)
	}
	if e.op.tokenHits != 0 {
		t.Fatal("token endpoint called after a provider error")
	}
	e.assertLogsClean("EVIL-PROVIDER-TEXT", state)
	// a refused token exchange: the provider's description is neither shown nor logged
	e.member("dewi@kopi.id", "sub-dewi")
	e.op.tokenFail = 400
	w = e.signIn("sub-dewi", "", nil, nil)
	wantRefused(t, w, 401, "upstream_invalid", "belum berhasil")
	if strings.Contains(w.Body.String(), "PROVIDER-SECRET-TEXT") {
		t.Fatal("token endpoint text reflected")
	}
	e.assertLogsClean("PROVIDER-SECRET-TEXT")
}

func TestUpstreamUnavailable(t *testing.T) {
	t.Run("token endpoint 500", func(t *testing.T) {
		e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
		e.member("dewi@kopi.id", "sub-dewi")
		e.op.tokenFail = 500
		wantRefused(t, e.signIn("sub-dewi", "", nil, nil), 502, "upstream_unavailable", "Kantor sedang tidak bisa dihubungi. Coba lagi sebentar.")
	})
	t.Run("discovery issuer differs", func(t *testing.T) {
		e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
		e.op.discIssuer = e.op.srv.URL + "/"
		wantRefused(t, e.get("/auth/upstream/kantor/start"), 502, "upstream_unavailable", "Kantor sedang tidak bisa dihubungi.")
	})
	t.Run("discovery over 1 MiB", func(t *testing.T) {
		e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
		e.op.discBody = []byte(`{"issuer":"` + strings.Repeat("x", 1<<20) + `"}`)
		wantRefused(t, e.get("/auth/upstream/kantor/start"), 502, "upstream_unavailable", "sedang tidak bisa dihubungi")
	})
	t.Run("redirect off the issuer host", func(t *testing.T) {
		other := httptest.NewServer(http.NotFoundHandler())
		t.Cleanup(other.Close)
		e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
		e.op.discRedir = other.URL + "/.well-known/openid-configuration"
		wantRefused(t, e.get("/auth/upstream/kantor/start"), 502, "upstream_unavailable", "sedang tidak bisa dihubungi")
	})
	t.Run("provider down", func(t *testing.T) {
		e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
		e.op.srv.Close()
		wantRefused(t, e.get("/auth/upstream/kantor/start"), 502, "upstream_unavailable", "sedang tidak bisa dihubungi")
	})
}

func TestUpstreamStartTargets(t *testing.T) {
	e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
	e.member("dewi@kopi.id", "sub-dewi")
	for to, want := range map[string]string{"//evil.example/x": "/app/", "https://evil.example": "/app/", "": "/app/", "/app/#/inbox": "/app/#/inbox"} {
		code, state := e.op.authorize(t, e.start("kantor", to), e.now, "sub-dewi", "", nil, nil)
		w := e.callback("kantor", code, state)
		if w.Code != 302 || w.Header().Get("Location") != want {
			t.Fatalf("to=%q: %d %q, want %q", to, w.Code, w.Header().Get("Location"), want)
		}
	}
	wantRefused(t, e.get("/auth/upstream/nope/start"), 404, "unknown_upstream", "Penyedia akun tidak ditemukan.")
	wantRefused(t, e.get("/auth/upstream/nope/callback?state=x&code=y"), 404, "unknown_upstream", "Penyedia akun tidak ditemukan.")
	// a JSON client gets the error body
	w := e.get("/auth/upstream/kantor/callback?state=x", "Accept", "application/json")
	var body struct {
		Error struct{ Code, Message string }
	}
	if w.Code != 400 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Error.Code != "invalid_state" {
		t.Fatalf("json error: %d %s", w.Code, w.Body)
	}
}

func TestSafePath(t *testing.T) {
	for in, want := range map[string]string{
		"/app/": "/app/", "/app/#/r/x?y=1": "/app/#/r/x?y=1", "/console/": "/console/",
		"": "/app/", "app": "/app/", "//evil.example": "/app/", "/\\evil.example": "/app/", "\\\\evil": "/app/",
		"https://evil.example/": "/app/", "javascript:alert(1)": "/app/", "/a\nb": "/app/", "/a\x00": "/app/", " /app/": "/app/",
	} {
		if got := SafePath(in); got != want {
			t.Errorf("SafePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProviders(t *testing.T) {
	e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
	for _, tc := range []struct {
		cfg  any
		want string
	}{{nil, "true"}, {true, "true"}, {false, "false"}, {"owners", `"owners"`}, {"owner", "true"}} {
		cfg := tc.cfg
		e.s.Password = func() any { return cfg }
		w := e.get("/auth/providers")
		want := `{"password":` + tc.want + `,"upstream":[{"id":"kantor","label":"Kantor"},{"id":"lain","label":"Lain"}]}`
		if w.Code != 200 || strings.TrimSpace(w.Body.String()) != want {
			t.Fatalf("password %v: %d %s", cfg, w.Code, w.Body)
		}
	}
	if b := e.get("/auth/providers").Body.String(); strings.Contains(b, e.op.srv.URL) || strings.Contains(b, "rangka-kopi") || strings.Contains(b, testSecret) {
		t.Fatalf("providers reveals configuration: %s", b)
	}
	e.s.Upstreams = nil
	if w := e.get("/auth/providers"); strings.TrimSpace(w.Body.String()) != `{"password":true,"upstream":[]}` {
		t.Fatalf("no upstream: %s", w.Body)
	}
}

// Review Focus 2: a callback URL carried to another browser (login CSRF) is refused there, does not consume the
// state of the browser that started the flow, and signs nobody in.
func TestUpstreamStateBoundToBrowser(t *testing.T) {
	e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
	e.member("dewi@kopi.id", "sub-dewi")
	code, state := e.op.authorize(t, e.start("kantor", "/app/"), e.now, "sub-dewi", "", nil, nil)
	path := "/auth/upstream/kantor/callback?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(state)
	other := RandomToken("", 32) // another browser's own binding cookie
	for _, bind := range []string{"", other} {
		w := e.getWith(bind, path)
		wantRefused(t, w, 400, "invalid_state", "Sesi masuk sudah habis. Mulai lagi.")
		cleared := false
		for _, c := range w.Result().Cookies() {
			cleared = cleared || (c.Name == bindCookieName && c.MaxAge < 0 && c.Path == "/auth/upstream/kantor/")
		}
		if !cleared {
			t.Fatalf("binding cookie not cleared: %v", w.Result().Cookies())
		}
	}
	if e.op.tokenHits != 0 || e.sessions() != 0 {
		t.Fatalf("another browser reached the token endpoint (%d) or got a session (%d)", e.op.tokenHits, e.sessions())
	}
	// the browser that started the flow still completes it, once
	if w := e.callback("kantor", code, state); w.Code != 302 || e.sessions() != 1 {
		t.Fatalf("starting browser: %d %s", w.Code, w.Body)
	}
	wantRefused(t, e.callback("kantor", code, state), 400, "invalid_state", "Sesi masuk sudah habis.")
	e.assertLogsClean(e.bind, other, state, code)
}

// A verified e-mail does not add a second subject to a person already linked at this issuer (an e-mail the
// provider reassigned, or a second account claiming it).
func TestUpstreamNoSecondSubjectByEmail(t *testing.T) {
	e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
	uid := e.member("dewi@kopi.id", "sub-dewi")
	wantRefused(t, e.signIn("sub-other", "dewi@kopi.id", true, nil), 403, "membership_pending", "Aksesmu sedang disiapkan.")
	if _, err := UserByIdentity(e.ctx, e.db, e.op.srv.URL, "sub-other"); err == nil {
		t.Fatal("second subject linked")
	}
	if e.sessions() != 0 {
		t.Fatal("session opened")
	}
	// a link at another issuer does not block the first link at this one
	if _, err := e.db.Exec(e.ctx, "delete from rk_identities"); err != nil {
		t.Fatal(err)
	}
	if err := LinkIdentity(e.ctx, e.db, "https://elsewhere.example", "sub-x", uid); err != nil {
		t.Fatal(err)
	}
	if w := e.signIn("sub-other", "dewi@kopi.id", true, nil); w.Code != 302 {
		t.Fatalf("first link at this issuer: %d %s", w.Code, w.Body)
	}
}

// Every writer of rk_users.email stores it trimmed and lower-cased, so the lower-cased claim finds the account.
func TestUpstreamEmailCase(t *testing.T) {
	e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
	if _, err := EnsureUser(e.ctx, e.db, " Owner@Kopi.ID ", "", "pw-123456", "pt_alpha", "owner", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := UpsertMember(e.ctx, e.db, " Kasir@Kopi.ID", "", "pt_alpha", "cashier", nil); err != nil {
		t.Fatal(err)
	}
	res, err := e.db.Query(e.ctx, "select email from rk_users order by email")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(res.Rows); got != "[[kasir@kopi.id] [owner@kopi.id]]" {
		t.Fatalf("stored e-mails %s", got)
	}
	for sub, email := range map[string]string{"sub-owner": "OWNER@kopi.id ", "sub-kasir": "kasir@KOPI.id"} {
		if w := e.signIn(sub, email, true, nil); w.Code != 302 {
			t.Fatalf("%s: %d %s", email, w.Code, w.Body)
		}
	}
}

func TestUpstreamHTTPSOnly(t *testing.T) {
	for raw, want := range map[string]bool{"https://id.example": true, "http://localhost:8080": true, "http://127.0.0.1:1": true,
		"http://[::1]:9": true, "http://id.example": false, "http://localhost.evil.example": false, "ftp://id.example": false, "": false, "/rel": false} {
		if got := SecureURL(raw); got != want {
			t.Errorf("SecureURL(%q) = %v", raw, got)
		}
	}
	e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
	e.op.jwksURI = "http://jwks.example/keys"
	wantRefused(t, e.get("/auth/upstream/kantor/start"), 502, "upstream_unavailable", "Kantor sedang tidak bisa dihubungi.")
	e.s.Upstreams[0].Issuer = "http://id.example"
	wantRefused(t, e.get("/auth/upstream/kantor/start"), 502, "upstream_unavailable", "Kantor sedang tidak bisa dihubungi.")
}

// A JWKS on another host than the issuer (Google's layout) works, including a redirect within that host.
func TestUpstreamJWKSOnAnotherHost(t *testing.T) {
	e := newUpEnv(t, testSecret, newRSAKey(t, "rsa-1"))
	e.member("dewi@kopi.id", "sub-dewi")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/keys/v2", http.StatusFound) })
	mux.HandleFunc("GET /keys/v2", e.op.serveJWKS)
	keys := httptest.NewServer(mux)
	t.Cleanup(keys.Close)
	e.op.jwksURI = strings.Replace(keys.URL, "127.0.0.1", "localhost", 1) + "/keys" // another host name than the issuer's
	if w := e.signIn("sub-dewi", "", nil, nil); w.Code != 302 {
		t.Fatalf("jwks on another host: %d %s", w.Code, w.Body)
	}
	if e.op.jwksHits != 1 {
		t.Fatalf("jwks hits %d", e.op.jwksHits)
	}
}

func TestCheckUpstreamRedirect(t *testing.T) {
	req := func(u string) *http.Request { r, _ := http.NewRequest("GET", u, nil); return r }
	for _, tc := range []struct {
		from, to string
		ok       bool
	}{
		{"https://keys.example/a", "https://keys.example/b", true},
		{"http://localhost/a", "https://localhost/b", true},
		{"https://keys.example/a", "http://keys.example/b", false},
		{"https://keys.example/a", "https://evil.example/b", false},
		{"https://keys.example/a", "https://keys.example:444/b", false},
	} {
		err := checkUpstreamRedirect(req(tc.to), []*http.Request{req(tc.from)})
		if (err == nil) != tc.ok {
			t.Errorf("%s -> %s: %v", tc.from, tc.to, err)
		}
	}
	via := []*http.Request{}
	for i := 0; i < 5; i++ {
		via = append(via, req("https://keys.example/a"))
	}
	if checkUpstreamRedirect(req("https://keys.example/b"), via) == nil {
		t.Error("sixth redirect followed")
	}
}
