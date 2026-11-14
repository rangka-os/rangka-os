package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

func orgEnv(t *testing.T) (context.Context, *Service, int64) {
	t.Helper()
	ctx := context.Background()
	db, err := pg.NewPool(ctx, testdb.New(t), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Exec(ctx, store.SystemSQL()); err != nil {
		t.Fatal(err)
	}
	Iterations = 1000
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(EnsureOrgCode(ctx, db, "pt_alpha", "PT Alpha", "ALP"))
	must(EnsureOrg(ctx, db, "pt_beta", "PT Beta"))
	uid, err := EnsureUser(ctx, db, "a@x.id", "A", "rahasia-123", "pt_alpha", "owner", "")
	must(err)
	_, err = EnsureUser(ctx, db, "a@x.id", "A", "rahasia-123", "pt_beta", "sales_rep", "")
	must(err)
	return ctx, &Service{DB: db}, uid
}

func TestMemberships(t *testing.T) {
	ctx, s, uid := orgEnv(t)
	ms, err := Memberships(ctx, s.DB, uid)
	if err != nil {
		t.Fatal(err)
	}
	want := []Membership{{"pt_alpha", "PT Alpha", "ALP", []string{"owner"}, false}, {"pt_beta", "PT Beta", "PT_BETA", []string{"sales_rep"}, false}}
	if !reflect.DeepEqual(ms, want) {
		t.Fatalf("got %+v", ms)
	}
	if c, _ := OrgCode(ctx, s.DB, "pt_beta"); c != "PT_BETA" {
		t.Fatal(c)
	}
}

func TestLoadUserNotMember(t *testing.T) {
	ctx, s, uid := orgEnv(t)
	if _, err := LoadUser(ctx, s.DB, uid, "pt_gamma"); !errors.Is(err, ErrNoMembership) {
		t.Fatalf("got %v", err)
	}
	u, err := LoadUser(ctx, s.DB, uid, "")
	if err != nil || u.Org != "pt_alpha" || u.Outlets == nil {
		t.Fatalf("%+v %v", u, err)
	}
	lone, _ := EnsureUser(ctx, s.DB, "b@x.id", "B", "rahasia-123", "pt_alpha", "owner", "")
	if err := RemoveMember(ctx, s.DB, "pt_alpha", lone); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUser(ctx, s.DB, lone, ""); !errors.Is(err, ErrNoMembership) {
		t.Fatalf("got %v", err)
	}
	if _, _, err := s.Login(ctx, "b@x.id", "rahasia-123", ""); !errors.Is(err, ErrNoMembership) {
		t.Fatalf("login got %v", err)
	}
}

func sessionOrg(t *testing.T, ctx context.Context, s *Service, cookie string) string {
	t.Helper()
	res, err := s.DB.Query(ctx, "select org_id from rk_sessions where id = $1", HashToken(cookie))
	if err != nil || len(res.Rows) != 1 {
		t.Fatal(err)
	}
	return res.Rows[0][0].(string)
}

func TestSwitchOrg(t *testing.T) {
	ctx, s, _ := orgEnv(t)
	cookie, u, err := s.Login(ctx, "a@x.id", "rahasia-123", "")
	if err != nil || u.Org != "pt_alpha" {
		t.Fatalf("%+v %v", u, err)
	}
	if u, err = s.SwitchOrg(ctx, cookie, "pt_beta"); err != nil || u.Org != "pt_beta" || u.Roles[0] != "sales_rep" {
		t.Fatalf("%+v %v", u, err)
	}
	if got := sessionOrg(t, ctx, s, cookie); got != "pt_beta" {
		t.Fatal(got)
	}
	if _, err := s.SwitchOrg(ctx, cookie, "pt_gamma"); !errors.Is(err, ErrNoMembership) {
		t.Fatalf("got %v", err)
	}
	if got := sessionOrg(t, ctx, s, cookie); got != "pt_beta" {
		t.Fatal(got)
	}
	s.Now = func() time.Time { return time.Now().Add(48 * time.Hour) }
	if _, err := s.SwitchOrg(ctx, cookie, "pt_alpha"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired: %v", err)
	}
}

func TestLoginPrefersRequestedOrg(t *testing.T) {
	ctx, s, _ := orgEnv(t)
	if _, u, err := s.Login(ctx, "a@x.id", "rahasia-123", "pt_beta"); err != nil || u.Org != "pt_beta" {
		t.Fatalf("%+v %v", u, err)
	}
	if _, u, err := s.Login(ctx, "a@x.id", "rahasia-123", "pt_gamma"); err != nil || u.Org != "pt_alpha" {
		t.Fatalf("%+v %v", u, err)
	}
}

func TestOutletsLoaded(t *testing.T) {
	ctx, s, uid := orgEnv(t)
	if err := SetOutlets(ctx, s.DB, "pt_alpha", uid, []string{"OUT-001"}); err != nil {
		t.Fatal(err)
	}
	u, err := LoadUser(ctx, s.DB, uid, "pt_alpha")
	if err != nil || !reflect.DeepEqual(u.Outlets, []string{"OUT-001"}) {
		t.Fatalf("%+v %v", u, err)
	}
}

func bearer(tok string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	return r
}

func cookieReq(c string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: c})
	return r
}

func jwtFor(t *testing.T, ctx context.Context, s *Service, uid int64, claims map[string]any) string {
	t.Helper()
	c := map[string]any{"iss": s.Issuer, "uid": uid, "typ": "access", "exp": time.Now().Add(time.Hour).Unix()}
	for k, v := range claims {
		c[k] = v
	}
	tok, err := s.SignJWT(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestRemovedMembershipEndsAccess(t *testing.T) {
	ctx, s, uid := orgEnv(t)
	s.Issuer = "http://rk.test"
	cookie, u, err := s.Login(ctx, "a@x.id", "rahasia-123", "pt_alpha")
	if err != nil {
		t.Fatal(err)
	}
	api, _, err := s.CreateToken(ctx, u, "ci", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	jwt := jwtFor(t, ctx, s, uid, map[string]any{"org": "pt_alpha", "scope": "openid"})
	reqs := map[string]func() *http.Request{
		"session": func() *http.Request { return cookieReq(cookie) },
		"token":   func() *http.Request { return bearer(api) },
		"jwt":     func() *http.Request { return bearer(jwt) },
	}
	for name, mk := range reqs {
		if p, err := s.Resolve(mk()); err != nil || p == nil || p.User.Org != "pt_alpha" {
			t.Fatalf("%s before removal: %+v %v", name, p, err)
		}
	}
	if err := RemoveMember(ctx, s.DB, "pt_alpha", uid); err != nil {
		t.Fatal(err)
	}
	for name, mk := range reqs {
		if p, err := s.Resolve(mk()); !errors.Is(err, ErrUnauthenticated) || p != nil {
			t.Fatalf("%s after removal: %+v %v", name, p, err)
		}
	}
}

func TestResolveEmptyOrgUnauthenticated(t *testing.T) {
	ctx, s, uid := orgEnv(t)
	s.Issuer = "http://rk.test"
	if _, err := s.DB.Query(ctx, "insert into rk_sessions (id, user_id, org_id, expires_at) values ($1, $2, '', $3)", HashToken("empty-sess"), uid, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Query(ctx, "insert into rk_tokens (user_id, org_id, name, token_hash) values ($1, '', 'x', $2)", uid, HashToken("rk_empty")); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]*http.Request{
		"session":    cookieReq("empty-sess"),
		"token":      bearer("rk_empty"),
		"jwt":        bearer(jwtFor(t, ctx, s, uid, map[string]any{"org": ""})),
		"jwt-no-org": bearer(jwtFor(t, ctx, s, uid, nil)),
	} {
		if p, err := s.Resolve(r); !errors.Is(err, ErrUnauthenticated) || p != nil {
			t.Fatalf("%s: %+v %v", name, p, err)
		}
	}
}
