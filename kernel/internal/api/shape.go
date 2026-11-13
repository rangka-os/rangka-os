package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/ratelimit"
)

const msgRateLimited = "Sistem sedang ramai. Coba lagi sebentar."

// shape slows the tenant down to its plan's rate (contract 0040 §9): a request over the rate waits for a token
// (X-Rangka-Throttle-Ms says how long), and past the plan's longest wait gets 429 rate_limited with Retry-After.
// Requests whose credentials this process has verified share the tenant's bucket; every other request (no
// credentials, credentials not verified yet, or credentials that failed) gets a bucket per client, keyed by a hash
// of its address (clientKey). Verifying never happens here: a credential counts as verified once principal() has
// resolved it or this kernel has just issued it as a session cookie (verified.go). The rate comes from the cached
// plan snapshot, so shaping never touches the database; without a plan, a rate in it, or before the first
// measurement nothing is shaped.
// Not shaped: /healthz, static web files, /_rangka/ and GET /api/v1/events/poll (it has its own floor).
func (s *Server) shape(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if issuesSessions(r) {
			sw := &sessionWatch{ResponseWriter: w, s: s, r: r}
			defer sw.watch() // a handler that only sets headers: net/http writes them after it returns
			w = sw
		}
		rate, ok := s.planRate()
		if !ok || unshaped(r) {
			h.ServeHTTP(w, r)
			return
		}
		key := "tenant"
		if !s.verified.has(credential(r)) {
			key = "ip:" + s.clientKey(r)
		}
		waited, err := s.limiter.Wait(r.Context(), key, rate)
		var refused *ratelimit.Refused
		switch {
		case err == nil:
			if waited > 0 {
				w.Header().Set("X-Rangka-Throttle-Ms", strconv.FormatInt(waited.Milliseconds(), 10))
			}
			h.ServeHTTP(w, r)
		case errors.As(err, &refused):
			w.Header().Set("Retry-After", strconv.Itoa(refused.RetrySeconds()))
			writeError(w, http.StatusTooManyRequests, "rate_limited", msgRateLimited)
		default: // the client went away while waiting
		}
	})
}

// planRate is the tenant's rate from the cached plan snapshot (contract 0028 §4 refreshes it about every 10 s).
func (s *Server) planRate() (ratelimit.Rate, bool) {
	if s.Engine == nil || s.DB == nil {
		return ratelimit.Rate{}, false
	}
	snap := s.Engine.Plan.Peek(s.DB, s.Engine.Reg(), s.Engine.Now(), s.Log)
	if snap == nil {
		return ratelimit.Rate{}, false
	}
	pr := snap.Current.Rate()
	if pr == nil {
		return ratelimit.Rate{}, false
	}
	return ratelimit.Rate{RPS: pr.RPS, Burst: int(pr.Burst), MaxWait: time.Duration(pr.MaxWaitMS) * time.Millisecond,
		MaxWaiting: int(pr.MaxWaiting)}, true
}

func unshaped(r *http.Request) bool {
	p := r.URL.Path
	switch {
	case p == "/healthz", strings.HasPrefix(p, "/_rangka/"):
		return true
	case r.Method == http.MethodGet && p == "/api/v1/events/poll":
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	// static web: everything GET that is not an API, sign-in, OAuth, MCP, file or webhook route
	for _, pre := range []string{"/api/", "/auth/", "/oauth/", "/mcp", "/files/", "/hooks/", "/.well-known/"} {
		if strings.HasPrefix(p, pre) {
			return false
		}
	}
	return true
}

// clientKey is a hash of the client's address, so no address is kept in memory. The address is the connection's,
// unless that connection comes from a trusted proxy (RANGKA_TRUSTED_PROXIES): then it is the last X-Forwarded-For
// hop that is not itself a trusted proxy. Without trusted proxies X-Forwarded-For is ignored, so a client cannot
// pick a fresh bucket per request by sending a new header (contract 0040 §9).
func (s *Server) clientKey(r *http.Request) string {
	sum := sha256.Sum256([]byte(s.proxies.clientIP(r, s.Log)))
	return hex.EncodeToString(sum[:12])
}
