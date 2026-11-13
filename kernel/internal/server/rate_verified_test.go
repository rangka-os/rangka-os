package server

import (
	"io"
	"net/http"
	"testing"
	"time"
)

// anon sends one request from the test client with the given headers and no cookie jar.
func anon(e *env, path string, hdr map[string]string) int {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.ts.URL+path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res.StatusCode
}

func count429(e *env, n int, hdr func(i int) map[string]string) int {
	e.t.Helper()
	refused := 0
	for i := 0; i < n; i++ {
		if anon(e, "/api/v1/meta", hdr(i)) == 429 {
			refused++
		}
	}
	return refused
}

// Audit F-3 (contract 0040 §9): a made-up Authorization header takes its client's bucket, not the tenant's, so a
// stranger cannot spend the tenant's rate and get the signed-in owner a 429.
func TestRateJunkCredentialsTakeTheClientBucket(t *testing.T) {
	e := rateEnv(t, ", rate: {rps: 2, burst: 2, max_wait_ms: 0, max_waiting: 0}")
	time.Sleep(1100 * time.Millisecond)
	junk := count429(e, 4, func(i int) map[string]string { return map[string]string{"Authorization": "Bearer junk"} })
	junk += count429(e, 2, func(i int) map[string]string { return map[string]string{"Authorization": "Bearer rk_junk"} })
	junk += count429(e, 2, func(i int) map[string]string { return map[string]string{"Cookie": "rk_session=junk"} })
	if junk < 4 {
		t.Fatalf("junk credentials were not limited by their client's bucket: %d refused of 8", junk)
	}
	// the owner (session cookie issued at sign-in) still has the tenant's whole burst
	e.must(200, "GET", "/api/v1/meta", nil)
	e.must(200, "GET", "/api/v1/meta", nil)
	e.must(429, "GET", "/api/v1/meta", nil)
	// an API token is verified by its first use (which takes its client's bucket), then shares the tenant's
	time.Sleep(1100 * time.Millisecond)
	tok := e.must(201, "POST", "/api/v1/tokens", map[string]any{"name": "rate"}).JSON["token"].(string)
	time.Sleep(1100 * time.Millisecond)
	if st := anon(e, "/api/v1/meta", map[string]string{"Authorization": "Bearer " + tok}); st != 200 {
		t.Fatalf("token first use %d", st)
	}
	e.must(200, "GET", "/api/v1/meta", nil)
	e.must(200, "GET", "/api/v1/meta", nil)
	if st := anon(e, "/api/v1/meta", map[string]string{"Authorization": "Bearer " + tok}); st != 429 {
		t.Fatalf("a verified token does not share the tenant's bucket: %d", st)
	}
}

// Audit F-4: without trusted proxies X-Forwarded-For is ignored, so a new header per request does not give a new
// bucket.
func TestRateIgnoresForwardedForByDefault(t *testing.T) {
	e := rateEnv(t, ", rate: {rps: 2, burst: 2, max_wait_ms: 0, max_waiting: 0}")
	time.Sleep(1100 * time.Millisecond)
	ips := []string{"203.0.113.1", "203.0.113.2", "203.0.113.3", "203.0.113.4", "203.0.113.5", "203.0.113.6"}
	if n := count429(e, 6, func(i int) map[string]string { return map[string]string{"X-Forwarded-For": ips[i]} }); n < 3 {
		t.Fatalf("rotating X-Forwarded-For escaped the client bucket: %d refused of 6", n)
	}
}

// With the connection's address trusted, the right-most untrusted X-Forwarded-For hop is the client: hops a client
// writes to the left do not change its bucket, distinct clients behind the proxy get their own.
func TestRateTrustedProxyForwardedFor(t *testing.T) {
	t.Setenv("RANGKA_TRUSTED_PROXIES", "127.0.0.1/32, ::1")
	e := rateEnv(t, ", rate: {rps: 2, burst: 2, max_wait_ms: 0, max_waiting: 0}")
	time.Sleep(1100 * time.Millisecond)
	ips := []string{"203.0.113.1", "203.0.113.2", "203.0.113.3", "203.0.113.4", "203.0.113.5", "203.0.113.6"}
	if n := count429(e, 6, func(i int) map[string]string { return map[string]string{"X-Forwarded-For": ips[i]} }); n != 0 {
		t.Fatalf("distinct clients behind a trusted proxy shared a bucket: %d refused", n)
	}
	time.Sleep(1100 * time.Millisecond)
	spoof := func(i int) map[string]string { return map[string]string{"X-Forwarded-For": ips[i] + ", 198.51.100.7"} }
	if n := count429(e, 6, spoof); n < 3 {
		t.Fatalf("hops written by the client changed its bucket: %d refused of 6", n)
	}
}
