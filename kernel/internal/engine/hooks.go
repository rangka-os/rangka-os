package engine

import (
	"context"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Hook is a named kernel function an action, automation or on_submit/on_cancel calls. It runs inside the
// caller's transaction and can create and update records through HookCtx.
type Hook func(h *HookCtx) (map[string]any, error)

// HookCtx gives a hook the record and transactional record operations.
type HookCtx struct {
	op     *op
	Model  *meta.Model
	Record store.Record
	Args   map[string]any
}

// Actor returns who runs the hook.
func (h *HookCtx) Actor() Actor { return h.op.actor }

// Context returns the request context.
func (h *HookCtx) Context() context.Context { return h.op.ctx }

// Create creates a record in the hook's transaction.
func (h *HookCtx) Create(model string, input map[string]any) (store.Record, []Issue, error) {
	r, err := h.op.create(model, input, CreateOpts{})
	if err != nil {
		return nil, nil, err
	}
	return r.Record, r.Warnings, nil
}

// Update updates a record in the hook's transaction.
func (h *HookCtx) Update(model, name string, patch map[string]any) (store.Record, error) {
	r, err := h.op.update(model, name, patch)
	if err != nil {
		return nil, err
	}
	return r.Record, nil
}

func registerBuiltinHooks(e *Engine) {
	e.Hooks["crm.convert_lead"] = convertLead
	e.Hooks["crm.mark_won"] = markWon
}

func or(a, b any) any {
	if s := str(a); s != "" {
		return s
	}
	return str(b)
}

// convertLead ports the prototype's crm.convert_lead: a customer and a qualified deal from a lead, and the
// lead marked qualified with links to both.
func convertLead(h *HookCtx) (map[string]any, error) {
	lead := h.Record
	c, _, err := h.Create("customer", map[string]any{
		"customer_name": or(lead["company_name"], lead["lead_name"]), "city": str(lead["city"]),
		"phone": str(lead["phone"]), "npwp": "",
	})
	if err != nil {
		return nil, err
	}
	d, _, err := h.Create("deal", map[string]any{
		"deal_name": "Peluang " + str(or(lead["company_name"], lead["lead_name"])), "customer": c["name"],
		"contact_person": str(lead["lead_name"]), "stage": "qualified", "value": 0, "expected_close": "",
		"owner": str(lead["owner"]), "source": str(lead["source"]), "lost_reason": "", "notes": str(lead["notes"]),
	})
	if err != nil {
		return nil, err
	}
	l, err := h.Update(h.Model.Name, str(lead["name"]), map[string]any{"status": "qualified", "converted_customer": c["name"], "converted_deal": d["name"]})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"record":  l,
		"message": "Pelanggan " + str(c["name"]) + " dan peluang " + str(d["name"]) + " dibuat",
		"open":    map[string]any{"model": "deal", "name": d["name"]},
	}, nil
}

// ActionOpts tune RunAction.
type ActionOpts struct {
	FromQueue bool // the job worker runs a queued action
}

// ActionResult is a hook's result, or the job that will run it.
type ActionResult struct {
	Queued bool           `json:"queued,omitempty"`
	JobID  int64          `json:"job_id,omitempty"`
	Result map[string]any `json:"result,omitempty"`
}

// ActionAllowed evaluates an action's if: on a record.
func (e *Engine) ActionAllowed(ctx context.Context, a Actor, m *meta.Model, act *meta.Action, rec store.Record) bool {
	if act.If == "" {
		return true
	}
	v, err := e.newScope(ctx, e.DB, a, m, rec, nil).eval(act.If)
	return err == nil && v == true
}

// RunAction runs a model action's hook on a record. Actions with queue: are put on the queue unless the
// worker is running them.
func (e *Engine) RunAction(ctx context.Context, a Actor, model, name, action string, args map[string]any, opts ActionOpts) (*ActionResult, error) {
	var out *ActionResult
	err := e.run(ctx, a, func(o *op) error {
		m, err := e.model(model)
		if err != nil {
			return err
		}
		act := m.Action(action)
		if act == nil {
			return notFound("action " + action + " of " + model)
		}
		rec, err := o.load(m, name)
		if err != nil {
			return err
		}
		if act.If != "" {
			v, err := o.scope(m, rec).eval(act.If)
			if err != nil || v != true {
				return conflict("action_not_allowed", "Aksi ini tidak bisa dijalankan pada status sekarang")
			}
		}
		if act.Queue != nil && !opts.FromQueue {
			if e.Queue == nil {
				return &StatusError{503, "no_queue", "this action runs in the background but the queue is not running"}
			}
			attempts := 0
			if r, ok := act.Queue["retries"]; ok {
				attempts = int(toInt(r)) + 1
			}
			id, err := e.Queue.Enqueue(o.ctx, o.tx, "action", a.Org, map[string]any{"model": model, "name": name, "action": action, "args": args,
				"actor": map[string]any{"id": a.ID, "user": a.UserName, "roles": a.Roles}}, EnqueueOpts{MaxAttempts: attempts})
			if err != nil {
				return err
			}
			if err := store.Audit(o.ctx, o.tx, a.Org, a.ID, "action.queued", model+"/"+name, action+" queued as job "+itoa(id), nil); err != nil {
				return err
			}
			out = &ActionResult{Queued: true, JobID: id}
			return nil
		}
		hook := e.Hooks[act.Hook]
		if hook == nil {
			return &StatusError{501, "hook_not_implemented", "hook " + act.Hook + " is not implemented in this kernel"}
		}
		res, err := hook(&HookCtx{op: o, Model: m, Record: rec, Args: args})
		if err != nil {
			return err
		}
		if err := store.Audit(o.ctx, o.tx, a.Org, a.ID, "action.run", model+"/"+name, action+" ("+act.Hook+")", nil); err != nil {
			return err
		}
		out = &ActionResult{Result: res}
		return nil
	})
	return out, err
}

func itoa(n int64) string {
	return formatInt(n)
}
