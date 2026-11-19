package connectors

import (
	"context"
	"fmt"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Confirmed webhooks (contract 0002, refinement of 19 Nov 2026): some gateways sign only part of a notification
// (Midtrans signs the order id, the status code and the gross amount, not the transaction status). A webhook trigger
// with confirm: reads the vendor's own answer before any flow runs:
//
//	confirm:
//	  action: get_status                 # a GET action of this connector
//	  input: {gateway_ref: order_id}     # CEL over the verified body
//
// After the signature is checked, the kernel sends that action with the org's keys (over https to a declared host,
// as any call) and emit sees its mapped output as `confirmed`, next to the body. A connector takes what a flow acts on
// (paid, the amount, the status) from `confirmed`, so a genuine notification resent with an unsigned field changed
// cannot make a payment the vendor does not report. A confirm that fails (network, a vendor error, an unknown
// transaction) starts no flow and answers 502, so the vendor sends the notification again later.

// confirmSpec is a trigger's confirm: block.
func confirmSpec(t map[string]any) (string, map[string]any, bool) {
	c := meta.AsMap(t["confirm"])
	if c == nil {
		return "", nil, false
	}
	return meta.Str(c, "action"), meta.AsMap(c["input"]), true
}

// confirm sends the confirm action for a verified webhook body and returns its mapped output.
func (rt *Runtime) confirm(ctx context.Context, org, connector, trigger string, t map[string]any, body any) (map[string]any, *HookResult) {
	action, inSpec, _ := confirmSpec(t)
	_, a, err := rt.Action(connector, action)
	if err != nil || action == "" {
		return nil, &HookResult{Status: 500, Body: "confirm: no action " + action}
	}
	if m := strings.ToUpper(meta.Str(meta.AsMap(a["request"]), "method")); m != "" && m != "GET" {
		// a webhook must never make the kernel write to the vendor (money out, a cancel)
		return nil, &HookResult{Status: 500, Body: "confirm: action " + action + " is not a GET"}
	}
	vars := map[string]any{"body": body}
	for k, v := range meta.AsMap(body) {
		vars[k] = v
	}
	input := map[string]any{}
	for _, k := range sortedKeys(inSpec) {
		src, ok := inSpec[k].(string)
		if !ok {
			continue
		}
		v, err := cel.Eval(src, varsEnv(vars))
		if err != nil || cel.Unwrap(v) == nil || fmt.Sprint(cel.Unwrap(v)) == "" {
			store.Audit(context.WithoutCancel(ctx), rt.DB, org, "connector:"+connector, "connector.error", "connector/"+connector,
				trigger+": webhook not confirmed (input "+k+" has no value)", map[string]any{"trigger": trigger, "code": "webhook_unconfirmed"})
			return nil, &HookResult{Status: 400, Body: "confirm input " + k + " has no value"}
		}
		input[k] = cel.Unwrap(v)
	}
	out, err := rt.Call(ctx, org, connector, action, input)
	if err != nil {
		// Call audited the failure itself (scrubbed); the vendor retries the notification
		store.Audit(context.WithoutCancel(ctx), rt.DB, org, "connector:"+connector, "connector.error", "connector/"+connector,
			trigger+": webhook not confirmed ("+action+" failed)", map[string]any{"trigger": trigger, "code": "webhook_unconfirmed"})
		return nil, &HookResult{Status: 502, Body: "confirm failed"}
	}
	return out, nil
}
