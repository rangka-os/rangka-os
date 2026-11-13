package connectors

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ErrEgressRefused is a connector request to an address the platform's egress policy does not allow.
var ErrEgressRefused = errors.New("egress_refused")

// Egress is the platform's connector egress policy, set by whoever runs the kernel (RANGKA_CONNECTOR_EGRESS), never by
// a company's files: on a hosted kernel the company controls system.rk and its connectors, so a connector of its own
// could otherwise reach the platform's internal services (the Raka sidecar on loopback or in the VPC, the cloud's
// metadata address) from the kernel's network (audit business-raka F1).
//
//   - "" or "any": no limit (self-hosting, demos whose mocks listen on loopback).
//   - "public": the kernel connects only to public addresses: not loopback, private (RFC 1918, fc00::/7), carrier-grade
//     NAT (100.64.0.0/10), link-local (169.254.0.0/16 with the cloud metadata address, fe80::/10), unspecified or
//     multicast. RANGKA_CONNECTOR_EGRESS_ALLOW lists CIDRs that stay reachable anyway (comma separated).
//
// The address is checked after DNS, on the connection itself, so a name that resolves to an internal address, or a
// redirect to one, is refused as well.
type Egress struct {
	Public bool
	Allow  []*net.IPNet
}

// EgressFromEnv reads RANGKA_CONNECTOR_EGRESS and RANGKA_CONNECTOR_EGRESS_ALLOW. An unknown policy or a bad CIDR is
// an error, so a typo never leaves a hosted kernel open.
func EgressFromEnv(getenv func(string) string) (Egress, error) {
	var e Egress
	switch p := strings.TrimSpace(getenv("RANGKA_CONNECTOR_EGRESS")); p {
	case "", "any":
	case "public":
		e.Public = true
	default:
		return e, fmt.Errorf("RANGKA_CONNECTOR_EGRESS=%q: use any or public", p)
	}
	for _, s := range strings.Split(getenv("RANGKA_CONNECTOR_EGRESS_ALLOW"), ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return e, fmt.Errorf("RANGKA_CONNECTOR_EGRESS_ALLOW: %v", err)
		}
		e.Allow = append(e.Allow, n)
	}
	return e, nil
}

// blocked are ranges the net.IP predicates do not cover: "this network" 0.0.0.0/8, carrier-grade NAT, IETF protocol
// assignments, benchmarking, reserved 240.0.0.0/4, and IPv6 prefixes that embed an IPv4 address the gateway reaches
// (NAT64 64:ff9b::/96 and 64:ff9b:1::/48, 6to4 2002::/16)
var blocked = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "240.0.0.0/4",
		"64:ff9b::/96", "64:ff9b:1::/48", "2002::/16"} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

// Allowed reports whether the policy lets a connector connect to ip.
func (e Egress) Allowed(ip net.IP) bool {
	if !e.Public {
		return true
	}
	for _, n := range e.Allow {
		if n.Contains(ip) {
			return true
		}
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, n := range blocked {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

// control refuses a connection whose address the policy does not allow; it runs for every dialled address.
func (e Egress) control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrEgressRefused, address)
	}
	ip := net.ParseIP(host)
	if ip == nil || !e.Allowed(ip) {
		return fmt.Errorf("%w: %s is not a public address", ErrEgressRefused, host)
	}
	return nil
}

// guard returns c with a transport that dials through the policy; c is returned as it is when the policy is open
// or its transport is not an *http.Transport (a test's). cache keeps one guarded transport per base transport, so
// connections are reused across calls instead of a new pool per call.
func (e Egress) guard(c *http.Client, cache *transportCache) *http.Client {
	if !e.Public {
		return c
	}
	key := c.Transport
	var base *http.Transport
	switch b := c.Transport.(type) {
	case nil:
		base = http.DefaultTransport.(*http.Transport)
	case *http.Transport:
		base = b
	default:
		return c
	}
	cache.Lock()
	defer cache.Unlock()
	if cache.m == nil {
		cache.m = map[http.RoundTripper]*http.Transport{}
	}
	t, ok := cache.m[key]
	if !ok {
		t = base.Clone()
		d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: e.control}
		t.DialContext = d.DialContext
		t.DialTLSContext = nil
		t.Proxy = nil // a proxy would dial the internal address for us
		cache.m[key] = t
	}
	out := *c
	out.Transport = t
	return &out
}

type transportCache struct {
	sync.Mutex
	m map[http.RoundTripper]*http.Transport
}
