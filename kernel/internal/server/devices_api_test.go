package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// Paired devices, device codes and pairing (contract 0057 §2).

// device is a second browser on the same kernel: its own cookie jar, no session.
func (e *env) device() *env {
	jar, _ := cookiejar.New(nil)
	return &env{t: e.t, s: e.s, ts: e.ts, apps: e.apps, site: e.site, jar: jar}
}

// pairingCode creates a pairing code for outlet as the signed-in caller of e.
func (e *env) pairingCode(outlet string) string {
	e.t.Helper()
	r := e.must(201, "POST", "/api/v1/devices/codes", map[string]any{"outlet": outlet})
	code, _ := r.JSON["code"].(string)
	if len(code) != 8 || strings.Trim(code, "ABCDEFGHJKMNPQRSTUVWXYZ23456789") != "" {
		e.t.Fatalf("pairing code %q", code)
	}
	if exp, _ := r.JSON["expires_at"].(string); exp == "" {
		e.t.Fatalf("no expires_at: %s", r.Body)
	}
	return code
}

func cookieOf(r *resp, name string) *http.Cookie {
	for _, c := range (&http.Response{Header: r.Header}).Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// pair pairs a fresh browser with a new code for outlet and returns the device client and the answer.
func (e *env) pair(outlet, name string) (*env, *resp) {
	e.t.Helper()
	code := e.pairingCode(outlet)
	d := e.device()
	return d, d.must(201, "POST", "/auth/device/pair", map[string]any{"code": code, "name": name})
}

func TestPairBrowserDevice(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	_, r := e.pair("OUT-01", "Kasir depan")
	if r.JSON["code"] != "K1" || r.JSON["outlet"] != "OUT-01" {
		t.Fatalf("pair = %s", r.Body)
	}
	if id, _ := r.JSON["device_id"].(string); len(id) != 24 || !strings.HasPrefix(id, "dev_") {
		t.Fatalf("device_id %q", id)
	}
	c := cookieOf(r, "rk_device")
	if c == nil || c.Value == "" || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.MaxAge != 400*24*3600 {
		t.Fatalf("rk_device cookie = %+v", c)
	}
	list := e.must(200, "GET", "/api/v1/devices?outlet=OUT-01", nil)
	items, _ := list.JSON["items"].([]any)
	if len(items) != 1 || get(items[0], "code") != "K1" || get(items[0], "name") != "Kasir depan" || get(items[0], "kind") != "browser" {
		t.Fatalf("devices = %s", list.Body)
	}
	if strings.Contains(string(list.Body), "token") {
		t.Fatalf("device list leaks a token field: %s", list.Body)
	}
}

func TestDeviceCodesPerOutlet(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	_, r1 := e.pair("OUT-01", "Kasir 1")
	_, r2 := e.pair("OUT-01", "Kasir 2")
	_, r3 := e.pair("OUT-02", "Kasir Bintaro")
	if r1.JSON["code"] != "K1" || r2.JSON["code"] != "K2" || r3.JSON["code"] != "K1" {
		t.Fatalf("codes %v %v %v", r1.JSON["code"], r2.JSON["code"], r3.JSON["code"])
	}
	e.must(204, "DELETE", "/api/v1/devices/"+r2.JSON["device_id"].(string), nil)
	_, r4 := e.pair("OUT-01", "Kasir 3")
	if r4.JSON["code"] != "K3" {
		t.Fatalf("after revoking K2 the next code is %v, want K3", r4.JSON["code"])
	}
	list := e.must(200, "GET", "/api/v1/devices?outlet=OUT-01", nil)
	items, _ := list.JSON["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("devices of OUT-01 = %s", list.Body)
	}
	for _, it := range items {
		if get(it, "code") == "K2" && get(it, "revoked_at") == nil {
			t.Fatalf("K2 not revoked: %s", list.Body)
		}
	}
	e.must(404, "DELETE", "/api/v1/devices/dev_00000000000000000000", nil)
}

func TestDeviceCodesConcurrent(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	codes := []string{e.pairingCode("OUT-01"), e.pairingCode("OUT-01")}
	got := make([]string, 2)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dev, _, err := e.s.Auth.PairDevice(context.Background(), codes[i], "Kasir", "browser")
			if err != nil {
				t.Error(err)
				return
			}
			got[i] = dev.Code
		}(i)
	}
	wg.Wait()
	if got[0] == got[1] || got[0] == "" || got[1] == "" {
		t.Fatalf("parallel pairings got codes %v", got)
	}
}

func TestPairingCodeSingleUse(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	code := e.pairingCode("OUT-01")
	d := e.device()
	d.must(201, "POST", "/auth/device/pair", map[string]any{"code": code, "name": "Kasir"})
	used := d.must(400, "POST", "/auth/device/pair", map[string]any{"code": code, "name": "Kasir"})
	unknown := d.must(400, "POST", "/auth/device/pair", map[string]any{"code": "ZZZZ2222", "name": "Kasir"})
	late := e.pairingCode("OUT-01")
	e.s.Auth.Now = func() time.Time { return fixedNow().Add(16 * time.Minute) }
	defer func() { e.s.Auth.Now = fixedNow }()
	expired := d.must(400, "POST", "/auth/device/pair", map[string]any{"code": late, "name": "Kasir"})
	for _, r := range []*resp{used, unknown, expired} {
		if get(r.JSON, "error", "code") != "pairing_code_invalid" {
			t.Fatalf("error = %s", r.Body)
		}
		if get(r.JSON, "error", "message") != get(used.JSON, "error", "message") || get(used.JSON, "error", "message") == "" {
			t.Fatalf("messages differ: %s vs %s", r.Body, used.Body)
		}
	}
}

func TestPairingRateLimit(t *testing.T) {
	e := newEnv(t, false, nil)
	d := e.device()
	for i := 0; i < 10; i++ {
		d.must(400, "POST", "/auth/device/pair", map[string]any{"code": "ZZZZ2222", "name": "Kasir"})
	}
	r := d.must(429, "POST", "/auth/device/pair", map[string]any{"code": "ZZZZ2222", "name": "Kasir"})
	if get(r.JSON, "error", "code") != "too_many_attempts" {
		t.Fatalf("11th try = %s", r.Body)
	}
	// edge registration shares the address's tries
	r = d.must(429, "POST", "/api/v1/edge/register", map[string]any{"pairing_code": "ZZZZ2222", "device": map[string]any{"name": "Edge"}})
	if get(r.JSON, "error", "code") != "too_many_attempts" {
		t.Fatalf("edge register = %s", r.Body)
	}
}

func TestDeviceAloneReadsNothing(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	d, _ := e.pair("OUT-01", "Kasir")
	d.must(401, "GET", "/api/v1/r/item", nil)
	d.must(401, "GET", "/auth/me", nil)
	d.must(401, "POST", "/api/v1/devices/codes", map[string]any{"outlet": "OUT-01"})
	// a person signed in on the paired browser is still that person
	d.must(200, "POST", "/auth/login", map[string]any{"email": "ceo@saga.co.id", "password": "rangka-demo"})
	d.must(200, "GET", "/api/v1/r/item", nil)
}

func TestEdgeRegisterToken(t *testing.T) {
	e := newEnv(t, false, nil)
	e.login()
	code := e.pairingCode("OUT-02")
	d := e.device()
	r := d.must(201, "POST", "/api/v1/edge/register", map[string]any{"pairing_code": code,
		"device": map[string]any{"name": "Kasir 1", "platform": "windows/amd64", "version": "0.1.0"}})
	tok, _ := r.JSON["token"].(string)
	if !strings.HasPrefix(tok, "rke_") || r.JSON["code"] != "K1" || r.JSON["outlet"] != "OUT-02" || r.JSON["org"] != "kopi-senja" {
		t.Fatalf("register = %s", r.Body)
	}
	if get(r.JSON, "sync", "push_max_items") != float64(100) || get(r.JSON, "sync", "pull_interval_s") != float64(60) {
		t.Fatalf("sync = %s", r.Body)
	}
	if cookieOf(r, "rk_device") != nil {
		t.Fatal("edge registration must not set the browser cookie")
	}
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/v1/r/item", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	p, err := e.s.Auth.Resolve(req)
	if err != nil || p == nil || p.Via != "device" || p.Device == nil || p.Device.Kind != "edge" || p.User != nil || p.Human() {
		t.Fatalf("resolve = %+v, %v", p, err)
	}
	if p.Device.ID != r.JSON["device_id"] || p.Device.Org != "kopi-senja" {
		t.Fatalf("device = %+v", p.Device)
	}
	edge := &env{t: t, s: e.s, ts: e.ts, jar: d.jar, token: tok}
	edge.must(401, "GET", "/api/v1/r/item", nil)
	// a revoked token answers device_revoked
	e.must(204, "DELETE", "/api/v1/devices/"+p.Device.ID, nil)
	if _, err := e.s.Auth.Resolve(req); err != auth.ErrDeviceRevoked {
		t.Fatalf("revoked token resolves: %v", err)
	}
	if r := edge.must(401, "GET", "/api/v1/r/item", nil); get(r.JSON, "error", "code") != "device_revoked" {
		t.Fatalf("revoked = %s", r.Body)
	}
}

func TestDeviceCodesWho(t *testing.T) {
	e := newEnv(t, false, nil)
	ctx := context.Background()
	for _, u := range [][]string{{"ho@kopisenja.id", "head_office"}, {"rep@kopisenja.id", "sales_rep"}} {
		if _, err := auth.EnsureUser(ctx, e.s.DB, u[0], "X", "rahasia-123", "kopi-senja", u[1], ""); err != nil {
			t.Fatal(err)
		}
	}
	ho := e.device()
	ho.must(200, "POST", "/auth/login", map[string]any{"email": "ho@kopisenja.id", "password": "rahasia-123"})
	ho.pairingCode("OUT-01")
	ho.must(200, "GET", "/api/v1/devices", nil)
	rep := e.device()
	rep.must(200, "POST", "/auth/login", map[string]any{"email": "rep@kopisenja.id", "password": "rahasia-123"})
	rep.must(403, "POST", "/api/v1/devices/codes", map[string]any{"outlet": "OUT-01"})
	rep.must(403, "GET", "/api/v1/devices", nil)
	e.login()
	if r := e.must(422, "POST", "/api/v1/devices/codes", map[string]any{"outlet": "OUT-99"}); get(r.JSON, "error", "code") != "unknown_outlet" {
		t.Fatalf("unknown outlet = %s", r.Body)
	}
}
