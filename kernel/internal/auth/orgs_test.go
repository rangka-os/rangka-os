package auth

import (
	"context"
	"errors"
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
	want := []Membership{{"pt_alpha", "PT Alpha", "ALP", []string{"owner"}}, {"pt_beta", "PT Beta", "PT_BETA", []string{"sales_rep"}}}
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
