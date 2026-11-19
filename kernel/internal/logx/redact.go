package logx

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"sync"
)

// Redacted replaces a value that must not reach the log.
const Redacted = "[REDACTED]"

// minSecretLen keeps short values (which would match ordinary words) out of the registered-secret replacer.
const minSecretLen = 8

var (
	secretsMu sync.RWMutex
	secrets   = map[string]bool{}
	replacer  *strings.Replacer // nil while no secret is registered
)

// RegisterSecret makes every later log line replace v with [REDACTED], in the message and in string fields. Values
// shorter than 8 characters are ignored.
func RegisterSecret(v string) {
	if len(v) < minSecretLen {
		return
	}
	secretsMu.Lock()
	defer secretsMu.Unlock()
	if secrets[v] {
		return
	}
	secrets[v] = true
	pairs := make([]string, 0, 2*len(secrets))
	for s := range secrets {
		pairs = append(pairs, s, Redacted)
	}
	replacer = strings.NewReplacer(pairs...)
}

// sensitiveKeys are field-name fragments whose values are never logged (case-insensitive substring match).
var sensitiveKeys = []string{
	"password", "passwd", "secret", "token", "authorization", "cookie", "api_key", "apikey",
	"private_key", "master_key", "database_url", "dsn", "email", "phone", "credential",
}

func sensitiveKey(k string) bool {
	k = strings.ToLower(k)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

var (
	// urlPassword is the password part of scheme://user:password@host.
	urlPassword = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://[^\s:/@]*):[^\s@/]*@`)
	// bearer is an Authorization header value.
	bearer = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=\-]{8,}`)
	email  = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
)

// RedactString removes registered secrets, URL passwords, bearer tokens and e-mail addresses from s.
func RedactString(s string) string {
	secretsMu.RLock()
	r := replacer
	secretsMu.RUnlock()
	if r != nil {
		s = r.Replace(s)
	}
	if strings.Contains(s, "://") {
		s = urlPassword.ReplaceAllString(s, "$1:"+Redacted+"@")
	}
	if strings.Contains(s, "@") {
		s = email.ReplaceAllString(s, Redacted)
	}
	return bearer.ReplaceAllString(s, "$1 "+Redacted)
}

func redactAttr(a slog.Attr) slog.Attr {
	if sensitiveKey(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, RedactString(v.String()))
	case slog.KindGroup:
		g := v.Group()
		out := make([]slog.Attr, len(g))
		for i, x := range g {
			out[i] = redactAttr(x)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}
	case slog.KindAny:
		switch x := v.Any().(type) {
		case error:
			return slog.String(a.Key, RedactString(x.Error()))
		case []byte:
			return slog.String(a.Key, RedactString(string(x)))
		case interface{ String() string }:
			return slog.String(a.Key, RedactString(x.String()))
		default:
			// Structs, maps and slices can hold anything; render them as text so the scrubbing still applies.
			return slog.String(a.Key, RedactString(slog.AnyValue(x).String()))
		}
	}
	return slog.Attr{Key: a.Key, Value: v}
}

// handler adds the context's fields to each record and redacts the message and every field before next sees them.
type handler struct{ next slog.Handler }

// Wrap returns h behind the context-field and redaction layer. NewJSON already uses it; tests and custom sinks can
// wrap their own handler.
func Wrap(h slog.Handler) slog.Handler { return &handler{next: h} }

func (h *handler) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }

func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, RedactString(r.Message), r.PC)
	for _, a := range Fields(ctx) {
		out.AddAttrs(redactAttr(a))
	}
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(redactAttr(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	red := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		red[i] = redactAttr(a)
	}
	return &handler{next: h.next.WithAttrs(red)}
}

func (h *handler) WithGroup(name string) slog.Handler { return &handler{next: h.next.WithGroup(name)} }
