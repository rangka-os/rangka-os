package auth

import (
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// policyEnv: pt_alpha has an owner (own@x.id) and a cashier (kasir@x.id), both with password "rahasia-123", and
// a user with a password but no membership (tamu@x.id).
func policyEnv(t *testing.T, setting any, upstreams ...Upstream) *Service {
	t.Helper()
	ctx, s, _ := orgEnv(t)
	for _, u := range []struct{ email, role string }{{"own@x.id", "owner"}, {"kasir@x.id", "cashier"}} {
		if _, err := EnsureUser(ctx, s.DB, u.email, u.email, "rahasia-123", "pt_alpha", u.role, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := EnsureUser(ctx, s.DB, "tamu@x.id", "Tamu", "rahasia-123", "pt_beta", "owner", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Query(ctx, "delete from rk_members where user_id = (select id from rk_users where email = 'tamu@x.id')"); err != nil {
		t.Fatal(err)
	}
	s.DefaultOrg = "pt_alpha"
	s.Password = func() any { return setting }
	s.Upstreams = upstreams
	s.Clients = func() []Client {
		return []Client{{ID: "omilo", Label: "Omilo", RedirectURIs: []string{"https://omilo.example/cb"}, Scopes: []string{"openid"}}}
	}
	return s
}

func TestPasswordPolicyLogin(t *testing.T) {
	kantor := Upstream{ID: "kantor", Label: "Kantor"}
	cases := []struct {
		name    string
		setting any
		ups     []Upstream
		email   string
		want    string // "" = signs in, else the copy of password_disabled
		noMem   bool
	}{
		{"default true, cashier", nil, nil, "kasir@x.id", "", false},
		{"true, cashier", true, nil, "kasir@x.id", "", false},
		{"owners, owner", "owners", []Upstream{kantor}, "own@x.id", "", false},
		{"owners, cashier", "owners", []Upstream{kantor}, "kasir@x.id", "Masuk dengan Kantor.", false},
		{"owners, no upstream", "owners", nil, "kasir@x.id", "Masuk lewat penyedia akun organisasimu.", false},
		{"owners, no membership", "owners", []Upstream{kantor}, "tamu@x.id", "", true},
		{"false, owner", false, []Upstream{kantor, {ID: "lain", Label: "Lain"}}, "own@x.id", "Masuk dengan Kantor.", false},
		{"false, no membership", false, []Upstream{kantor}, "tamu@x.id", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := policyEnv(t, c.setting, c.ups...)
			tok, u, err := s.Login(t.Context(), c.email, "rahasia-123", "pt_alpha")
			var pd *ErrPasswordDisabled
			switch {
			case c.noMem:
				if !errors.Is(err, ErrNoMembership) {
					t.Fatalf("want no_membership, got %v", err)
				}
			case c.want != "":
				if !errors.As(err, &pd) || pd.Message != c.want || tok != "" || u != nil {
					t.Fatalf("want password_disabled %q, got %v", c.want, err)
				}
				if n := sessionCount(t, s); n != 0 {
					t.Fatalf("a refused password sign-in opened %d sessions", n)
				}
			default:
				if err != nil || tok == "" {
					t.Fatalf("want sign-in, got %v", err)
				}
			}
		})
	}
}

func TestPasswordPolicyWrongPasswordStillBadLogin(t *testing.T) {
	s := policyEnv(t, false)
	if _, _, err := s.Login(t.Context(), "kasir@x.id", "salah", "pt_alpha"); !errors.Is(err, ErrBadLogin) {
		t.Fatalf("want bad_login, got %v", err)
	}
	if _, _, err := s.Login(t.Context(), "nobody@x.id", "x", "pt_alpha"); !errors.Is(err, ErrBadLogin) {
		t.Fatalf("want bad_login, got %v", err)
	}
}

func sessionCount(t *testing.T, s *Service) int {
	t.Helper()
	res, err := s.DB.Query(t.Context(), "select count(*) from rk_sessions")
	if err != nil {
		t.Fatal(err)
	}
	return int(res.Rows[0][0].(int64))
}

func postAuthorize(s *Service, email, password string) *httptest.ResponseRecorder {
	f := url.Values{"email": {email}, "password": {password}, "response_type": {"code"}, "client_id": {"omilo"},
		"redirect_uri": {"https://omilo.example/cb"}, "scope": {"openid"}, "code_challenge": {"abc"}, "code_challenge_method": {"S256"}}
	r := httptest.NewRequest("POST", "/oauth/authorize", strings.NewReader(f.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.Authorize(w, r)
	return w
}

func TestPasswordPolicyOAuthForm(t *testing.T) {
	kantor := Upstream{ID: "kantor", Label: "Kantor"}
	s := policyEnv(t, "owners", kantor)
	if w := postAuthorize(s, "own@x.id", "rahasia-123"); w.Code != http.StatusFound || !strings.Contains(w.Header().Get("Location"), "code=") {
		t.Fatalf("owner: %d %s", w.Code, w.Body)
	}
	w := postAuthorize(s, "kasir@x.id", "rahasia-123")
	if w.Code != 403 || !strings.Contains(w.Body.String(), "Masuk dengan Kantor.") {
		t.Fatalf("cashier: %d %s", w.Code, w.Body)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == CookieName {
			t.Fatal("a refused sign-in set a session cookie")
		}
	}
	if w := postAuthorize(s, "kasir@x.id", "salah"); w.Code != 401 {
		t.Fatalf("wrong password: %d", w.Code)
	}
	s.Password = func() any { return false }
	if w := postAuthorize(s, "own@x.id", "rahasia-123"); w.Code != 403 {
		t.Fatalf("false, owner: %d %s", w.Code, w.Body)
	}
	s.Password = nil
	if w := postAuthorize(s, "kasir@x.id", "rahasia-123"); w.Code != http.StatusFound {
		t.Fatalf("default: %d %s", w.Code, w.Body)
	}
}

func getAuthorize(s *Service) *httptest.ResponseRecorder {
	q := url.Values{"response_type": {"code"}, "client_id": {"omilo"}, "redirect_uri": {"https://omilo.example/cb"}, "scope": {"openid"},
		"state": {"st-1"}, "code_challenge": {"abc"}, "code_challenge_method": {"S256"}}
	w := httptest.NewRecorder()
	s.Authorize(w, httptest.NewRequest("GET", "/oauth/authorize?"+q.Encode(), nil))
	return w
}

var upstreamHref = regexp.MustCompile(`<a class="btn" href="([^"]+)">Masuk dengan ([^<]+)</a>`)

// The kernel's own OAuth sign-in page (Omilo and other auth.clients) offers the upstreams and shows the password
// form only as auth.password allows (final review I2).
func TestOAuthLoginPageUpstreams(t *testing.T) {
	kantor, lain := Upstream{ID: "kantor", Label: "Kantor"}, Upstream{ID: "lain", Label: "Lain"}
	cases := []struct {
		name    string
		setting any
		ups     []Upstream
		form    string // "open": the form as is, "owner": behind "Masuk sebagai pemilik", "": no form
	}{
		{"default, no upstream", nil, nil, "open"},
		{"true, upstreams", true, []Upstream{kantor, lain}, "open"},
		{"owners", "owners", []Upstream{kantor}, "owner"},
		{"owners, no upstream", "owners", nil, "owner"},
		{"false", false, []Upstream{kantor, lain}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := policyEnv(t, c.setting, c.ups...)
			w := getAuthorize(s)
			body := w.Body.String()
			if w.Code != 200 {
				t.Fatalf("status %d", w.Code)
			}
			links := upstreamHref.FindAllStringSubmatch(body, -1)
			if len(links) != len(c.ups) {
				t.Fatalf("want %d upstream buttons, got %d in %s", len(c.ups), len(links), body)
			}
			for i, l := range links {
				if l[2] != c.ups[i].Label {
					t.Fatalf("button %d label %q", i, l[2])
				}
				href, err := url.Parse(html.UnescapeString(l[1]))
				if err != nil || href.Path != "/auth/upstream/"+c.ups[i].ID+"/start" {
					t.Fatalf("button %d href %q", i, l[1])
				}
				to := href.Query().Get("to")
				if SafePath(to) != to {
					t.Fatalf("SafePath rewrites the return path %q", to)
				}
				back, _ := url.Parse(to)
				if back.Path != "/oauth/authorize" {
					t.Fatalf("return path %q", to)
				}
				for k, v := range map[string]string{"client_id": "omilo", "redirect_uri": "https://omilo.example/cb", "state": "st-1",
					"code_challenge": "abc", "code_challenge_method": "S256", "response_type": "code", "scope": "openid"} {
					if got := back.Query().Get(k); got != v {
						t.Fatalf("return path %s = %q, want %q", k, got, v)
					}
				}
			}
			hasForm := strings.Contains(body, `name="password"`)
			behindOwner := strings.Contains(body, "<summary>Masuk sebagai pemilik</summary>")
			switch c.form {
			case "open":
				if !hasForm || behindOwner {
					t.Fatalf("want the password form shown: %s", body)
				}
				if (len(c.ups) > 0) != strings.Contains(body, `role="separator"`) {
					t.Fatal("the separator belongs between upstream buttons and the form")
				}
			case "owner":
				if !hasForm || !behindOwner || strings.Contains(body, "<details open>") {
					t.Fatalf("want the form closed behind Masuk sebagai pemilik: %s", body)
				}
			default:
				if hasForm || behindOwner {
					t.Fatalf("auth.password false must not show a password form: %s", body)
				}
			}
		})
	}
}

// Under owners, a refused non-owner gets the upstream buttons with the form closed; an owner's wrong password keeps
// the owner form open.
func TestOAuthLoginPageAfterPost(t *testing.T) {
	s := policyEnv(t, "owners", Upstream{ID: "kantor", Label: "Kantor"})
	w := postAuthorize(s, "kasir@x.id", "rahasia-123")
	if w.Code != 403 || !upstreamHref.MatchString(w.Body.String()) || strings.Contains(w.Body.String(), "<details open>") {
		t.Fatalf("cashier: %d %s", w.Code, w.Body)
	}
	w = postAuthorize(s, "own@x.id", "salah")
	if w.Code != 401 || !strings.Contains(w.Body.String(), "<details open>") {
		t.Fatalf("owner, wrong password: %d %s", w.Code, w.Body)
	}
}

// SwitchOrg applies auth.password to a session opened with a password, not to an upstream session (final review
// M2). a@x.id (orgEnv) is owner in pt_alpha and sales_rep in pt_beta.
func TestSwitchOrgPasswordPolicy(t *testing.T) {
	ctx, s, uid := orgEnv(t)
	s.Password = func() any { return "owners" }
	s.Upstreams = []Upstream{{ID: "kantor", Label: "Kantor"}}
	tok, u, err := s.Login(ctx, "a@x.id", "rahasia-123", "pt_alpha")
	if err != nil || u.Org != "pt_alpha" {
		t.Fatalf("owner password sign-in: %v", err)
	}
	var pd *ErrPasswordDisabled
	if _, err := s.SwitchOrg(ctx, tok, "pt_beta"); !errors.As(err, &pd) || pd.Message != "Masuk dengan Kantor." {
		t.Fatalf("password session into pt_beta: %v", err)
	}
	res, err := s.DB.Query(ctx, "select org_id from rk_sessions where id = $1", HashToken(tok))
	if err != nil || res.Rows[0][0] != "pt_alpha" {
		t.Fatalf("a refused switch moved the session: %v %v", res, err)
	}
	if _, err := s.SwitchOrg(ctx, tok, "pt_alpha"); err != nil {
		t.Fatalf("password session staying in pt_alpha: %v", err)
	}

	alpha, err := LoadUser(ctx, s.DB, uid, "pt_alpha")
	if err != nil {
		t.Fatal(err)
	}
	up, err := s.openSession(ctx, alpha, UpstreamSessionTTL, SessionUpstream, false)
	if err != nil {
		t.Fatal(err)
	}
	if u, err := s.SwitchOrg(ctx, up, "pt_beta"); err != nil || u.Org != "pt_beta" {
		t.Fatalf("upstream session into pt_beta: %v", err)
	}

	s.Password = nil
	if u, err := s.SwitchOrg(ctx, tok, "pt_beta"); err != nil || u.Org != "pt_beta" {
		t.Fatalf("password session, auth.password true: %v", err)
	}
	s.Password = func() any { return false }
	if _, err := s.SwitchOrg(ctx, tok, "pt_alpha"); !errors.As(err, &pd) {
		t.Fatalf("auth.password false, password session: %v", err)
	}
}
