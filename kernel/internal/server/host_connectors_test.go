package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/secrets"
)

// Contract 0063 §2 and its Order of work step 1: the kernel's host-provided secret seam. A host connector
// (provided_by: host) gets every auth field from the platform that runs the kernel, through a secrets:host token.

const (
	hostKey   = "doku-shared-key-0063-tenant-only-zzzz" // the host's per-tenant value: never in an answer
	hostBrand = "BRN-0063-0001"
)

// hostConnectorFile is the valid fixture of testdata/host-connector (the validators' golden input).
func hostConnectorFile(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../../testdata/host-connector/connectors/rangka_pay/connector.rk")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// hostEnv is Kopi Senja on the db driver with the rangka_pay host connector in the shared connectors directory and
// RANGKA_HOST_CONNECTORS set to served ("" = unset, as on a self-hosted kernel).
func hostEnv(t *testing.T, served string) *env {
	t.Helper()
	t.Setenv("RANGKA_MASTER_KEY", testKEK(t))
	t.Setenv("RANGKA_MASTER_KEY_FILE", "")
	t.Setenv("RANGKA_SECRETS_DRIVER", "")
	t.Setenv("RANGKA_HOST_CONNECTORS", served)
	file := hostConnectorFile(t)
	e := newEnv(t, false, func(apps, site string) {
		dir := filepath.Join(filepath.Dir(apps), "rangka-connectors", "rangka_pay")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "connector.rk"), file, 0o644); err != nil {
			t.Fatal(err)
		}
	})
	e.login()
	return e
}

// installHostCS proposes config.connectors += rangka_pay and validates it.
func installHostCS(e *env) map[string]any {
	e.t.Helper()
	cs := e.must(201, "POST", "/api/v1/changesets", map[string]any{"title": "Pasang Pembayaran Rangka", "ops": []any{
		map[string]any{"op": "patch", "path": "config.rk", "patch": []any{map[string]any{"op": "add", "path": "/connectors", "value": []any{"rangka_pay"}}}}}}).JSON
	return e.must(200, "POST", "/api/v1/changesets/"+cs["id"].(string)+"/validate", nil).JSON
}

func hostTokenEnv(e *env, scopes ...string) *env {
	e.t.Helper()
	tok := e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "rangka-cloud", "scopes": scopes}).JSON["token"].(string)
	return &env{t: e.t, ts: e.ts, token: tok}
}

// Without RANGKA_HOST_CONNECTORS the host connector is host_unavailable and cannot be installed.
func TestHostConnectorUnavailable(t *testing.T) {
	e := hostEnv(t, "")
	c := catalogueEntry(t, e.must(200, "GET", "/api/v1/catalogue", nil).JSON["connectors"], "rangka_pay")
	if c["state"] != "host_unavailable" || c["provided_by"] != "host" || get(c, "auth", 0, "source") != "host" {
		t.Fatalf("catalogue entry: %v", c)
	}
	if o := catalogueEntry(t, e.must(200, "GET", "/api/v1/catalogue", nil).JSON["connectors"], "xendit"); o["provided_by"] != nil {
		t.Fatalf("an owner connector says provided_by: %v", o)
	}
	v := installHostCS(e)
	if v["status"] != "invalid" || !strings.Contains(fmtJSON(v), `"connector_host_unavailable"`) {
		t.Fatalf("validate: %s", fmtJSON(v))
	}
	// a secrets:host token cannot write an owner connector (403) on any kernel
	host := hostTokenEnv(e, "secrets:host")
	if r := host.do("PUT", "/api/v1/connectors/xendit/auth/"+firstSecretField(t, e, "xendit"), map[string]any{"value": "x"}); r.Status != 403 || get(r.JSON, "error", "code") != "forbidden" {
		t.Fatalf("secrets:host on an owner connector: %d %s", r.Status, r.Body)
	}
}

// With RANGKA_HOST_CONNECTORS=rangka_pay: installing it validates; the owner's PUT is 409 secret_host_provided; a
// secrets:host write makes it ready; GET says source: host without hint or set_by; audit says by: host.
func TestHostConnectorServed(t *testing.T) {
	e := hostEnv(t, "rangka_pay")
	if d := e.s.API.Secrets.Driver(); d != "db" {
		t.Fatalf("a hosted kernel's driver = %s, want db", d)
	}
	c := catalogueEntry(t, e.must(200, "GET", "/api/v1/catalogue", nil).JSON["connectors"], "rangka_pay")
	if c["state"] != "available" || c["provided_by"] != "host" {
		t.Fatalf("catalogue before install: %v", c)
	}
	v := installHostCS(e)
	if v["status"] != "validated" {
		t.Fatalf("validate: %s", fmtJSON(v))
	}
	id := v["id"].(string)
	pv := e.must(200, "POST", "/api/v1/changesets/"+id+"/preview", nil)
	for _, x := range get(pv.JSON, "preview", "requires").([]any) {
		if r := x.(map[string]any); r["connector"] != "rangka_pay" || r["source"] != "host" {
			t.Fatalf("requires: %s", pv.Body)
		}
	}
	e.must(200, "POST", "/api/v1/changesets/"+id+"/approve", map[string]any{"note": "ok"})
	if ap := e.must(200, "POST", "/api/v1/changesets/"+id+"/apply", nil).JSON; ap["status"] != "applied" {
		t.Fatalf("apply: %v", ap)
	}
	if c := catalogueEntry(t, e.must(200, "GET", "/api/v1/catalogue", nil).JSON["connectors"], "rangka_pay"); c["state"] != "needs_auth" || c["installed"] != true {
		t.Fatalf("catalogue after install: %v", c)
	}

	const path = "/api/v1/connectors/rangka_pay/auth"
	// the owner, and a secrets:write token, get 409 for PUT and DELETE
	for _, who := range []*env{e, hostTokenEnv(e, "secrets:write")} {
		for _, m := range []string{"PUT", "DELETE"} {
			if r := who.do(m, path+"/shared_key", map[string]any{"value": hostKey}); r.Status != 409 || get(r.JSON, "error", "code") != "secret_host_provided" {
				t.Fatalf("%s by a non-host caller: %d %s", m, r.Status, r.Body)
			}
		}
	}
	// the host writes both fields
	host := hostTokenEnv(e, "secrets:host")
	host.must(204, "PUT", path+"/shared_key", map[string]any{"value": hostKey})
	host.must(204, "PUT", path+"/brand_id", map[string]any{"value": hostBrand})
	g := e.must(200, "GET", path, nil)
	noLeak(t, "GET auth", g.Body, hostKey)
	if g.JSON["provided_by"] != "host" {
		t.Fatalf("GET auth: %s", g.Body)
	}
	for _, name := range []string{"shared_key", "brand_id"} {
		f := authField(t, g, name)
		if f["set"] != true || f["source"] != "host" || f["hint"] != nil || f["set_by"] != nil || f["version"] == nil || f["set_at"] == nil {
			t.Fatalf("%s: %v", name, f)
		}
	}
	if c := catalogueEntry(t, e.must(200, "GET", "/api/v1/catalogue", nil).JSON["connectors"], "rangka_pay"); c["state"] != "ready" {
		t.Fatalf("catalogue after the host write: %v", c)
	}
	// the runtime reads the host's value
	if v, ok, err := e.s.API.Secrets.Value(t.Context(), "kopi-senja", "rangka_pay", "shared_key"); err != nil || !ok || v != hostKey {
		t.Fatalf("runtime value: %v %v", ok, err)
	}
	a := e.must(200, "GET", "/api/v1/system/audit?kind=secret.set", nil)
	noLeak(t, "audit", a.Body, hostKey)
	if !strings.Contains(string(a.Body), `"by":"host"`) {
		t.Fatalf("audit secret.set: %s", a.Body)
	}
	host.must(204, "DELETE", path+"/brand_id", nil)
	if d := e.must(200, "GET", "/api/v1/system/audit?kind=secret.delete", nil); !strings.Contains(string(d.Body), `"by":"host"`) {
		t.Fatalf("audit secret.delete: %s", d.Body)
	}
	if c := catalogueEntry(t, e.must(200, "GET", "/api/v1/catalogue", nil).JSON["connectors"], "rangka_pay"); c["state"] != "needs_auth" {
		t.Fatalf("catalogue after the host delete: %v", c)
	}
	// secrets:host on an owner connector: 403
	if r := host.do("PUT", "/api/v1/connectors/xendit/auth/"+firstSecretField(t, e, "xendit"), map[string]any{"value": "x"}); r.Status != 403 {
		t.Fatalf("secrets:host on an owner connector: %d %s", r.Status, r.Body)
	}
	// the owner still writes an owner connector
	e.must(204, "PUT", "/api/v1/connectors/xendit/auth/"+firstSecretField(t, e, "xendit"), map[string]any{"value": "xnd_development_0063_secret_key"})
}

// secrets:host is minted only by an owner with a browser session.
func TestSecretsHostMint(t *testing.T) {
	e := hostEnv(t, "rangka_pay")
	tok := hostTokenEnv(e)
	if r := tok.do("POST", "/api/v1/tokens", map[string]any{"name": "x", "scopes": []string{"secrets:host"}}); r.Status != 403 {
		t.Fatalf("a token mints secrets:host: %d %s", r.Status, r.Body)
	}
	rep := repEnv(t, e)
	if r := rep.do("POST", "/api/v1/tokens", map[string]any{"name": "x", "scopes": []string{"secrets:host"}}); r.Status != 403 {
		t.Fatalf("a sales rep mints secrets:host: %d %s", r.Status, r.Body)
	}
}

// RANGKA_HOST_CONNECTORS forces the db driver: unset becomes db, any other driver is refused.
func TestHostedSecretsDriver(t *testing.T) {
	served := map[string]bool{"rangka_pay": true}
	if p, err := hostedSecretsDriver(secrets.Platform{}, served); err != nil || p.Driver != "db" {
		t.Fatalf("unset: %v %v", p, err)
	}
	if p, err := hostedSecretsDriver(secrets.Platform{Driver: "db"}, served); err != nil || p.Driver != "db" {
		t.Fatalf("db: %v %v", p, err)
	}
	for _, d := range []string{"env", "file", "vault"} {
		if _, err := hostedSecretsDriver(secrets.Platform{Driver: d}, served); err == nil {
			t.Fatalf("%s accepted on a hosted kernel", d)
		}
	}
	if p, err := hostedSecretsDriver(secrets.Platform{Driver: "env"}, nil); err != nil || p.Driver != "env" {
		t.Fatalf("self-hosted: %v %v", p, err)
	}
}

func fmtJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
