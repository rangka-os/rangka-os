package connectors

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
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

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0).To4(), Mask: net.CIDRMask(10, 32)}

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
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || cgnat.Contains(ip))
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
// or its transport is not an *http.Transport (a test's).
func (e Egress) guard(c *http.Client) *http.Client {
	if !e.Public {
		return c
	}
	var t *http.Transport
	switch base := c.Transport.(type) {
	case nil:
		t = http.DefaultTransport.(*http.Transport).Clone()
	case *http.Transport:
		t = base.Clone()
	default:
		return c
	}
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: e.control}
	t.DialContext = d.DialContext
	t.DialTLSContext = nil
	t.Proxy = nil // a proxy would dial the internal address for us
	out := *c
	out.Transport = t
	return &out
}
