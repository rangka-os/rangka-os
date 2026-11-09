package connectors

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// Scrubber replaces every active secret value of an (org, connector) with [redacted:<field>], in its plain, base64,
// URL-encoded and Bearer forms (contract 0017 §6). It runs before anything is written: audit entries, job errors,
// dead letters, log lines and stored responses. Scrubbing by value catches a token a vendor echoes back in an error,
// which key-name redaction misses.
type Scrubber struct {
	pairs [][2]string // form → replacement, longest first
}

// Add registers one more form of a field's value (a Basic header the kernel built, say).
func (s *Scrubber) Add(field, form string) {
	if len(form) < 4 {
		return
	}
	s.pairs = append(s.pairs, [2]string{form, "[redacted:" + field + "]"})
	sort.SliceStable(s.pairs, func(i, j int) bool { return len(s.pairs[i][0]) > len(s.pairs[j][0]) })
}

func (s *Scrubber) addValue(field, v string) {
	if len(v) < 4 { // a 1–3 character "secret" would scrub every word it appears in
		return
	}
	for _, form := range []string{v, base64.StdEncoding.EncodeToString([]byte(v)), base64.RawURLEncoding.EncodeToString([]byte(v)),
		url.QueryEscape(v), url.PathEscape(v), base64.StdEncoding.EncodeToString([]byte(v + ":"))} {
		s.Add(field, form)
	}
}

// String scrubs a text.
func (s *Scrubber) String(text string) string {
	if s == nil {
		return text
	}
	for _, p := range s.pairs {
		text = strings.ReplaceAll(text, p[0], p[1])
	}
	return text
}

// Value scrubs every string inside a JSON-like value.
func (s *Scrubber) Value(v any) any {
	if s == nil {
		return v
	}
	switch t := v.(type) {
	case string:
		return s.String(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = s.Value(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = s.Value(x)
		}
		return out
	case nil, bool, int, int64, float64:
		return v
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var back any
	if json.Unmarshal([]byte(s.String(string(b))), &back) != nil {
		return nil
	}
	return back
}

// Scrubber collects the active secret values of an org's connector: every version still accepted (§8), so an old
// token echoed during a rotation is caught too.
func (r *Resolver) Scrubber(ctx context.Context, org, connector string) (*Scrubber, error) {
	s := &Scrubber{}
	c, ok := r.connector(connector)
	if !ok || r.Secrets == nil {
		return s, nil
	}
	for _, x := range meta.AsList(meta.AsMap(c["auth"])["fields"]) {
		f := meta.AsMap(x)
		if f["secret"] != true {
			continue
		}
		n := meta.Str(f, "name")
		vals, err := r.Secrets.Accepted(ctx, org, connector, n)
		if err != nil {
			return nil, err
		}
		for _, v := range vals {
			s.addValue(n, v)
		}
	}
	return s, nil
}
