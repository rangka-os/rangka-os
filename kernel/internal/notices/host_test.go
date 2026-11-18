package notices

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestHostFromEnv(t *testing.T) {
	lookupIP = func(_ context.Context, host string) ([]net.IP, error) {
		switch host {
		case "cloud.internal":
			return []net.IP{net.ParseIP("10.0.0.7")}, nil
		case "example.com":
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		}
		return nil, errors.New("no such host")
	}
	t.Cleanup(func() {
		lookupIP = func(ctx context.Context, h string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip", h)
		}
	})
	if h, err := HostFromEnv(env(nil)); h != nil || err != nil {
		t.Fatalf("unset: %v %v", h, err)
	}
	for _, c := range []struct {
		url, secret string
		ok          bool
	}{
		{"https://cloud.example/api/host/v1/notices", "rkn1.x.y", true},
		{"http://127.0.0.1:8080/api/host/v1/notices", "s", true},
		{"http://localhost/n", "s", true},
		{"http://10.1.2.3/n", "s", true},
		{"http://cloud.internal/n", "s", true},
		{"http://example.com/n", "s", false},
		{"ftp://cloud.example/n", "s", false},
		{"https://user:pw@cloud.example/n", "s", false},
		{"https://cloud.example/n?x=1", "s", false},
		{"https://cloud.example/n", "", false},
		{"", "s", false},
	} {
		h, err := HostFromEnv(env(map[string]string{EnvHostURL: c.url, EnvHostSecret: c.secret}))
		if (err == nil) != c.ok || (c.ok && h == nil) {
			t.Errorf("%q: %v %v", c.url, h, err)
		}
		if err != nil && c.secret != "" && strings.Contains(err.Error(), c.secret+"x") {
			t.Errorf("the secret is in the error")
		}
	}
}

func TestPostOutcomes(t *testing.T) {
	var got http.Header
	var body map[string]any
	status, answer, retryAfter := 202, `{"status":"accepted"}`, ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		json.NewDecoder(r.Body).Decode(&body)
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(status)
		w.Write([]byte(answer))
	}))
	defer srv.Close()
	h, err := HostFromEnv(env(map[string]string{EnvHostURL: srv.URL + "/api/host/v1/notices", EnvHostSecret: "rkn1.abc.def"}))
	if err != nil {
		t.Fatal(err)
	}
	ev := map[string]any{"id": "kernel.approval.requested:apr_1", "org": "toko-budi", "kind": "kernel.approval.requested"}
	o := h.Post(context.Background(), ev)
	if !o.Done || o.Duplicate || got.Get("Authorization") != "Bearer rkn1.abc.def" || got.Get("Content-Type") != "application/json" || body["id"] != ev["id"] {
		t.Fatalf("accepted: %+v %v %v", o, got, body)
	}
	status, answer = 200, `{"status":"duplicate"}`
	if o := h.Post(context.Background(), ev); !o.Done || !o.Duplicate {
		t.Fatalf("duplicate: %+v", o)
	}
	status, answer, retryAfter = 429, `{"error":{"code":"rate_limited"}}`, "7"
	if o := h.Post(context.Background(), ev); o.Done || !o.Retry || o.RetryAfter != 7*time.Second || o.Code != "rate_limited" {
		t.Fatalf("429: %+v", o)
	}
	retryAfter = ""
	for _, c := range []struct {
		status int
		answer string
		retry  bool
		code   string
	}{
		{500, "", true, "internal_server_error"}, {503, "", true, "service_unavailable"},
		{400, `{"error":{"code":"bad_notice","field":"title"}}`, false, "bad_notice"}, {401, `{"error":{"code":"unauthenticated"}}`, false, "unauthenticated"},
		{410, `{"error":{"code":"business_gone"}}`, false, "business_gone"}, {413, `{"error":{"code":"too_large"}}`, false, "too_large"},
		{302, "", false, "found"},
	} {
		status, answer = c.status, c.answer
		if o := h.Post(context.Background(), ev); o.Done || o.Retry != c.retry || o.Code != c.code || o.Status != c.status {
			t.Errorf("%d: %+v", c.status, o)
		}
	}
	srv.Close()
	if o := h.Post(context.Background(), ev); !o.Retry || o.Code != "network" || strings.Contains(o.Err.Error(), "rkn1.abc.def") {
		t.Fatalf("no connection: %+v", o)
	}
	if o := h.Post(context.Background(), map[string]any{"data": strings.Repeat("x", MaxBody)}); o.Retry || o.Code != "too_large" {
		t.Fatalf("over 16 KB is never sent: %+v", o)
	}
}

func TestBackoff(t *testing.T) {
	want := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, 6 * time.Hour, 6 * time.Hour, 6 * time.Hour}
	for i, w := range want {
		if b := Backoff(i + 1); b != w {
			t.Errorf("attempt %d: %v, want %v", i+1, b, w)
		}
	}
}
