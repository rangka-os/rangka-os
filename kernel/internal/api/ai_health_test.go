package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// healthSidecar answers GET /healthz with 200 while *up is true, 503 otherwise, and counts the probes.
func healthSidecar(t *testing.T, up *atomic.Bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	hits := &atomic.Int32{}
	side := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if !up.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(side.Close)
	return side, hits
}

func TestAIFeatureCachesHealth(t *testing.T) {
	up := &atomic.Bool{}
	up.Store(true)
	side, hits := healthSidecar(t, up)
	s := &Server{AI: &AIConfig{URL: side.URL, CallbackURL: "http://127.0.0.1:8080", HealthFailTTL: 50 * time.Millisecond}}
	ctx := context.Background()

	// a healthy answer is reused: two /meta within 30 s probe once
	if !s.aiFeature(ctx, true) || !s.aiFeature(ctx, true) {
		t.Fatal("healthy sidecar reported off")
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("probes after two healthy /meta = %d, want 1", n)
	}

	// a failure is reused only for the short TTL, then probed again
	s.ai.healthAt = time.Time{}
	up.Store(false)
	if s.aiFeature(ctx, true) || s.aiFeature(ctx, true) {
		t.Fatal("failing sidecar reported on")
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("probes after two failed /meta = %d, want 2", n)
	}
	up.Store(true)
	time.Sleep(60 * time.Millisecond)
	if !s.aiFeature(ctx, true) {
		t.Fatal("recovered sidecar not seen after the failure TTL")
	}
	if n := hits.Load(); n != 3 {
		t.Fatalf("probes after the failure TTL = %d, want 3", n)
	}
}

// A request cancelled before /meta runs (the browser left) still probes the sidecar and does not record it as down.
func TestAIFeatureIgnoresRequestCancel(t *testing.T) {
	up := &atomic.Bool{}
	up.Store(true)
	side, hits := healthSidecar(t, up)
	s := &Server{AI: &AIConfig{URL: side.URL, CallbackURL: "http://127.0.0.1:8080"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !s.aiFeature(ctx, true) {
		t.Fatal("cancelled request reported the sidecar as down")
	}
	if !s.aiFeature(context.Background(), true) || hits.Load() != 1 {
		t.Fatalf("after a cancelled request: probes %d, want 1 healthy cached probe", hits.Load())
	}
}

// Without a callback URL the sidecar cannot reach /mcp: the assistant stays off and nothing is probed.
func TestAIFeatureNeedsCallbackURL(t *testing.T) {
	up := &atomic.Bool{}
	up.Store(true)
	side, hits := healthSidecar(t, up)
	s := &Server{AI: &AIConfig{URL: side.URL}}
	if s.aiFeature(context.Background(), true) || hits.Load() != 0 {
		t.Fatalf("no callback URL: feature on or probed (%d)", hits.Load())
	}
}
