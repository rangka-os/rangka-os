package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
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
