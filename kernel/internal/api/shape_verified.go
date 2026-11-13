package api

import (
	"crypto/sha256"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// Which bucket a request takes (contract 0040 §9). A header is not proof: anyone can send
// "Authorization: Bearer junk", and if that alone chose the tenant's bucket, a stranger could spend the tenant's
// rate and get every signed-in person a 429. So the tenant's bucket is for credentials this process has verified,
// and shaping learns that without a database query of its own:
//   - principal() notes a credential once auth.Resolve accepts it, and forgets it once Resolve refuses it;
//   - a session cookie this kernel issues (sign-in, upstream and OIDC callbacks) is noted as it is set, and the
//     request's cookie is forgotten when a response clears it (sign-out).
// The first request with a credential this process has not seen yet takes its client's bucket; so does every
// request whose credential failed. Notes last verifiedTTL, so a revoked session may keep the tenant's bucket (not
// its access) that long.

const (
	verifiedTTL = 10 * time.Minute
	verifiedMax = 20000 // entries; past it expired ones are dropped, then everything (a cold start, not an error)
)

// credKey identifies the credential auth.Resolve reads: the bearer token if there is one, else the session cookie.
type credKey [16]byte

// credential is the key of the request's credential, ok=false when it carries none that Resolve would read.
func credential(r *http.Request) (credKey, bool) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return keyOf("b:" + strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")))
	}
	if c, err := r.Cookie(auth.CookieName); err == nil && c.Value != "" {
		return keyOf("c:" + c.Value)
	}
	return credKey{}, false
}

func keyOf(v string) (credKey, bool) {
	sum := sha256.Sum256([]byte(v))
	var k credKey
	copy(k[:], sum[:])
	return k, true
}

// verifiedSet remembers hashes of credentials verified in this process. The zero value is ready to use.
type verifiedSet struct {
	mu  sync.Mutex
	m   map[credKey]time.Time // expiry
	now func() time.Time      // tests; nil = time.Now
}

func (v *verifiedSet) clock() time.Time {
	if v.now != nil {
		return v.now()
	}
	return time.Now()
}

func (v *verifiedSet) has(k credKey, ok bool) bool {
	if !ok {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	exp, found := v.m[k]
	return found && v.clock().Before(exp)
}

func (v *verifiedSet) note(k credKey, ok bool) {
	if !ok {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.clock()
	if v.m == nil {
		v.m = map[credKey]time.Time{}
	}
	if _, found := v.m[k]; !found && len(v.m) >= verifiedMax {
		for key, exp := range v.m {
			if !now.Before(exp) {
				delete(v.m, key)
			}
		}
		if len(v.m) >= verifiedMax {
			clear(v.m)
		}
	}
	v.m[k] = now.Add(verifiedTTL)
}

func (v *verifiedSet) forget(k credKey, ok bool) {
	if !ok {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.m, k)
}

// issuesSessions: the routes that set or clear the session cookie (sign-in and sign-out, upstream and OIDC
// callbacks). Only these responses are watched, so streaming routes keep their own ResponseWriter.
func issuesSessions(r *http.Request) bool {
	p := r.URL.Path
	return strings.HasPrefix(p, "/auth/") || strings.HasPrefix(p, "/oauth/")
}

// sessionWatch notes the session cookie a response sets, and forgets the request's cookie when the response
// clears it.
type sessionWatch struct {
	http.ResponseWriter
	s    *Server
	r    *http.Request
	done bool
}

func (w *sessionWatch) WriteHeader(code int) {
	w.watch()
	w.ResponseWriter.WriteHeader(code)
}

func (w *sessionWatch) Write(b []byte) (int, error) {
	w.watch()
	return w.ResponseWriter.Write(b)
}

func (w *sessionWatch) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *sessionWatch) watch() {
	if w.done {
		return
	}
	w.done = true
	resp := http.Response{Header: w.Header()}
	for _, c := range resp.Cookies() {
		if c.Name != auth.CookieName {
			continue
		}
		if c.Value == "" || c.MaxAge < 0 {
			if old, err := w.r.Cookie(auth.CookieName); err == nil && old.Value != "" {
				w.s.verified.forget(keyOf("c:" + old.Value))
			}
			continue
		}
		w.s.verified.note(keyOf("c:" + c.Value))
	}
}

// trustedProxies are the addresses whose X-Forwarded-For is believed (RANGKA_TRUSTED_PROXIES: IPs or CIDRs,
// separated by commas or spaces). Empty, the default: X-Forwarded-For is ignored and the connection's address is
// the client's.
type trustedProxies struct {
	once sync.Once
	list []netip.Prefix
	env  string // tests; "" = read RANGKA_TRUSTED_PROXIES
}

func (t *trustedProxies) load(log func(format string, args ...any)) {
	t.once.Do(func() {
		raw := t.env
		if raw == "" {
			raw = os.Getenv("RANGKA_TRUSTED_PROXIES")
		}
		for _, f := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
			if p, err := netip.ParsePrefix(f); err == nil {
				t.list = append(t.list, p.Masked())
				continue
			}
			if a, err := netip.ParseAddr(f); err == nil {
				a = a.Unmap()
				t.list = append(t.list, netip.PrefixFrom(a, a.BitLen()))
				continue
			}
			if log != nil {
				log("RANGKA_TRUSTED_PROXIES: %q is not an IP or CIDR; ignored", f)
			}
		}
	})
}

func (t *trustedProxies) trusts(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range t.list {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// clientIP is the connection's address, or, when that is a trusted proxy, the right-most X-Forwarded-For hop
// that is not a trusted proxy (hops left of it were written by the client and prove nothing). A hop that is not
// an address stops the walk at the last trusted one.
func (t *trustedProxies) clientIP(r *http.Request, log func(format string, args ...any)) string {
	t.load(log)
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	cur, err := netip.ParseAddr(host)
	if err != nil || len(t.list) == 0 || !t.trusts(cur) {
		return host
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(hops[i])
		if ap, err := netip.ParseAddrPort(hop); err == nil {
			hop = ap.Addr().String()
		}
		a, err := netip.ParseAddr(hop)
		if err != nil {
			break
		}
		cur = a.Unmap()
		if !t.trusts(cur) {
			break
		}
	}
	return cur.String()
}
