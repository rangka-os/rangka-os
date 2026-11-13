package connectors

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEgressAllowed(t *testing.T) {
	pub := Egress{Public: true}
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true, "100.63.255.255": true,
		"127.0.0.1": false, "::1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false, "fc00::1": false,
		"169.254.169.254": false, "fe80::1": false, "100.64.0.1": false, "0.0.0.0": false, "::": false, "224.0.0.1": false,
		"::ffff:127.0.0.1": false, "::ffff:10.0.0.1": false,
	} {
		if got := pub.Allowed(net.ParseIP(addr)); got != want {
			t.Errorf("public %s: %v, want %v", addr, got, want)
		}
	}
	if !(Egress{}).Allowed(net.ParseIP("127.0.0.1")) {
		t.Error("the zero policy refused loopback")
	}
	_, vpc, _ := net.ParseCIDR("10.20.0.0/16")
	if e := (Egress{Public: true, Allow: []*net.IPNet{vpc}}); !e.Allowed(net.ParseIP("10.20.3.4")) || e.Allowed(net.ParseIP("10.21.0.1")) {
		t.Error("allow list")
	}
}

func TestEgressFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if e, err := EgressFromEnv(env(nil)); err != nil || e.Public {
		t.Fatalf("unset: %+v %v", e, err)
	}
	e, err := EgressFromEnv(env(map[string]string{"RANGKA_CONNECTOR_EGRESS": "public", "RANGKA_CONNECTOR_EGRESS_ALLOW": "10.20.0.0/16, 192.168.5.0/24"}))
	if err != nil || !e.Public || len(e.Allow) != 2 {
		t.Fatalf("public: %+v %v", e, err)
	}
	for _, bad := range []map[string]string{{"RANGKA_CONNECTOR_EGRESS": "publik"}, {"RANGKA_CONNECTOR_EGRESS_ALLOW": "10.0.0.0/33"}} {
		if _, err := EgressFromEnv(env(bad)); err == nil {
			t.Errorf("%v: no error", bad)
		}
	}
}

// Audit business-raka F1: under "public" a company connector cannot reach a service on the kernel's own host (the
// Raka sidecar on loopback), by address, by a name that resolves to it, or through a redirect from a public URL.
func TestEgressGuardRefusesInternal(t *testing.T) {
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("raka")) }))
	defer internal.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(internal.URL, "http://"))
	rt := &Runtime{Egress: Egress{Public: true}}
	for _, u := range []string{internal.URL, "http://localhost:" + port} {
		_, err := rt.client(false, "").Get(u)
		if !errors.Is(err, ErrEgressRefused) {
			t.Errorf("%s: %v, want egress_refused", u, err)
		}
	}
	// the open policy (self-hosting, demos) still reaches it
	if resp, err := (&Runtime{}).client(false, "").Get(internal.URL); err != nil {
		t.Fatalf("open policy: %v", err)
	} else {
		resp.Body.Close()
	}
	// and so does an allowed range
	_, lo, _ := net.ParseCIDR("127.0.0.0/8")
	if resp, err := (&Runtime{Egress: Egress{Public: true, Allow: []*net.IPNet{lo}}}).client(false, "").Get(internal.URL); err != nil {
		t.Fatalf("allowed range: %v", err)
	} else {
		resp.Body.Close()
	}
}

func TestEgressBlockedRanges(t *testing.T) {
	pub := Egress{Public: true}
	for _, addr := range []string{"0.1.2.3", "192.0.0.8", "198.18.0.1", "240.0.0.1", "64:ff9b::7f00:1", "64:ff9b:1::1", "2002:7f00:1::1"} {
		if pub.Allowed(net.ParseIP(addr)) {
			t.Errorf("%s allowed", addr)
		}
	}
}

// One guarded transport per runtime and base, so calls reuse connections (audit platform-env A6).
func TestEgressReusesTransport(t *testing.T) {
	rt := &Runtime{Egress: Egress{Public: true}}
	if a, b := rt.client(false, "").Transport, rt.client(true, "x").Transport; a != b {
		t.Fatal("a new transport per call")
	}
	if other := (&Runtime{Egress: Egress{Public: true}}).client(false, "").Transport; other == rt.client(false, "").Transport {
		t.Fatal("transport shared across runtimes with their own policies")
	}
}

// Audit platform-env A5: the OIDC upstream client gets this transport, so an issuer system.rk names on loopback or
// a private address is refused under "public" like a connector.
func TestEgressTransport(t *testing.T) {
	if (Egress{}).Transport() != nil {
		t.Fatal("open policy: want nil (the default transport)")
	}
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{}")) }))
	defer issuer.Close()
	c := &http.Client{Transport: Egress{Public: true}.Transport()}
	if _, err := c.Get(issuer.URL + "/.well-known/openid-configuration"); !errors.Is(err, ErrEgressRefused) {
		t.Fatalf("loopback issuer: %v, want egress_refused", err)
	}
}
