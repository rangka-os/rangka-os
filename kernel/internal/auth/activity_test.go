package auth

import (
	"net/http/httptest"
	"testing"
	"time"
)

// Contract 0055 §1–2: a person's session and requests stamp rk_members.last_active_at at most every 5 minutes; a
// system session never does, and logging out keeps the stamp.
func TestLastActiveAt(t *testing.T) {
	ctx, s, uid := orgEnv(t)
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	active := func() *time.Time {
		t.Helper()
		res, err := s.DB.Query(ctx, "select last_active_at from rk_members where org_id = 'pt_alpha' and user_id = $1", uid)
		if err != nil {
			t.Fatal(err)
		}
		if v, ok := res.Rows[0][0].(time.Time); ok {
			return &v
		}
		return nil
	}
	request := func(cookie string) *Principal {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/v1/me", nil)
		r.AddCookie(s.SessionCookie(cookie, 60))
		p, err := s.Resolve(r)
		if err != nil || p == nil {
			t.Fatalf("resolve: %v %v", p, err)
		}
		return p
	}

	sys, _, err := s.LoginAs(ctx, "a@x.id", "rahasia-123", "pt_alpha", true)
	if err != nil {
		t.Fatal(err)
	}
	if p := request(sys); !p.System {
		t.Fatal("system session resolved without System")
	}
	if a := active(); a != nil {
		t.Fatalf("system session counted as activity: %v", a)
	}

	cookie, _, err := s.Login(ctx, "a@x.id", "rahasia-123", "pt_alpha")
	if err != nil {
		t.Fatal(err)
	}
	if a := active(); a == nil || !a.Equal(now) {
		t.Fatalf("sign-in not counted: %v", a)
	}

	now = now.Add(2 * time.Minute)
	request(cookie)
	if a := active(); !a.Equal(now.Add(-2 * time.Minute)) {
		t.Fatalf("written inside the 5-minute throttle: %v", a)
	}

	now = now.Add(4 * time.Minute)
	request(cookie)
	if a := active(); !a.Equal(now) {
		t.Fatalf("not written after 5 minutes: %v", a)
	}

	stamp := now
	now = now.Add(time.Hour)
	request(sys)
	if err := s.Logout(ctx, cookie); err != nil {
		t.Fatal(err)
	}
	if a := active(); !a.Equal(stamp) {
		t.Fatalf("system request or logout changed the stamp: %v", a)
	}
}
