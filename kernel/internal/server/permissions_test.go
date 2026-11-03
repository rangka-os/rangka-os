package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// permPerson is a member of the perm-site org. record is the core user record (user.name in if:).
type permPerson struct {
	key, email, role, record string
}

var permPeople = []permPerson{
	{"owner", "owner@example.test", "owner", "USR-001"},
	{"clerkA", "clerk.a@example.test", "clerk", "USR-002"},
	{"clerkB", "clerk.b@example.test", "clerk", "USR-003"},
	{"manager", "manager@example.test", "manager", "USR-004"},
	{"viewer", "viewer@example.test", "viewer", "USR-005"},
	{"nobody", "nobody@example.test", "", "USR-006"},
}

const permPassword = "rangka-test"

// permFixture seeds V1 (clerk A's) and V2 (clerk B's).
const permFixture = `voucher:
  - {name: V1, title: Bensin, owner: USR-002, amount: 100000}
  - {name: V2, title: Parkir, owner: USR-003, amount: 20000}
note:
  - {name: N1, body: halo}
`

// newPermEnv starts the kernel on testdata/perm-apps + testdata/perm-site with every permPeople member.
func newPermEnv(t *testing.T) *env {
	t.Helper()
	dbURL := testdb.New(t)
	auth.BcryptCost = 4
	apps, _ := filepath.Abs("../../testdata/perm-apps")
	site, _ := filepath.Abs("../../testdata/perm-site")
	fx := t.TempDir()
	if err := os.WriteFile(filepath.Join(fx, "perm.rk"), []byte(permFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{AppsDir: apps, SiteDir: site, DatabaseURL: dbURL, Addr: ":0", Schemas: schema.Embedded(), Version: "test", Now: fixedNow,
		Logf: func(string, ...any) {}, FilesDir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	reg, err := LoadRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pg.NewPool(ctx, dbURL, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Seed(ctx, pool, reg, SeedOptions{Org: "perm-site", FixturesDir: fx, Email: permPeople[0].email, Password: permPassword,
		UserRecord: permPeople[0].record, Now: fixedNow}); err != nil {
		t.Fatal(err)
	}
	for _, p := range permPeople[1:] {
		if _, err := auth.EnsureUser(ctx, pool, p.email, p.key, permPassword, "perm-site", p.role, p.record); err != nil {
			t.Fatal(err)
		}
	}
	pool.Close()
	s, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.Start(ctx)
	ts := httptest.NewServer(s.Handler())
	jar, _ := cookiejar.New(nil)
	e := &env{t: t, s: s, ts: ts, jar: jar}
	t.Cleanup(func() {
		ts.Close()
		cancel()
		s.Wait()
		s.Close()
	})
	for i := 0; i < 100 && !s.Hub.Listening(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	return e
}

// clients signs p in and returns a cookie client and an API-token client.
func (e *env) clients(p permPerson) (cookie, token *env) {
	e.t.Helper()
	jar, _ := cookiejar.New(nil)
	cookie = &env{t: e.t, s: e.s, ts: e.ts, jar: jar}
	cookie.must(200, "POST", "/auth/login", map[string]any{"email": p.email, "password": permPassword})
	tok := cookie.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "matrix"}).JSON["token"].(string)
	return cookie, &env{t: e.t, s: e.s, ts: e.ts, token: tok}
}

// permAllowed is the expected decision of the perm fixture: manager everything (tested actions), clerk
// read/create/write/submit/print on vouchers it owns, viewer read only, a member without roles nothing.
func permAllowed(p permPerson, action, owner string) bool {
	switch p.role {
	case "owner", "manager":
		return true
	case "clerk":
		if owner != p.record {
			return false
		}
		switch action {
		case "read", "create", "write", "submit", "print":
			return true
		}
	case "viewer":
		return action == "read"
	}
	return false
}

func want403(t *testing.T, r *resp, what, model, action string) {
	t.Helper()
	if r.Status != http.StatusForbidden || dig(r.JSON, "error", "code") != "permission_denied" || dig(r.JSON, "error", "model") != model ||
		dig(r.JSON, "error", "action") != action || dig(r.JSON, "error", "message") == "" {
		t.Fatalf("%s = %d %s, want 403 permission_denied on %s/%s", what, r.Status, r.Body, model, action)
	}
}

// expect asserts status okStatus when allowed, else the 403 body for action.
func expect(t *testing.T, c *env, allowed bool, okStatus int, action, method, path string, body any) *resp {
	t.Helper()
	r := c.do(method, path, body)
	what := method + " " + path
	if !allowed {
		want403(t, r, what, "voucher", action)
		return r
	}
	if r.Status != okStatus {
		t.Fatalf("%s = %d %s, want %d", what, r.Status, r.Body, okStatus)
	}
	if okStatus == http.StatusNotFound && dig(r.JSON, "error", "code") != "not_found" {
		t.Fatalf("%s: want the catch-all not_found, got %s", what, r.Body)
	}
	return r
}

func TestPermissionMatrix(t *testing.T) {
	e := newPermEnv(t)
	sys := engine.Actor{Org: "perm-site", ID: "seed", System: true}
	ctx := context.Background()
	people := map[string]permPerson{}
	cookies, tokens := map[string]*env{}, map[string]*env{}
	for _, p := range permPeople {
		people[p.key] = p
		cookies[p.key], tokens[p.key] = e.clients(p)
	}

	// SSE: clerk A sees the update of V1 (own) but not of V2 (clerk B's), which comes first.
	t.Run("sse", func(t *testing.T) {
		req, _ := http.NewRequest("GET", e.ts.URL+"/api/v1/events?models=voucher", nil)
		req.Header.Set("Authorization", "Bearer "+tokens["clerkA"].token)
		res, err := http.DefaultClient.Do(req)
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("events: %v %v", err, res)
		}
		defer res.Body.Close()
		time.Sleep(100 * time.Millisecond)
		m := cookies["manager"]
		m.must(200, "PATCH", "/api/v1/r/voucher/V2", map[string]any{"amount": 25000})
		m.must(200, "PATCH", "/api/v1/r/voucher/V1", map[string]any{"amount": 110000})
		evs := readEvents(t, res.Body, 1, 2*time.Second)
		if evs[0]["model"] != "voucher" || evs[0]["name"] != "V1" || evs[0]["op"] != "updated" {
			t.Fatalf("clerk A got %v, want only V1's update", evs[0])
		}
	})

	// meta: can per model from Allowed
	t.Run("meta", func(t *testing.T) {
		clerk := cookies["clerkA"].must(200, "GET", "/api/v1/meta", nil).JSON
		if dig(clerk, "models", "voucher", "can", "cancel") != false || dig(clerk, "models", "voucher", "can", "read") != true ||
			dig(clerk, "models", "note", "can", "delete") != true {
			t.Fatalf("clerk can: %v / %v", dig(clerk, "models", "voucher", "can"), dig(clerk, "models", "note", "can"))
		}
		mgr := tokens["manager"].must(200, "GET", "/api/v1/meta", nil).JSON
		if dig(mgr, "models", "voucher", "can", "cancel") != true || dig(mgr, "models", "voucher", "can", "import") != false {
			t.Fatalf("manager can: %v", dig(mgr, "models", "voucher", "can"))
		}
		nob := cookies["nobody"].must(200, "GET", "/api/v1/meta", nil).JSON
		if dig(nob, "models", "voucher", "can", "read") != false {
			t.Fatalf("nobody can: %v", dig(nob, "models", "voucher", "can"))
		}
	})

	// REST over cookie session and API token
	for _, p := range permPeople {
		for _, iface := range []string{"cookie", "token"} {
			p, iface := p, iface
			t.Run(p.key+"/"+iface, func(t *testing.T) {
				c := cookies[p.key]
				if iface == "token" {
					c = tokens[p.key]
				}
				tag := "matrix " + p.key + " " + iface
				mk := func(owner string) string {
					res, err := e.s.Engine.Create(ctx, sys, "voucher", map[string]any{"title": tag, "owner": owner, "amount": 1000})
					if err != nil {
						t.Fatal(err)
					}
					return res.Record["name"].(string)
				}
				recs := map[string]string{"USR-002": mk("USR-002"), "USR-003": mk("USR-003")}

				// list: rows the caller may not read are dropped and not counted
				l := expect(t, c, p.role != "", 200, "read", "GET", "/api/v1/r/voucher?filter="+url.QueryEscape("title == '"+tag+"'"), nil)
				if p.role != "" {
					want := 0
					for owner := range recs {
						if permAllowed(p, "read", owner) {
							want++
						}
					}
					if l.JSON["total"] != float64(want) || len(l.JSON["data"].([]any)) != want {
						t.Fatalf("list: %s, want %d rows", l.Body, want)
					}
				}

				// create: the new record's if: decides
				expect(t, c, permAllowed(p, "create", p.record), 201, "create", "POST", "/api/v1/r/voucher", map[string]any{"title": tag + " new", "owner": p.record})
				if p.role == "clerk" {
					expect(t, c, false, 201, "create", "POST", "/api/v1/r/voucher", map[string]any{"title": tag + " other", "owner": "USR-001"})
				}

				for _, owner := range []string{"USR-002", "USR-003"} {
					name := recs[owner]
					base := "/api/v1/r/voucher/" + name
					ok := func(action string) bool { return permAllowed(p, action, owner) }
					expect(t, c, ok("read"), 200, "read", "GET", base, nil)
					// print and attachments: the guard loads the record (read) and then checks the action; allowed
					// callers reach the catch-all 404 for print until S3 adds the handler
					guarded := func(action string) string {
						if !ok("read") {
							return "read"
						}
						return action
					}
					expect(t, c, ok("print"), 404, guarded("print"), "GET", base+"/print", nil)
					attachments(t, c, cookies["manager"], base, ok("read"), ok("write"), guarded("write"))
					expect(t, c, ok("write"), 200, "write", "PATCH", base, map[string]any{"amount": 2000})
					// model action: write; the hook is declared but not implemented, so allowed callers get 501
					expect(t, c, ok("write"), 501, "write", "POST", base+"/actions/stamp", map[string]any{})
					sub := expect(t, c, ok("submit"), 200, "submit", "POST", base+"/submit", map[string]any{})
					if ok("submit") {
						name = sub.JSON["name"].(string)
						base = "/api/v1/r/voucher/" + name
					}
					expect(t, c, ok("cancel"), 200, "cancel", "POST", base+"/cancel", map[string]any{})
					expect(t, c, ok("delete"), 204, "delete", "DELETE", "/api/v1/r/voucher/"+mk(owner), nil)
				}
				// a model without permissions: stays open to every member
				c.must(200, "GET", "/api/v1/r/note/N1", nil)
			})
		}
	}

	// MCP over the API token: the same checks, as isError results
	for _, p := range permPeople {
		p := p
		t.Run(p.key+"/mcp", func(t *testing.T) {
			m := tokens[p.key]
			mcpCall := func(name string, args map[string]any) map[string]any {
				t.Helper()
				return rpc(m, 3, "tools/call", map[string]any{"name": name, "arguments": args})
			}
			wantDenied := func(res map[string]any, what string) {
				t.Helper()
				if res["isError"] != true || dig(res, "structuredContent", "error", "code") != "permission_denied" ||
					dig(res, "structuredContent", "error", "model") != "voucher" || dig(res, "structuredContent", "error", "action") != "read" ||
					dig(res, "structuredContent", "error", "message") == "" {
					t.Fatalf("%s: %v, want permission_denied voucher/read", what, res)
				}
			}
			for owner, name := range map[string]string{"USR-002": "V1", "USR-003": "V2"} {
				res := mcpCall("rangka_get_record", map[string]any{"model": "voucher", "name": name})
				if permAllowed(p, "read", owner) {
					if res["isError"] != false || dig(res, "structuredContent", "name") != name {
						t.Fatalf("get %s: %v", name, res)
					}
				} else {
					wantDenied(res, "get "+name)
				}
			}
			res := mcpCall("rangka_query", map[string]any{"model": "voucher", "filter": "name == 'V1' || name == 'V2'"})
			if p.role == "" {
				wantDenied(res, "query")
				return
			}
			want := 0
			for _, owner := range []string{"USR-002", "USR-003"} {
				if permAllowed(p, "read", owner) {
					want++
				}
			}
			if res["isError"] != false || dig(res, "structuredContent", "total") != float64(want) {
				t.Fatalf("query: %v, want %d", res, want)
			}
		})
	}

	// flows run by a person run as that person; dashboards hide widgets over unreadable models
	t.Run("flows and dashboards", func(t *testing.T) {
		cookies["manager"].must(200, "POST", "/api/v1/flows/make_voucher/run", map[string]any{"event": map[string]any{}})
		r := cookies["viewer"].do("POST", "/api/v1/flows/make_voucher/run", map[string]any{"event": map[string]any{}})
		want403(t, r, "viewer runs make_voucher", "voucher", "create")
		d := cookies["nobody"].must(200, "GET", "/api/v1/dashboards/perm_overview", nil).JSON
		if dig(d, "widgets", 0, "error", "code") != "permission_denied" || dig(d, "widgets", 0, "error", "message") == "" {
			t.Fatalf("nobody dashboard: %v", d)
		}
	})
}

// attachments checks the S6 routes on a draft voucher at base for caller c (mgr may write it): list needs read,
// upload write, download read and delete write on the attachment's record. A caller who cannot read the record
// gets 403 read everywhere (writeAction is then "read").
func attachments(t *testing.T, c, mgr *env, base string, canRead, canWrite bool, writeAction string) {
	t.Helper()
	data := []byte("nota bensin\n")
	att := mgr.upload(base+"/attachments", "mgr.txt", data)
	if att.Status != http.StatusCreated {
		t.Fatalf("manager upload: %d %s", att.Status, att.Body)
	}
	href := att.JSON["url"].(string)

	l := expect(t, c, canRead, 200, "read", "GET", base+"/attachments", nil)
	if canRead && (len(l.JSON["data"].([]any)) != 1 || dig(l.JSON, "data", 0, "id") != att.JSON["id"] ||
		dig(l.JSON, "data", 0, "filename") != "mgr.txt" || dig(l.JSON, "data", 0, "size") != float64(len(data))) {
		t.Fatalf("list: %s", l.Body)
	}

	up := c.upload(base+"/attachments", "own.txt", data)
	if !canWrite {
		want403(t, up, "upload "+base, "voucher", writeAction)
	} else if up.Status != http.StatusCreated || up.JSON["filename"] != "own.txt" {
		t.Fatalf("upload %s = %d %s, want 201", base, up.Status, up.Body)
	}

	dl := c.do("GET", href, nil)
	if !canRead {
		want403(t, dl, "download "+href, "voucher", "read")
	} else if dl.Status != http.StatusOK || string(dl.Body) != string(data) {
		t.Fatalf("download %s = %d %q, want 200", href, dl.Status, dl.Body)
	}

	del := c.do("DELETE", href, nil)
	if !canWrite {
		want403(t, del, "delete "+href, "voucher", writeAction)
		mgr.must(http.StatusNoContent, "DELETE", href, nil)
	} else if del.Status != http.StatusNoContent {
		t.Fatalf("delete %s = %d %s, want 204", href, del.Status, del.Body)
	}
	if canWrite {
		c.must(http.StatusNoContent, "DELETE", up.JSON["url"].(string), nil)
	}
}

// dig is get without panics: nil when a step is missing or of another type.
func dig(m any, path ...any) any {
	cur := m
	for _, p := range path {
		switch k := p.(type) {
		case string:
			mm, ok := cur.(map[string]any)
			if !ok {
				return nil
			}
			cur = mm[k]
		case int:
			l, ok := cur.([]any)
			if !ok || k >= len(l) {
				return nil
			}
			cur = l[k]
		}
	}
	return cur
}
