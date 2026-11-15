package connectors

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/jobs"
	"github.com/rangka-os/rangka-os/kernel/internal/rk"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
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
		_, err := rt.client(false, false, "").Get(u)
		if !errors.Is(err, ErrEgressRefused) {
			t.Errorf("%s: %v, want egress_refused", u, err)
		}
	}
	// the open policy (self-hosting, demos) still reaches it
	if resp, err := (&Runtime{}).client(false, false, "").Get(internal.URL); err != nil {
		t.Fatalf("open policy: %v", err)
	} else {
		resp.Body.Close()
	}
	// and so does an allowed range
	_, lo, _ := net.ParseCIDR("127.0.0.0/8")
	if resp, err := (&Runtime{Egress: Egress{Public: true, Allow: []*net.IPNet{lo}}}).client(false, false, "").Get(internal.URL); err != nil {
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
	if a, b := rt.client(false, false, "").Transport, rt.client(false, true, "x").Transport; a != b {
		t.Fatal("a new transport per call")
	}
	if other := (&Runtime{Egress: Egress{Public: true}}).client(false, false, "").Transport; other == rt.client(false, false, "").Transport {
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

// testedCompany is an x_demo runtime whose current content is tested, so only the egress policy decides the send.
// The vendor URL is rewritten to host:port of srv (when given) so a TLS test server stands in for api.example.com.
func testedCompany(t *testing.T, srv *httptest.Server, eg Egress) *Runtime {
	t.Helper()
	port := ""
	if srv != nil {
		_, port, _ = net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))
	}
	rt, _ := stateRTEdit(t, map[string]string{"o1": "tok"}, func(s string) string {
		if port == "" {
			return s
		}
		return strings.Replace(s, "https://api.example.com/ping", "https://api.example.com:"+port+"/ping", 1)
	})
	_, h, _, _ := rt.State(context.Background(), "o1", "x_demo")
	if err := store.SaveConnectorTest(context.Background(), rt.DB, "o1", "x_demo", h, "owner", true, nil); err != nil {
		t.Fatal(err)
	}
	rt.Egress = eg
	rt.HTTP = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	return rt
}

func stubLookup(m map[string]string) func(context.Context, string) ([]string, error) {
	return func(_ context.Context, host string) ([]string, error) {
		if ip, ok := m[host]; ok {
			return []string{ip}, nil
		}
		return nil, errors.New("no such host: " + host)
	}
}

func wantHostPrivate(t *testing.T, err error) {
	t.Helper()
	var p *jobs.Permanent
	if !errors.As(err, &p) || !strings.Contains(err.Error(), "connector_host_private") {
		t.Fatalf("got %v, want a permanent connector_host_private", err)
	}
}

// The name resolves to a private address: refused at send time, whatever RANGKA_CONNECTOR_EGRESS says (here: open).
func TestCompanyConnectorPrivateResolved(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer srv.Close()
	for name, ip := range map[string]string{"private": "10.0.0.5", "metadata": "169.254.169.254", "loopback": "127.0.0.1"} {
		t.Run(name, func(t *testing.T) {
			rt := testedCompany(t, srv, Egress{lookup: stubLookup(map[string]string{"api.example.com": ip})})
			_, err := rt.Call(context.Background(), "o1", "x_demo", "ping", map[string]any{"message": "halo"})
			wantHostPrivate(t, err)
		})
	}
}

// A numeric shorthand host (127.1 is 127.0.0.1) never reaches a loopback service. How 127.1 resolves depends on the
// platform resolver (cgo: 127.0.0.1; pure Go: no such host), so the direct half accepts a refusal or a DNS error and
// asserts the server saw nothing; the call path uses a stub that answers what 127.1 means.
func TestCompanyConnectorNumericShorthandHost(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write([]byte(`{}`)) }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))
	rt := &Runtime{HTTP: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}}
	c := rt.client(true, false, "127.1")
	_, err := c.Get("https://127.1:" + port + "/")
	var dnsErr *net.DNSError
	if !errors.Is(err, ErrEgressRefused) && !errors.As(err, &dnsErr) {
		t.Fatalf("127.1: %v, want egress_refused or a DNS error", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("the server got %d requests", hits.Load())
	}
	// through the call path, with a resolver that maps the vendor name to what 127.1 means
	rt = testedCompany(t, srv, Egress{lookup: stubLookup(map[string]string{"api.example.com": "127.0.0.1"})})
	_, err = rt.Call(context.Background(), "o1", "x_demo", "ping", map[string]any{"message": "halo"})
	wantHostPrivate(t, err)
	if hits.Load() != 0 {
		t.Fatalf("the server got %d requests", hits.Load())
	}
}

func TestCompanyConnectorAllowList(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"ok":true}`)) }))
	defer srv.Close()
	_, lo, _ := net.ParseCIDR("127.0.0.0/8")
	rt := testedCompany(t, srv, Egress{Allow: []*net.IPNet{lo}, lookup: stubLookup(map[string]string{"api.example.com": "127.0.0.1"})})
	out, err := rt.Call(context.Background(), "o1", "x_demo", "ping", map[string]any{"message": "halo"})
	if err != nil || out["ok"] != true {
		t.Fatalf("allowed address: %v %v", out, err)
	}
}

// A catalogue connector follows RANGKA_CONNECTOR_EGRESS as before: open policy reaches loopback, public refuses it
// (and the refusal stays a plain call failure, not connector_host_private).
func TestCatalogueConnectorEgressUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer srv.Close()
	if resp, err := (&Runtime{}).client(false, false, "").Get(srv.URL); err != nil {
		t.Fatalf("open policy: %v", err)
	} else {
		resp.Body.Close()
	}
	if _, err := (&Runtime{Egress: Egress{Public: true}}).client(false, false, "").Get(srv.URL); !errors.Is(err, ErrEgressRefused) {
		t.Fatalf("public policy: %v", err)
	}
	// company clients are public whatever the platform policy
	if _, err := (&Runtime{}).client(true, false, "").Get(srv.URL); !errors.Is(err, ErrEgressRefused) {
		t.Fatalf("company, open policy: %v", err)
	}
}

// The OAuth2 refresh of a company connector goes through the public dialer too: a token_url that resolves to a private
// address is refused as connector_host_private, and the stored access token is not dropped for it.
func TestCompanyOAuthRefreshPrivate(t *testing.T) {
	rt := &Runtime{Egress: Egress{lookup: stubLookup(map[string]string{"auth.example.com": "10.0.0.5"})}}
	c := rt.client(true, true, "auth.example.com")
	_, err := c.Post("https://auth.example.com/token", "application/x-www-form-urlencoded", strings.NewReader("a=b"))
	if !errors.Is(err, ErrEgressRefused) {
		t.Fatalf("got %v", err)
	}
}

// A catalogue OAuth2 connector keeps its old refresh failure behaviour under RANGKA_CONNECTOR_EGRESS=public: a refused
// token_url address is a failed refresh (needs_auth, audited), not connector_host_private.
func TestCatalogueOAuthRefreshEgressRefusedStaysNeedsAuth(t *testing.T) {
	ctx := context.Background()
	rt, _ := stateRT(t, nil)
	rt.Reg().Connectors["oacat"] = &rk.Doc{Data: map[string]any{
		"name": "oacat",
		"auth": map[string]any{"type": "oauth2", "token_url": "https://auth.example.com/token"},
	}}
	rt.Reg().Site.System["connector_auth"] = map[string]any{"oacat": map[string]any{"refresh_token": "r", "client_id": "c"}}
	rt.Egress = Egress{Public: true, lookup: stubLookup(map[string]string{"auth.example.com": "10.0.0.5"})}
	rt.HTTP = &http.Client{Transport: &http.Transport{}}
	err := rt.refreshIfDue(ctx, "o1", "oacat")
	if !errors.Is(err, ErrNeedsAuth) || errors.Is(err, ErrEgressRefused) {
		t.Fatalf("got %v, want needs_auth only", err)
	}
	res, qerr := rt.DB.Query(ctx, `select 1 from rk_audit where kind = 'connector.needs_auth' and target = 'connector/oacat'`)
	if qerr != nil || len(res.Maps()) != 1 {
		t.Fatalf("needs_auth audits: %v %v", res, qerr)
	}
}
