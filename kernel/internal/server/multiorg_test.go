package server

import (
	"context"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// twoOrgEnv adds a second org (pt_beta) next to kopi-senja: the seed user belongs to both,
// beta@example.test only to pt_beta.
func twoOrgEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, false, nil)
	ctx := context.Background()
	db := e.s.DB
	if err := auth.EnsureOrgCode(ctx, db, "pt_beta", "PT Beta", "BETA"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.EnsureUser(ctx, db, "ceo@saga.co.id", "", "rangka-demo", "pt_beta", "owner", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.EnsureUser(ctx, db, "beta@example.test", "Beta", "rangka-demo", "pt_beta", "owner", ""); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) loginAs(email string) {
	e.t.Helper()
	e.must(200, "POST", "/auth/login", map[string]any{"email": email, "password": "rangka-demo"})
}

func TestAuthMeListsOrgs(t *testing.T) {
	e := twoOrgEnv(t)
	e.login()
	me := e.must(200, "GET", "/auth/me", nil).JSON
	if get(me, "org", "id") != "kopi-senja" || get(me, "orgs", 0, "id") != "kopi-senja" || get(me, "orgs", 1, "id") != "pt_beta" ||
		get(me, "orgs", 1, "code") != "BETA" || get(me, "orgs", 1, "name") != "PT Beta" || get(me, "orgs", 1, "roles", 0) != "owner" {
		t.Fatalf("me: %v", me)
	}
	if me["via"] != "session" || me["approver"] != true || get(me, "user", "outlets") == nil {
		t.Fatalf("me keeps old fields: %v", me)
	}
	if n := len(me["orgs"].([]any)); n != 2 {
		t.Fatalf("orgs: %d", n)
	}
}

func TestSwitchOrgIsolation(t *testing.T) {
	e := twoOrgEnv(t)
	e.login()
	c := e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "Hanya Kopi Senja"}).JSON
	name := c["name"].(string)
	r := e.must(200, "POST", "/auth/org", map[string]any{"org": "pt_beta"})
	if get(r.JSON, "org", "id") != "pt_beta" || len(r.JSON["orgs"].([]any)) != 2 {
		t.Fatalf("switch body: %v", r.JSON)
	}
	ck := r.Header.Get("Set-Cookie")
	for _, want := range []string{"rk_org=pt_beta", "Path=/", "Max-Age=31536000", "SameSite=Lax"} {
		if !strings.Contains(ck, want) {
			t.Fatalf("cookie %q lacks %s", ck, want)
		}
	}
	if strings.Contains(ck, "HttpOnly") {
		t.Fatalf("rk_org must be readable by the web app: %s", ck)
	}
	l := e.must(200, "GET", "/api/v1/r/customer?limit=200", nil)
	if strings.Contains(string(l.Body), "Hanya Kopi Senja") {
		t.Fatal("record of kopi-senja visible in pt_beta")
	}
	e.must(404, "GET", "/api/v1/r/customer/"+name, nil)
	if me := e.must(200, "GET", "/auth/me", nil).JSON; get(me, "org", "id") != "pt_beta" {
		t.Fatalf("session org: %v", me)
	}
	e.must(200, "POST", "/auth/org", map[string]any{"org": "kopi-senja"})
	l = e.must(200, "GET", "/api/v1/r/customer?limit=200", nil)
	if !strings.Contains(string(l.Body), "Hanya Kopi Senja") {
		t.Fatal("record missing after switching back")
	}
	e.must(200, "GET", "/api/v1/r/customer/"+name, nil)
}

func TestSwitchToForeignOrg403(t *testing.T) {
	e := twoOrgEnv(t)
	e.loginAs("beta@example.test")
	r := e.must(403, "POST", "/auth/org", map[string]any{"org": "kopi-senja"})
	if get(r.JSON, "error", "code") != "permission_denied" || get(r.JSON, "error", "model") != "org" || get(r.JSON, "error", "action") != "switch" {
		t.Fatalf("body: %s", r.Body)
	}
	if me := e.must(200, "GET", "/auth/me", nil).JSON; get(me, "org", "id") != "pt_beta" {
		t.Fatalf("org changed: %v", me)
	}
}

func TestLoginNoMembership(t *testing.T) {
	e := twoOrgEnv(t)
	ctx := context.Background()
	if _, err := e.s.DB.Query(ctx, "insert into rk_users (email, full_name, password_hash) values ('lone@example.test', 'Lone', $1)", mustHash(t)); err != nil {
		t.Fatal(err)
	}
	r := e.must(403, "POST", "/auth/login", map[string]any{"email": "lone@example.test", "password": "rangka-demo"})
	if get(r.JSON, "error", "code") != "no_membership" {
		t.Fatalf("body: %s", r.Body)
	}
}

func mustHash(t *testing.T) string {
	t.Helper()
	h, err := auth.HashPassword("rangka-demo")
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestTokenOrgFixed(t *testing.T) {
	e := twoOrgEnv(t)
	e.login()
	tok := e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "cli"}).JSON["token"].(string)
	e.token = tok
	r := e.must(400, "POST", "/auth/org", map[string]any{"org": "pt_beta"})
	if get(r.JSON, "error", "code") != "org_fixed" {
		t.Fatalf("body: %s", r.Body)
	}
}

func TestRemovedMemberLosesSession(t *testing.T) {
	e := twoOrgEnv(t)
	e.loginAs("beta@example.test")
	e.must(200, "GET", "/auth/me", nil)
	res, err := e.s.DB.Query(context.Background(), "select id from rk_users where email = 'beta@example.test'")
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.RemoveMember(context.Background(), e.s.DB, "pt_beta", res.Rows[0][0].(int64)); err != nil {
		t.Fatal(err)
	}
	e.must(401, "GET", "/auth/me", nil)
}

func TestOrgsListOnlyMine(t *testing.T) {
	e := twoOrgEnv(t)
	e.loginAs("beta@example.test")
	l := e.must(200, "GET", "/api/v1/orgs", nil).JSON
	d := l["data"].([]any)
	if len(d) != 1 || get(l, "data", 0, "id") != "pt_beta" || get(l, "data", 0, "code") != "BETA" || get(l, "data", 0, "current") != true || get(l, "data", 0, "role") != "owner" {
		t.Fatalf("orgs: %v", l)
	}
	if get(l, "data", 0, "usage") == nil {
		t.Fatalf("usage missing: %v", l)
	}
	e2 := twoOrgEnv(t)
	e2.login()
	if n := len(e2.must(200, "GET", "/api/v1/orgs", nil).JSON["data"].([]any)); n != 2 {
		t.Fatalf("ceo sees %d orgs", n)
	}
}

func TestMembersOfForeignOrg403(t *testing.T) {
	e := twoOrgEnv(t)
	e.loginAs("beta@example.test")
	r := e.must(403, "GET", "/api/v1/orgs/kopi-senja/members", nil)
	if get(r.JSON, "error", "code") != "permission_denied" || get(r.JSON, "error", "model") != "org" || get(r.JSON, "error", "action") != "read" {
		t.Fatalf("body: %s", r.Body)
	}
	e.must(200, "GET", "/api/v1/orgs/pt_beta/members", nil)
}

func TestDevOpenMeHasOrg(t *testing.T) {
	e := newEnv(t, true, nil)
	me := e.must(200, "GET", "/auth/me", nil).JSON
	if get(me, "org", "id") != "kopi-senja" || len(me["orgs"].([]any)) != 1 {
		t.Fatalf("me: %v", me)
	}
}
