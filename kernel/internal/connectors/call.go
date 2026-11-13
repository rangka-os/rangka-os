package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/jobs"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Runtime sends connector actions and serves connector webhooks.
type Runtime struct {
	Resolver
	DB   *pg.Pool
	HTTP *http.Client // nil = a client with a 30 s timeout
	// Egress is the platform's egress policy (RANGKA_CONNECTOR_EGRESS); the zero value allows every address
	Egress     Egress
	transports transportCache
	Now        func() time.Time
	Log        func(format string, args ...any)
	// StartFlow queues a flow with an event in qr (the transaction that records the event key; the server wires the
	// queue); nil = events are only audited.
	StartFlow func(ctx context.Context, qr pg.Querier, org, flow string, event map[string]any, uniqueKey string) error
}

// maxResponse is how much of a vendor's answer the kernel reads; stored responses are cut to 4 KB after scrubbing.
const (
	maxResponse = 1 << 20
	maxStored   = 4 << 10
)

// CallError is a failed call. Its message is already scrubbed.
type CallError struct {
	Status    int
	Message   string
	Retryable bool
}

func (e *CallError) Error() string { return e.Message }

func (rt *Runtime) client(secret bool, host string) *http.Client {
	base := rt.HTTP
	if base == nil {
		base = &http.Client{Timeout: 30 * time.Second}
	}
	c := *rt.Egress.guard(base, &rt.transports)
	// a request that carries a secret follows redirects only to the same host (§5)
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if secret && (req.URL.Hostname() != host || req.URL.Scheme != "https") {
			return fmt.Errorf("%w: redirect to %s", ErrHostRefused, req.URL.Hostname())
		}
		return nil
	}
	return &c
}

// Call renders and sends one action for an org, maps its output and audits it (scrubbed). The rendered request lives
// only in this call.
func (rt *Runtime) Call(ctx context.Context, org, connector, action string, input map[string]any) (map[string]any, error) {
	if err := rt.refreshIfDue(ctx, org, connector); err != nil && !errors.Is(err, errNoOAuth) {
		return nil, rt.fail(ctx, org, connector, action, nil, err)
	}
	_, a, err := rt.Action(connector, action)
	if err != nil {
		return nil, &jobs.Permanent{Err: err}
	}
	req, err := rt.Render(ctx, org, connector, action, input)
	if err != nil {
		return nil, rt.fail(ctx, org, connector, action, nil, err)
	}
	var body io.Reader
	if req.BodyBytes != nil {
		body = bytes.NewReader(req.BodyBytes)
	}
	hr, err := http.NewRequestWithContext(ctx, req.Method, req.URL, body)
	if err != nil {
		return nil, rt.fail(ctx, org, connector, action, req, err)
	}
	for k, v := range req.Headers {
		hr.Header.Set(k, v)
	}
	if body != nil {
		hr.Header.Set("Content-Type", "application/json")
	}
	hr.Header.Set("Accept", "application/json")
	resp, err := rt.client(req.Secret, req.Host).Do(hr)
	if err != nil {
		return nil, rt.fail(ctx, org, connector, action, req, &CallError{Message: req.scrub.String(err.Error()), Retryable: !errors.Is(err, ErrHostRefused) && !errors.Is(err, ErrEgressRefused)})
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	text := req.scrub.String(string(raw))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, rt.fail(ctx, org, connector, action, req, &CallError{Status: resp.StatusCode, Retryable: retryable(a, resp.StatusCode),
			Message: fmt.Sprintf("%s.%s: HTTP %d: %s", connector, action, resp.StatusCode, cut(text))})
	}
	var parsed any
	if len(bytes.TrimSpace(raw)) > 0 && json.Unmarshal(raw, &parsed) != nil {
		parsed = text
	}
	out := mapOutput(a, parsed)
	out = req.scrub.Value(out).(map[string]any)
	if err := store.Audit(ctx, rt.DB, org, "connector:"+connector, "connector.call", "connector/"+connector,
		fmt.Sprintf("%s → HTTP %d", action, resp.StatusCode), map[string]any{"action": action, "status": resp.StatusCode, "output": out}); err != nil {
		rt.logf("connectors: audit: %v", err)
	}
	return out, nil
}

// retryable follows the action's retry.on; without it, 429 and 5xx are retried.
func retryable(a map[string]any, status int) bool {
	on := meta.AsList(meta.AsMap(a["retry"])["on"])
	if len(on) == 0 {
		return status == 429 || status >= 500
	}
	for _, x := range on {
		if fmt.Sprint(x) == fmt.Sprint(status) {
			return true
		}
	}
	return false
}

// MaxAttempts is retry.max + 1 (at least 1).
func MaxAttempts(a map[string]any) int {
	switch n := meta.AsMap(a["retry"])["max"].(type) {
	case int64:
		return int(n) + 1
	case float64:
		return int(n) + 1
	}
	return 1
}

func cut(s string) string {
	if len(s) > maxStored {
		return s[:maxStored] + "…"
	}
	return s
}

// mapOutput reads output: (name → CEL over the answer's fields); without output: the whole answer is kept.
func mapOutput(a map[string]any, parsed any) map[string]any {
	spec := meta.AsMap(a["output"])
	obj := meta.AsMap(parsed)
	if len(spec) == 0 {
		if obj != nil {
			return obj
		}
		return map[string]any{"response": parsed}
	}
	vars := map[string]any{"response": parsed}
	for k, v := range obj {
		vars[k] = v
	}
	out := map[string]any{}
	for k, x := range spec {
		src, ok := x.(string)
		if !ok {
			continue
		}
		if v, err := cel.Eval(src, varsEnv(vars)); err == nil {
			out[k] = cel.Unwrap(v)
		} else {
			out[k] = nil
		}
	}
	return out
}

// fail audits connector.error (scrubbed) and returns the error the queue sees: a Permanent one when a retry cannot
// help (keys missing, a refused host, a status the action does not retry).
func (rt *Runtime) fail(ctx context.Context, org, connector, action string, req *Rendered, err error) error {
	scrub := (*Scrubber)(nil)
	if req != nil {
		scrub = req.scrub
	} else if s, e := rt.Scrubber(ctx, org, connector); e == nil {
		scrub = s
	}
	msg := scrub.String(err.Error())
	code := "call_failed"
	var ce *CallError
	permanent := true
	switch {
	case errors.Is(err, ErrNeedsAuth):
		code = "connector_needs_auth"
	case errors.Is(err, ErrHostRefused):
		code = "secret_host_refused"
	case errors.As(err, &ce):
		permanent = !ce.Retryable
	}
	if aerr := store.Audit(context.WithoutCancel(ctx), rt.DB, org, "connector:"+connector, "connector.error", "connector/"+connector,
		action+": "+msg, map[string]any{"action": action, "code": code}); aerr != nil {
		rt.logf("connectors: audit: %v", aerr)
	}
	out := &CallError{Message: code + ": " + msg}
	if ce != nil {
		out.Status = ce.Status
	}
	if permanent {
		return &jobs.Permanent{Err: out}
	}
	return out
}

func (rt *Runtime) logf(format string, args ...any) {
	if rt.Log != nil {
		rt.Log(format, args...)
	}
}

func (rt *Runtime) now() time.Time {
	if rt.Now != nil {
		return rt.Now()
	}
	return time.Now()
}

// ResolvePort turns a call target into (connector, action): connector.action, or a port (payment.qris.create) that
// system.rk ports: maps to a connector which provides: it (contract 0002 §B).
func ResolvePort(reg *meta.Registry, call string) (string, string, error) {
	conn, action, ok := strings.Cut(call, ".")
	if !ok {
		return "", "", fmt.Errorf("%w: %s", ErrUnknown, call)
	}
	if reg.Connectors[conn] != nil {
		return conn, action, nil
	}
	i := strings.LastIndex(call, ".")
	port := call[:i]
	name, _ := meta.AsMap(reg.SystemConfig("ports"))[port].(string)
	c := reg.Connectors[name]
	if c == nil {
		return "", "", fmt.Errorf("%w: %s (no connector, and system.rk ports has no %s)", ErrUnknown, call, port)
	}
	data := meta.AsMap(c.Data)
	pv := meta.AsMap(data["provides"])
	if pv == nil {
		pv = meta.AsMap(meta.AsMap(data["experimental"])["provides"])
	}
	act, _ := pv[call].(string)
	if act == "" {
		return "", "", fmt.Errorf("%w: connector %s does not provide %s", ErrUnknown, name, call)
	}
	return name, act, nil
}
