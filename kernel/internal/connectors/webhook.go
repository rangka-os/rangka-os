package connectors

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"math/rand/v2"
	"net/http"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// maxHookBody is the inbound body limit (contract 0017 §7).
const maxHookBody = 1 << 20

// HookResult is what the webhook route answers.
type HookResult struct {
	Status int
	Body   string // the challenge echo, or a short message
	Events int    // events emitted
	Dupes  int    // events dropped as duplicates
}

// trigger returns a webhook trigger's definition.
func (rt *Runtime) trigger(connector, name string) (map[string]any, map[string]any, bool) {
	c, ok := rt.connector(connector)
	if !ok {
		return nil, nil, false
	}
	for _, x := range meta.AsList(c["triggers"]) {
		t := meta.AsMap(x)
		if meta.Str(t, "name") == name && meta.Str(t, "via") == "webhook" {
			return c, t, true
		}
	}
	return nil, nil, false
}

// authAccepted reads auth.<field> from a verify expression and returns every accepted version (§8).
func (rt *Runtime) authAccepted(ctx context.Context, org, connector, ref string) ([]string, error) {
	field := strings.TrimPrefix(strings.TrimSpace(ref), "auth.")
	if rt.Secrets == nil {
		return nil, nil
	}
	return rt.Secrets.Accepted(ctx, org, connector, field)
}

// HandleHook serves POST (and the GET challenge) /hooks/<org>/<connector>/<trigger>. The org's secrets verify the
// request; the signature is computed over the raw body bytes and compared in constant time. A declared check that
// fails or a missing signature is 401. Each emitted event starts the flows with on: <connector>.<trigger> once:
// a duplicate (same event body) creates nothing.
func (rt *Runtime) HandleHook(ctx context.Context, org, connector, trigger string, r *http.Request) HookResult {
	c, t, ok := rt.trigger(connector, trigger)
	if !ok {
		return HookResult{Status: 404, Body: "no webhook trigger " + connector + "." + trigger}
	}
	_ = c
	verify := meta.AsMap(t["verify"])
	if r.Method == http.MethodGet {
		return rt.challenge(ctx, org, connector, verify, r)
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxHookBody+1))
	if err != nil {
		return HookResult{Status: 400, Body: "could not read the body"}
	}
	if len(raw) > maxHookBody {
		return HookResult{Status: 413, Body: "body over 1 MB"}
	}
	if res, ok := rt.verify(ctx, org, connector, verify, r, raw); !ok {
		store.Audit(context.WithoutCancel(ctx), rt.DB, org, "connector:"+connector, "connector.error", "connector/"+connector,
			trigger+": webhook refused ("+res.Body+")", map[string]any{"trigger": trigger, "code": "webhook_refused"})
		return res
	}
	var body any
	if err := json.Unmarshal(raw, &body); err != nil {
		return HookResult{Status: 400, Body: "body is not JSON"}
	}
	scrub, _ := rt.Scrubber(ctx, org, connector)
	items := forEach(body, meta.Str(t, "for_each"))
	res := HookResult{Status: 200, Body: "ok"}
	flows := rt.flowsOn(connector + "." + trigger)
	for _, it := range items {
		ev := emit(meta.AsMap(t["emit"]), body, it)
		ev = scrub.Value(ev).(map[string]any)
		key := eventKey(org, connector, trigger, ev)
		fresh, err := rt.remember(ctx, org, connector, trigger, key)
		if err != nil {
			rt.logf("connectors: dedupe: %v", err)
			return HookResult{Status: 500, Body: "could not record the event"}
		}
		if !fresh {
			res.Dupes++
			continue
		}
		res.Events++
		store.Audit(ctx, rt.DB, org, "connector:"+connector, "connector.event", "connector/"+connector, trigger, map[string]any{"trigger": trigger, "event": ev})
		for _, f := range flows {
			if rt.StartFlow == nil {
				continue
			}
			if err := rt.StartFlow(ctx, org, f, ev, "hook:"+key+":"+f); err != nil {
				rt.logf("connectors: start flow %s: %v", f, err)
				return HookResult{Status: 500, Body: "could not start the flow"}
			}
		}
	}
	return res
}

// flowsOn lists the installed flows with on: <connector>.<trigger>, sorted.
func (rt *Runtime) flowsOn(on string) []string {
	reg := rt.Reg()
	out := []string{}
	for name, f := range reg.Flows {
		if f.On == on && reg.IsInstalled(f.App) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// challenge answers a subscription check such as Meta's hub.challenge: the expression (CEL) sees the query string
// as nested maps (hub.verify_token) and auth.<field>; true echoes the challenge value.
func (rt *Runtime) challenge(ctx context.Context, org, connector string, verify map[string]any, r *http.Request) HookResult {
	expr, _ := verify["challenge"].(string)
	if expr == "" {
		return HookResult{Status: 405, Body: "this trigger has no challenge"}
	}
	q := map[string]any{}
	for k, vs := range r.URL.Query() {
		cur := q
		parts := strings.Split(k, ".")
		for i, p := range parts {
			if i == len(parts)-1 {
				cur[p] = vs[0]
				break
			}
			next, ok := cur[p].(map[string]any)
			if !ok {
				next = map[string]any{}
				cur[p] = next
			}
			cur = next
		}
	}
	fields := placeholderAuth(expr)
	for _, f := range fields {
		vals, err := rt.authAccepted(ctx, org, connector, f)
		if err != nil || len(vals) == 0 {
			return HookResult{Status: 401, Body: "connector_needs_auth"}
		}
		for _, v := range vals { // any accepted version passes
			vars := map[string]any{}
			for k, x := range q {
				vars[k] = x
			}
			vars["auth"] = map[string]any{f: v}
			if ok, err := cel.Eval(expr, varsEnv(vars)); err == nil && cel.Unwrap(ok) == true {
				ch, _ := meta.AsMap(q["hub"])["challenge"].(string)
				if ch == "" {
					ch = r.URL.Query().Get("challenge")
				}
				return HookResult{Status: 200, Body: ch}
			}
		}
	}
	return HookResult{Status: 401, Body: "challenge refused"}
}

func placeholderAuth(expr string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(expr, func(r rune) bool { return !(r == '.' || r == '_' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') }) {
		if strings.HasPrefix(f, "auth.") && !seen[f] {
			seen[f] = true
			out = append(out, strings.TrimPrefix(f, "auth."))
		}
	}
	return out
}

// verify checks verify.signature (HMAC over the raw body, header value hex with an optional sha256= prefix) or
// verify.header + verify.equals (a shared token), against every accepted version of the secret, in constant time.
func (rt *Runtime) verify(ctx context.Context, org, connector string, verify map[string]any, r *http.Request, raw []byte) (HookResult, bool) {
	if sig := meta.AsMap(verify["signature"]); sig != nil {
		got := strings.TrimSpace(r.Header.Get(meta.Str(sig, "header")))
		if got == "" {
			return HookResult{Status: 401, Body: "signature missing"}, false
		}
		if i := strings.Index(got, "="); i > 0 && i < 10 {
			got = got[i+1:]
		}
		want, err := hex.DecodeString(strings.ToLower(got))
		if err != nil {
			return HookResult{Status: 401, Body: "signature is not hex"}, false
		}
		newHash, err := hashOf(meta.Str(sig, "alg"))
		if err != nil {
			return HookResult{Status: 500, Body: err.Error()}, false
		}
		keys, err := rt.authAccepted(ctx, org, connector, meta.Str(sig, "key"))
		if err != nil || len(keys) == 0 {
			return HookResult{Status: 401, Body: "connector_needs_auth"}, false
		}
		for _, k := range keys {
			m := hmac.New(newHash, []byte(k))
			m.Write(raw)
			if hmac.Equal(m.Sum(nil), want) {
				return HookResult{}, true
			}
		}
		return HookResult{Status: 401, Body: "signature does not match"}, false
	}
	if h, eq := meta.Str(verify, "header"), meta.Str(verify, "equals"); h != "" && eq != "" {
		got := r.Header.Get(h)
		if got == "" {
			return HookResult{Status: 401, Body: "token missing"}, false
		}
		keys, err := rt.authAccepted(ctx, org, connector, eq)
		if err != nil || len(keys) == 0 {
			return HookResult{Status: 401, Body: "connector_needs_auth"}, false
		}
		for _, k := range keys {
			if subtle.ConstantTimeCompare([]byte(got), []byte(k)) == 1 {
				return HookResult{}, true
			}
		}
		return HookResult{Status: 401, Body: "token does not match"}, false
	}
	return HookResult{}, true
}

func hashOf(alg string) (func() hash.Hash, error) {
	switch strings.ToLower(alg) {
	case "", "hmac-sha256", "sha256":
		return sha256.New, nil
	case "hmac-sha1", "sha1":
		return sha1.New, nil
	case "hmac-sha512", "sha512":
		return sha512.New, nil
	}
	return nil, errors.New("unsupported signature alg " + alg)
}

// item is one element of a for_each path, with the object each named segment passed through.
type item struct {
	value any
	scope map[string]any
}

// forEach walks a path such as entry[].changes[].value.messages[]: each [] flattens a list, and every segment's
// object is bound by its name (entry, changes, value, messages) for emit. An empty path is the body itself.
func forEach(body any, path string) []item {
	if strings.TrimSpace(path) == "" {
		return []item{{value: body, scope: map[string]any{}}}
	}
	cur := []item{{value: body, scope: map[string]any{}}}
	for _, seg := range strings.Split(path, ".") {
		flat := strings.HasSuffix(seg, "[]")
		name := strings.TrimSuffix(seg, "[]")
		next := []item{}
		for _, it := range cur {
			v := meta.AsMap(it.value)[name]
			if flat {
				for _, x := range meta.AsList(v) {
					next = append(next, item{value: x, scope: with(it.scope, name, x)})
				}
			} else if v != nil {
				next = append(next, item{value: v, scope: with(it.scope, name, v)})
			}
		}
		cur = next
	}
	return cur
}

func with(m map[string]any, k string, v any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for a, b := range m {
		out[a] = b
	}
	out[k] = v
	return out
}

// emit evaluates each emit expression (CEL) over the body's fields, the for_each scope and item.
func emit(spec map[string]any, body any, it item) map[string]any {
	vars := map[string]any{"body": body}
	for k, v := range meta.AsMap(body) {
		vars[k] = v
	}
	for k, v := range it.scope {
		vars[k] = v
	}
	vars["item"] = it.value
	out := map[string]any{}
	if len(spec) == 0 {
		if m := meta.AsMap(it.value); m != nil {
			return m
		}
		return map[string]any{"item": it.value}
	}
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

func eventKey(org, connector, trigger string, ev map[string]any) string {
	b, _ := json.Marshal(ev) // map keys are sorted
	sum := sha256.Sum256([]byte(org + "|" + connector + "|" + trigger + "|" + string(b)))
	return hex.EncodeToString(sum[:16])
}

// remember records an event key; false when it was seen before (a vendor retry or a replay).
func (rt *Runtime) remember(ctx context.Context, org, connector, trigger, key string) (bool, error) {
	res, err := rt.DB.Query(ctx, `insert into rk_hook_events (org_id, connector, trigger, key) values ($1, $2, $3, $4)
		on conflict do nothing returning key`, org, connector, trigger, key)
	if err != nil {
		return false, fmt.Errorf("hook events: %w", err)
	}
	if len(res.Rows) == 1 && rand.IntN(200) == 0 { // now and then, forget events older than 30 days
		rt.DB.Exec(ctx, "delete from rk_hook_events where at < now() - interval '30 days'")
	}
	return len(res.Rows) == 1, nil
}
