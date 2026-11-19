package logx

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// RequestIDHeader carries the request id in and out (the Rangka Cloud router sets it on the way in).
const RequestIDHeader = "X-Request-Id"

// SlowRequest is the duration above which a successful request gets an access line (a variable only for tests).
var SlowRequest = time.Second

const maxRequestIDLen = 128

// validRequestID accepts 1-128 characters from [A-Za-z0-9._:-], so a caller cannot inject text into the log.
func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == ':' || c == '-') {
			return false
		}
	}
	return true
}

// NewRequestID returns 16 random bytes as 32 hex characters.
func NewRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// HTTP gives every request an id and logs the requests worth a line. The id is the incoming X-Request-Id when it is
// well formed, else a new one; it is echoed in the response header and put on the request context (WithRequestID).
//
// With a non-nil logger, a request that ends with status 5xx (or panics) logs one line at error, and a request that
// takes longer than SlowRequest logs one line at info. Ordinary requests are not logged. Event streams and hijacked
// connections are long by design and get no slow line. The route is the ServeMux pattern, never the raw path.
func HTTP(next http.Handler, l *slog.Logger) http.Handler {
	if l != nil {
		l = l.With(slog.String("component", CompHTTP))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !validRequestID(id) {
			id = NewRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		r = r.WithContext(WithRequestID(r.Context(), id))
		if l == nil {
			next.ServeHTTP(w, r)
			return
		}
		rec := &recorder{ResponseWriter: w}
		start := time.Now()
		defer func() {
			p := recover()
			if p != nil && p == http.ErrAbortHandler {
				panic(p)
			}
			status := rec.statusCode()
			if p != nil {
				status = http.StatusInternalServerError
			}
			d := time.Since(start)
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("route", route(r)),
				slog.Int("status", status),
				slog.Int64("duration_ms", d.Milliseconds()),
			}
			switch {
			case p != nil:
				l.LogAttrs(r.Context(), slog.LevelError, "request panicked", attrs...)
				panic(p)
			case status >= 500:
				l.LogAttrs(r.Context(), slog.LevelError, "request failed", attrs...)
			case d > SlowRequest && !rec.hijacked && !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream"):
				l.LogAttrs(r.Context(), slog.LevelInfo, "slow request", attrs...)
			}
		}()
		next.ServeHTTP(rec, r)
	})
}

// route is the pattern ServeMux matched (Go 1.22 sets it on the request it serves), or "unmatched". The raw path is
// never used: it carries record names and ids.
func route(r *http.Request) string {
	if r.Pattern != "" {
		return r.Pattern
	}
	return "unmatched"
}

// recorder notes the status code and keeps Flush and Hijack working for streams and upgrades.
type recorder struct {
	http.ResponseWriter
	status   int
	hijacked bool
}

func (w *recorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *recorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *recorder) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *recorder) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("logx: the response writer does not support hijacking")
	}
	w.hijacked = true
	return h.Hijack()
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *recorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }
