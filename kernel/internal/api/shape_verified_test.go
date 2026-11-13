package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

func TestClientIP(t *testing.T) {
	req := func(remote string, xff ...string) *http.Request {
		r := httptest.NewRequest("GET", "/api/v1/meta", nil)
		r.RemoteAddr = remote
		for _, v := range xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		return r
	}
	none := &trustedProxies{}
	if got := none.clientIP(req("10.0.0.5:4000", "203.0.113.9"), nil); got != "10.0.0.5" {
		t.Fatalf("no trusted proxies: X-Forwarded-For must be ignored, got %s", got)
	}
	tp := &trustedProxies{env: "10.0.0.0/8 192.168.1.1, nonsense"}
	logged := ""
	cases := []struct {
		r    *http.Request
		want string
	}{
		{req("203.0.113.50:1", "198.51.100.1"), "203.0.113.50"},                   // untrusted connection: its own address
		{req("10.0.0.5:1"), "10.0.0.5"},                                           // trusted, no header
		{req("10.0.0.5:1", "198.51.100.1"), "198.51.100.1"},                       // one hop
		{req("10.0.0.5:1", "6.6.6.6, 198.51.100.1"), "198.51.100.1"},              // client-written hop on the left
		{req("10.0.0.5:1", "6.6.6.6, 198.51.100.1, 192.168.1.1"), "198.51.100.1"}, // two proxies
		{req("10.0.0.5:1", "6.6.6.6", "198.51.100.1, 10.1.2.3"), "198.51.100.1"},  // several header lines
		{req("10.0.0.5:1", "garbage, 10.9.9.9"), "10.9.9.9"},                      // not an address: stop at the last trusted
		{req("10.0.0.5:1", "198.51.100.1:5555"), "198.51.100.1"},                  // hop with a port
		{req("[::ffff:10.0.0.5]:1", "2001:db8::1"), "2001:db8::1"},                // mapped IPv4 connection
		{req("10.0.0.5:1", "10.2.2.2, 10.3.3.3"), "10.2.2.2"},                     // every hop trusted: the left-most
	}
	for i, c := range cases {
		if got := tp.clientIP(c.r, func(f string, a ...any) { logged = f }); got != c.want {
			t.Errorf("case %d: got %s, want %s", i, got, c.want)
		}
	}
	if logged == "" {
		t.Error("an entry that is not an IP or CIDR is not logged")
	}
}

func TestVerifiedSet(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	v := &verifiedSet{now: func() time.Time { return now }}
	r := httptest.NewRequest("GET", "/api/v1/meta", nil)
	if v.has(credential(r)) {
		t.Fatal("no credential counted as verified")
	}
	r.Header.Set("Authorization", "Bearer abc")
	if v.has(credential(r)) {
		t.Fatal("unseen credential counted as verified")
	}
	v.note(credential(r))
	if !v.has(credential(r)) {
		t.Fatal("noted credential not verified")
	}
	other := httptest.NewRequest("GET", "/", nil)
	other.Header.Set("Authorization", "Bearer abd")
	if v.has(credential(other)) {
		t.Fatal("a different token shares the note")
	}
	now = now.Add(verifiedTTL)
	if v.has(credential(r)) {
		t.Fatal("note outlives verifiedTTL")
	}
	v.note(credential(r))
	v.forget(credential(r))
	if v.has(credential(r)) {
		t.Fatal("forgotten credential still verified")
	}
	// a bearer token is what Resolve reads, even with a session cookie next to it
	both := httptest.NewRequest("GET", "/", nil)
	both.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "sess"})
	cookieOnly := httptest.NewRequest("GET", "/", nil)
	cookieOnly.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "sess"})
	v.note(credential(cookieOnly))
	both.Header.Set("Authorization", "Bearer junk")
	if v.has(credential(both)) {
		t.Fatal("a junk bearer token next to a verified cookie took the tenant's bucket")
	}
	// bounded: past verifiedMax the set starts over instead of growing
	for i := 0; i < verifiedMax+5; i++ {
		k, _ := keyOf(string(rune(i)) + "x")
		v.note(k, true)
	}
	if len(v.m) > verifiedMax {
		t.Fatalf("set grew to %d", len(v.m))
	}
}

func TestSessionWatch(t *testing.T) {
	s := &Server{}
	// sign-in: the cookie the response sets is verified
	r := httptest.NewRequest("POST", "/auth/login", nil)
	rec := httptest.NewRecorder()
	sw := &sessionWatch{ResponseWriter: rec, s: s, r: r}
	http.SetCookie(sw, &http.Cookie{Name: auth.CookieName, Value: "fresh", MaxAge: 3600})
	sw.WriteHeader(200)
	next := httptest.NewRequest("GET", "/api/v1/meta", nil)
	next.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "fresh"})
	if !s.verified.has(credential(next)) {
		t.Fatal("issued session cookie not verified")
	}
	// sign-out: the request's cookie is forgotten
	out := httptest.NewRequest("POST", "/auth/logout", nil)
	out.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "fresh"})
	sw = &sessionWatch{ResponseWriter: httptest.NewRecorder(), s: s, r: out}
	http.SetCookie(sw, &http.Cookie{Name: auth.CookieName, Value: "", MaxAge: -1})
	sw.watch() // a handler that writes no body: shape calls watch after it returns
	if s.verified.has(credential(next)) {
		t.Fatal("cleared session cookie still verified")
	}
	if !issuesSessions(httptest.NewRequest("GET", "/auth/upstream/x/callback", nil)) || issuesSessions(httptest.NewRequest("GET", "/api/v1/events", nil)) {
		t.Fatal("issuesSessions routes")
	}
}
