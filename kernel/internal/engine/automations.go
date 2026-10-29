package engine

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

var (
	changesTo = regexp.MustCompile(`^\s*([a-z_][a-z0-9_]*)\s+changes\s+to\s+(.+?)\s*$`)
	changes   = regexp.MustCompile(`^\s*([a-z_][a-z0-9_]*)\s+changes\s*$`)
)

// automationFires decides whether an automation's when: matches a write. Forms: "<field> changes to <CEL
// value>", "<field> changes", or an event name (created, updated, submitted, cancelled). A new record
// counts as a change from empty.
func automationFires(when string, old, rec store.Record, event string) bool {
	if m := changesTo.FindStringSubmatch(when); m != nil {
		want, err := cel.Eval(m[2], &cel.Env{})
		if err != nil {
			return false
		}
		var before any
		if old != nil {
			before = old[m[1]]
		}
		return cel.Equal(rec[m[1]], want) && (old == nil || !cel.Equal(before, want))
	}
	if m := changes.FindStringSubmatch(when); m != nil {
		return old == nil || !cel.Equal(old[m[1]], rec[m[1]])
	}
	return strings.TrimSpace(when) == event
}

// automations runs the model's automations: whose when: matches and if: holds, by calling their hook in the
// same transaction.
func (o *op) automations(m *meta.Model, old, rec store.Record, event string) error {
	for _, av := range meta.AsList(m.Raw["automations"]) {
		au := meta.AsMap(av)
		if !automationFires(meta.Str(au, "when"), old, rec, event) {
			continue
		}
		if cond := meta.Str(au, "if"); cond != "" {
			v, err := o.scope(m, rec).eval(cond)
			if err != nil || !cel.Truthy(v) {
				continue
			}
		}
		hook := o.e.Hooks[meta.Str(au, "do")]
		if hook == nil {
			if err := store.Audit(o.ctx, o.tx, o.actor.Org, o.actor.ID, "hook.skipped", m.Name+"/"+str(rec["name"]), "automation "+meta.Str(au, "name")+": hook "+meta.Str(au, "do")+" is not implemented", nil); err != nil {
				return err
			}
			continue
		}
		res, err := hook(&HookCtx{op: o, Model: m, Record: rec})
		if err != nil {
			return fmt.Errorf("automation %s: %w", meta.Str(au, "name"), err)
		}
		if err := store.Audit(o.ctx, o.tx, o.actor.Org, o.actor.ID, "automation.run", m.Name+"/"+str(rec["name"]), meta.Str(au, "name")+": "+str(res["message"]), nil); err != nil {
			return err
		}
	}
	return nil
}

// markWon is crm.mark_won: a won deal becomes a draft sales invoice for its customer (once per deal).
func markWon(h *HookCtx) (map[string]any, error) {
	deal := h.Record
	marker := "Dari peluang " + str(deal["name"])
	reg := h.op.e.Reg()
	inv := reg.Model("sales_invoice")
	if inv == nil {
		return map[string]any{"message": "app sales is not installed"}, nil
	}
	existing, err := store.All(h.op.ctx, h.op.tx, reg, h.op.actor.Org, inv, false)
	if err != nil {
		return nil, err
	}
	for _, r := range existing {
		if strings.HasPrefix(str(r["notes"]), marker) {
			return map[string]any{"message": "Faktur " + str(r["name"]) + " sudah ada untuk peluang ini", "open": map[string]any{"model": "sales_invoice", "name": r["name"]}}, nil
		}
	}
	outlets, err := store.All(h.op.ctx, h.op.tx, reg, h.op.actor.Org, reg.Model("outlet"), false)
	if err != nil {
		return nil, err
	}
	if len(outlets) == 0 {
		return nil, badRequest("crm.mark_won needs an outlet for the invoice")
	}
	sort.Slice(outlets, func(i, j int) bool { return str(outlets[i]["name"]) < str(outlets[j]["name"]) })
	created, _, err := h.Create("sales_invoice", map[string]any{"customer": deal["customer"], "outlet": outlets[0]["name"],
		"notes": marker + " (" + str(deal["deal_name"]) + ")", "items": []any{}})
	if err != nil {
		return nil, err
	}
	return map[string]any{"message": "Draf faktur " + str(created["name"]) + " dibuat dari peluang " + str(deal["name"]),
		"open": map[string]any{"model": "sales_invoice", "name": created["name"]}}, nil
}
