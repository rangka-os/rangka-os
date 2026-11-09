package server

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/secrets"
)

// Contract 0017: connector secrets. The API is write-only: no answer, audit entry or database row holds a value in
// the clear. Only the org's owner (or a role in config.rk connector_auth) reaches it; the assistant never does.

const (
	waToken  = "EAAG-test-access-token-0017-abcd" // 32 characters: the hint is "abcd"
	waSecret = "app-secret-0017-zyxw-long-enough"
	waVerify = "verify-0017"
)

// secretsEnv starts Kopi Senja with system.rk secrets.driver set to driver; kek is RANGKA_MASTER_KEY ("" = none).
func secretsEnv(t *testing.T, driver, kek string, extraSystem, extraConfig string) *env {
	t.Helper()
	t.Setenv("RANGKA_MASTER_KEY", kek)
	t.Setenv("RANGKA_MASTER_KEY_FILE", "")
	return newEnv(t, false, func(apps, site string) {
		p := filepath.Join(site, "system.rk")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Replace(string(b), "secrets: {driver: env}", "secrets: {driver: "+driver+"}", 1) + extraSystem
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if extraConfig != "" {
			p = filepath.Join(site, "config.rk")
			b, _ = os.ReadFile(p)
			if err := os.WriteFile(p, append(b, []byte(extraConfig)...), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func testKEK(t *testing.T) string {
	t.Helper()
	k, err := secrets.NewKEKText()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func authField(t *testing.T, r *resp, name string) map[string]any {
	t.Helper()
	for _, x := range r.JSON["fields"].([]any) {
		if f := x.(map[string]any); f["field"] == name {
			return f
		}
	}
	t.Fatalf("no field %s in %s", name, r.Body)
	return nil
}

// noLeak fails when any of the values shows up in body.
func noLeak(t *testing.T, where string, body []byte, values ...string) {
	t.Helper()
	for _, v := range values {
		for _, form := range []string{v, base64.StdEncoding.EncodeToString([]byte(v)), url.QueryEscape(v)} {
			if strings.Contains(string(body), form) {
				t.Fatalf("%s holds a secret value: %s", where, body)
			}
		}
	}
}

func TestConnectorAuthDBDriver(t *testing.T) {
	kekText := testKEK(t)
	e := secretsEnv(t, "db", kekText, "", "")
	e.login()
	const path = "/api/v1/connectors/whatsapp_business/auth"

	r := e.must(200, "GET", path, nil)
	if r.JSON["driver"] != "db" || r.JSON["writable"] != true || authField(t, r, "access_token")["set"] != false {
		t.Fatalf("before: %s", r.Body)
	}
	wa := catalogueEntry(t, e.must(200, "GET", "/api/v1/catalogue", nil).JSON["connectors"], "whatsapp_business")
	if wa["state"] != "needs_auth" {
		t.Fatalf("catalogue before: %v", wa)
	}

	for field, v := range map[string]string{"access_token": waToken, "app_secret": waSecret, "verify_token": waVerify} {
		if r := e.do("PUT", path+"/"+field, map[string]any{"value": v}); r.Status != 204 || len(r.Body) != 0 {
			t.Fatalf("PUT %s: %d %s", field, r.Status, r.Body)
		}
	}
	// one required field still missing: still needs auth
	wa = catalogueEntry(t, e.must(200, "GET", "/api/v1/catalogue", nil).JSON["connectors"], "whatsapp_business")
	if wa["state"] != "needs_auth" || get(wa, "auth", 0, "set") != true {
		t.Fatalf("catalogue with three fields: %v", wa)
	}
	e.must(204, "PUT", path+"/phone_number_id", map[string]any{"value": "1098765432"})

	r = e.must(200, "GET", path, nil)
	at := authField(t, r, "access_token")
	if at["set"] != true || at["secret"] != true || at["hint"] != "abcd" || at["version"] != float64(1) || at["set_by"] != "ceo@saga.co.id" || at["set_at"] == nil {
		t.Fatalf("access_token: %v", at)
	}
	if _, ok := at["value"]; ok {
		t.Fatalf("a secret field carries a value: %v", at)
	}
	if v := authField(t, r, "verify_token"); v["set"] != true || v["hint"] != nil { // short values get no hint
		t.Fatalf("verify_token: %v", v)
	}
	if p := authField(t, r, "phone_number_id"); p["value"] != "1098765432" || p["secret"] != false { // not a credential
		t.Fatalf("phone_number_id: %v", p)
	}
	noLeak(t, "GET auth", r.Body, waToken, waSecret, waVerify)

	// every required field set: the installed connector is ready
	cat := e.must(200, "GET", "/api/v1/catalogue", nil)
	noLeak(t, "catalogue", cat.Body, waToken, waSecret, waVerify)
	if wa = catalogueEntry(t, cat.JSON["connectors"], "whatsapp_business"); wa["state"] != "ready" {
		t.Fatalf("catalogue after: %v", wa)
	}
	noLeak(t, "meta", e.must(200, "GET", "/api/v1/meta", nil).Body, waToken, waSecret, waVerify)
	noLeak(t, "status", e.must(200, "GET", "/api/v1/system/status", nil).Body, waToken, waSecret, waVerify)
	audit := e.must(200, "GET", "/api/v1/system/audit?kind=secret.", nil)
	noLeak(t, "audit", audit.Body, waToken, waSecret, waVerify)
	if !strings.Contains(string(audit.Body), "secret.set") || !strings.Contains(string(audit.Body), `"hint":"abcd"`) {
		t.Fatalf("audit: %s", audit.Body)
	}

	// the database holds ciphertext only
	ctx := context.Background()
	for _, q := range []string{"select coalesce(string_agg(ciphertext::text || hint, ','), '') from rk_secrets",
		"select coalesce(string_agg(coalesce(data::text, '') || summary, ','), '') from rk_audit",
		"select coalesce(string_agg(wrapped::text, ','), '') from rk_org_keys"} {
		res, err := e.s.DB.Query(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		noLeak(t, q, []byte(res.Rows[0][0].(string)), waToken, waSecret, waVerify)
	}

	if res, err := e.s.DB.Query(ctx, "select ciphertext from rk_secrets"); err != nil || len(res.Rows) == 0 {
		t.Fatal(err)
	} else {
		for _, row := range res.Rows {
			noLeak(t, "rk_secrets.ciphertext", row[0].([]byte), waToken, waSecret, waVerify)
		}
	}

	// the runtime reads the newest value; rotation keeps the previous one for inbound checks until retire_at
	svc := e.s.API.Secrets
	if v, ok, err := svc.Value(ctx, "kopi-senja", "whatsapp_business", "access_token"); err != nil || !ok || v != waToken {
		t.Fatalf("Value: %q %v %v", v, ok, err)
	}
	e.must(204, "PUT", path+"/app_secret", map[string]any{"value": waSecret + "-v2"})
	if acc, err := svc.Accepted(ctx, "kopi-senja", "whatsapp_business", "app_secret"); err != nil || len(acc) != 2 || acc[0] != waSecret+"-v2" || acc[1] != waSecret {
		t.Fatalf("Accepted after rotation: %v %v", acc, err)
	}
	if v, _, _ := svc.Value(ctx, "kopi-senja", "whatsapp_business", "app_secret"); v != waSecret+"-v2" {
		t.Fatalf("Value after rotation: %q", v)
	}
	if f := authField(t, e.must(200, "GET", path, nil), "app_secret"); f["version"] != float64(2) {
		t.Fatalf("version after rotation: %v", f)
	}

	// a row copied to another field or org does not decrypt (a fresh service: nothing cached)
	kek, _ := secrets.ParseKEK(kekText)
	fresh := &secrets.Service{DB: e.s.DB, Reg: e.s.Engine.Reg, KEK: kek}
	if _, err := e.s.DB.Exec(ctx, `insert into rk_secrets (org_id, connector, field, version, ciphertext, nonce, dek_id, hint, set_by)
		select org_id, connector, 'phone_number_id', 99, ciphertext, nonce, dek_id, '', 'test' from rk_secrets
		where org_id = 'kopi-senja' and field = 'access_token'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fresh.Value(ctx, "kopi-senja", "whatsapp_business", "phone_number_id"); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("copied to another field: %v", err)
	}
	if err := auth.EnsureOrg(ctx, e.s.DB, "pt-lain", "PT Lain"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Set(ctx, "pt-lain", "whatsapp_business", "access_token", "other-org-token-value", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.DB.Exec(ctx, `update rk_secrets b set ciphertext = a.ciphertext, nonce = a.nonce
		from rk_secrets a where a.org_id = 'kopi-senja' and a.field = 'access_token' and b.org_id = 'pt-lain' and b.field = 'access_token'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fresh.Value(ctx, "pt-lain", "whatsapp_business", "access_token"); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("copied to another org: %v", err)
	}

	// delete: unset again, audited, and the connector needs auth
	e.must(204, "DELETE", path+"/access_token", nil)
	if f := authField(t, e.must(200, "GET", path, nil), "access_token"); f["set"] != false {
		t.Fatalf("after delete: %v", f)
	}
	if _, ok, _ := svc.Value(ctx, "kopi-senja", "whatsapp_business", "access_token"); ok {
		t.Fatal("value still readable after delete")
	}
	if wa = catalogueEntry(t, e.must(200, "GET", "/api/v1/catalogue", nil).JSON["connectors"], "whatsapp_business"); wa["state"] != "needs_auth" {
		t.Fatalf("catalogue after delete: %v", wa)
	}
	if !strings.Contains(string(e.must(200, "GET", "/api/v1/system/audit?kind=secret.delete", nil).Body), "access_token") {
		t.Fatal("no secret.delete audit entry")
	}

	// unknown connector or field, empty value
	e.must(404, "GET", "/api/v1/connectors/nope/auth", nil)
	if r := e.do("PUT", path+"/nope", map[string]any{"value": "x"}); r.Status != 404 || get(r.JSON, "error", "code") != "unknown_field" {
		t.Fatalf("unknown field: %d %s", r.Status, r.Body)
	}
	if r := e.do("PUT", path+"/access_token", map[string]any{"value": ""}); r.Status != 400 {
		t.Fatalf("empty value: %d %s", r.Status, r.Body)
	}
}

// Only the owner, or a role in config.rk connector_auth, reaches the routes. A sales_rep, the assistant and OIDC
// helpers get 403; an API token may read but not write.
func TestConnectorAuthWho(t *testing.T) {
	e := secretsEnv(t, "db", testKEK(t), "", "connector_auth: [sales_manager]\n")
	e.login()
	const path = "/api/v1/connectors/whatsapp_business/auth"
	rep := repEnv(t, e)
	for _, m := range []string{"GET", "PUT", "DELETE"} {
		p := path
		if m != "GET" {
			p += "/access_token"
		}
		if r := rep.do(m, p, map[string]any{"value": waToken}); r.Status != 403 || get(r.JSON, "error", "code") != "forbidden" {
			t.Fatalf("sales_rep %s: %d %s", m, r.Status, r.Body)
		}
	}
	ai := assistantEnv(t, e, 10*time.Minute)
	for _, m := range []string{"GET", "PUT", "DELETE"} {
		p := path
		if m != "GET" {
			p += "/access_token"
		}
		if r := ai.do(m, p, map[string]any{"value": waToken}); r.Status != 403 || get(r.JSON, "error", "code") != "ai_forbidden" {
			t.Fatalf("assistant %s: %d %s", m, r.Status, r.Body)
		}
	}
	tok := e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "cli"}).JSON["token"].(string)
	api := &env{t: t, ts: e.ts, token: tok}
	api.must(200, "GET", path, nil)
	if r := api.do("PUT", path+"/access_token", map[string]any{"value": waToken}); r.Status != 403 {
		t.Fatalf("api token PUT: %d %s", r.Status, r.Body)
	}
	// a role granted connector_auth may write
	if _, err := auth.EnsureUser(context.Background(), e.s.DB, "mgr@example.test", "Mgr", "rangka-demo", "kopi-senja", "sales_manager", "USR-001"); err != nil {
		t.Fatal(err)
	}
	mgr := &env{t: t, ts: e.ts, jar: newJar()}
	mgr.loginAs("mgr@example.test")
	mgr.must(204, "PUT", path+"/access_token", map[string]any{"value": waToken})
	if f := authField(t, e.must(200, "GET", path, nil), "access_token"); f["set"] != true || f["set_by"] != "mgr@example.test" {
		t.Fatalf("set by manager: %v", f)
	}
	if n := e.must(200, "GET", "/api/v1/system/audit?kind=secret.set", nil); !strings.Contains(string(n.Body), "mgr@example.test") {
		t.Fatalf("audit actor: %s", n.Body)
	}
}

// env, file and vault are read only: writes answer 409, and GET reports set from system.rk connector_auth.
func TestConnectorAuthReadOnlyDriver(t *testing.T) {
	t.Setenv("WA_TEST_ACCESS_TOKEN", waToken)
	e := secretsEnv(t, "env", "", `connector_auth:
  whatsapp_business:
    access_token: "${WA_TEST_ACCESS_TOKEN}"
    app_secret: "${WA_TEST_APP_SECRET_UNSET}"
    phone_number_id: "1098765432"
`, "")
	e.login()
	const path = "/api/v1/connectors/whatsapp_business/auth"
	for _, m := range []string{"PUT", "DELETE"} {
		if r := e.do(m, path+"/access_token", map[string]any{"value": waToken}); r.Status != 409 || get(r.JSON, "error", "code") != "secrets_read_only_driver" {
			t.Fatalf("%s on env: %d %s", m, r.Status, r.Body)
		}
	}
	r := e.must(200, "GET", path, nil)
	noLeak(t, "GET auth (env)", r.Body, waToken)
	if r.JSON["driver"] != "env" || r.JSON["writable"] != false {
		t.Fatalf("driver: %s", r.Body)
	}
	if f := authField(t, r, "access_token"); f["set"] != true || f["value"] != nil {
		t.Fatalf("access_token (env): %v", f)
	}
	if f := authField(t, r, "app_secret"); f["set"] != false {
		t.Fatalf("app_secret with an unset variable: %v", f)
	}
	if f := authField(t, r, "phone_number_id"); f["set"] != true || f["value"] != "1098765432" {
		t.Fatalf("phone_number_id literal: %v", f)
	}
	noLeak(t, "files system.rk", e.must(200, "GET", "/api/v1/files/system.rk", nil).Body, waToken)
	wa := catalogueEntry(t, e.must(200, "GET", "/api/v1/catalogue", nil).JSON["connectors"], "whatsapp_business")
	if wa["state"] != "needs_auth" || get(wa, "auth", 0, "set") != true {
		t.Fatalf("catalogue (env): %v", wa)
	}
}

// The db driver without a key-encryption key starts, and refuses writes.
func TestConnectorAuthKeyMissing(t *testing.T) {
	e := secretsEnv(t, "db", "", "", "")
	e.login()
	r := e.do("PUT", "/api/v1/connectors/whatsapp_business/auth/access_token", map[string]any{"value": waToken})
	if r.Status != 409 || get(r.JSON, "error", "code") != "secrets_key_missing" {
		t.Fatalf("no KEK: %d %s", r.Status, r.Body)
	}
	e.must(200, "GET", "/api/v1/connectors/whatsapp_business/auth", nil)
}

// rangka secrets rewrap moves the data keys to a new KEK; the values still read with the new key only.
func TestSecretsRewrap(t *testing.T) {
	oldText := testKEK(t)
	e := secretsEnv(t, "db", oldText, "", "")
	e.login()
	e.must(204, "PUT", "/api/v1/connectors/whatsapp_business/auth/access_token", map[string]any{"value": waToken})
	ctx := context.Background()
	old, _ := secrets.ParseKEK(oldText)
	next, _ := secrets.ParseKEK(testKEK(t))
	if n, err := secrets.Rewrap(ctx, e.s.DB, old, next); err != nil || n != 1 {
		t.Fatalf("rewrap: %d %v", n, err)
	}
	svc := &secrets.Service{DB: e.s.DB, Reg: e.s.Engine.Reg, KEK: next}
	if v, ok, err := svc.Value(ctx, "kopi-senja", "whatsapp_business", "access_token"); err != nil || !ok || v != waToken {
		t.Fatalf("after rewrap: %q %v %v", v, ok, err)
	}
	stale := &secrets.Service{DB: e.s.DB, Reg: e.s.Engine.Reg, KEK: old}
	if _, _, err := stale.Value(ctx, "kopi-senja", "whatsapp_business", "access_token"); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("old key after rewrap: %v", err)
	}
}
