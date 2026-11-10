package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// SignJWT signs claims with the RS256 key.
func (s *Service) SignJWT(ctx context.Context, claims map[string]any) (string, error) {
	k, kid, err := s.signingKey(ctx)
	if err != nil {
		return "", err
	}
	h, _ := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid})
	c, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(nil, k, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// MintAssistantToken issues the delegated token the embedded assistant calls MCP with (contract 0020): an
// access JWT acting as u with scope rangka.mcp and claim ai: true. Nothing is stored; it expires after ttl,
// and Resolve reloads the membership on every call, so removing the person ends it too.
func (s *Service) MintAssistantToken(ctx context.Context, u *User, ttl time.Duration) (string, error) {
	now := s.now()
	return s.SignJWT(ctx, map[string]any{"iss": s.Issuer, "sub": fmt.Sprint(u.ID), "uid": u.ID, "aud": "rangka-ai", "iat": now.Unix(),
		"exp": now.Add(ttl).Unix(), "scope": "rangka.mcp", "org": u.Org, "email": u.Email, "typ": "access", "ai": true})
}

// VerifyJWT checks signature, issuer and expiry of a token this provider issued.
func (s *Service) VerifyJWT(tok string) (map[string]any, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil, errors.New("not a JWT")
	}
	k, kid, err := s.signingKey(context.Background())
	if err != nil {
		return nil, err
	}
	var hdr map[string]any
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(hb, &hdr) != nil || hdr["alg"] != "RS256" || hdr["kid"] != kid {
		return nil, errors.New("bad JWT header")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&k.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		return nil, errors.New("bad JWT signature")
	}
	cb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(cb, &claims); err != nil {
		return nil, err
	}
	if claims["iss"] != s.Issuer {
		return nil, errors.New("wrong issuer")
	}
	if exp, _ := claims["exp"].(float64); int64(exp) < s.now().Unix() {
		return nil, errors.New("token expired")
	}
	if claims["typ"] != "access" {
		return nil, errors.New("not an access token")
	}
	return claims, nil
}

// Discovery serves /.well-known/openid-configuration.
func (s *Service) Discovery(w http.ResponseWriter, r *http.Request) {
	iss := s.Issuer
	writeJSON(w, 200, map[string]any{
		"issuer": iss, "authorization_endpoint": iss + "/oauth/authorize", "token_endpoint": iss + "/oauth/token",
		"userinfo_endpoint": iss + "/oauth/userinfo", "jwks_uri": iss + "/oauth/jwks",
		"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code"},
		"subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported": []string{"openid", "profile", "email", "rangka.mcp"}, "token_endpoint_auth_methods_supported": []string{"none"},
		"code_challenge_methods_supported": []string{"S256"}, "claims_supported": []string{"sub", "email", "name", "org", "roles"},
	})
}

// JWKS serves the public signing key.
func (s *Service) JWKS(w http.ResponseWriter, r *http.Request) {
	k, kid, err := s.signingKey(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "server_error", "error_description": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"keys": []any{map[string]any{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": kid,
		"n": base64.RawURLEncoding.EncodeToString(k.PublicKey.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.PublicKey.E)).Bytes())}}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func oauthError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]any{"error": code, "error_description": desc})
}

// loginPage is Rangka ID's sign-in page, in the design system's AuthLayout look (web/design-system AuthLayout):
// a Bata brand panel with the Sendi joint beside the form, a strip above it on phones, light and dark. With Back
// set it shows only the error and a link back (a refused upstream sign-in, contract 0042 §8).
var loginPage = template.Must(template.New("login").Parse(`<!doctype html><html lang="id"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><meta name="color-scheme" content="light dark"><title>Masuk · Rangka ID</title>
<style>
:root{--app:#fff;--ink:#1c1917;--ink2:#44403c;--muted:#6b625c;--field:#f7f5f3;--border:#8c827b;--line:#eeeae7;--primary:#1c1917;--on-primary:#fff;--primary-hover:#3a3330;--focus:#e0623f;--chip:#f6ede6;--chip-ink:#a33b23;--bad:#ae2e24;--bad-tint:#fdecea}
@media (prefers-color-scheme:dark){:root{--app:#1a1715;--ink:#ede8e4;--ink2:#cfc7c1;--muted:#a79d96;--field:#13110f;--border:#7a706a;--line:#2a2522;--primary:#ede8e4;--on-primary:#1a1715;--primary-hover:#fff;--focus:#f08a6b;--chip:#3a2219;--chip-ink:#f08a6b;--bad:#fd9891;--bad-tint:#5d1f1a}}
*{box-sizing:border-box}html,body{height:100%}body{margin:0;font:13px/20px ui-sans-serif,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:var(--app);color:var(--ink);display:flex}
.brand{flex:0 0 44%;max-width:640px;position:relative;overflow:hidden;display:flex;flex-direction:column;justify-content:space-between;gap:32px;padding:32px 40px;background:#b4432a;color:#fff}
.logo{display:inline-flex;align-items:center;gap:8px;font-weight:600;font-size:15px;letter-spacing:-.01em}.logo svg{width:22px;height:22px}
.joint{position:absolute;right:-12%;bottom:-10%;width:70%;max-width:420px;opacity:.14;pointer-events:none}
.pitch{position:relative;max-width:360px}.pitch h2{margin:0;font-weight:600;font-size:32px;line-height:38px;letter-spacing:-.02em}.pitch p{margin:12px 0 0;font-size:14px;line-height:22px;color:#f6ede6}
main{flex:1;min-width:0;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:24px;padding:40px 24px}
form,.box{width:min(360px,100%);display:flex;flex-direction:column;gap:16px}
.chip{align-self:flex-start;display:inline-flex;align-items:center;height:24px;padding:0 10px;border-radius:999px;background:var(--chip);color:var(--chip-ink);font-size:12px;font-weight:500}
h1{margin:0;font-size:20px;line-height:28px;font-weight:600;letter-spacing:-.01em}.sub{margin:-8px 0 0;color:var(--muted)}
label{display:flex;flex-direction:column;gap:4px;font-size:12px;line-height:16px;color:var(--ink2)}
input{font:inherit;height:28px;padding:0 8px;border:1px solid var(--border);border-radius:6px;background:var(--field);color:var(--ink)}
input:focus{outline:0;border-color:var(--focus);box-shadow:0 0 0 2px var(--chip);background:var(--app)}
button{font:inherit;font-weight:500;height:32px;border:0;border-radius:6px;background:var(--primary);color:var(--on-primary);cursor:pointer}button:hover{background:var(--primary-hover)}
button:focus-visible,.btn:focus-visible{outline:2px solid var(--focus);outline-offset:2px}
.btn{display:inline-flex;align-items:center;justify-content:center;height:32px;border-radius:6px;background:var(--primary);color:var(--on-primary);font-weight:500;text-decoration:none}.btn:hover{background:var(--primary-hover)}
.err{margin:0;padding:8px 12px;border-radius:6px;background:var(--bad-tint);color:var(--bad)}
.foot{width:min(360px,100%);color:var(--muted);font-size:12px}
@media (max-width:860px){body{flex-direction:column}.brand{flex:none;max-width:none;padding:20px 16px;gap:12px}.pitch h2{font-size:20px;line-height:26px}.pitch p{display:none}.joint{width:160px;right:-24px;bottom:-40px}main{justify-content:flex-start;padding:24px 16px}}
@media (max-width:600px){input,button{height:40px}}
</style></head>
<body><div class="brand"><span class="logo"><svg viewBox="0 0 256 256" aria-hidden="true"><path fill="#fff" fill-rule="evenodd" d="M215.15 95L153.15 33L69.64 33L131.64 95ZM61.15 217L123.15 217L123.15 103.49L61.15 41.49Z"/></svg>rangka</span>
<svg class="joint" viewBox="0 0 256 256" aria-hidden="true"><path fill="#fff" fill-rule="evenodd" d="M215.15 95L153.15 33L69.64 33L131.64 95ZM61.15 217L123.15 217L123.15 103.49L61.15 41.49Z"/></svg>
<div class="pitch"><h2>Jadi juragan, bukan admin.</h2><p>Raka menyusun alur bisnismu dan ikut menjalankannya. Uang tetap kamu yang setujui.</p></div></div>
<main>{{if .Back}}<div class="box"><h1>Masuk ke akunmu</h1><p class="err" role="alert">{{.Error}}</p><a class="btn" href="{{.Back}}">Kembali ke halaman masuk</a></div>{{else}}<form method="post" action="/oauth/authorize"><span class="chip">{{.Client}}</span><h1>Masuk ke akunmu</h1><p class="sub">Pakai akun Rangka ID untuk masuk ke {{.Client}}.</p>
{{if .Error}}<p class="err" role="alert">{{.Error}}</p>{{end}}
<label>Email<input name="email" type="email" autocomplete="username" required autofocus></label>
<label>Kata sandi<input name="password" type="password" autocomplete="current-password" required></label>
{{range $k, $v := .Params}}<input type="hidden" name="{{$k}}" value="{{$v}}">{{end}}
<button>Masuk</button></form>{{end}}<div class="foot">Rangka ID</div></main></body></html>`))

// Authorize handles GET/POST /oauth/authorize (code flow with PKCE S256). Without a session it shows a
// sign-in form; the POST of that form signs in and continues.
func (s *Service) Authorize(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	p := map[string]string{}
	for _, k := range []string{"response_type", "client_id", "redirect_uri", "scope", "state", "code_challenge", "code_challenge_method", "nonce"} {
		p[k] = r.Form.Get(k)
	}
	c := s.Client(p["client_id"])
	if c == nil {
		http.Error(w, "unknown client_id", 400)
		return
	}
	okURI := false
	for _, u := range c.RedirectURIs {
		okURI = okURI || u == p["redirect_uri"]
	}
	if !okURI {
		http.Error(w, "redirect_uri is not registered for this client", 400)
		return
	}
	back := func(q url.Values) {
		u, _ := url.Parse(p["redirect_uri"])
		vals := u.Query()
		for k, v := range q {
			vals[k] = v
		}
		if p["state"] != "" {
			vals.Set("state", p["state"])
		}
		u.RawQuery = vals.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	}
	if p["response_type"] != "code" {
		back(url.Values{"error": {"unsupported_response_type"}})
		return
	}
	if p["code_challenge"] == "" || p["code_challenge_method"] != "S256" {
		back(url.Values{"error": {"invalid_request"}, "error_description": {"PKCE with code_challenge_method=S256 is required"}})
		return
	}
	scopes := strings.Fields(p["scope"])
	for _, sc := range scopes {
		allowed := false
		for _, a := range c.Scopes {
			allowed = allowed || a == sc
		}
		if !allowed {
			back(url.Values{"error": {"invalid_scope"}, "error_description": {"scope " + sc + " is not allowed for this client"}})
			return
		}
	}
	var u *User
	if r.Method == http.MethodPost && r.Form.Get("email") != "" {
		tok, user, err := s.Login(r.Context(), r.Form.Get("email"), r.Form.Get("password"), s.DefaultOrg)
		if err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			var pd *ErrPasswordDisabled
			if errors.As(err, &pd) {
				w.WriteHeader(403)
			} else {
				w.WriteHeader(401)
			}
			loginPage.Execute(w, map[string]any{"Client": c.Label, "Params": p, "Error": err.Error()})
			return
		}
		http.SetCookie(w, s.SessionCookie(tok, int(s.SessionTTL.Seconds())))
		u = user
	} else if pr, _ := s.sessionPrincipal(r); pr != nil {
		u = pr.User
	}
	if u == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		loginPage.Execute(w, map[string]any{"Client": c.Label, "Params": p})
		return
	}
	code := RandomToken("", 24)
	if _, err := s.DB.Query(r.Context(), `insert into rk_oauth_codes (code_hash, client_id, user_id, org_id, redirect_uri, scope, nonce, challenge, expires_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, HashToken(code), c.ID, u.ID, u.Org, p["redirect_uri"], p["scope"], p["nonce"], p["code_challenge"], s.now().Add(10*time.Minute)); err != nil {
		back(url.Values{"error": {"server_error"}})
		return
	}
	back(url.Values{"code": {code}})
}

func (s *Service) sessionPrincipal(r *http.Request) (*Principal, error) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return nil, nil
	}
	res, err := s.DB.Query(r.Context(), "select user_id, org_id from rk_sessions where id = $1 and expires_at > $2", HashToken(c.Value), s.now())
	if err != nil || len(res.Rows) == 0 {
		return nil, err
	}
	u, err := LoadUser(r.Context(), s.DB, res.Rows[0][0].(int64), res.Rows[0][1].(string))
	if errors.Is(err, ErrNoMembership) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Principal{User: u, Via: "session"}, nil
}

// Token handles POST /oauth/token (authorization_code with code_verifier).
func (s *Service) Token(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oauthError(w, 405, "invalid_request", "use POST")
		return
	}
	if err := r.ParseForm(); err != nil {
		oauthError(w, 400, "invalid_request", err.Error())
		return
	}
	if r.Form.Get("grant_type") != "authorization_code" {
		oauthError(w, 400, "unsupported_grant_type", "only authorization_code is supported")
		return
	}
	res, err := s.DB.Query(r.Context(), `delete from rk_oauth_codes where code_hash = $1
		returning client_id, user_id, org_id, redirect_uri, scope, nonce, challenge, expires_at`, HashToken(r.Form.Get("code")))
	if err != nil {
		oauthError(w, 500, "server_error", err.Error())
		return
	}
	if len(res.Rows) == 0 {
		oauthError(w, 400, "invalid_grant", "unknown or used code")
		return
	}
	row := res.Rows[0]
	clientID, uid, org, redirect, scope, nonce, challenge := row[0].(string), row[1].(int64), row[2].(string), row[3].(string), row[4].(string), row[5].(string), row[6].(string)
	if exp, _ := row[7].(time.Time); s.now().After(exp) {
		oauthError(w, 400, "invalid_grant", "code expired")
		return
	}
	if r.Form.Get("client_id") != clientID || r.Form.Get("redirect_uri") != redirect {
		oauthError(w, 400, "invalid_grant", "client_id or redirect_uri does not match the authorization request")
		return
	}
	sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
	if subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) != 1 {
		oauthError(w, 400, "invalid_grant", "code_verifier does not match code_challenge")
		return
	}
	u, err := LoadUser(r.Context(), s.DB, uid, org)
	if err != nil {
		oauthError(w, 400, "invalid_grant", "user not found")
		return
	}
	now := s.now().Unix()
	ttl := int64(3600)
	access, err := s.SignJWT(r.Context(), map[string]any{"iss": s.Issuer, "sub": fmt.Sprint(u.ID), "uid": u.ID, "aud": clientID, "iat": now, "exp": now + ttl,
		"scope": scope, "org": u.Org, "email": u.Email, "typ": "access"})
	if err != nil {
		oauthError(w, 500, "server_error", err.Error())
		return
	}
	out := map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": ttl, "scope": scope}
	if strings.Contains(" "+scope+" ", " openid ") {
		claims := map[string]any{"iss": s.Issuer, "sub": fmt.Sprint(u.ID), "aud": clientID, "iat": now, "exp": now + ttl, "email": u.Email,
			"name": u.FullName, "org": u.Org, "typ": "id"}
		if nonce != "" {
			claims["nonce"] = nonce
		}
		idt, err := s.SignJWT(r.Context(), claims)
		if err != nil {
			oauthError(w, 500, "server_error", err.Error())
			return
		}
		out["id_token"] = idt
	}
	store.Audit(r.Context(), s.DB, u.Org, u.Email, "auth.oauth_token", "client/"+clientID, "access token issued ("+scope+")", nil)
	writeJSON(w, 200, out)
}

// UserInfo serves /oauth/userinfo for a bearer access token.
func (s *Service) UserInfo(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	claims, err := s.VerifyJWT(tok)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		oauthError(w, 401, "invalid_token", err.Error())
		return
	}
	uid, _ := claims["uid"].(float64)
	u, err := LoadUser(r.Context(), s.DB, int64(uid), fmt.Sprint(claims["org"]))
	if err != nil {
		oauthError(w, 401, "invalid_token", "user not found")
		return
	}
	writeJSON(w, 200, map[string]any{"sub": fmt.Sprint(u.ID), "email": u.Email, "email_verified": true, "name": u.FullName, "org": u.Org, "roles": u.Roles})
}
