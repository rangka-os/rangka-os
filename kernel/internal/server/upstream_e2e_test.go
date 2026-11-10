package server

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// e2eProvider is a minimal OpenID Provider over real HTTP: discovery, JWKS, authorization (answers with a
// redirect back to the kernel, as a browser would be sent) and a token endpoint that checks the client secret,
// the code and PKCE.
type e2eProvider struct {
	srv      *httptest.Server
	key      *rsa.PrivateKey
	clientID string
	secret   string

	now func() time.Time // set once the kernel is up, before the first sign-in

	mu     sync.Mutex
	grants map[string]e2eGrant
}

type e2eGrant struct{ nonce, challenge, redirect, sub, email string }

func newE2EProvider(t *testing.T, clientID, secret string) *e2eProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &e2eProvider{key: key, clientID: clientID, secret: secret, grants: map[string]e2eGrant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"issuer": p.srv.URL, "authorization_endpoint": p.srv.URL + "/authorize",
			"token_endpoint": p.srv.URL + "/token", "jwks_uri": p.srv.URL + "/jwks"})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		b := base64.RawURLEncoding.EncodeToString
		json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
			"n": b(key.N.Bytes()), "e": b(big.NewInt(int64(key.E)).Bytes())}}})
	})
	mux.HandleFunc("GET /authorize", p.authorize)
	mux.HandleFunc("POST /token", p.token)
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

// authorize signs in the person the provider is told about in the query (sub, email: test-only parameters) and
// redirects to redirect_uri with a code and the state it was given.
func (p *e2eProvider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("client_id") != p.clientID || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" {
		http.Error(w, "bad authorization request", 400)
		return
	}
	code := auth.RandomToken("", 16)
	p.mu.Lock()
	p.grants[code] = e2eGrant{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri"), sub: q.Get("sub"), email: q.Get("email")}
	p.mu.Unlock()
	back, _ := url.Parse(q.Get("redirect_uri"))
	bq := back.Query()
	bq.Set("code", code)
	bq.Set("state", q.Get("state"))
	back.RawQuery = bq.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound)
}

func (p *e2eProvider) token(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id, sec, ok := r.BasicAuth()
	p.mu.Lock()
	g, found := p.grants[r.PostForm.Get("code")]
	delete(p.grants, r.PostForm.Get("code"))
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !ok || id != p.clientID || sec != p.secret || !found || r.PostForm.Get("redirect_uri") != g.redirect ||
		base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"invalid_grant"}`))
		return
	}
	now := p.now()
	b := base64.RawURLEncoding.EncodeToString
	h, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	c, _ := json.Marshal(map[string]any{"iss": p.srv.URL, "sub": g.sub, "aud": p.clientID, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		"nonce": g.nonce, "email": g.email, "email_verified": true, "name": "Sari"})
	in := b(h) + "." + b(c)
	digest := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": in + "." + b(sig)})
}

// A host pushes a member through the member API; the person signs in through the upstream with a browser that
// follows every redirect; the host removes them and the same browser is signed out (contracts 0042 and 0043).
func TestUpstreamSignInAndMemberAPIEndToEnd(t *testing.T) {
	const clientID, clientSecret = "rangka-kopi", "e2e-client-secret"
	op := newE2EProvider(t, clientID, clientSecret)
	t.Setenv("E2E_CLIENT_SECRET", clientSecret)
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "system.rk")
		b, _ := os.ReadFile(p)
		cfg := "  two_factor: owners\n  password: owners\n  upstream:\n    - {id: kantor, label: Kantor, issuer: '" + op.srv.URL +
			"', client_id: " + clientID + ", client_secret: '${E2E_CLIENT_SECRET}'}\n"
		out := strings.Replace(string(b), "  two_factor: owners\n", cfg, 1)
		if out == string(b) {
			t.Fatal("system.rk has no auth.two_factor line to extend")
		}
		os.WriteFile(p, []byte(out), 0o644)
	})
	op.now = e.s.Auth.Now // the kernel tests run on a fixed clock; the id_token is issued on it
	if got := e.must(200, "GET", "/auth/providers", nil).Body; !strings.Contains(string(got), `"id":"kantor"`) {
		t.Fatalf("providers: %s", got)
	}

	// the owner signs in with the password (auth.password: owners) and mints the host's token
	e.login()
	tok := e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "cloud", "scopes": []any{"members:write"}})
	host := &env{t: t, ts: e.ts, token: tok.JSON["token"].(string)}

	push := host.must(200, "PUT", memberPath("sub-sari"), map[string]any{"email": "sari@example.com", "name": "Sari", "role": "sales_rep", "outlets": []any{"OUT-01"}})
	if push.JSON["created"] != true {
		t.Fatalf("push: %s", push.Body)
	}
	// give the cashier a password, to show that auth.password: owners refuses it
	hash, err := auth.HashPassword("kasir-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.DB.Query(t.Context(), "update rk_users set password_hash = $1 where email = 'sari@example.com'", hash); err != nil {
		t.Fatal(err)
	}

	// the browser: a cookie jar, redirects followed one step at a time
	browser := &env{t: t, ts: e.ts, jar: newJar()}
	step := func(rawURL string) (status int, location string) {
		t.Helper()
		req, _ := http.NewRequest("GET", rawURL, nil)
		c := &http.Client{Jar: browser.jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode >= 400 {
			t.Logf("%s answered %d: %s", rawURL[:strings.Index(rawURL, "?")], res.StatusCode, body)
		}
		return res.StatusCode, res.Header.Get("Location")
	}
	status, loc := step(e.ts.URL + "/auth/upstream/kantor/start?to=" + url.QueryEscape("/app/"))
	if status != 302 || !strings.HasPrefix(loc, op.srv.URL+"/authorize?") {
		t.Fatalf("start: %d %s", status, loc)
	}
	// the test tells the provider who is signing in; a real provider asks the person
	loc += "&sub=" + url.QueryEscape("sub-sari") + "&email=sari%40example.com"
	status, loc = step(loc)
	if status != 302 || !strings.Contains(loc, "/auth/upstream/kantor/callback?") {
		t.Fatalf("authorize: %d %s", status, loc)
	}
	// the kernel's registered redirect_uri names its public address; the test server stands in for it
	cb, _ := url.Parse(loc)
	status, loc = step(e.ts.URL + cb.Path + "?" + cb.RawQuery)
	if status != 302 || loc != "/app/" {
		t.Fatalf("callback: %d %s", status, loc)
	}

	me := browser.must(200, "GET", "/auth/me", nil)
	if get(me.JSON, "user", "email") != "sari@example.com" || get(me.JSON, "user", "roles", 0) != "sales_rep" ||
		get(me.JSON, "user", "outlets", 0) != "OUT-01" || get(me.JSON, "user", "org") != "kopi-senja" {
		t.Fatalf("me: %s", me.Body)
	}

	// auth.password: owners refuses her password while she is a member
	pw := (&env{t: t, ts: e.ts, jar: newJar()}).do("POST", "/auth/login", map[string]any{"email": "sari@example.com", "password": "kasir-pass-123"})
	if pw.Status != 403 || get(pw.JSON, "error", "code") != "password_disabled" {
		t.Fatalf("cashier password sign-in: %d %s", pw.Status, pw.Body)
	}

	// the host removes her: the same cookie is refused at once
	host.must(204, "DELETE", memberPath("sub-sari"), nil)
	browser.must(401, "GET", "/auth/me", nil)
	if r := (&env{t: t, ts: e.ts, jar: newJar()}).do("POST", "/auth/login", map[string]any{"email": "sari@example.com", "password": "kasir-pass-123"}); r.Status == 200 {
		t.Fatalf("removed cashier signed in with a password: %s", r.Body)
	}

	// the owner still signs in with the password
	(&env{t: t, ts: e.ts, jar: newJar()}).must(200, "POST", "/auth/login", map[string]any{"email": "ceo@saga.co.id", "password": "rangka-demo"})
}
