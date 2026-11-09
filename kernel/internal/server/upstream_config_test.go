package server

import (
	"reflect"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

func TestUpstreamsConfig(t *testing.T) {
	env := map[string]string{"KANTOR_ID": "rangka-kopi", "KANTOR_SECRET": "s3cret-value"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	cfg := []any{
		map[string]any{"id": "kantor", "label": "Kantor", "issuer": "https://id.example", "client_id": "${KANTOR_ID}", "client_secret": "${KANTOR_SECRET}"},
		map[string]any{"id": "publik", "issuer": "https://other.example", "client_id": "literal-id"},
	}
	got, err := upstreams(cfg, lookup)
	if err != nil {
		t.Fatal(err)
	}
	want := []auth.Upstream{
		{ID: "kantor", Label: "Kantor", Issuer: "https://id.example", ClientID: "rangka-kopi", ClientSecret: "s3cret-value"},
		{ID: "publik", Label: "publik", Issuer: "https://other.example", ClientID: "literal-id"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if ups, err := upstreams(nil, lookup); err != nil || len(ups) != 0 {
		t.Fatalf("no auth.upstream: %v %v", ups, err)
	}
	// a missing variable stops the start, naming the variable and never a value
	env["KANTOR_SECRET"] = ""
	delete(env, "KANTOR_ID")
	_, err = upstreams(cfg, lookup)
	if err == nil || !strings.Contains(err.Error(), "KANTOR_ID") || strings.Contains(err.Error(), "rangka-kopi") {
		t.Fatalf("missing client_id variable: %v", err)
	}
	env["KANTOR_ID"] = "rangka-kopi"
	_, err = upstreams(cfg, lookup)
	if err == nil || !strings.Contains(err.Error(), "KANTOR_SECRET") || strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("empty client_secret variable: %v", err)
	}
}

// auth.upstream is checked at start: the error names the entry and never carries a secret.
func TestUpstreamsConfigRefused(t *testing.T) {
	lookup := func(k string) (string, bool) { return "env-value-SECRET", true }
	ok := func() map[string]any {
		return map[string]any{"id": "kantor", "issuer": "https://id.example", "client_id": "cid", "client_secret": "${KANTOR_SECRET}"}
	}
	for name, tc := range map[string]struct {
		edit func(m map[string]any) []any
		want string
	}{
		"no id":          {func(m map[string]any) []any { delete(m, "id"); return []any{m} }, "auth.upstream[0]: id"},
		"bad id":         {func(m map[string]any) []any { m["id"] = "Kantor-1"; return []any{m} }, `id "Kantor-1" must match`},
		"duplicate id":   {func(m map[string]any) []any { return []any{m, ok()} }, "auth.upstream kantor: id is used twice"},
		"no issuer":      {func(m map[string]any) []any { delete(m, "issuer"); return []any{m} }, "auth.upstream kantor: issuer is required"},
		"http issuer":    {func(m map[string]any) []any { m["issuer"] = "http://id.example"; return []any{m} }, "issuer must be an https URL"},
		"no client_id":   {func(m map[string]any) []any { delete(m, "client_id"); return []any{m} }, "auth.upstream kantor: client_id is required"},
		"literal secret": {func(m map[string]any) []any { m["client_secret"] = "literal-SECRET"; return []any{m} }, "client_secret must be a ${ENV} reference"},
	} {
		_, err := upstreams(tc.edit(ok()), lookup)
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "SECRET") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// a loopback issuer may be http (development)
	m := ok()
	m["issuer"] = "http://localhost:9000"
	if _, err := upstreams([]any{m}, lookup); err != nil {
		t.Errorf("loopback issuer: %v", err)
	}
}

// A kernel without auth.upstream answers as before: password sign-in only, no upstream (contract 0042 compatibility).
func TestProvidersWithoutUpstream(t *testing.T) {
	e := newEnv(t, false, nil)
	r := e.do("GET", "/auth/providers", nil)
	if r.Status != 200 || strings.TrimSpace(string(r.Body)) != `{"password":true,"upstream":[]}` {
		t.Fatalf("providers: %d %s", r.Status, r.Body)
	}
	if r := e.do("GET", "/auth/upstream/kantor/start", nil); r.Status != 404 || !strings.Contains(string(r.Body), "Penyedia akun tidak ditemukan.") {
		t.Fatalf("unknown upstream: %d %s", r.Status, r.Body)
	}
}
