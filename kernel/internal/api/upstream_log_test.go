package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The upstream callback's query carries the provider's code and the kernel's state: the request log keeps the
// path only (contract 0042 §1: codes and state values never reach a log).
func TestRequestLogDropsUpstreamQuery(t *testing.T) {
	var logs []string
	s := &Server{Log: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }}
	h := s.logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(302) }))
	for _, p := range []string{"/auth/upstream/kantor/callback?code=CODE-SECRET&state=STATE-SECRET", "/auth/upstream/kantor/start?to=%2Fapp%2F"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", p, nil))
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/r/item?limit=5", nil))
	all := strings.Join(logs, "\n")
	if strings.Contains(all, "CODE-SECRET") || strings.Contains(all, "STATE-SECRET") || !strings.Contains(all, "GET /auth/upstream/kantor/callback 302") {
		t.Fatalf("log:\n%s", all)
	}
	if !strings.Contains(all, "/api/v1/r/item?limit=5") {
		t.Fatalf("other routes keep their query:\n%s", all)
	}
}
