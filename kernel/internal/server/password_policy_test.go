package server

import (
	"context"
	"net/http/cookiejar"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// POST /auth/login under auth.password (contract 0042 §6): the JSON answer is 403 password_disabled after the
// password verifies; a wrong password stays 401 bad_login.
func TestLoginPasswordPolicyHTTP(t *testing.T) {
	e := newEnv(t, false, nil)
	if _, err := auth.EnsureUser(context.Background(), e.s.DB, "kasir2@example.com", "Kasir", "kasir-pass-123", "kopi-senja", "cashier", ""); err != nil {
		t.Fatal(err)
	}
	e.s.Auth.Upstreams = []auth.Upstream{{ID: "kantor", Label: "Kantor"}}
	login := func(email, pw string) *resp {
		e.jar, _ = cookiejar.New(nil)
		return e.do("POST", "/auth/login", map[string]any{"email": email, "password": pw})
	}
	e.s.Auth.Password = func() any { return "owners" }
	if r := login("ceo@saga.co.id", "rangka-demo"); r.Status != 200 {
		t.Fatalf("owner under owners: %d %s", r.Status, r.Body)
	}
	r := login("kasir2@example.com", "kasir-pass-123")
	if r.Status != 403 || get(r.JSON, "error", "code") != "password_disabled" || get(r.JSON, "error", "message") != "Masuk dengan Kantor." {
		t.Fatalf("cashier under owners: %d %s", r.Status, r.Body)
	}
	if r := login("kasir2@example.com", "salah"); r.Status != 401 || get(r.JSON, "error", "code") != "bad_login" {
		t.Fatalf("wrong password: %d %s", r.Status, r.Body)
	}
	e.s.Auth.Password = func() any { return false }
	if r := login("ceo@saga.co.id", "rangka-demo"); r.Status != 403 || get(r.JSON, "error", "code") != "password_disabled" {
		t.Fatalf("owner under false: %d %s", r.Status, r.Body)
	}
	e.s.Auth.Password = nil
	if r := login("kasir2@example.com", "kasir-pass-123"); r.Status != 200 {
		t.Fatalf("default: %d %s", r.Status, r.Body)
	}
}

// POST /auth/org from a password session into an organisation where auth.password refuses this member's password
// is 403 password_disabled and leaves the session where it was (final review M2).
func TestSwitchOrgPasswordPolicyHTTP(t *testing.T) {
	e := newEnv(t, false, nil)
	ctx := context.Background()
	if err := auth.EnsureOrg(ctx, e.s.DB, "pt_beta", "PT Beta"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.EnsureUser(ctx, e.s.DB, "ceo@saga.co.id", "", "rangka-demo", "pt_beta", "cashier", ""); err != nil {
		t.Fatal(err)
	}
	e.s.Auth.Upstreams = []auth.Upstream{{ID: "kantor", Label: "Kantor"}}
	e.s.Auth.Password = func() any { return "owners" }
	e.login()
	r := e.must(403, "POST", "/auth/org", map[string]any{"org": "pt_beta"})
	if get(r.JSON, "error", "code") != "password_disabled" || get(r.JSON, "error", "message") != "Masuk dengan Kantor." {
		t.Fatalf("switch into pt_beta: %s", r.Body)
	}
	if me := e.must(200, "GET", "/auth/me", nil).JSON; get(me, "org", "id") != "kopi-senja" {
		t.Fatalf("a refused switch moved the session: %v", me)
	}
	e.s.Auth.Password = nil
	e.must(200, "POST", "/auth/org", map[string]any{"org": "pt_beta"})
}
