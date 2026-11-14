package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// Pengaturan → Tim (contract 0056 §4): the owner's session manages the team at /api/v1/team.

const linkPrefix = "http://localhost:8080/#/masuk/tautan?t="

// teamEnv is kopi-senja with the owner signed in.
func teamEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, false, nil)
	e.login()
	return e
}

// invite adds a member through POST /api/v1/team and returns the answer.
func invite(e *env, email, role string, outlets ...any) *resp {
	e.t.Helper()
	if outlets == nil {
		outlets = []any{}
	}
	return e.must(201, "POST", "/api/v1/team", map[string]any{"email": email, "full_name": "Sari", "role": role, "outlets": outlets})
}

// linkToken is the token of a signin_link.
func linkToken(t *testing.T, r *resp) string {
	t.Helper()
	l, _ := r.JSON["signin_link"].(string)
	if !strings.HasPrefix(l, linkPrefix) || len(l) <= len(linkPrefix) {
		t.Fatalf("signin_link: %s", r.Body)
	}
	return strings.TrimPrefix(l, linkPrefix)
}

// browser is a browser with no session.
func browser(e *env) *env {
	jar, _ := cookiejar.New(nil)
	return &env{t: e.t, s: e.s, ts: e.ts, jar: jar}
}

// A new member gets a user record, pending, and a one-time sign-in link; only the link's hash is stored.
func TestTeamInviteCreatesLinkAndRecord(t *testing.T) {
	e := teamEnv(t)
	r := invite(e, " Sari@Example.com ", "sales_rep", "OUT-01")
	tok := linkToken(t, r)
	id := int64(r.JSON["user_id"].(float64))
	name, access := memberRecordOf(e, "sari@example.com")
	if name == "" || access != "pending" || r.JSON["record"] != name {
		t.Fatalf("record %q access %q: %s", name, access, r.Body)
	}
	sum := sha256.Sum256([]byte(tok))
	if countRows(e, "select 1 from rk_signin_links where token_hash = $1 and user_id = $2 and org_id = 'kopi-senja' and used_at is null", hex.EncodeToString(sum[:]), id) != 1 ||
		countRows(e, "select 1 from rk_signin_links where token_hash = $1", tok) != 0 {
		t.Fatal("link not stored as its hash")
	}
	if dbOne(e, "select password_hash from rk_users where id = $1", id) != nil || dbOne(e, "select full_name from rk_users where id = $1", id) != "Sari" {
		t.Fatal("a new account has a name and no password until the link is used")
	}
	if dbOne(e, "select array_to_string(outlets, ',') from rk_members where org_id = 'kopi-senja' and user_id = $1", id) != "OUT-01" {
		t.Fatal("outlets")
	}
	g := e.must(200, "GET", "/api/v1/team", nil).JSON
	if g["managed_by"] != "kernel" {
		t.Fatalf("managed_by: %v", g)
	}
	var sari map[string]any
	for _, m := range g["members"].([]any) {
		if m.(map[string]any)["email"] == "sari@example.com" {
			sari = m.(map[string]any)
		}
	}
	if sari == nil || sari["access"] != "pending" || sari["record"] != name || sari["role"] != "sales_rep" || sari["full_name"] != "Sari" ||
		fmt.Sprint(sari["outlets"]) != "[OUT-01]" || sari["user_id"] != float64(id) {
		t.Fatalf("list: %v", g["members"])
	}
	audit := string(e.must(200, "GET", "/api/v1/system/audit?kind=member.upsert", nil).Body)
	if !strings.Contains(audit, `"via":"team"`) || strings.Contains(audit, tok) {
		t.Fatalf("audit: %s", audit)
	}
	// unknown role and outlet are refused like the Member API
	if r := e.do("POST", "/api/v1/team", map[string]any{"email": "x@example.com", "role": "owner"}); r.Status != 422 || errCode(r) != "unknown_role" {
		t.Fatalf("owner role: %d %s", r.Status, r.Body)
	}
	if r := e.do("POST", "/api/v1/team", map[string]any{"email": "x@example.com", "role": "sales_rep", "outlets": []any{"NOPE"}}); r.Status != 422 || errCode(r) != "unknown_outlet" {
		t.Fatalf("outlet: %d %s", r.Status, r.Body)
	}
}

// The link sets a password once and opens a session (the record turns active); a used, expired or unknown link is
// link_invalid and changes nothing.
func TestTeamLinkSetsPasswordOnce(t *testing.T) {
	e := teamEnv(t)
	tok := linkToken(t, invite(e, "sari@example.com", "sales_rep"))
	b := browser(e)
	b.must(200, "POST", "/auth/link", map[string]any{"token": tok, "password": "kopi-pagi-123"})
	if me := b.must(200, "GET", "/auth/me", nil).JSON; get(me, "user", "email") != "sari@example.com" {
		t.Fatalf("me: %v", me)
	}
	if _, access := memberRecordOf(e, "sari@example.com"); access != "active" {
		t.Fatalf("access after link: %q", access)
	}
	if dbOne(e, "select method from rk_sessions s join rk_users u on u.id = s.user_id where u.email = 'sari@example.com'") != "link" {
		t.Fatal("session method")
	}
	browser(e).must(200, "POST", "/auth/login", map[string]any{"email": "sari@example.com", "password": "kopi-pagi-123"})
	hash := dbOne(e, "select password_hash from rk_users where email = 'sari@example.com'")

	// second use
	r := browser(e).do("POST", "/auth/link", map[string]any{"token": tok, "password": "lain-sekali-456"})
	if r.Status != 400 || errCode(r) != "link_invalid" {
		t.Fatalf("second use: %d %s", r.Status, r.Body)
	}
	msg := get(r.JSON, "error", "message") // one message for unknown, used and expired links
	if dbOne(e, "select password_hash from rk_users where email = 'sari@example.com'") != hash {
		t.Fatal("a refused link changed the password")
	}
	// unknown
	if r := browser(e).do("POST", "/auth/link", map[string]any{"token": "nope", "password": "lain-sekali-456"}); r.Status != 400 || errCode(r) != "link_invalid" || get(r.JSON, "error", "message") != msg {
		t.Fatalf("unknown: %d %s", r.Status, r.Body)
	}
	// expired after 72 hours
	tok2 := linkToken(t, invite(e, "budi@example.com", "sales_rep"))
	e.s.Auth.Now = func() time.Time { return fixedNow().Add(72*time.Hour + time.Minute) }
	r = browser(e).do("POST", "/auth/link", map[string]any{"token": tok2, "password": "kopi-pagi-123"})
	if r.Status != 400 || errCode(r) != "link_invalid" || get(r.JSON, "error", "message") != msg {
		t.Fatalf("expired: %d %s", r.Status, r.Body)
	}
	if dbOne(e, "select password_hash from rk_users where email = 'budi@example.com'") != nil {
		t.Fatal("an expired link set a password")
	}
	// a short password is refused and the link stays usable
	e.s.Auth.Now = fixedNow
	if r := browser(e).do("POST", "/auth/link", map[string]any{"token": tok2, "password": "pendek"}); r.Status != 422 || errCode(r) != "weak_password" {
		t.Fatalf("short: %d %s", r.Status, r.Body)
	}
	browser(e).must(200, "POST", "/auth/link", map[string]any{"token": tok2, "password": "cukup-panjang-1"})
}

// A real change of role or outlets revokes the member's sessions, as in contract 0043; an unchanged PATCH does not.
func TestTeamPatchRevokes(t *testing.T) {
	e := teamEnv(t)
	id := int64(invite(e, "sari@example.com", "sales_rep", "OUT-01").JSON["user_id"].(float64))
	path := fmt.Sprintf("/api/v1/team/%d", id)
	sess := personSession(e, "sari@example.com", "kopi-senja")
	e.must(200, "PATCH", path, map[string]any{"role": "sales_rep"})
	sess.must(200, "GET", "/auth/me", nil)
	e.must(200, "PATCH", path, map[string]any{"outlets": []any{"OUT-01", "OUT-02"}})
	sess.must(401, "GET", "/auth/me", nil)
	if dbOne(e, "select role from rk_members where org_id = 'kopi-senja' and user_id = $1", id) != "sales_rep" ||
		dbOne(e, "select array_to_string(outlets, ',') from rk_members where org_id = 'kopi-senja' and user_id = $1", id) != "OUT-01,OUT-02" {
		t.Fatal("outlets change")
	}
	sess = personSession(e, "sari@example.com", "kopi-senja")
	e.must(200, "PATCH", path, map[string]any{"role": "sales_manager"})
	sess.must(401, "GET", "/auth/me", nil)
	if dbOne(e, "select role from r_user where org_id = 'kopi-senja' and account_id = $1", id) != "sales_manager" {
		t.Fatal("record not synced after PATCH")
	}
	if r := e.do("PATCH", path, map[string]any{"role": "owner"}); r.Status != 422 || errCode(r) != "unknown_role" {
		t.Fatalf("owner role: %d %s", r.Status, r.Body)
	}
	if r := e.do("PATCH", "/api/v1/team/999999", map[string]any{"role": "sales_rep"}); r.Status != 404 {
		t.Fatalf("not a member: %d %s", r.Status, r.Body)
	}
}

// The owner of one organisation cannot change or remove a member of another: PATCH is 404, DELETE a no-op, and the
// other organisation's membership and sessions stay as they were.
func TestTeamOtherOrgMemberUntouched(t *testing.T) {
	e := teamEnv(t)
	ctx, db := context.Background(), e.s.DB
	if err := auth.EnsureOrgCode(ctx, db, "pt_beta", "PT Beta", "BETA"); err != nil {
		t.Fatal(err)
	}
	for _, u := range [][3]string{{"boss@beta.test", "pt_beta", "owner"}, {"staff@beta.test", "pt_beta", "sales_rep"}} {
		if _, err := auth.EnsureUser(ctx, db, u[0], "", "rangka-demo", u[1], u[2], ""); err != nil {
			t.Fatal(err)
		}
	}
	id := dbOne(e, "select id from rk_users where email = 'staff@beta.test'").(int64)
	if _, err := db.Query(ctx, "update rk_members set outlets = array['OUT-09'] where org_id = 'pt_beta' and user_id = $1", id); err != nil {
		t.Fatal(err)
	}
	sess := personSession(e, "staff@beta.test", "pt_beta")
	path := fmt.Sprintf("/api/v1/team/%d", id)
	state := func() string {
		return fmt.Sprintf("%v|%v|%d", dbOne(e, "select role from rk_members where org_id = 'pt_beta' and user_id = $1", id),
			dbOne(e, "select array_to_string(outlets, ',') from rk_members where org_id = 'pt_beta' and user_id = $1", id),
			countRows(e, "select 1 from rk_sessions where user_id = $1 and org_id = 'pt_beta'", id))
	}
	before := state()
	if before != "sales_rep|OUT-09|1" {
		t.Fatalf("setup: %s", before)
	}
	if r := e.do("PATCH", path, map[string]any{"role": "sales_manager", "outlets": []any{"OUT-01"}}); r.Status != 404 {
		t.Fatalf("PATCH other org member: %d %s", r.Status, r.Body)
	}
	if r := e.do("DELETE", path, nil); r.Status != 204 {
		t.Fatalf("DELETE other org member: %d %s", r.Status, r.Body)
	}
	if after := state(); after != before {
		t.Fatalf("other org member changed: %s -> %s", before, after)
	}
	sess.must(200, "GET", "/auth/me", nil)
}

// The owner cannot be removed or changed; removing a member keeps the record (removed) and ends an unused link.
func TestTeamDeleteOwnerRefused(t *testing.T) {
	e := teamEnv(t)
	owner := dbOne(e, "select id from rk_users where email = 'ceo@saga.co.id'").(int64)
	if r := e.do("DELETE", fmt.Sprintf("/api/v1/team/%d", owner), nil); r.Status != 409 || errCode(r) != "owner_protected" {
		t.Fatalf("delete owner: %d %s", r.Status, r.Body)
	}
	if r := e.do("PATCH", fmt.Sprintf("/api/v1/team/%d", owner), map[string]any{"role": "sales_rep"}); r.Status != 409 || errCode(r) != "owner_protected" {
		t.Fatalf("patch owner: %d %s", r.Status, r.Body)
	}
	r := invite(e, "sari@example.com", "sales_rep", "OUT-01")
	tok, id := linkToken(t, r), int64(r.JSON["user_id"].(float64))
	e.must(204, "DELETE", fmt.Sprintf("/api/v1/team/%d", id), nil)
	if countRows(e, "select 1 from rk_members where org_id = 'kopi-senja' and user_id = $1", id) != 0 {
		t.Fatal("membership left")
	}
	if _, access := memberRecordOf(e, "sari@example.com"); access != "removed" {
		t.Fatalf("record access %q", access)
	}
	if r := browser(e).do("POST", "/auth/link", map[string]any{"token": tok, "password": "kopi-pagi-123"}); r.Status != 400 || errCode(r) != "link_invalid" {
		t.Fatalf("link of a removed member: %d %s", r.Status, r.Body)
	}
	e.must(204, "DELETE", fmt.Sprintf("/api/v1/team/%d", id), nil) // idempotent
}

// Only an owner with a browser session manages the team: another role and any API token are 403 forbidden.
func TestTeamNonOwnerForbidden(t *testing.T) {
	e := teamEnv(t)
	if _, err := auth.EnsureUser(context.Background(), e.s.DB, "kasir@example.com", "Kasir", "kasir-pass-123", "kopi-senja", "cashier", ""); err != nil {
		t.Fatal(err)
	}
	k := browser(e)
	k.must(200, "POST", "/auth/login", map[string]any{"email": "kasir@example.com", "password": "kasir-pass-123"})
	tok := &env{t: t, ts: e.ts, token: e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "cli"}).JSON["token"].(string)}
	for _, c := range []*env{k, tok} {
		for _, req := range [][2]string{{"GET", "/api/v1/team"}, {"POST", "/api/v1/team"}, {"PATCH", "/api/v1/team/1"}, {"DELETE", "/api/v1/team/1"}} {
			r := c.do(req[0], req[1], map[string]any{"email": "x@example.com", "role": "sales_rep"})
			if r.Status != 403 || errCode(r) != "forbidden" {
				t.Fatalf("%s %s: %d %s", req[0], req[1], r.Status, r.Body)
			}
		}
	}
	if countRows(e, "select 1 from rk_users where email = 'x@example.com'") != 0 {
		t.Fatal("a refused call wrote")
	}
}

// auth.team: host leaves the list readable and refuses every write.
func TestTeamHostModeRefusesWrites(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "system.rk")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(p, bytes.Replace(b, []byte("  session_ttl: 24h\n"), []byte("  session_ttl: 24h\n  team: host\n"), 1), 0o644)
	})
	e.login()
	g := e.must(200, "GET", "/api/v1/team", nil).JSON
	if g["managed_by"] != "host" || len(g["members"].([]any)) == 0 {
		t.Fatalf("list: %v", g)
	}
	for _, req := range [][2]string{{"POST", "/api/v1/team"}, {"PATCH", "/api/v1/team/1"}, {"DELETE", "/api/v1/team/1"}} {
		r := e.do(req[0], req[1], map[string]any{"email": "sari@example.com", "role": "sales_rep"})
		if r.Status != 403 || errCode(r) != "team_managed_by_host" {
			t.Fatalf("%s: %d %s", req[0], r.Status, r.Body)
		}
	}
	if countRows(e, "select 1 from rk_users where email = 'sari@example.com'") != 0 {
		t.Fatal("host mode wrote")
	}
}

// An e-mail another organisation holds is refused (account_elsewhere, owner_protected); an account that is a member
// nowhere and has a password is linked without a sign-in link.
func TestTeamAccountElsewhere(t *testing.T) {
	e := teamEnv(t)
	ctx, db := context.Background(), e.s.DB
	if err := auth.EnsureOrgCode(ctx, db, "pt_beta", "PT Beta", "BETA"); err != nil {
		t.Fatal(err)
	}
	for _, u := range [][3]string{{"boss@beta.test", "pt_beta", "owner"}, {"staff@beta.test", "pt_beta", "sales_rep"}} {
		if _, err := auth.EnsureUser(ctx, db, u[0], "", "rangka-demo", u[1], u[2], ""); err != nil {
			t.Fatal(err)
		}
	}
	hash, err := auth.HashPassword("yatim-pass-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Query(ctx, "insert into rk_users (email, full_name, password_hash) values ('orphan@example.test', 'Yatim', $1)", hash); err != nil {
		t.Fatal(err)
	}
	for email, code := range map[string]string{"staff@beta.test": "account_elsewhere", "boss@beta.test": "owner_protected"} {
		r := e.do("POST", "/api/v1/team", map[string]any{"email": email, "role": "sales_rep"})
		if r.Status != 409 || errCode(r) != code {
			t.Fatalf("%s: %d %s", email, r.Status, r.Body)
		}
		if countRows(e, "select 1 from rk_members m join rk_users u on u.id = m.user_id where u.email = $1 and m.org_id = 'kopi-senja'", email) != 0 {
			t.Fatalf("%s: a refusal wrote", email)
		}
	}
	r := e.must(201, "POST", "/api/v1/team", map[string]any{"email": "orphan@example.test", "role": "sales_rep"})
	if _, ok := r.JSON["signin_link"]; ok || countRows(e, "select 1 from rk_signin_links") != 0 {
		t.Fatalf("an existing account got a sign-in link: %s", r.Body)
	}
}

// A link only sets the first password of an account this organisation alone holds: once the account is in another
// organisation, has a password or has an identity link, the link is link_invalid and changes nothing.
func TestTeamLinkRefusedForAccountElsewhere(t *testing.T) {
	e := teamEnv(t)
	ctx, db := context.Background(), e.s.DB
	tok := linkToken(t, invite(e, "sari@example.com", "sales_rep"))
	if err := auth.EnsureOrgCode(ctx, db, "pt_beta", "PT Beta", "BETA"); err != nil {
		t.Fatal(err)
	}
	// rangka user add into pt_beta of the existing passwordless account
	if _, err := db.Query(ctx, "insert into rk_members (org_id, user_id, role) select 'pt_beta', id, 'owner' from rk_users where email = 'sari@example.com'"); err != nil {
		t.Fatal(err)
	}
	if r := browser(e).do("POST", "/auth/link", map[string]any{"token": tok, "password": "kopi-pagi-123"}); r.Status != 400 || errCode(r) != "link_invalid" {
		t.Fatalf("account elsewhere: %d %s", r.Status, r.Body)
	}
	if dbOne(e, "select password_hash from rk_users where email = 'sari@example.com'") != nil {
		t.Fatal("a link set the password of an account another organisation holds")
	}

	// a password set some other way (rangka user --reset-password) ends the link
	tok = linkToken(t, invite(e, "budi@example.com", "sales_rep"))
	hash, err := auth.HashPassword("milik-budi-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Query(ctx, "update rk_users set password_hash = $1 where email = 'budi@example.com'", hash); err != nil {
		t.Fatal(err)
	}
	if r := browser(e).do("POST", "/auth/link", map[string]any{"token": tok, "password": "kopi-pagi-123"}); r.Status != 400 || errCode(r) != "link_invalid" {
		t.Fatalf("account with a password: %d %s", r.Status, r.Body)
	}
	if dbOne(e, "select password_hash from rk_users where email = 'budi@example.com'") != hash {
		t.Fatal("a link overwrote a password")
	}

	// an identity link (upstream sign-in) ends it too
	tok = linkToken(t, invite(e, "citra@example.com", "sales_rep"))
	id := dbOne(e, "select id from rk_users where email = 'citra@example.com'").(int64)
	if err := auth.LinkIdentity(ctx, db, "https://id.example", "sub-citra", id); err != nil {
		t.Fatal(err)
	}
	if r := browser(e).do("POST", "/auth/link", map[string]any{"token": tok, "password": "kopi-pagi-123"}); r.Status != 400 || errCode(r) != "link_invalid" {
		t.Fatalf("account with an identity: %d %s", r.Status, r.Body)
	}
}

// Inviting again a person with no way in (removed before using the link, or still pending) gives a new link and
// ends the old one.
func TestTeamReinviteReplacesLink(t *testing.T) {
	e := teamEnv(t)
	r := invite(e, "sari@example.com", "sales_rep")
	old, id := linkToken(t, r), int64(r.JSON["user_id"].(float64))
	e.must(204, "DELETE", fmt.Sprintf("/api/v1/team/%d", id), nil)
	again := linkToken(t, invite(e, "sari@example.com", "sales_rep"))
	if again == old {
		t.Fatal("same link")
	}
	if r := browser(e).do("POST", "/auth/link", map[string]any{"token": old, "password": "kopi-pagi-123"}); r.Status != 400 || errCode(r) != "link_invalid" {
		t.Fatalf("old link after re-invite: %d %s", r.Status, r.Body)
	}
	// still pending: inviting again replaces the link
	third := linkToken(t, invite(e, "sari@example.com", "sales_rep"))
	if countRows(e, "select 1 from rk_signin_links where user_id = $1 and used_at is null", id) != 1 {
		t.Fatal("more than one live link")
	}
	if r := browser(e).do("POST", "/auth/link", map[string]any{"token": again, "password": "kopi-pagi-123"}); r.Status != 400 || errCode(r) != "link_invalid" {
		t.Fatalf("replaced link: %d %s", r.Status, r.Body)
	}
	browser(e).must(200, "POST", "/auth/link", map[string]any{"token": third, "password": "kopi-pagi-123"})
	// signed in now: inviting again gives no link
	if r := invite(e, "sari@example.com", "sales_rep"); r.JSON["signin_link"] != nil {
		t.Fatalf("link for an account with a way in: %s", r.Body)
	}
}
