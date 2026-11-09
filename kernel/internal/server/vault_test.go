package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/secrets"
)

// fakeVault serves KV v2 reads and transit encrypt/decrypt. Transit "encrypts" with a reversible stand-in bound to
// the context, which is enough to show the kernel never holds the KEK and that the context binds a data key to its org.
type fakeVault struct {
	mu       sync.Mutex
	kv       map[string]string
	encrypts int
	tokenOK  string
}

func (f *fakeVault) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("X-Vault-Token") != f.tokenOK {
		w.WriteHeader(403)
		w.Write([]byte(`{"errors":["permission denied"]}`))
		return
	}
	var body map[string]string
	json.NewDecoder(r.Body).Decode(&body)
	switch {
	case r.Method == "GET" && r.URL.Path == "/v1/secret/data/rangka":
		data := map[string]any{}
		for k, v := range f.kv {
			data[k] = v
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"data": data, "metadata": map[string]any{"version": 3}}})
	case r.Method == "POST" && r.URL.Path == "/v1/transit/encrypt/rangka":
		f.encrypts++
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"ciphertext": "vault:v1:" + body["context"] + ":" + body["plaintext"]}})
	case r.Method == "POST" && r.URL.Path == "/v1/transit/decrypt/rangka":
		parts := strings.SplitN(strings.TrimPrefix(body["ciphertext"], "vault:v1:"), ":", 2)
		if len(parts) != 2 || parts[0] != body["context"] {
			w.WriteHeader(400)
			w.Write([]byte(`{"errors":["cipher: message authentication failed"]}`))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"plaintext": parts[1]}})
	default:
		w.WriteHeader(404)
	}
}

// The vault driver reads ${VAR} bindings from a KV secret: GET reports set, never the value; writes are 409.
func TestVaultDriver(t *testing.T) {
	fv := &fakeVault{tokenOK: "hvs.test-token", kv: map[string]string{"WA_ACCESS_TOKEN": waToken}}
	vs := httptest.NewServer(fv)
	defer vs.Close()
	t.Setenv("VAULT_ADDR", vs.URL)
	t.Setenv("VAULT_TOKEN", "hvs.test-token")
	e := secretsEnv(t, "vault, path: secret/data/rangka", "", `connector_auth:
  whatsapp_business:
    access_token: "${WA_ACCESS_TOKEN}"
    app_secret: "${WA_APP_SECRET_NOT_IN_VAULT}"
`, "")
	e.login()
	r := e.must(200, "GET", "/api/v1/connectors/whatsapp_business/auth", nil)
	noLeak(t, "GET auth (vault)", r.Body, waToken)
	if r.JSON["driver"] != "vault" || authField(t, r, "access_token")["set"] != true || authField(t, r, "app_secret")["set"] != false {
		t.Fatalf("vault status: %s", r.Body)
	}
	if v, ok, err := e.s.API.Secrets.Value(context.Background(), "kopi-senja", "whatsapp_business", "access_token"); err != nil || !ok || v != waToken {
		t.Fatalf("runtime value: %q %v %v", v, ok, err)
	}
	if r := e.do("PUT", "/api/v1/connectors/whatsapp_business/auth/access_token", map[string]any{"value": "x-long-enough-value"}); r.Status != 409 ||
		get(r.JSON, "error", "code") != "secrets_read_only_driver" {
		t.Fatalf("PUT on vault: %d %s", r.Status, r.Body)
	}
}

// RANGKA_MASTER_KEY_VAULT keeps the KEK in Vault transit: data keys are wrapped by Vault, rk_org_keys names the
// transit key, and a data key copied to another org does not decrypt.
func TestVaultTransitKEK(t *testing.T) {
	fv := &fakeVault{tokenOK: "hvs.test-token"}
	vs := httptest.NewServer(fv)
	defer vs.Close()
	t.Setenv("VAULT_ADDR", vs.URL)
	t.Setenv("VAULT_TOKEN", "hvs.test-token")
	t.Setenv("RANGKA_MASTER_KEY_VAULT", "rangka")
	e := secretsEnv(t, "db", "", "", "")
	e.login()
	e.must(204, "PUT", "/api/v1/connectors/whatsapp_business/auth/access_token", map[string]any{"value": waToken})
	ctx := context.Background()
	res, err := e.s.DB.Query(ctx, "select kek_id, wrapped from rk_org_keys where org_id = 'kopi-senja'")
	if err != nil || len(res.Rows) != 1 || res.Rows[0][0] != "vault-transit:rangka" || !strings.HasPrefix(string(res.Rows[0][1].([]byte)), "vault:v1:") {
		t.Fatalf("org key: %v %v", res, err)
	}
	if fv.encrypts != 1 {
		t.Fatalf("transit encrypts: %d", fv.encrypts)
	}
	fresh := &secrets.Service{DB: e.s.DB, Reg: e.s.Engine.Reg, KEK: e.s.API.Secrets.KEK} // nothing cached
	if v, ok, err := fresh.Value(ctx, "kopi-senja", "whatsapp_business", "access_token"); err != nil || !ok || v != waToken {
		t.Fatalf("value through transit: %q %v %v", v, ok, err)
	}
	// the wrapped data key copied to another org: Vault refuses it (the context names the org)
	if _, err := e.s.DB.Exec(ctx, `insert into rk_org_keys (org_id, dek_id, wrapped, nonce, kek_id) select 'pt-lain', dek_id, wrapped, nonce, kek_id from rk_org_keys where org_id = 'kopi-senja'`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.DB.Exec(ctx, `insert into rk_secrets (org_id, connector, field, version, ciphertext, nonce, dek_id, hint, set_by)
		select 'pt-lain', connector, field, version, ciphertext, nonce, dek_id, hint, set_by from rk_secrets where org_id = 'kopi-senja'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fresh.Value(ctx, "pt-lain", "whatsapp_business", "access_token"); err == nil {
		t.Fatal("a data key copied to another org decrypted")
	}
}

// A token with secrets:write may set secrets (Rangka Cloud provisioning); only a person who may manage secrets mints
// one; an unknown scope is refused; a plain token still may not write.
func TestSecretsWriteScope(t *testing.T) {
	e := secretsEnv(t, "db", testKEK(t), "", "")
	e.login()
	const path = "/api/v1/connectors/whatsapp_business/auth/access_token"
	tok := e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "cloud", "scopes": []any{"secrets:write"}})
	if get(tok.JSON, "scopes", 0) != "secrets:write" {
		t.Fatalf("token: %s", tok.Body)
	}
	scoped := &env{t: t, ts: e.ts, token: tok.JSON["token"].(string)}
	scoped.must(204, "PUT", path, map[string]any{"value": waToken})
	scoped.must(204, "DELETE", path, nil)
	plain := &env{t: t, ts: e.ts, token: e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "ci"}).JSON["token"].(string)}
	if r := plain.do("PUT", path, map[string]any{"value": waToken}); r.Status != 403 {
		t.Fatalf("plain token: %d", r.Status)
	}
	if r := e.do("POST", "/api/v1/tokens", map[string]any{"name": "x", "scopes": []any{"admin:all"}}); r.Status != 400 {
		t.Fatalf("unknown scope: %d %s", r.Status, r.Body)
	}
	rep := repEnv(t, e)
	if r := rep.do("POST", "/api/v1/tokens", map[string]any{"name": "x", "scopes": []any{"secrets:write"}}); r.Status != 403 {
		t.Fatalf("sales_rep minting secrets:write: %d %s", r.Status, r.Body)
	}
	// a scoped token cannot mint another scoped token: minting needs a person
	if r := scoped.do("POST", "/api/v1/tokens", map[string]any{"name": "y", "scopes": []any{"secrets:write"}}); r.Status != 403 {
		t.Fatalf("token minting secrets:write: %d %s", r.Status, r.Body)
	}
	if !strings.Contains(string(e.must(200, "GET", "/api/v1/system/audit?kind=auth.token_created", nil).Body), "secrets:write") {
		t.Fatal("the token's scope is not audited")
	}
}
