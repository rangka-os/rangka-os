package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

const memberIssuer = "https://id.example"

// memberEnv is kopi-senja with upstream "kantor" configured, the owner signed in (e) and a members:write token
// minted by that owner (host).
func memberEnv(t *testing.T) (e, host *env) {
	t.Helper()
	e = newEnv(t, false, nil)
	e.s.Auth.Upstreams = []auth.Upstream{{ID: "kantor", Label: "Kantor", Issuer: memberIssuer}}
	e.login()
	tok := e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "cloud", "scopes": []any{"members:write"}})
	if get(tok.JSON, "scopes", 0) != "members:write" {
		t.Fatalf("token: %s", tok.Body)
	}
	return e, &env{t: t, ts: e.ts, token: tok.JSON["token"].(string)}
}

func memberPath(sub string) string { return "/api/v1/members/kantor/" + url.PathEscape(sub) }

// dbOne runs a query that returns one value (nil when no row).
func dbOne(e *env, sql string, args ...any) any {
	e.t.Helper()
	res, err := e.s.DB.Query(context.Background(), sql, args...)
	if err != nil {
		e.t.Fatal(err)
	}
	if len(res.Rows) == 0 {
		return nil
	}
	return res.Rows[0][0]
}

// personToken mints an API token for a member in org, as that person would.
func personToken(e *env, email, org string) *env {
	e.t.Helper()
	ctx := context.Background()
	id := dbOne(e, "select id from rk_users where email = $1", email).(int64)
	u, err := auth.LoadUser(ctx, e.s.DB, id, org)
	if err != nil {
		e.t.Fatal(err)
	}
	tok, _, err := e.s.Auth.CreateToken(ctx, u, "own", 0)
	if err != nil {
		e.t.Fatal(err)
	}
	return &env{t: e.t, ts: e.ts, token: tok}
}

// personSession opens a browser session for a member in org (passwordless people sign in through the upstream).
func personSession(e *env, email, org string) *env {
	e.t.Helper()
	id := dbOne(e, "select id from rk_users where email = $1", email).(int64)
	tok := auth.RandomToken("", 32)
	if _, err := e.s.DB.Query(context.Background(), "insert into rk_sessions (id, user_id, org_id, expires_at) values ($1, $2, $3, now() + interval '1 hour')",
		auth.HashToken(tok), id, org); err != nil {
		e.t.Fatal(err)
	}
	return &env{t: e.t, ts: e.ts, jar: newJar(), hdr: http.Header{"Cookie": {auth.CookieName + "=" + tok}}}
}

func countRows(e *env, sql string, args ...any) int64 {
	e.t.Helper()
	return dbOne(e, "select count(*) from ("+sql+") x", args...).(int64)
}

// A new subject becomes a passwordless member linked to the upstream; the same push again changes nothing.
func TestMemberAPICreate(t *testing.T) {
	e, host := memberEnv(t)
	r := host.must(200, "PUT", memberPath("sub-sari"), map[string]any{"email": " Sari@Example.com ", "name": "Sari", "role": "sales_rep", "outlets": []any{"OUT-01"}})
	if r.JSON["created"] != true || r.JSON["user_id"] == nil {
		t.Fatalf("create: %s", r.Body)
	}
	id := int64(r.JSON["user_id"].(float64))
	if dbOne(e, "select email from rk_users where id = $1", id) != "sari@example.com" || dbOne(e, "select password_hash from rk_users where id = $1", id) != nil ||
		dbOne(e, "select full_name from rk_users where id = $1", id) != "Sari" {
		t.Fatal("user not stored lowercase, passwordless, named")
	}
	if dbOne(e, "select user_id from rk_identities where issuer = $1 and subject = 'sub-sari'", memberIssuer) != id {
		t.Fatal("identity not linked")
	}
	if dbOne(e, "select role from rk_members where org_id = 'kopi-senja' and user_id = $1", id) != "sales_rep" ||
		dbOne(e, "select array_to_string(outlets, ',') from rk_members where org_id = 'kopi-senja' and user_id = $1", id) != "OUT-01" {
		t.Fatal("membership")
	}
	// the same push is idempotent and revokes nothing
	sess, tok := personSession(e, "sari@example.com", "kopi-senja"), personToken(e, "sari@example.com", "kopi-senja")
	r = host.must(200, "PUT", memberPath("sub-sari"), map[string]any{"email": "sari@example.com", "name": "Lain", "role": "sales_rep", "outlets": []any{"OUT-01"}})
	if r.JSON["created"] != false || int64(r.JSON["user_id"].(float64)) != id {
		t.Fatalf("again: %s", r.Body)
	}
	sess.must(200, "GET", "/auth/me", nil)
	tok.must(200, "GET", "/auth/me", nil)
	host.must(200, "PUT", memberPath("sub-sari"), map[string]any{"email": "sari@example.com", "name": "", "role": "sales_rep", "outlets": []any{"OUT-01"}})
	if dbOne(e, "select full_name from rk_users where id = $1", id) != "Sari" {
		t.Fatal("a name is set only when empty; an empty name never overwrites")
	}
	// omitted outlets keep the member's outlets: no change, no revocation
	host.must(200, "PUT", memberPath("sub-sari"), map[string]any{"email": "sari@example.com", "role": "sales_rep"})
	sess.must(200, "GET", "/auth/me", nil)
	if dbOne(e, "select array_to_string(outlets, ',') from rk_members where org_id = 'kopi-senja' and user_id = $1", id) != "OUT-01" {
		t.Fatal("omitted outlets changed the membership")
	}
	audit := string(e.must(200, "GET", "/api/v1/system/audit?kind=member.upsert", nil).Body)
	if !strings.Contains(audit, "user/sari@example.com") || !strings.Contains(audit, "ceo@saga.co.id") || strings.Contains(audit, host.token) {
		t.Fatalf("audit: %s", audit)
	}
}

// A real change of role or outlets revokes the person's sessions and tokens in that organisation.
func TestMemberAPIChangeRevokes(t *testing.T) {
	for name, change := range map[string]map[string]any{
		"role":    {"email": "sari@example.com", "role": "sales_manager", "outlets": []any{"OUT-01"}},
		"outlets": {"email": "sari@example.com", "role": "sales_rep", "outlets": []any{"OUT-01", "OUT-02"}},
	} {
		t.Run(name, func(t *testing.T) {
			e, host := memberEnv(t)
			host.must(200, "PUT", memberPath("sub-sari"), map[string]any{"email": "sari@example.com", "role": "sales_rep", "outlets": []any{"OUT-01"}})
			sess, tok := personSession(e, "sari@example.com", "kopi-senja"), personToken(e, "sari@example.com", "kopi-senja")
			host.must(200, "PUT", memberPath("sub-sari"), change)
			sess.must(401, "GET", "/auth/me", nil)
			tok.must(401, "GET", "/auth/me", nil)
			if n := countRows(e, "select 1 from rk_sessions s join rk_users u on u.id = s.user_id where u.email = 'sari@example.com'"); n != 0 {
				t.Fatalf("sessions left: %d", n)
			}
			// the owner's own session and the host's token are untouched
			e.must(200, "GET", "/auth/me", nil)
			host.must(200, "PUT", memberPath("sub-sari"), change)
		})
	}
}

// DELETE removes the membership, revokes at once, keeps the account and link, and is idempotent.
func TestMemberAPIDelete(t *testing.T) {
	e, host := memberEnv(t)
	id := int64(host.must(200, "PUT", memberPath("sub-sari"), map[string]any{"email": "sari@example.com", "role": "sales_rep"}).JSON["user_id"].(float64))
	sess, tok := personSession(e, "sari@example.com", "kopi-senja"), personToken(e, "sari@example.com", "kopi-senja")
	host.must(204, "DELETE", memberPath("sub-sari"), nil)
	sess.must(401, "GET", "/auth/me", nil)
	tok.must(401, "GET", "/auth/me", nil)
	if countRows(e, "select 1 from rk_members where user_id = $1", id) != 0 || countRows(e, "select 1 from rk_tokens where user_id = $1", id) != 0 {
		t.Fatal("membership or token left")
	}
	if dbOne(e, "select email from rk_users where id = $1", id) != "sari@example.com" || dbOne(e, "select user_id from rk_identities where subject = 'sub-sari'") != id {
		t.Fatal("account or identity removed")
	}
	host.must(204, "DELETE", memberPath("sub-sari"), nil)
	host.must(204, "DELETE", memberPath("sub-nobody"), nil)
	if !strings.Contains(string(e.must(200, "GET", "/api/v1/system/audit?kind=member.remove", nil).Body), "user/sari@example.com") {
		t.Fatal("removal not audited")
	}
	// the same person can be pushed again; a session left from a membership ended without revocation (rangka user
	// remove) does not come back with the new membership
	if r := host.must(200, "PUT", memberPath("sub-sari"), map[string]any{"email": "sari@example.com", "role": "sales_rep"}); r.JSON["created"] != false {
		t.Fatalf("again: %s", r.Body)
	}
	stale := personSession(e, "sari@example.com", "kopi-senja")
	if _, err := e.s.DB.Query(context.Background(), "delete from rk_members where org_id = 'kopi-senja' and user_id = $1", id); err != nil {
		t.Fatal(err)
	}
	stale.must(401, "GET", "/auth/me", nil)
	host.must(200, "PUT", memberPath("sub-sari"), map[string]any{"email": "sari@example.com", "role": "sales_rep"})
	stale.must(401, "GET", "/auth/me", nil)
}

// Only an API token with members:write, minted by an owner with a session, reaches the routes.
func TestMemberAPIGuards(t *testing.T) {
	e, host := memberEnv(t)
	body := map[string]any{"email": "sari@example.com", "role": "sales_rep"}
	forbidden := func(who string, c *env, method string) {
		t.Helper()
		r := c.do(method, memberPath("sub-sari"), body)
		if r.Status != 403 || errCode(r) != "forbidden" {
			t.Fatalf("%s %s: %d %s", who, method, r.Status, r.Body)
		}
	}
	plain := &env{t: t, ts: e.ts, token: e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "ci"}).JSON["token"].(string)}
	secretsTok := &env{t: t, ts: e.ts, token: e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "s", "scopes": []any{"secrets:write"}}).JSON["token"].(string)}
	for _, m := range []string{"PUT", "DELETE"} {
		forbidden("owner session", e, m)
		forbidden("plain token", plain, m)
		forbidden("secrets:write token", secretsTok, m)
	}
	(&env{t: t, ts: e.ts}).must(401, "PUT", memberPath("sub-sari"), body)
	if n := countRows(e, "select 1 from rk_users where email = 'sari@example.com'"); n != 0 {
		t.Fatal("a refused call wrote")
	}
	// minting: a non-owner, and a token, are refused; an unknown scope is 400
	rep := repEnv(t, e)
	if r := rep.do("POST", "/api/v1/tokens", map[string]any{"name": "x", "scopes": []any{"members:write"}}); r.Status != 403 || errCode(r) != "forbidden" {
		t.Fatalf("sales_rep minting members:write: %d %s", r.Status, r.Body)
	}
	if r := host.do("POST", "/api/v1/tokens", map[string]any{"name": "y", "scopes": []any{"members:write"}}); r.Status != 403 {
		t.Fatalf("token minting members:write: %d %s", r.Status, r.Body)
	}
	if r := e.do("POST", "/api/v1/tokens", map[string]any{"name": "z", "scopes": []any{"members:admin"}}); r.Status != 400 || !strings.Contains(errMsg(r), "members:write") {
		t.Fatalf("unknown scope: %d %s", r.Status, r.Body)
	}
	// the token reaches the member API and nothing else: no data, no change sets, no secrets, no tokens
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/v1/r/customer", nil},
		{"POST", "/api/v1/r/customer", map[string]any{"customer_name": "Lewat token"}},
		{"GET", "/api/v1/changesets", nil},
		{"POST", "/api/v1/changesets", map[string]any{"title": "x", "ops": []any{}}},
		{"PUT", "/api/v1/connectors/whatsapp_business/auth/access_token", map[string]any{"value": "x-long-enough-value"}},
		{"POST", "/api/v1/tokens", map[string]any{"name": "child"}},
		{"GET", "/auth/me", nil},
	} {
		if r := host.do(c.method, c.path, c.body); r.Status != 403 || errCode(r) != "scope_limited" {
			t.Fatalf("members:write token on %s %s: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
	if countRows(e, "select 1 from rk_tokens where name = 'child'") != 0 {
		t.Fatal("a token minted a token")
	}
	// nor MCP: the token is refused before any method runs (0043 §1), where a plain token is served
	plain.must(200, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	for _, m := range []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "tools/list"},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "rangka_list", "arguments": map[string]any{"model": "customer"}}},
	} {
		if r := host.do("POST", "/mcp", m); r.Status != 401 || strings.Contains(string(r.Body), "customer_name") {
			t.Fatalf("members:write token on MCP %s: %d %s", m["method"], r.Status, r.Body)
		}
	}
	// a plain token cannot mint a token either; an owner session still mints secrets:write
	if r := plain.do("POST", "/api/v1/tokens", map[string]any{"name": "child"}); r.Status != 403 || errCode(r) != "forbidden" {
		t.Fatalf("plain token minting: %d %s", r.Status, r.Body)
	}
	e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "s2", "scopes": []any{"secrets:write"}})
	// unknown upstream
	for _, m := range []string{"PUT", "DELETE"} {
		r := host.do(m, "/api/v1/members/lain/sub-sari", body)
		if r.Status != 404 || errCode(r) != "unknown_upstream" {
			t.Fatalf("unknown upstream %s: %d %s", m, r.Status, r.Body)
		}
	}
	// a kernel without auth.upstream answers 404 to every call
	e.s.Auth.Upstreams = nil
	if r := host.do("PUT", memberPath("sub-sari"), body); r.Status != 404 || errCode(r) != "unknown_upstream" {
		t.Fatalf("no upstreams: %d %s", r.Status, r.Body)
	}
}

// The owner is never created, changed or removed; refusals of role, outlet and body write nothing.
func TestMemberAPIRefusals(t *testing.T) {
	e, host := memberEnv(t)
	ctx := context.Background()
	owner := dbOne(e, "select id from rk_users where email = 'ceo@saga.co.id'").(int64)
	// in order: the owner is linked to sub-owner only for the last two (once linked, the owner's e-mail under
	// another subject is identity_conflict, resolved before owner_protected)
	for _, tc := range []struct {
		name   string
		sub    string
		body   any
		status int
		code   string
	}{
		{"owner by e-mail", "sub-x", map[string]any{"email": "CEO@saga.co.id", "role": "sales_rep"}, 409, "owner_protected"},
		{"role owner", "sub-x", map[string]any{"email": "new@example.com", "role": "owner"}, 422, "unknown_role"},
		{"unknown role", "sub-x", map[string]any{"email": "new@example.com", "role": "chef"}, 422, "unknown_role"},
		{"missing role", "sub-x", map[string]any{"email": "new@example.com"}, 422, "unknown_role"},
		{"unknown outlet", "sub-x", map[string]any{"email": "new@example.com", "role": "sales_rep", "outlets": []any{"OUT-01", "OUT-99"}}, 422, "unknown_outlet"},
		{"missing e-mail", "sub-x", map[string]any{"role": "sales_rep"}, 422, "bad_request"},
		{"invalid e-mail", "sub-x", map[string]any{"email": "Sari <sari@example.com>", "role": "sales_rep"}, 422, "bad_request"},
		{"not an e-mail", "sub-x", map[string]any{"email": "sari", "role": "sales_rep"}, 422, "bad_request"},
		{"body not an object", "sub-x", []any{1}, 422, "bad_request"},
		{"owner by identity", "sub-owner", map[string]any{"email": "ceo@saga.co.id", "role": "sales_rep"}, 409, "owner_protected"},
		{"owner linked, other", "sub-owner", map[string]any{"email": "new@example.com", "role": "sales_rep"}, 409, "owner_protected"},
	} {
		if tc.sub == "sub-owner" {
			if err := auth.LinkIdentity(ctx, e.s.DB, memberIssuer, "sub-owner", owner); err != nil {
				t.Fatal(err)
			}
		}
		r := host.do("PUT", memberPath(tc.sub), tc.body)
		if r.Status != tc.status || errCode(r) != tc.code || errMsg(r) == "" {
			t.Errorf("%s: %d %s", tc.name, r.Status, r.Body)
		}
	}
	if n := countRows(e, "select 1 from rk_users where email = 'new@example.com'"); n != 0 {
		t.Fatal("a refusal created a user")
	}
	if dbOne(e, "select role from rk_members where org_id = 'kopi-senja' and user_id = $1", owner) != "owner" ||
		dbOne(e, "select email from rk_users where id = $1", owner) != "ceo@saga.co.id" {
		t.Fatal("the owner changed")
	}
	if countRows(e, "select 1 from rk_identities where subject = 'sub-x'") != 0 || countRows(e, "select 1 from rk_audit where kind like 'member.%'") != 0 {
		t.Fatal("a refusal wrote")
	}
	if r := host.do("PUT", memberPath("sub-y"), map[string]any{"email": "ceo@saga.co.id", "role": "sales_rep"}); r.Status != 409 || errCode(r) != "identity_conflict" {
		t.Fatalf("owner e-mail under another subject: %d %s", r.Status, r.Body)
	}
	if r := host.do("DELETE", memberPath("sub-owner"), nil); r.Status != 409 || errCode(r) != "owner_protected" {
		t.Fatalf("delete owner: %d %s", r.Status, r.Body)
	}
	e.must(200, "GET", "/auth/me", nil) // the owner's session survived
}

// An e-mail change of a linked subject updates the account; an e-mail another account holds is refused.
func TestMemberAPIEmailChange(t *testing.T) {
	e, host := memberEnv(t)
	id := int64(host.must(200, "PUT", memberPath("sub-sari"), map[string]any{"email": "sari@example.com", "role": "sales_rep"}).JSON["user_id"].(float64))
	r := host.must(200, "PUT", memberPath("sub-sari"), map[string]any{"email": "sari.baru@example.com", "role": "sales_rep"})
	if int64(r.JSON["user_id"].(float64)) != id || r.JSON["created"] != false || dbOne(e, "select email from rk_users where id = $1", id) != "sari.baru@example.com" {
		t.Fatalf("e-mail change: %s", r.Body)
	}
	repEnv(t, e) // rep@example.test exists
	r = host.do("PUT", memberPath("sub-sari"), map[string]any{"email": "rep@example.test", "role": "sales_manager"})
	if r.Status != 409 || errCode(r) != "email_taken" {
		t.Fatalf("taken: %d %s", r.Status, r.Body)
	}
	if dbOne(e, "select email from rk_users where id = $1", id) != "sari.baru@example.com" || dbOne(e, "select role from rk_members where user_id = $1", id) != "sales_rep" {
		t.Fatal("a refused e-mail change wrote")
	}
}

// An existing account found by e-mail is linked (its password kept); one linked to another subject is refused.
func TestMemberAPIExistingAccount(t *testing.T) {
	e, host := memberEnv(t)
	repEnv(t, e)
	hash := dbOne(e, "select password_hash from rk_users where email = 'rep@example.test'")
	r := host.must(200, "PUT", memberPath("sub-rep"), map[string]any{"email": "rep@example.test", "role": "sales_rep"})
	if r.JSON["created"] != false || dbOne(e, "select password_hash from rk_users where email = 'rep@example.test'") != hash {
		t.Fatalf("existing account: %s", r.Body)
	}
	if dbOne(e, "select subject from rk_identities where issuer = $1 and user_id = $2", memberIssuer, int64(r.JSON["user_id"].(float64))) != "sub-rep" {
		t.Fatal("not linked")
	}
	r = host.do("PUT", memberPath("sub-other"), map[string]any{"email": "rep@example.test", "role": "sales_manager"})
	if r.Status != 409 || errCode(r) != "identity_conflict" {
		t.Fatalf("second subject: %d %s", r.Status, r.Body)
	}
	if countRows(e, "select 1 from rk_identities where subject = 'sub-other'") != 0 || dbOne(e, "select role from rk_members m join rk_users u on u.id = m.user_id where u.email = 'rep@example.test'") != "sales_rep" {
		t.Fatal("a refused link wrote")
	}
}

// An identity_conflict found after UpsertMember wrote (a link made concurrently, here by a trigger) rolls back the
// whole PUT: no user, no membership, no link, no audit.
func TestMemberAPIConflictAfterUpsertWritesNothing(t *testing.T) {
	e, host := memberEnv(t)
	ctx := context.Background()
	for _, q := range []string{
		`create function rk_test_steal() returns trigger language plpgsql as $$ begin
			insert into rk_identities (issuer, subject, user_id) values ('` + memberIssuer + `', 'sub-race', (select id from rk_users where email = 'ceo@saga.co.id'));
			return new; end $$`,
		`create trigger rk_test_steal after insert on rk_members for each row execute function rk_test_steal()`,
	} {
		if _, err := e.s.DB.Query(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	r := host.do("PUT", memberPath("sub-race"), map[string]any{"email": "race@example.com", "role": "sales_rep"})
	if r.Status != 409 || errCode(r) != "identity_conflict" {
		t.Fatalf("race: %d %s", r.Status, r.Body)
	}
	if countRows(e, "select 1 from rk_users where email = 'race@example.com'") != 0 || countRows(e, "select 1 from rk_identities where subject = 'sub-race'") != 0 ||
		countRows(e, "select 1 from rk_audit where kind = 'member.upsert'") != 0 {
		t.Fatal("the failed PUT left rows")
	}
}

// A new person past the plan's users limit is the plan_limit refusal; an existing member is not a new person.
func TestMemberAPIPlanLimit(t *testing.T) {
	e, host := memberEnv(t)
	ctx := context.Background()
	if _, err := auth.EnsureUser(ctx, e.s.DB, "kept@example.com", "", "rahasia-123", "kopi-senja", "sales_rep", ""); err != nil {
		t.Fatal(err)
	}
	setPlan(e, "mulai") // 3 people
	people := countRows(e, "select distinct user_id from rk_members")
	for i := people; i < 3; i++ {
		if _, err := auth.EnsureUser(ctx, e.s.DB, fmt.Sprintf("p%d@example.com", i), "", "rahasia-123", "kopi-senja", "sales_rep", ""); err != nil {
			t.Fatal(err)
		}
	}
	r := host.do("PUT", memberPath("sub-new"), map[string]any{"email": "new@example.com", "role": "sales_rep"})
	if r.Status != 403 || errCode(r) != "plan_limit" || !strings.Contains(errMsg(r), "pengguna") {
		t.Fatalf("past the limit: %d %s", r.Status, r.Body)
	}
	if countRows(e, "select 1 from rk_users where email = 'new@example.com'") != 0 || countRows(e, "select 1 from rk_identities where subject = 'sub-new'") != 0 {
		t.Fatal("a refused new person was written")
	}
	// an existing member is linked and changed under a full plan
	host.must(200, "PUT", memberPath("sub-kept"), map[string]any{"email": "kept@example.com", "role": "sales_manager"})
}

// The token acts in its minter's organisation only; the person's other organisation, sessions and tokens stay.
func TestMemberAPIOtherOrgUntouched(t *testing.T) {
	e, host := memberEnv(t)
	ctx := context.Background()
	host.must(200, "PUT", memberPath("sub-sari"), map[string]any{"email": "sari@example.com", "role": "sales_rep"})
	if err := auth.EnsureOrgCode(ctx, e.s.DB, "pt_beta", "PT Beta", "BETA"); err != nil {
		t.Fatal(err)
	}
	id := dbOne(e, "select id from rk_users where email = 'sari@example.com'").(int64)
	if _, err := e.s.DB.Query(ctx, "insert into rk_members (org_id, user_id, role, outlets) values ('pt_beta', $1, 'sales_manager', '{X-1}')", id); err != nil {
		t.Fatal(err)
	}
	betaSess, betaTok := personSession(e, "sari@example.com", "pt_beta"), personToken(e, "sari@example.com", "pt_beta")
	host.must(200, "PUT", memberPath("sub-sari"), map[string]any{"email": "sari@example.com", "role": "sales_manager", "outlets": []any{"OUT-02"}})
	host.must(204, "DELETE", memberPath("sub-sari"), nil)
	if dbOne(e, "select role from rk_members where org_id = 'pt_beta' and user_id = $1", id) != "sales_manager" ||
		dbOne(e, "select array_to_string(outlets, ',') from rk_members where org_id = 'pt_beta' and user_id = $1", id) != "X-1" {
		t.Fatal("pt_beta membership touched")
	}
	if get(betaSess.must(200, "GET", "/auth/me", nil).JSON, "org", "id") != "pt_beta" {
		t.Fatal("pt_beta session")
	}
	betaTok.must(200, "GET", "/auth/me", nil)
	if countRows(e, "select 1 from rk_members where org_id = 'kopi-senja' and user_id = $1", id) != 0 {
		t.Fatal("kopi-senja membership kept")
	}
}

// An existing account found by e-mail is this token's only when it belongs to this organisation or to none; the
// e-mail of a linked account another organisation holds is not changed (contract 0043 §2).
func TestMemberAPIAccountElsewhere(t *testing.T) {
	e, host := memberEnv(t)
	ctx := context.Background()
	db := e.s.DB
	if err := auth.EnsureOrgCode(ctx, db, "pt_beta", "PT Beta", "BETA"); err != nil {
		t.Fatal(err)
	}
	for _, u := range []struct{ email, org, role string }{
		{"boss@beta.test", "pt_beta", "owner"},
		{"staff@beta.test", "pt_beta", "sales_rep"},
		{"dual@example.test", "pt_beta", "sales_rep"},
		{"dual@example.test", "kopi-senja", "sales_rep"},
	} {
		if _, err := auth.EnsureUser(ctx, db, u.email, "", "rangka-demo", u.org, u.role, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Query(ctx, "insert into rk_users (email, full_name) values ('orphan@example.test', 'Yatim')"); err != nil {
		t.Fatal(err)
	}
	refused := func(sub, email, code string) {
		t.Helper()
		r := host.do("PUT", memberPath(sub), map[string]any{"email": email, "role": "sales_rep"})
		if r.Status != 409 || errCode(r) != code {
			t.Fatalf("%s: %d %s", email, r.Status, r.Body)
		}
		if countRows(e, "select 1 from rk_identities where subject = $1", sub) != 0 ||
			countRows(e, "select 1 from rk_members m join rk_users u on u.id = m.user_id where u.email = $1 and m.org_id = 'kopi-senja'", email) != 0 {
			t.Fatalf("%s: a refusal wrote", email)
		}
	}
	refused("sub-boss", "boss@beta.test", "owner_protected") // another organisation's owner
	refused("sub-staff", "staff@beta.test", "account_elsewhere")
	// a member of this organisation that pt_beta also holds is not linked either: the subject would enter pt_beta
	if r := host.do("PUT", memberPath("sub-dual"), map[string]any{"email": "dual@example.test", "role": "sales_manager"}); r.Status != 409 || errCode(r) != "account_elsewhere" {
		t.Fatalf("dual: %d %s", r.Status, r.Body)
	}
	if countRows(e, "select 1 from rk_identities where subject = 'sub-dual'") != 0 {
		t.Fatal("dual linked")
	}
	if dbOne(e, "select role from rk_members m join rk_users u on u.id = m.user_id where u.email = 'dual@example.test' and m.org_id = 'kopi-senja'") != "sales_rep" {
		t.Fatal("a refused push changed the membership here")
	}
	// an orphan account (no membership anywhere) is accepted
	r := host.must(200, "PUT", memberPath("sub-orphan"), map[string]any{"email": "orphan@example.test", "role": "sales_rep", "outlets": []any{"OUT-01", "OUT-01", "OUT-02"}})
	if r.JSON["created"] != false || dbOne(e, "select subject from rk_identities i join rk_users u on u.id = i.user_id where u.email = 'orphan@example.test'") != "sub-orphan" {
		t.Fatalf("orphan: %s", r.Body)
	}
	if dbOne(e, "select array_to_string(m.outlets, ',') from rk_members m join rk_users u on u.id = m.user_id where u.email = 'orphan@example.test' and m.org_id = 'kopi-senja'") != "OUT-01,OUT-02" {
		t.Fatal("duplicate outlets kept")
	}
	// the e-mail of a linked account pt_beta also holds is not this token's to change (linked here by its own sign-in)
	dual := dbOne(e, "select id from rk_users where email = 'dual@example.test'").(int64)
	if err := auth.LinkIdentity(ctx, db, memberIssuer, "sub-dual", dual); err != nil {
		t.Fatal(err)
	}
	r = host.do("PUT", memberPath("sub-dual"), map[string]any{"email": "dual.baru@example.test", "role": "sales_manager"})
	if r.Status != 409 || errCode(r) != "account_elsewhere" {
		t.Fatalf("e-mail change of a shared account: %d %s", r.Status, r.Body)
	}
	if countRows(e, "select 1 from rk_users where email = 'dual@example.test'") != 1 {
		t.Fatal("a refused e-mail change wrote")
	}
	// the owner of pt_beta, linked by its own sign-in and a non-owner member here, keeps its e-mail too
	boss := dbOne(e, "select id from rk_users where email = 'boss@beta.test'").(int64)
	if err := auth.LinkIdentity(ctx, db, memberIssuer, "sub-boss2", boss); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Query(ctx, "insert into rk_members (org_id, user_id, role) values ('kopi-senja', $1, 'sales_rep')", boss); err != nil {
		t.Fatal(err)
	}
	r = host.do("PUT", memberPath("sub-boss2"), map[string]any{"email": "boss.baru@example.test", "role": "sales_rep"})
	if r.Status != 409 || errCode(r) != "owner_protected" || dbOne(e, "select email from rk_users where id = $1", boss) != "boss@beta.test" {
		t.Fatalf("owner elsewhere e-mail change: %d %s", r.Status, r.Body)
	}
	// a subject already linked to another organisation's owner is refused before any write, even unchanged e-mail
	if _, err := db.Query(ctx, "delete from rk_members where org_id = 'kopi-senja' and user_id = $1", boss); err != nil {
		t.Fatal(err)
	}
	r = host.do("PUT", memberPath("sub-boss2"), map[string]any{"email": "boss@beta.test", "role": "sales_manager"})
	if r.Status != 409 || errCode(r) != "owner_protected" ||
		countRows(e, "select 1 from rk_members where org_id = 'kopi-senja' and user_id = $1", boss) != 0 {
		t.Fatalf("linked owner of pt_beta: %d %s", r.Status, r.Body)
	}
}

// memberRecordOf is the user record (contract 0055) of the account with email in kopi-senja: its name from
// rk_members.record (or the record with that account_id) and its access.
func memberRecordOf(e *env, email string) (name, access string) {
	e.t.Helper()
	id := dbOne(e, "select id from rk_users where email = $1", email)
	if id == nil {
		return "", ""
	}
	n, _ := dbOne(e, "select name from r_user where org_id = 'kopi-senja' and account_id = $1", id).(string)
	if n == "" {
		return "", ""
	}
	a, _ := dbOne(e, "select access from r_user where org_id = 'kopi-senja' and name = $1", n).(string)
	return n, a
}

// Contract 0055 §2: a member pushed by the host gets a user record, pending until the first sign-in.
func TestMemberAPICreatesRecord(t *testing.T) {
	e, host := memberEnv(t)
	id := int64(host.must(200, "PUT", memberPath("sub-sari"), map[string]any{"email": "sari@example.com", "name": "Sari", "role": "sales_rep", "outlets": []any{"OUT-01"}}).JSON["user_id"].(float64))
	name, access := memberRecordOf(e, "sari@example.com")
	if name == "" || access != "pending" {
		t.Fatalf("record %q access %q", name, access)
	}
	if dbOne(e, "select record from rk_members where org_id = 'kopi-senja' and user_id = $1", id) != name ||
		dbOne(e, "select role from r_user where org_id = 'kopi-senja' and name = $1", name) != "sales_rep" ||
		dbOne(e, "select outlet from r_user_outlet where org_id = 'kopi-senja' and parent = $1", name) != "OUT-01" {
		t.Fatal("record not in step with the membership")
	}
}

// Contract 0055 §2: removing a member keeps the record, marked removed with no outlets.
func TestMemberAPIDeleteMarksRemoved(t *testing.T) {
	e, host := memberEnv(t)
	host.must(200, "PUT", memberPath("sub-sari"), map[string]any{"email": "sari@example.com", "role": "sales_rep", "outlets": []any{"OUT-01"}})
	before, _ := memberRecordOf(e, "sari@example.com")
	host.must(204, "DELETE", memberPath("sub-sari"), nil)
	name, access := memberRecordOf(e, "sari@example.com")
	if name != before || access != "removed" || countRows(e, "select 1 from r_user_outlet where org_id = 'kopi-senja' and parent = $1", name) != 0 {
		t.Fatalf("record %q (was %q) access %q", name, before, access)
	}
}

// Contract 0055 §5: a failed sync does not fail the member write; the repair makes the record later.
func TestMemberAPISyncFailureStillOK(t *testing.T) {
	e, host := memberEnv(t)
	ctx := context.Background()
	if _, err := e.s.DB.Exec(ctx, `create function rk_test_no_user() returns trigger language plpgsql as $$ begin raise exception 'naming broken'; end $$;
		create trigger rk_test_no_user before insert on r_user for each row execute function rk_test_no_user()`); err != nil {
		t.Fatal(err)
	}
	id := int64(host.must(200, "PUT", memberPath("sub-sari"), map[string]any{"email": "sari@example.com", "role": "sales_rep"}).JSON["user_id"].(float64))
	if countRows(e, "select 1 from rk_members where org_id = 'kopi-senja' and user_id = $1", id) != 1 {
		t.Fatal("membership not stored")
	}
	if name, _ := memberRecordOf(e, "sari@example.com"); name != "" {
		t.Fatalf("record %q made through a broken trigger", name)
	}
	if _, err := e.s.DB.Exec(ctx, "drop trigger rk_test_no_user on r_user"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Engine.RepairMemberRecords(ctx, "kopi-senja"); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if name, access := memberRecordOf(e, "sari@example.com"); name == "" || access != "pending" {
		t.Fatalf("after repair: %q %q", name, access)
	}
}

// Contract 0055 §2: the first sign-in sets signed_in_at and makes the record active.
func TestFirstSignInActivates(t *testing.T) {
	e := newEnv(t, false, nil)
	if name, access := memberRecordOf(e, "ceo@saga.co.id"); name != "USR-001" || access != "pending" {
		t.Fatalf("seeded owner: %q %q", name, access)
	}
	e.login()
	if dbOne(e, "select signed_in_at is not null from rk_users where email = 'ceo@saga.co.id'") != true {
		t.Fatal("signed_in_at not set")
	}
	if _, access := memberRecordOf(e, "ceo@saga.co.id"); access != "active" {
		t.Fatalf("after sign-in: %q", access)
	}
}
