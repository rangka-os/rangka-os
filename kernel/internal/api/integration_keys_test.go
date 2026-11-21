package api

import (
	"net/http/httptest"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/ratelimit"
)

func TestKeyRoute(t *testing.T) {
	for _, c := range []struct {
		method, path, action, model string
		ok                          bool
	}{
		{"GET", "/api/v1/openapi.json", "", "", true},
		{"GET", "/api/v1/r/customer", "read", "customer", true},
		{"POST", "/api/v1/r/customer", "write", "customer", true},
		{"GET", "/api/v1/r/customer/CUST-0001", "read", "customer", true},
		{"PATCH", "/api/v1/r/customer/CUST-0001", "write", "customer", true},
		{"DELETE", "/api/v1/r/customer/CUST-0001", "write", "customer", true},
		{"POST", "/api/v1/r/customer/submit", "write", "customer", true},
		{"POST", "/api/v1/r/customer/CUST-0001/submit", "write", "customer", true},
		{"POST", "/api/v1/r/customer/CUST-0001/actions/send", "write", "customer", true},
		{"GET", "/api/v1/aggregate/customer", "read", "customer", true},
		{"POST", "/api/v1/r/customer/CUST-0001/attachments", "", "", false},
		{"GET", "/api/v1/r/customer/CUST-0001/attachments", "", "", false},
		{"GET", "/api/v1/r/customer/CUST-0001/print", "", "", false},
		{"POST", "/api/v1/openapi.json", "", "", false},
		{"GET", "/api/v1/tokens", "", "", false},
		{"POST", "/api/v1/tokens", "", "", false},
		{"GET", "/api/v1/meta", "", "", false},
		{"GET", "/api/v1/export/r/customer", "", "", false},
		{"GET", "/api/v1/r/", "", "", false},
		{"POST", "/mcp", "", "", false},
	} {
		a, m, ok := keyRoute(httptest.NewRequest(c.method, c.path, nil))
		if a != c.action || m != c.model || ok != c.ok {
			t.Errorf("%s %s = (%q, %q, %v), want (%q, %q, %v)", c.method, c.path, a, m, ok, c.action, c.model, c.ok)
		}
	}
}

func TestKeyGuardScopes(t *testing.T) {
	p := &auth.Principal{Via: "token", Scope: "api:read:customer api:write:lead"}
	for _, c := range []struct {
		method, path string
		ok           bool
	}{
		{"GET", "/api/v1/r/customer", true}, {"POST", "/api/v1/r/customer", false}, {"GET", "/api/v1/r/lead", false},
		{"POST", "/api/v1/r/lead", true}, {"GET", "/api/v1/openapi.json", true}, {"GET", "/api/v1/tokens", false},
	} {
		err := keyGuard(p, httptest.NewRequest(c.method, c.path, nil))
		if (err == nil) != c.ok {
			t.Errorf("%s %s: err = %v", c.method, c.path, err)
		}
	}
	star := &auth.Principal{Via: "token", Scope: "api:read:*"}
	if keyGuard(star, httptest.NewRequest("GET", "/api/v1/r/anything", nil)) != nil || keyGuard(star, httptest.NewRequest("POST", "/api/v1/r/anything", nil)) == nil {
		t.Error("api:read:* reads every model and writes none")
	}
}

func TestIntegrationKeyScopeGrammar(t *testing.T) {
	for s, want := range map[string]bool{"api:read:customer": true, "api:write:customer": true, "api:read:*": true, "api:write:*": false,
		"api:admin:customer": false, "api:read:": false, "api:read:Customer": false, "api:read:a:b": false, "members:write": false, "secrets:write": false} {
		if got := auth.IsAPIScope(s); got != want {
			t.Errorf("IsAPIScope(%q) = %v", s, got)
		}
		if auth.ValidTokenScope(s) != (want || auth.TokenScopes[s]) {
			t.Errorf("ValidTokenScope(%q)", s)
		}
	}
	if (&auth.Principal{Via: "session", Scope: "api:read:*"}).IsIntegrationKey() {
		t.Error("only a token is a key")
	}
}

func TestKeyRateAndBucketMarks(t *testing.T) {
	r := keyRate(ratelimit.Rate{RPS: 10, Burst: 20, MaxWaiting: 8})
	if r.RPS != 5 || r.Burst != 10 || r.MaxWaiting != 4 {
		t.Fatalf("keyRate = %+v", r)
	}
	if r := keyRate(ratelimit.Rate{RPS: 1, Burst: 1, MaxWaiting: 1}); r.RPS != 1 || r.Burst != 1 || r.MaxWaiting != 1 {
		t.Fatalf("keyRate floor = %+v", r)
	}
	var v verifiedSet
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer rk_x")
	k, ok := credential(req)
	v.note(k, ok)
	if v.isKey(k, ok) {
		t.Fatal("a plain credential is not a key")
	}
	v.noteKey(k, ok)
	if !v.isKey(k, ok) {
		t.Fatal("noteKey")
	}
	v.forget(k, ok)
	if v.isKey(k, ok) || v.has(k, ok) {
		t.Fatal("forget keeps the key mark")
	}
}
