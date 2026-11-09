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
