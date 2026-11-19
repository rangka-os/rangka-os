package logx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func lines(t *testing.T, b *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, ln := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if ln == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("not one JSON object per line: %q: %v", ln, err)
		}
		out = append(out, m)
	}
	return out
}

func TestJSONLineShape(t *testing.T) {
	var b bytes.Buffer
	l := NewJSON(&b, slog.LevelInfo)
	ctx := With(WithRequestID(context.Background(), "req-1"), slog.String("flow", "sales.post"), slog.Int64("actor", 42))
	l.ErrorContext(ctx, "flow failed", slog.String("component", CompFlow), slog.String("model", "sales.order"))
	got := lines(t, &b)
	if len(got) != 1 {
		t.Fatalf("lines = %d", len(got))
	}
	m := got[0]
	for k, want := range map[string]any{"level": "error", "msg": "flow failed", "component": "flow", "request_id": "req-1", "flow": "sales.post", "model": "sales.order", "actor": float64(42)} {
		if m[k] != want {
			t.Errorf("%s = %v, want %v", k, m[k], want)
		}
	}
	ts, _ := m["time"].(string)
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil || !strings.HasSuffix(ts, "Z") {
		t.Errorf("time = %q, want RFC 3339 in UTC", ts)
	}
}

func TestLogfShimLevels(t *testing.T) {
	var b bytes.Buffer
	logf := Logf(NewJSON(&b, slog.LevelInfo), CompQueue)
	logf("job %s done", "a")
	logf("job %s: %v", "b", errors.New("boom"))
	logf("WARNING: --dev-open")
	got := lines(t, &b)
	want := []string{"info", "warn", "warn"}
	for i, m := range got {
		if m["level"] != want[i] || m["component"] != "queue" {
			t.Errorf("line %d = %v, want level %s component queue", i, m, want[i])
		}
	}
	if got[1]["msg"] != "job b: boom" {
		t.Errorf("msg = %v", got[1]["msg"])
	}
}

func TestSkipFormats(t *testing.T) {
	var n int
	f := SkipFormats(func(string, ...any) { n++ }, "%s %s %d %s")
	f("%s %s %d %s", "GET", "/x", 200, "1ms")
	f("other %s", "x")
	if n != 1 {
		t.Fatalf("calls = %d, want 1", n)
	}
}

// TestSecretsNeverReachTheHandler fails if a known secret value reaches the underlying handler, through the
// message, a field, an error, a context field or a logger's With fields.
func TestSecretsNeverReachTheHandler(t *testing.T) {
	secret := "fixture-" + strings.Repeat("q7", 6) // built at run time: a value the test registers, not a credential
	RegisterSecret(secret)
	var b bytes.Buffer
	l := NewJSON(&b, slog.LevelDebug)
	ctx := With(context.Background(), slog.String("note", "has "+secret))
	l.With(slog.String("cfg", secret)).InfoContext(ctx, "connect postgres://rk:"+secret+"@db/rk failed",
		slog.String("detail", "x"+secret+"y"),
		slog.Any("err", errors.New("auth "+secret)),
		slog.Group("g", slog.String("inner", secret)),
		slog.Any("bytes", []byte(secret)),
		slog.Any("map", map[string]string{"k": secret}))
	Logf(l, CompKernel)("token %s", secret)
	if strings.Contains(b.String(), secret) {
		t.Fatalf("secret reached the handler:\n%s", b.String())
	}
	if !strings.Contains(b.String(), Redacted) {
		t.Fatalf("no redaction marker:\n%s", b.String())
	}
}

func TestRedactionPatterns(t *testing.T) {
	var b bytes.Buffer
	l := NewJSON(&b, slog.LevelInfo)
	l.Info("dial postgres://rangka:hunter2pw@db:5432/rk and mail owner@example.co.id with Bearer abcdefghijklmnop",
		slog.String("password", "pw-value"), slog.String("user_email", "a@b.cd"), slog.String("Authorization", "x"),
		slog.String("DATABASE_URL", "postgres://u@h/d"))
	out := b.String()
	for _, leak := range []string{"hunter2pw", "owner@example.co.id", "abcdefghijklmnop", "pw-value", "a@b.cd", `"Authorization":"x"`, "postgres://u@h/d"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %q in %s", leak, out)
		}
	}
	if !strings.Contains(out, "postgres://rangka:[REDACTED]@db:5432/rk") {
		t.Errorf("URL password not redacted in place: %s", out)
	}
}

func TestResolveFormat(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for in, want := range map[string]string{"": "json", "auto": "json", "JSON": "json", "text": "text"} {
		if got, err := ResolveFormat(in, f); err != nil || got != want {
			t.Errorf("ResolveFormat(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ResolveFormat("yaml", f); err == nil {
		t.Error("yaml accepted")
	}
}

func serve(t *testing.T, h http.Handler, l *slog.Logger, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	HTTP(h, l).ServeHTTP(w, req)
	return w
}

func TestRequestIDHonouredOrMinted(t *testing.T) {
	var seen string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = RequestID(r.Context()) })
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set(RequestIDHeader, "cloud-abc_123:1")
	w := serve(t, h, nil, req)
	if seen != "cloud-abc_123:1" || w.Header().Get(RequestIDHeader) != seen {
		t.Fatalf("incoming id not honoured: ctx %q header %q", seen, w.Header().Get(RequestIDHeader))
	}
	for _, bad := range []string{"", "has space", "new\nline", `{"x":1}`, strings.Repeat("a", 129)} {
		req := httptest.NewRequest("GET", "/x", nil)
		if bad != "" {
			req.Header[RequestIDHeader] = []string{bad}
		}
		w := serve(t, h, nil, req)
		got := w.Header().Get(RequestIDHeader)
		if got == bad || len(got) != 32 || seen != got {
			t.Errorf("for %q: header %q ctx %q, want a fresh 32-char id", bad, got, seen)
		}
	}
}

func TestAccessLines(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/r/{model}/{name}", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	mux.HandleFunc("GET /ok", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) { time.Sleep(SlowRequest + 50*time.Millisecond) })
	mux.HandleFunc("GET /stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		time.Sleep(SlowRequest + 50*time.Millisecond)
	})
	var b bytes.Buffer
	l := NewJSON(&b, slog.LevelInfo)

	w := serve(t, mux, l, httptest.NewRequest("GET", "/api/v1/r/sales.order/SO-0001", nil))
	got := lines(t, &b)
	if len(got) != 1 {
		t.Fatalf("5xx lines = %d: %s", len(got), b.String())
	}
	m := got[0]
	if m["level"] != "error" || m["request_id"] != w.Header().Get(RequestIDHeader) || m["status"] != float64(500) ||
		m["route"] != "GET /api/v1/r/{model}/{name}" || m["method"] != "GET" || m["component"] != "http" {
		t.Fatalf("5xx line = %v (response id %s)", m, w.Header().Get(RequestIDHeader))
	}
	if strings.Contains(b.String(), "SO-0001") {
		t.Errorf("raw path leaked: %s", b.String())
	}

	b.Reset()
	serve(t, mux, l, httptest.NewRequest("GET", "/ok", nil))
	serve(t, mux, l, httptest.NewRequest("GET", "/stream", nil))
	if b.Len() != 0 {
		t.Fatalf("ordinary or streaming request logged: %s", b.String())
	}
	serve(t, mux, l, httptest.NewRequest("GET", "/slow", nil))
	got = lines(t, &b)
	if len(got) != 1 || got[0]["level"] != "info" || got[0]["msg"] != "slow request" || got[0]["route"] != "GET /slow" {
		t.Fatalf("slow line = %v", got)
	}
}

func TestPanicLogsErrorAndRepanics(t *testing.T) {
	var b bytes.Buffer
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("kaboom") })
	defer func() {
		if recover() == nil {
			t.Fatal("panic swallowed")
		}
		got := lines(t, &b)
		if len(got) != 1 || got[0]["level"] != "error" || got[0]["status"] != float64(500) || got[0]["route"] != "unmatched" {
			t.Fatalf("panic line = %v", got)
		}
	}()
	serve(t, h, NewJSON(&b, slog.LevelInfo), httptest.NewRequest("GET", "/x", nil))
}

// TestRedactionInsideValues covers the audit's F1 and F5 cases: secrets inside structs, maps, DSNs, query strings,
// JSON bodies and URL passwords with a raw "@", and ordinary words that must survive.
func TestRedactionInsideValues(t *testing.T) {
	leak := "fixture" + strings.Repeat("Z5", 5)
	var b bytes.Buffer
	l := NewJSON(&b, slog.LevelInfo)
	l.Info("x", "cfg", struct{ User, Password string }{"u", leak})
	l.Info("x", "m", map[string]string{"api_token": leak})
	l.Info("dsn host=db user=rk password=" + leak + " dbname=rk")
	l.Info("GET /cb?code=1&access_token=" + leak + "&x=2")
	l.Info(`body {"user":"u","client_secret":"` + leak + `"}`)
	l.Info("dial postgres://rk:p@" + leak + "@db/rk")
	l.Info("auth Basic " + leak + "AAAA")
	if strings.Contains(b.String(), leak) {
		t.Fatalf("leaked:\n%s", b.String())
	}
	b.Reset()
	l.Info("switched to Basic subscription; bearer of news")
	if !strings.Contains(b.String(), "Basic subscription") || !strings.Contains(b.String(), "bearer of news") {
		t.Fatalf("ordinary words blanked: %s", b.String())
	}
}

func TestLowercaseWarningIsWarn(t *testing.T) {
	var b bytes.Buffer
	Logf(NewJSON(&b, slog.LevelInfo), CompKernel)("warning: connected as a superuser")
	if m := lines(t, &b)[0]; m["level"] != "warn" {
		t.Fatalf("level = %v", m["level"])
	}
}

func TestInformationalStatusIsNotTheFinalStatus(t *testing.T) {
	var b bytes.Buffer
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusInternalServerError)
	})
	serve(t, h, NewJSON(&b, slog.LevelInfo), httptest.NewRequest("GET", "/x", nil))
	got := lines(t, &b)
	if len(got) != 1 || got[0]["status"] != float64(500) {
		t.Fatalf("lines = %v", got)
	}
}

// TestAuthorizationCredentialsStayRedacted covers re-audit D1: a key=value match must take the credential after a
// Bearer or Basic scheme with it.
func TestAuthorizationCredentialsStayRedacted(t *testing.T) {
	tok := "eyJ" + strings.Repeat("hb6", 6)
	basic := strings.Repeat("dXNlcjpw", 3)
	for _, in := range []string{
		"Authorization: Bearer " + tok,
		"authorization=Bearer " + tok,
		"Authorization: Basic " + basic,
		fmt.Sprintf("%v", http.Header{"Authorization": {"Bearer " + tok}}),
		fmt.Sprintf("%+v", http.Header{"Authorization": {"Basic " + basic}}),
		`{"authorization":"Bearer ` + tok + `"}`,
	} {
		out := RedactString(in)
		if strings.Contains(out, tok) || strings.Contains(out, basic) {
			t.Errorf("%q -> %q", in, out)
		}
	}
}

// TestProseAfterASensitiveWordSurvives covers re-audit D2: kernel warnings keep their text.
func TestProseAfterASensitiveWordSurvives(t *testing.T) {
	for _, in := range []string{
		"secrets: RANGKA_SECRETS_DRIVER=file without RANGKA_SECRETS_PATH",
		"secrets: driver vault without VAULT_ADDR; connector secrets read as unset",
		"0042_email_verification: ok",
		"invalid credential: user not found",
	} {
		if out := RedactString(in); out != in {
			t.Errorf("%q -> %q", in, out)
		}
	}
}

func BenchmarkRedactPlainMessage(b *testing.B) {
	msg := "member records: kopi-senja: 12 synced; plan refresh took 3ms; queue workers 2 idle"
	for i := 0; i < b.N; i++ {
		RedactString(msg)
	}
}
