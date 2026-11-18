package connectors

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/files"
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
	// companyTransports is the guarded pool of company connectors, kept apart from transports: its policy differs
	companyTransports transportCache
	Now               func() time.Time
	Log               func(format string, args ...any)
	// StartFlow queues a flow with an event in qr (the transaction that records the event key; the server wires the
	// queue); nil = events are only audited.
	StartFlow func(ctx context.Context, qr pg.Querier, org, flow string, event map[string]any, uniqueKey string) error
	// Files stores the attachments a mail connector receives (contract 0068 §2); nil = files capability off.
	Files files.Store
	// FilesFull says the plan's file storage is full (contract 0057 §1): received attachments are then listed, not kept.
	FilesFull func() bool
	// MailTLS is the base TLS configuration of mail connections (tests trust their own certificate); nil = system roots.
	MailTLS *tls.Config
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

// client picks the transport: a company connector (contract 0033 §2) always dials through the public policy,
// a catalogue connector follows the platform's policy.
func (rt *Runtime) client(company, secret bool, host string) *http.Client {
	base := rt.HTTP
	if base == nil {
		base = &http.Client{Timeout: 30 * time.Second}
	}
	var c http.Client
	if company {
		c = *rt.Egress.forCompany().guard(base, &rt.companyTransports)
	} else {
		c = *rt.Egress.guard(base, &rt.transports)
	}
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
	if sb, err := store.IsSandbox(ctx, rt.DB, org); err != nil {
		return nil, err
	} else if sb {
		return rt.simulate(ctx, org, connector, action, input)
	}
	c, a, err := rt.Action(connector, action)
	if err != nil {
		return nil, &jobs.Permanent{Err: err}
	}
	if err := rt.gate(ctx, org, connector, action, c); err != nil {
		return nil, rt.fail(ctx, org, connector, action, nil, err)
	}
	if mailOp(a) != "" { // a mail action (contract 0068)
		return rt.callMail(ctx, org, connector, action, c, a, input)
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
	resp, err := rt.client(meta.IsCompanyConnector(connector), req.Secret, req.Host).Do(hr)
	if err != nil {
		if errors.Is(err, ErrEgressRefused) && meta.IsCompanyConnector(connector) {
			return nil, rt.fail(ctx, org, connector, action, req, err) // connector_host_private, not retried
		}
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
	data := map[string]any{"action": action, "status": resp.StatusCode, "output": out}
	if isTesting(ctx) { // the test endpoint audits connector.test; the answer stays out of the audit (contract 0033 §3)
		delete(data, "output")
	}
	if err := store.Audit(ctx, rt.DB, org, "connector:"+connector, "connector.call", "connector/"+connector,
		fmt.Sprintf("%s → HTTP %d", action, resp.StatusCode), data); err != nil {
		rt.logf("connectors: audit: %v", err)
	}
	return out, nil
}

// gate runs before anything is sent, once per call. A catalogue connector only refreshes its OAuth2 token when due.
// A company connector (contract 0033 §3, §4) runs only while its current content is tested (the owner's test run
// excepted), so an edited file sends nothing, not even an OAuth2 refresh; it then needs every required key
// (connector_needs_auth even when the action uses none of the missing ones), and a write action (method not GET) of a
// money-touching connector is sent only after the owner confirmed the writes of that content. Content that is not
// tested and lacks keys answers connector_needs_auth: the keys come first, then the test.
func (rt *Runtime) gate(ctx context.Context, org, connector, action string, c map[string]any) error {
	if !meta.IsCompanyConnector(connector) {
		if err := rt.refreshIfDue(ctx, org, connector); err != nil && !errors.Is(err, errNoOAuth) {
			return err
		}
		return nil
	}
	_, tested, writesOK, err := rt.checked(ctx, org, connector)
	if err != nil {
		return err
	}
	if !tested && !isTesting(ctx) {
		if err := rt.needsAuth(ctx, org, connector, c); err != nil {
			return err
		}
		return ErrUntested
	}
	// tested content (or the owner's test) may refresh an expired OAuth2 token: a missing access token is obtained here
	if err := rt.refreshIfDue(ctx, org, connector); err != nil && !errors.Is(err, errNoOAuth) {
		return err
	}
	if err := rt.needsAuth(ctx, org, connector, c); err != nil {
		return err
	}
	if writesOK {
		return nil
	}
	for _, w := range meta.WriteActions(c) {
		if w == action && rt.Reg().MoneyTouching(connector) {
			return ErrWritesUnconfirmed
		}
	}
	return nil
}

// needsAuth is ErrNeedsAuth when a required key of the connector is not set for org.
func (rt *Runtime) needsAuth(ctx context.Context, org, connector string, c map[string]any) error {
	missing, err := rt.missingAuth(ctx, org, connector, c)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s.%s is not set", ErrNeedsAuth, connector, strings.Join(missing, ", "))
	}
	return nil
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
	case errors.Is(err, ErrEgressRefused) && meta.IsCompanyConnector(connector):
		code = "connector_host_private"
	case errors.Is(err, ErrUntested):
		code = "connector_untested"
	case errors.Is(err, ErrWritesUnconfirmed):
		code = "connector_writes_unconfirmed"
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

// simulate stands in for a call from the ruang coba (contract 0052 §2): nothing is sent. The action and its input
// go to the audit trail and the job result, so the owner sees what would have gone out. The sandbox has no
// connector keys, so the request is not rendered.
func (rt *Runtime) simulate(ctx context.Context, org, connector, action string, input map[string]any) (map[string]any, error) {
	if _, _, err := rt.Action(connector, action); err != nil {
		return nil, &jobs.Permanent{Err: err}
	}
	out := map[string]any{"simulated": true, "connector": connector, "action": action, "input": input}
	if err := store.Audit(ctx, rt.DB, org, "connector:"+connector, "connector.simulated", "connector/"+connector,
		action+" (ruang coba: tidak dikirim)", out); err != nil {
		rt.logf("connectors: audit: %v", err)
	}
	return out, nil
}
