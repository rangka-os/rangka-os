package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/logx"
)

// TestHTTPRequestIDThroughTheKernel checks the served handler end to end: the incoming X-Request-Id is echoed, and
// the access line carries the same id and the ServeMux route pattern (seen through the API's middleware chain),
// never the raw path.
func TestHTTPRequestIDThroughTheKernel(t *testing.T) {
	e := newEnv(t, false, nil)
	var b bytes.Buffer
	e.s.Cfg.Logger = logx.NewJSON(&b, slog.LevelInfo)
	old := logx.SlowRequest
	logx.SlowRequest = -1 // every request is "slow", so each one logs a line
	ts := httptest.NewServer(e.s.httpHandler())
	defer func() { ts.Close(); logx.SlowRequest = old }()

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/r/sales.order/SO-0001", nil)
	req.Header.Set("X-Request-Id", "cloud-req-1")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if got := res.Header.Get("X-Request-Id"); got != "cloud-req-1" {
		t.Fatalf("response X-Request-Id = %q", got)
	}
	res, err = http.Get(ts.URL + "/api/v1/meta")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	minted := res.Header.Get("X-Request-Id")
	if len(minted) != 32 {
		t.Fatalf("minted X-Request-Id = %q", minted)
	}
	ts.Close()

	var http0 []map[string]any
	for _, ln := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("not JSON: %q", ln)
		}
		if m["component"] == "http" {
			http0 = append(http0, m)
		}
	}
	if len(http0) != 2 {
		t.Fatalf("http lines = %d:\n%s", len(http0), b.String())
	}
	if http0[0]["request_id"] != "cloud-req-1" || http0[0]["route"] != "GET /api/v1/r/{model}/{name}" {
		t.Errorf("first line = %v", http0[0])
	}
	if http0[1]["request_id"] != minted || http0[1]["route"] != "GET /api/v1/meta" {
		t.Errorf("second line = %v (minted %s)", http0[1], minted)
	}
	if strings.Contains(b.String(), "SO-0001") {
		t.Errorf("raw path in the log:\n%s", b.String())
	}
}

func TestAPILogDropsTheLegacyAccessLineOnlyWithALogger(t *testing.T) {
	var got []string
	logFor := func(string) func(string, ...any) {
		return func(f string, a ...any) { got = append(got, f) }
	}
	apiLog(nil, logFor)("%s %s %d %s", "GET", "/api/v1/meta", 200, "1ms")
	if len(got) != 1 {
		t.Fatalf("text mode lost the access line: %v", got)
	}
	got = nil
	l := apiLog(logx.NewJSON(&bytes.Buffer{}, slog.LevelInfo), logFor)
	l("%s %s %d %s", "GET", "/api/v1/meta", 200, "1ms")
	l("print: %v", "x")
	if len(got) != 1 || got[0] != "print: %v" {
		t.Fatalf("json mode lines = %v", got)
	}
}
