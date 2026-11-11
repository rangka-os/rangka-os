// Package connectors runs connector.rk files (contract 0002 §A, with contract 0017 §5–§8 for secrets): outbound
// actions rendered in memory at send time, inbound webhooks verified against the org's secrets, and a scrubber that
// removes every active secret value of an (org, connector) before anything is written.
//
// Deterministic only: no LLM is in this path. A job stores the action, input, org and connector, never the rendered
// request.
package connectors

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/secrets"
)

// Error codes (contract 0017 §2, §5).
var (
	ErrNeedsAuth    = errors.New("connector_needs_auth")
	ErrHostRefused  = errors.New("secret_host_refused")
	ErrUnknown      = errors.New("unknown_connector_action")
	ErrNotInstalled = errors.New("connector_not_installed")
)

// Rendered is one request, in memory only. It is dropped after the call.
type Rendered struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    any // nil = no body; sent as JSON
	// BodyBytes is the JSON body exactly as sent (nil = none). A signed request (auth.sign) hashes these bytes, so
	// Call sends them as they are and never marshals the body again.
	BodyBytes []byte
	Secret    bool
	Host      string
	scrub     *Scrubber
}

// Resolver reads a connector's settings and auth values for an org.
type Resolver struct {
	Reg     func() *meta.Registry
	Secrets *secrets.Service
}

var placeholder = regexp.MustCompile(`\{([a-z_][a-z0-9_]*(?:\.[a-z_][a-z0-9_]*)?)\}`)

// connector returns the connector's data and whether it exists.
func (r *Resolver) connector(name string) (map[string]any, bool) {
	c := r.Reg().Connectors[name]
	if c == nil {
		return nil, false
	}
	return meta.AsMap(c.Data), true
}

// Action returns an action's definition.
func (r *Resolver) Action(connector, action string) (map[string]any, map[string]any, error) {
	c, ok := r.connector(connector)
	if !ok {
		return nil, nil, fmt.Errorf("%w: %s.%s", ErrUnknown, connector, action)
	}
	for _, x := range meta.AsList(c["actions"]) {
		if a := meta.AsMap(x); meta.Str(a, "name") == action {
			return c, a, nil
		}
	}
	return nil, nil, fmt.Errorf("%w: %s.%s", ErrUnknown, connector, action)
}

// settings evaluates the connector's settings defaults (CEL).
func settings(c map[string]any) map[string]any {
	out := map[string]any{}
	for _, x := range meta.AsList(c["settings"]) {
		s := meta.AsMap(x)
		n := meta.Str(s, "name")
		src, ok := s["default"].(string)
		if n == "" || !ok {
			continue
		}
		if v, err := cel.Eval(src, &cel.Env{Vars: func(string) (any, bool) { return nil, false }}); err == nil {
			out[n] = cel.Unwrap(v)
		}
	}
	return out
}

func secretFields(c map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, x := range meta.AsList(meta.AsMap(c["auth"])["fields"]) {
		f := meta.AsMap(x)
		if f["secret"] == true {
			out[meta.Str(f, "name")] = true
		}
	}
	return out
}

// pageURLKey carries the next page's URL (a poll following next:) into Render.
type pageURLKey struct{}

func varsEnv(vars map[string]any) *cel.Env {
	return &cel.Env{Vars: func(n string) (any, bool) { v, ok := vars[n]; return v, ok }}
}

// Render builds the request of an action for one org. Every {auth.x} is read from the secrets driver; a required
// auth field that is unset is ErrNeedsAuth. A request that carries a secret must use https and go to a declared host
// (hosts:, else the hosts the connector's own URLs name), or it is ErrHostRefused (contract 0017 §5).
func (r *Resolver) Render(ctx context.Context, org, connector, action string, input map[string]any) (*Rendered, error) {
	c, a, err := r.Action(connector, action)
	if err != nil {
		return nil, err
	}
	req := meta.AsMap(a["request"])
	set := settings(c)
	in := map[string]any{}
	for k, v := range input {
		in[k] = v
	}
	// input defaults (CEL over settings and input)
	for _, x := range meta.AsList(a["input"]) {
		f := meta.AsMap(x)
		n := meta.Str(f, "name")
		if _, ok := in[n]; ok || n == "" {
			continue
		}
		if src, ok := f["default"].(string); ok {
			if v, err := cel.Eval(src, varsEnv(map[string]any{"settings": set, "input": in})); err == nil {
				in[n] = cel.Unwrap(v)
			}
		} else if f["required"] == true {
			return nil, fmt.Errorf("input %s is required", n)
		}
	}
	scrub, err := r.Scrubber(ctx, org, connector)
	if err != nil {
		return nil, err
	}
	sec := secretFields(c)
	authVals := map[string]string{}
	carries := false
	var lookupErr error
	lookup := func(name string, inURL, inQuery bool) (any, bool) {
		head, rest, dotted := strings.Cut(name, ".")
		var v any
		var ok bool
		switch {
		case dotted && head == "auth":
			val, have, err := r.authValue(ctx, org, connector, rest)
			if err != nil {
				lookupErr = err
				return nil, false
			}
			if !have {
				if fieldRequired(c, rest) {
					lookupErr = fmt.Errorf("%w: %s.%s is not set", ErrNeedsAuth, connector, rest)
				}
				return nil, false
			}
			if sec[rest] {
				carries = true
			}
			authVals[rest] = val
			v, ok = val, true
		case dotted && head == "input":
			v, ok = in[rest]
		case dotted && head == "settings":
			v, ok = set[rest]
		case !dotted:
			v, ok = set[name]
		}
		if !ok || v == nil {
			return nil, false
		}
		if inURL {
			text := fmt.Sprint(v)
			if inQuery {
				return url.QueryEscape(text), true
			}
			return url.PathEscape(text), true
		}
		return v, true
	}
	render := func(x any, inURL bool) any { return template(x, inURL, lookup) }
	rawURL, _ := req["url"].(string)
	out := &Rendered{Method: strings.ToUpper(meta.Str(req, "method")), Headers: map[string]string{}, scrub: scrub}
	out.URL, _ = render(rawURL, true).(string)
	hdrs := meta.AsMap(req["headers"])
	hkeys := make([]string, 0, len(hdrs))
	for k := range hdrs {
		hkeys = append(hkeys, k)
	}
	sort.Strings(hkeys)
	for _, k := range hkeys {
		if v := render(hdrs[k], false); v != nil && fmt.Sprint(v) != "" {
			out.Headers[k] = fmt.Sprint(v)
		}
	}
	if q := meta.AsMap(req["query"]); len(q) > 0 {
		u, err := url.Parse(out.URL)
		if err != nil {
			return nil, fmt.Errorf("url: %w", err)
		}
		vals := u.Query()
		keys := make([]string, 0, len(q))
		for k := range q {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if v := render(q[k], false); v != nil && fmt.Sprint(v) != "" {
				vals.Set(k, fmt.Sprint(v))
			}
		}
		u.RawQuery = vals.Encode()
		out.URL = u.String()
	}
	if next, ok := ctx.Value(pageURLKey{}).(string); ok && next != "" {
		// a poll following the answer's next page (next_url): the vendor's URL replaces the rendered one, on the
		// same host only; its scheme stays the action's (a vendor that links http:// keeps https)
		orig, err1 := url.Parse(out.URL)
		nu, err2 := url.Parse(next)
		if err1 == nil && err2 == nil && nu.Host == "" && strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") {
			nu = orig.ResolveReference(nu) // a path on the action's host
		}
		if err1 != nil || err2 != nil || nu.Host == "" {
			return nil, fmt.Errorf("next page %q is not a URL", scrub.String(next))
		}
		if !strings.EqualFold(nu.Hostname(), orig.Hostname()) || nu.Port() != orig.Port() {
			return nil, fmt.Errorf("%w: next page of %s.%s on %s, not %s", ErrHostRefused, connector, action, nu.Hostname(), orig.Hostname())
		}
		nu.Scheme = orig.Scheme
		nu.User = nil
		out.URL = nu.String()
	}
	if b, ok := req["body"]; ok {
		out.Body = render(b, false)
	}
	if lookupErr != nil {
		return nil, lookupErr
	}
	// auth.type basic: the first secret field is the user name (Xendit: the secret key, empty password)
	if meta.Str(meta.AsMap(c["auth"]), "type") == "basic" && out.Headers["Authorization"] == "" {
		for _, x := range meta.AsList(meta.AsMap(c["auth"])["fields"]) {
			f := meta.AsMap(x)
			if f["secret"] != true {
				continue
			}
			n := meta.Str(f, "name")
			v, have, err := r.authValue(ctx, org, connector, n)
			if err != nil {
				return nil, err
			}
			if !have {
				return nil, fmt.Errorf("%w: %s.%s is not set", ErrNeedsAuth, connector, n)
			}
			basic := base64.StdEncoding.EncodeToString([]byte(v + ":"))
			out.Headers["Authorization"] = "Basic " + basic
			scrub.Add(n, basic)
			carries = true
			break
		}
	}
	// oauth2: the access token goes in the Authorization header unless the action sets one
	if meta.Str(meta.AsMap(c["auth"]), "type") == "oauth2" && out.Headers["Authorization"] == "" {
		v, have, err := r.authValue(ctx, org, connector, "access_token")
		if err != nil {
			return nil, err
		}
		if !have {
			return nil, fmt.Errorf("%w: %s.access_token is not set", ErrNeedsAuth, connector)
		}
		out.Headers["Authorization"] = "Bearer " + v
		carries = true
	}
	u, err := url.Parse(out.URL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("url %q does not render to an absolute URL", scrub.String(out.URL))
	}
	if out.Body != nil && out.Method != "GET" {
		b, err := json.Marshal(out.Body)
		if err != nil {
			return nil, fmt.Errorf("body: %w", err)
		}
		out.BodyBytes = b
	}
	// auth.sign: an HMAC over canonical lines (DOKU Non-SNAP), contract 0002 refinement of 11 Nov 2026
	if spec := parseSign(meta.AsMap(c["auth"])["sign"]); spec != nil {
		authFn := func(field string) (string, bool, error) {
			v, have, err := r.authValue(ctx, org, connector, field)
			if err != nil || !have {
				return "", false, err
			}
			if sec[field] {
				carries = true
				scrub.Add(field, v)
			}
			return v, true, nil
		}
		if err := signRequest(spec, out, u.EscapedPath(), authFn, set); err != nil {
			return nil, err
		}
	}
	out.Host = strings.ToLower(u.Hostname())
	out.Secret = carries
	if carries {
		if u.Scheme != "https" {
			return nil, fmt.Errorf("%w: %s.%s sends a secret over %s", ErrHostRefused, connector, action, u.Scheme)
		}
		if !allowedHosts(c)[out.Host] {
			return nil, fmt.Errorf("%w: %s.%s may not send a secret to %s", ErrHostRefused, connector, action, out.Host)
		}
	}
	return out, nil
}

func fieldRequired(c map[string]any, field string) bool {
	for _, x := range meta.AsList(meta.AsMap(c["auth"])["fields"]) {
		f := meta.AsMap(x)
		if meta.Str(f, "name") == field {
			return f["required"] != false
		}
	}
	return true
}

// allowedHosts is hosts:, else the hosts the connector's own action URLs name after templating with the settings'
// defaults (an input-templated host is never allowed to carry a secret).
func allowedHosts(c map[string]any) map[string]bool {
	out := map[string]bool{}
	declared, ok := c["hosts"].([]any)
	if !ok {
		declared, ok = meta.AsMap(c["experimental"])["hosts"].([]any)
	}
	if ok {
		for _, h := range declared {
			if s, ok := h.(string); ok {
				out[strings.ToLower(s)] = true
			}
		}
		return out
	}
	set := settings(c)
	for _, x := range meta.AsList(c["actions"]) {
		raw := meta.Str(meta.AsMap(meta.AsMap(x)["request"]), "url")
		for k, v := range set {
			raw = strings.ReplaceAll(raw, "{"+k+"}", fmt.Sprint(v))
		}
		h := meta.URLHost(raw)
		if h != "" && !strings.ContainsAny(h, "{$") {
			out[h] = true
		}
	}
	return out
}

// template fills {placeholders}. A string that is exactly one placeholder takes the raw value (a list stays a
// list); a placeholder without a value makes the whole string (or map entry) absent.
func template(x any, inURL bool, lookup func(name string, inURL, inQuery bool) (any, bool)) any {
	switch t := x.(type) {
	case string:
		if m := placeholder.FindStringSubmatch(t); m != nil && m[0] == t && !inURL {
			v, ok := lookup(m[1], false, false)
			if !ok {
				return nil
			}
			return v
		}
		missing := false
		q := strings.Index(t, "?")
		out := placeholder.ReplaceAllStringFunc(t, func(s string) string {
			pos := strings.Index(t, s)
			v, ok := lookup(s[1:len(s)-1], inURL, inURL && q >= 0 && pos > q)
			if !ok {
				missing = missing || !inURL
				return ""
			}
			return fmt.Sprint(v)
		})
		if missing {
			return nil
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, v := range t {
			if r := template(v, false, lookup); r != nil {
				out[k] = r
			}
		}
		return out
	case []any:
		out := []any{}
		for _, v := range t {
			if r := template(v, false, lookup); r != nil {
				out = append(out, r)
			}
		}
		return out
	}
	return x
}

func (r *Resolver) authValue(ctx context.Context, org, connector, field string) (string, bool, error) {
	if r.Secrets == nil {
		return "", false, nil
	}
	return r.Secrets.Value(ctx, org, connector, field)
}
