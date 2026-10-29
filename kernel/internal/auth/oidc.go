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

var loginPage = template.Must(template.New("login").Parse(`<!doctype html><html lang="id"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>Masuk · Rangka ID</title>
<style>body{font:14px/20px system-ui,sans-serif;background:#f7f8f9;color:#172b4d;display:grid;place-items:center;min-height:100vh;margin:0}
form{background:#fff;border:1px solid #dcdfe4;border-radius:8px;padding:24px;width:min(320px,90vw);display:grid;gap:12px}
h1{font-size:18px;margin:0}label{display:grid;gap:4px;font-size:12px;color:#44546f}input{font:inherit;padding:8px;border:1px solid #8590a2;border-radius:4px}
button{font:inherit;padding:8px;border:0;border-radius:4px;background:#0c66e4;color:#fff;cursor:pointer}.err{color:#ae2e24}</style></head>
<body><form method="post" action="/oauth/authorize"><h1>Masuk ke {{.Client}}</h1>
{{if .Error}}<p class="err">{{.Error}}</p>{{end}}
<label>Email<input name="email" type="email" autocomplete="username" required></label>
<label>Kata sandi<input name="password" type="password" autocomplete="current-password" required></label>
{{range $k, $v := .Params}}<input type="hidden" name="{{$k}}" value="{{$v}}">{{end}}
<button>Masuk</button></form></body></html>`))

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
			w.WriteHeader(401)
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
	res, err := s.DB.Query(r.Context(), "select user_id, org_id from rk_sessions where id = $1 and expires_at > now()", HashToken(c.Value))
	if err != nil || len(res.Rows) == 0 {
		return nil, err
	}
	u, err := LoadUser(r.Context(), s.DB, res.Rows[0][0].(int64), res.Rows[0][1].(string))
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
