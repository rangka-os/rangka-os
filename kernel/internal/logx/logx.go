// Package logx is the kernel's structured logging: a log/slog setup with a JSON or text format, context fields
// (request_id, flow, job_id, ...), redaction of secrets and PII, and a request-id middleware. The log contract that
// Rangka Cloud parses is docs/logging.md.
//
// Call sites keep the older func(format string, args ...any) signature through Logf, so subsystems can move to
// slog one at a time.
package logx

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Formats accepted by --log-format and RANGKA_LOG_FORMAT.
const (
	FormatAuto = "auto" // json when the output is not a terminal, text on a terminal
	FormatJSON = "json"
	FormatText = "text"
)

// Component names (docs/logging.md). Other values may appear; these are the ones Rangka Cloud groups by.
const (
	CompKernel    = "kernel"
	CompHTTP      = "http"
	CompQueue     = "queue"
	CompScheduler = "scheduler"
	CompFlow      = "flow"
	CompConnector = "connector"
	CompMigrate   = "migrate"
	CompRealtime  = "realtime"
)

// ResolveFormat turns a --log-format value into json or text. auto (or "") is json unless out is a terminal.
func ResolveFormat(value string, out *os.File) (string, error) {
	switch v := strings.ToLower(strings.TrimSpace(value)); v {
	case FormatJSON, FormatText:
		return v, nil
	case "", FormatAuto:
		if isTerminal(out) {
			return FormatText, nil
		}
		return FormatJSON, nil
	default:
		return "", fmt.Errorf("--log-format: %q is not one of auto, json, text", value)
	}
}

func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// NewJSON returns a logger that writes one JSON object per line to w: time (UTC, RFC 3339), level (lowercase), msg,
// then the record's fields. Every record passes the redacting handler first.
func NewJSON(w io.Writer, level slog.Leveler) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level, ReplaceAttr: replaceBuiltins})
	return slog.New(Wrap(h))
}

func replaceBuiltins(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a
	}
	switch a.Key {
	case slog.TimeKey:
		return slog.Time(slog.TimeKey, a.Value.Time().UTC())
	case slog.LevelKey:
		return slog.String(slog.LevelKey, strings.ToLower(a.Value.Any().(slog.Level).String()))
	}
	return a
}

// Logf adapts a logger to the kernel's func(format, args...) signature for one component. The level is warn when
// any argument is an error or the message starts with "WARNING", else info; call sites that need error pick it
// with slog directly.
func Logf(l *slog.Logger, component string) func(format string, args ...any) {
	l = l.With(slog.String("component", component))
	return func(format string, args ...any) {
		level := slog.LevelInfo
		for _, a := range args {
			if _, ok := a.(error); ok {
				level = slog.LevelWarn
				break
			}
		}
		msg := fmt.Sprintf(format, args...)
		if strings.HasPrefix(msg, "WARNING") {
			level = slog.LevelWarn
		}
		l.Log(context.Background(), level, msg)
	}
}

// SkipFormats wraps f so that calls with one of the given format strings are dropped.
func SkipFormats(f func(string, ...any), formats ...string) func(string, ...any) {
	return func(format string, args ...any) {
		for _, s := range formats {
			if format == s {
				return
			}
		}
		f(format, args...)
	}
}

type ctxKey struct{}

// With returns ctx carrying extra log fields; every log call made with that context (slog's *Context methods)
// includes them. Later values for the same key win.
func With(ctx context.Context, attrs ...slog.Attr) context.Context {
	prev, _ := ctx.Value(ctxKey{}).([]slog.Attr)
	next := make([]slog.Attr, 0, len(prev)+len(attrs))
	for _, p := range prev {
		if !hasKey(attrs, p.Key) {
			next = append(next, p)
		}
	}
	return context.WithValue(ctx, ctxKey{}, append(next, attrs...))
}

func hasKey(attrs []slog.Attr, key string) bool {
	for _, a := range attrs {
		if a.Key == key {
			return true
		}
	}
	return false
}

// Fields returns the log fields carried by ctx.
func Fields(ctx context.Context) []slog.Attr {
	if ctx == nil {
		return nil
	}
	a, _ := ctx.Value(ctxKey{}).([]slog.Attr)
	return a
}

// WithRequestID puts the request id on ctx (field request_id).
func WithRequestID(ctx context.Context, id string) context.Context {
	return With(ctx, slog.String("request_id", id))
}

// RequestID returns the request id on ctx, or "".
func RequestID(ctx context.Context) string {
	for _, a := range Fields(ctx) {
		if a.Key == "request_id" {
			return a.Value.String()
		}
	}
	return ""
}
