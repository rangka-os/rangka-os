package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/ratelimit"
)

const msgRateLimited = "Sistem sedang ramai. Coba lagi sebentar."

// shape slows the tenant down to its plan's rate (contract 0040 §9): a request over the rate waits for a token
// (X-Rangka-Throttle-Ms says how long), and past the plan's longest wait gets 429 rate_limited with Retry-After.
// Signed-in requests share the tenant's bucket; requests without credentials (sign-in, public portals) get a
// bucket per client, keyed by a hash of its address. The rate comes from the cached plan snapshot, so shaping
// never touches the database; without a plan, a rate in it, or before the first measurement nothing is shaped.
// Not shaped: /healthz, static web files, /_rangka/ and GET /api/v1/events/poll (it has its own floor).
func (s *Server) shape(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rate, ok := s.planRate()
		if !ok || unshaped(r) {
			h.ServeHTTP(w, r)
			return
		}
		key := "tenant"
		if !hasCredentials(r) {
			key = "ip:" + clientKey(r)
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

func hasCredentials(r *http.Request) bool {
	if r.Header.Get("Authorization") != "" {
		return true
	}
	c, err := r.Cookie(auth.CookieName)
	return err == nil && c.Value != ""
}

// clientKey is a hash of the client's address (the first X-Forwarded-For hop when a proxy set one, with the
// connection's address), so no address is kept in memory.
func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	fwd, _, _ := strings.Cut(r.Header.Get("X-Forwarded-For"), ",")
	sum := sha256.Sum256([]byte(host + "|" + strings.TrimSpace(fwd)))
	return hex.EncodeToString(sum[:12])
}
