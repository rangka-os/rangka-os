package secrets

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// HashiCorp Vault (contract 0017 §2): the vault driver reads ${VAR} bindings from a KV secret at system.rk
// secrets.path (KV v2 "secret/data/rangka" or v1 "kv/rangka"), and Vault transit can hold the KEK of the db driver
// (RANGKA_MASTER_KEY_VAULT=<transit key name>), so the key never leaves Vault. The address and token come from the
// standard VAULT_ADDR and VAULT_TOKEN (VAULT_NAMESPACE for Vault Enterprise).

// Vault is a small Vault HTTP client.
type Vault struct {
	Addr, Token, Namespace string
	HTTP                   *http.Client

	mu    sync.Mutex
	kv    map[string]kvEntry // path → values, at most 5 minutes
	nowFn func() time.Time
}

type kvEntry struct {
	vals map[string]string
	at   time.Time
}

// ErrVaultConfig is a vault driver or transit KEK without VAULT_ADDR and VAULT_TOKEN.
var ErrVaultConfig = errors.New("vault: set VAULT_ADDR and VAULT_TOKEN")

// VaultFromEnv reads VAULT_ADDR, VAULT_TOKEN and VAULT_NAMESPACE; nil when VAULT_ADDR is unset.
func VaultFromEnv(getenv func(string) string) *Vault {
	if getenv == nil {
		getenv = os.Getenv
	}
	addr := strings.TrimRight(getenv("VAULT_ADDR"), "/")
	if addr == "" {
		return nil
	}
	return &Vault{Addr: addr, Token: getenv("VAULT_TOKEN"), Namespace: getenv("VAULT_NAMESPACE")}
}

func (v *Vault) client() *http.Client {
	if v.HTTP != nil {
		return v.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (v *Vault) now() time.Time {
	if v.nowFn != nil {
		return v.nowFn()
	}
	return time.Now()
}

func (v *Vault) do(ctx context.Context, method, path string, body any) (map[string]any, error) {
	if v == nil || v.Addr == "" || v.Token == "" {
		return nil, ErrVaultConfig
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, v.Addr+"/v1/"+strings.TrimLeft(path, "/"), rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Vault-Token", v.Token)
	if v.Namespace != "" {
		req.Header.Set("X-Vault-Namespace", v.Namespace)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := v.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("vault: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Vault's errors never echo secret values; the body is a list of messages
		return nil, fmt.Errorf("vault: %s %s: HTTP %d", method, path, resp.StatusCode)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("vault: %s %s: %w", method, path, err)
	}
	return out, nil
}

// Read returns one key of a KV secret (v2 data.data, else v1 data), cached for 5 minutes.
func (v *Vault) Read(ctx context.Context, path, key string) (string, bool, error) {
	v.mu.Lock()
	e, ok := v.kv[path]
	v.mu.Unlock()
	if !ok || v.now().Sub(e.at) >= cacheTTL {
		res, err := v.do(ctx, http.MethodGet, path, nil)
		if err != nil {
			return "", false, err
		}
		data, _ := res["data"].(map[string]any)
		if inner, ok := data["data"].(map[string]any); ok { // KV v2
			data = inner
		}
		e = kvEntry{vals: map[string]string{}, at: v.now()}
		for k, x := range data {
			if s, ok := x.(string); ok {
				e.vals[k] = s
			}
		}
		v.mu.Lock()
		if v.kv == nil {
			v.kv = map[string]kvEntry{}
		}
		v.kv[path] = e
		v.mu.Unlock()
	}
	val, ok := e.vals[key]
	return val, ok && val != "", nil
}

// transitKEK wraps data keys with Vault transit encrypt/decrypt; the key material stays in Vault.
type transitKEK struct {
	v    *Vault
	name string
}

func (t *transitKEK) wrap(ctx context.Context, org, dekID string, dek []byte) ([]byte, error) {
	res, err := t.v.do(ctx, http.MethodPost, "transit/encrypt/"+t.name, map[string]any{
		"plaintext": base64.StdEncoding.EncodeToString(dek), "context": base64.StdEncoding.EncodeToString(dekAAD(org, dekID))})
	if err != nil {
		return nil, err
	}
	ct, _ := mapStr(res, "data", "ciphertext")
	if ct == "" {
		return nil, errors.New("vault transit: no ciphertext")
	}
	return []byte(ct), nil
}

func (t *transitKEK) unwrap(ctx context.Context, org, dekID string, wrapped []byte) ([]byte, error) {
	res, err := t.v.do(ctx, http.MethodPost, "transit/decrypt/"+t.name, map[string]any{
		"ciphertext": string(wrapped), "context": base64.StdEncoding.EncodeToString(dekAAD(org, dekID))})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecrypt, err)
	}
	pt, _ := mapStr(res, "data", "plaintext")
	dek, err := base64.StdEncoding.DecodeString(pt)
	if err != nil || len(dek) != 32 {
		return nil, ErrDecrypt
	}
	return dek, nil
}

func mapStr(m map[string]any, path ...string) (string, bool) {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur = mm[p]
	}
	s, ok := cur.(string)
	return s, ok
}
