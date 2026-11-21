package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// keyEnv is kopi-senja with the owner signed in.
func keyEnv(t *testing.T, mutate func(apps, site string)) *env {
	t.Helper()
	e := newEnv(t, false, mutate)
	e.login()
	return e
}

// mintKey creates an integration key as the owner and returns its id and a client that sends it.
func mintKey(t *testing.T, e *env, role string, scopes ...string) (int64, *env) {
	t.Helper()
	r := e.must(201, "POST", "/api/v1/integration-keys", map[string]any{"name": "akuntansi", "role": role, "scopes": scopes})
	id := int64(r.JSON["id"].(float64))
	return id, &env{t: t, ts: e.ts, token: r.JSON["token"].(string)}
}

func TestIntegrationKeyScopesAndRevoke(t *testing.T) {
	e := keyEnv(t, nil)
	e.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "Pelanggan Uji"})
	id, key := mintKey(t, e, "sales_manager", "api:read:customer")

	// read is allowed for the scoped model, and the service member is no person
	key.must(200, "GET", "/api/v1/r/customer", nil)
	if n := countRows(e, "select 1 from rk_users where service and email like 'key-%@integration.invalid'"); n != 1 {
		t.Fatalf("service members = %d", n)
	}
	if n := countRows(e, "select 1 from rk_users where service and password_hash is not null"); n != 0 {
		t.Fatal("a service member has a password")
	}
	// write needs its own scope, per model; read of another model is refused by the scope, not by the role
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/v1/r/customer"}, {"PATCH", "/api/v1/r/customer/CUST-0001"}, {"DELETE", "/api/v1/r/customer/CUST-0001"},
		{"GET", "/api/v1/r/lead"}, {"GET", "/api/v1/aggregate/lead"}, {"POST", "/api/v1/r/customer/CUST-0001/submit"},
	} {
		if r := key.do(c.method, c.path, map[string]any{"customer_name": "x"}); r.Status != 403 || errCode(r) != "scope_limited" {
			t.Fatalf("%s %s: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
	// a write key can write, and still obeys the role
	_, w := mintKey(t, e, "sales_manager", "api:read:customer", "api:write:customer")
	w.must(201, "POST", "/api/v1/r/customer", map[string]any{"customer_name": "Dari kunci"})
	_, rep := mintKey(t, e, "sales_rep", "api:read:deal", "api:read:*")
	if r := rep.do("GET", "/api/v1/r/deal", nil); r.Status == 401 || strings.Contains(string(r.Body), "scope_limited") {
		t.Fatalf("read:* must pass the scope guard: %d %s", r.Status, r.Body)
	}

	// the list reports the key, its role and its requests today
	l := e.must(200, "GET", "/api/v1/integration-keys", nil)
	rows := l.JSON["data"].([]any)
	if len(rows) != 3 || get(rows, 0, "role") != "sales_manager" || get(rows, 0, "requests_today").(float64) < 1 {
		t.Fatalf("list: %s", l.Body)
	}
	if strings.Contains(string(l.Body), "rk_") && strings.Contains(string(l.Body), "token") {
		t.Fatalf("list leaks a token: %s", l.Body)
	}

	// revoking removes the member and the token; the next call is unauthenticated; repeating is fine
	e.must(204, "DELETE", "/api/v1/integration-keys/"+strconv.FormatInt(id, 10), nil)
	key.must(401, "GET", "/api/v1/r/customer", nil)
	e.must(204, "DELETE", "/api/v1/integration-keys/"+strconv.FormatInt(id, 10), nil)
	if n := countRows(e, "select 1 from rk_tokens where id = $1", id); n != 0 {
		t.Fatal("token stayed")
	}
}

func TestIntegrationKeyRefusedRoutes(t *testing.T) {
	e := keyEnv(t, nil)
	_, key := mintKey(t, e, "sales_manager", "api:read:*", "api:write:customer")
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/v1/tokens", map[string]any{"name": "child"}},
		{"GET", "/api/v1/integration-keys", nil},
		{"POST", "/api/v1/integration-keys", map[string]any{"name": "k", "role": "sales_rep", "scopes": []any{"api:read:*"}}},
		{"DELETE", "/api/v1/integration-keys/1", nil},
		{"GET", "/api/v1/changesets", nil},
		{"POST", "/api/v1/changesets", map[string]any{"title": "x", "ops": []any{}}},
		{"PUT", "/api/v1/connectors/whatsapp_business/auth/access_token", map[string]any{"value": "x-long-enough-value"}},
		{"PUT", "/api/v1/members/kantor/sub-1", map[string]any{"email": "a@example.com", "role": "sales_rep"}},
		{"GET", "/api/v1/members/options", nil},
		{"GET", "/api/v1/orgs/kopi-senja/members", nil},
		{"GET", "/api/v1/meta", nil},
		{"GET", "/auth/me", nil},
		{"GET", "/api/v1/r/customer/CUST-0001/attachments", nil},
		{"GET", "/api/v1/r/customer/CUST-0001/print", nil},
		{"GET", "/api/v1/export/r/customer", nil},
		{"POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}},
	} {
		r := key.do(c.method, c.path, c.body)
		if r.Status != 403 || !strings.Contains(string(r.Body), "scope_limited") {
			t.Fatalf("key on %s %s: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
	if n := countRows(e, "select 1 from rk_tokens where name = 'child' or name = 'k'"); n != 0 {
		t.Fatal("a key minted a token")
	}
	// a plain token is still served on MCP, so the refusal above is the key's
	personToken(e, "ceo@saga.co.id", "kopi-senja").must(200, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
}

func TestIntegrationKeyMinting(t *testing.T) {
	e := keyEnv(t, nil)
	ok := map[string]any{"name": "k", "role": "sales_rep", "scopes": []any{"api:read:customer"}}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for a, b := range ok {
			m[a] = b
		}
		m[k] = v
		return m
	}
	// only an owner with a session
	rep := repEnv(t, e)
	if r := rep.do("POST", "/api/v1/integration-keys", ok); r.Status != 403 || errCode(r) != "forbidden" {
		t.Fatalf("non-owner: %d %s", r.Status, r.Body)
	}
	if r := personToken(e, "ceo@saga.co.id", "kopi-senja").do("POST", "/api/v1/integration-keys", ok); r.Status != 403 || errCode(r) != "forbidden" {
		t.Fatalf("token caller: %d %s", r.Status, r.Body)
	}
	(&env{t: t, ts: e.ts}).must(401, "POST", "/api/v1/integration-keys", ok)
	// /tokens never mints an api: scope
	if r := e.do("POST", "/api/v1/tokens", map[string]any{"name": "x", "scopes": []any{"api:read:customer"}}); r.Status != 403 || errCode(r) != "forbidden" {
		t.Fatalf("tokens api scope: %d %s", r.Status, r.Body)
	}
	for _, c := range []struct {
		name string
		body map[string]any
		code string
	}{
		{"owner role", with("role", "owner"), "unknown_role"},
		{"missing role", with("role", "nope"), "unknown_role"},
		{"unknown model", with("scopes", []any{"api:read:nope"}), "unknown_scope"},
		{"write star", with("scopes", []any{"api:write:*"}), "unknown_scope"},
		{"other scope", with("scopes", []any{"members:write"}), "unknown_scope"},
		{"secrets", with("scopes", []any{"secrets:write"}), "unknown_scope"},
		{"assistant", with("scopes", []any{"assistant"}), "unknown_scope"},
		{"no scopes", with("scopes", []any{}), "unknown_scope"},
		{"unknown outlet", with("outlets", []any{"OUT-NOPE"}), "unknown_outlet"},
	} {
		if r := e.do("POST", "/api/v1/integration-keys", c.body); r.Status != 422 || errCode(r) != c.code {
			t.Fatalf("%s: %d %s", c.name, r.Status, r.Body)
		}
	}
	if n := countRows(e, "select 1 from rk_users where service"); n != 0 {
		t.Fatalf("refused calls left %d service members", n)
	}
	// a key cannot sign in
	r := mintKeyRaw(t, e)
	(&env{t: t, ts: e.ts, jar: newJar()}).must(401, "POST", "/auth/login", map[string]any{"email": r, "password": ""})
}

func mintKeyRaw(t *testing.T, e *env) string {
	t.Helper()
	mintKey(t, e, "sales_rep", "api:read:customer")
	return dbOne(e, "select email from rk_users where service limit 1").(string)
}

func TestIntegrationKeyPlanLimit(t *testing.T) {
	e := keyEnv(t, func(apps, site string) {
		p := filepath.Join(site, "features.rk")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(strings.Replace(string(b), "limits: {users: 3,", "limits: {integration_keys: 1, users: 3,", 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	setPlan(e, "mulai")
	mintKey(t, e, "sales_rep", "api:read:customer")
	r := e.do("POST", "/api/v1/integration-keys", map[string]any{"name": "dua", "role": "sales_rep", "scopes": []any{"api:read:customer"}})
	if r.Status != 403 || errCode(r) != "plan_limit" || !strings.Contains(errMsg(r), "kunci integrasi") {
		t.Fatalf("second key: %d %s", r.Status, r.Body)
	}
	l := e.must(200, "GET", "/api/v1/integration-keys", nil)
	if l.JSON["limit"].(float64) != 1 || l.JSON["used"].(float64) != 1 {
		t.Fatalf("list limit/used: %s", l.Body)
	}
	// a service member is no person: users stay at the owner alone
	p := e.must(200, "GET", "/api/v1/plan", nil)
	if get(p.JSON, "usage", "users", "used").(float64) != 1 {
		t.Fatalf("a key counted as a user: %s", p.Body)
	}
}

func TestOpenAPIFilteredAndGolden(t *testing.T) {
	e := keyEnv(t, nil)
	owner := e.must(200, "GET", "/api/v1/openapi.json", nil)
	if owner.JSON["openapi"] != "3.1.0" || get(owner.JSON, "components", "securitySchemes", "bearerAuth", "scheme") != "bearer" {
		t.Fatalf("header: %.300s", owner.Body)
	}
	paths := func(r *resp) map[string]any { return r.JSON["paths"].(map[string]any) }
	if _, ok := paths(owner)["/api/v1/r/customer"]; !ok {
		t.Fatal("owner doc misses customer")
	}
	(&env{t: t, ts: e.ts}).must(401, "GET", "/api/v1/openapi.json", nil)

	// a key sees only what its scopes allow: read of customer, no write path, no other model
	_, ro := mintKey(t, e, "sales_manager", "api:read:customer")
	k := ro.must(200, "GET", "/api/v1/openapi.json", nil)
	if len(paths(k)) != 2 {
		t.Fatalf("read-only key paths: %v", keysOf(paths(k)))
	}
	if _, ok := get(paths(k), "/api/v1/r/customer").(map[string]any)["post"]; ok {
		t.Fatal("read-only key sees create")
	}
	_, rw := mintKey(t, e, "sales_manager", "api:read:customer", "api:write:customer")
	if _, ok := get(paths(rw.must(200, "GET", "/api/v1/openapi.json", nil)), "/api/v1/r/customer").(map[string]any)["post"]; !ok {
		t.Fatal("write key misses create")
	}
	// a person with a narrower role sees less than the owner
	ops := func(r *resp) (n int) {
		for _, p := range paths(r) {
			n += len(p.(map[string]any))
		}
		return n
	}
	rep := repEnv(t, e)
	if n, m := ops(rep.must(200, "GET", "/api/v1/openapi.json", nil)), ops(owner); n >= m {
		t.Fatalf("role filter: rep sees %d operations, owner %d", n, m)
	}

	// golden: the owner's document is stable, byte for byte
	golden := filepath.Join("..", "..", "testdata", "openapi", "kopi-senja.json")
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	_ = enc.Encode(owner.JSON)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, []byte(buf.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (UPDATE_GOLDEN=1 writes it)", err)
	}
	if string(want) != buf.String() {
		t.Fatal("openapi.json differs from testdata/openapi/kopi-senja.json; review the change and run with UPDATE_GOLDEN=1")
	}
}

func keysOf(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
