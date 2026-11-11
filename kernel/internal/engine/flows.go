package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Notification is what a notify step produced. v0 delivers through the "log" driver: the audit log and
// the kernel log.
type Notification struct {
	Channel    string   `json:"channel"`
	To         string   `json:"to"`
	Recipients []string `json:"recipients"`
	Text       string   `json:"text"`
}

// FlowResult summarises a flow run.
type FlowResult struct {
	Flow            string         `json:"flow"`
	Skipped         bool           `json:"skipped,omitempty"`
	Steps           []string       `json:"steps"`
	Created         []string       `json:"created"`
	Updated         []string       `json:"updated"`
	Calls           []string       `json:"calls"`
	Notifications   []Notification `json:"notifications"`
	WaitingJob      int64          `json:"waiting_job,omitempty"`      // a wait: step put the rest on the queue
	WaitingApproval string         `json:"waiting_approval,omitempty"` // an approve: step waits for this request
}

// RunFlow runs a flow from its first step for a caller (REST). event is the trigger payload (flows with on:).
// Flow steps are not row-scoped, so a member with a row scope may not start a flow directly (contract 0010
// §5); the scheduler goes through RunFlowFrom and queued jobs through RunFlowResume, both unguarded. A run started
// here is manual (contract 0026): it may not create append_only ledger rows, also after a wait or an approval.
func (e *Engine) RunFlow(ctx context.Context, a Actor, flowName string, event map[string]any) (*FlowResult, error) {
	if e.RowScope(a) != nil {
		return nil, Denied("flow", "run")
	}
	// a flow a connector event starts records what a gateway or a vendor said happened (a payment, a sale): run by
	// hand, its event is whatever the caller writes, so only the owner may (audit rel uang B, temuan 6)
	if f := e.Reg().Flows[flowName]; f != nil && flowConnector(e, f) != "" && !a.System && !hasAnyRole(a, []string{"owner"}) {
		return nil, Denied("flow", "run")
	}
	return e.runFlowFrom(ctx, a, flowName, event, 0, nil, true)
}

// RunFlowFrom runs a flow (never manual, contract 0026) in one transaction from top-level step start with vars (the
// scheduler and record events; a queued job resumes through RunFlowResume). Steps: find, find_all, create, update, for_each (the steps under do: once per item of a list), notify (log driver), call (connector action, queued as a
// call job when the runtime is wired, else only recorded), if/then/else, wait (the remaining steps are
// queued for later) and approve (the remaining steps wait for a decision). Every step may have if: and as:.
//
// A flow runs as its actor: find, create and update steps are authorized like the public entry points
// (read, create, write), so a person running a flow can do no more than they could by hand. Flows the
// kernel starts (schedules, events) run as a System actor; a wait: resumes as the actor that started the run.
func (e *Engine) RunFlowFrom(ctx context.Context, a Actor, flowName string, event map[string]any, start int, vars map[string]any) (*FlowResult, error) {
	return e.runFlowFrom(ctx, a, flowName, event, start, vars, false)
}

// RunFlowResume is RunFlowFrom for a queued flow job resuming after wait:; manual is the job's "manual" arg, so a
// run a caller started by hand stays manual after the wait (contract 0026).
func (e *Engine) RunFlowResume(ctx context.Context, a Actor, flowName string, event map[string]any, start int, vars map[string]any, manual bool) (*FlowResult, error) {
	return e.runFlowFrom(ctx, a, flowName, event, start, vars, manual)
}

// runFlowFrom is RunFlowFrom; manual marks a run a caller started with an event they wrote (REST), which may not
// create append_only ledger rows (contract 0026): those come only from real events, hooks and seed.
func (e *Engine) runFlowFrom(ctx context.Context, a Actor, flowName string, event map[string]any, start int, vars map[string]any, manual bool) (*FlowResult, error) {
	f := e.Reg().Flows[flowName]
	if f == nil || !e.Reg().IsInstalled(f.App) {
		return nil, notFound("flow " + flowName)
	}
	out := newFlowResult(flowName)
	err := e.run(ctx, a, func(o *op) error {
		o.manualRun = manual
		// a fresh result and copy of the variables per attempt: a retried run (contract 0025) must not report the
		// steps of the rolled-back attempt
		out = newFlowResult(flowName)
		v := make(map[string]any, len(vars))
		for k, x := range vars {
			v[k] = x
		}
		return e.flowIn(o, f, event, start, v, out)
	})
	if c := flowConnector(e, f); err != nil && c != "" {
		store.Audit(context.WithoutCancel(ctx), e.DB, a.Org, a.ID, "connector.error", "connector/"+c, "flow "+flowName+": "+err.Error(), nil)
	}
	return out, err
}

// lockFlowRead takes the transaction-scoped advisory lock of model m in the actor's org (contract 0025); taking it
// again in the same transaction is a no-op wait-free re-entry.
func (o *op) lockFlowRead(m string) error {
	return store.LockKey(o.ctx, o.tx, "flowread:"+o.actor.Org+"/"+m)
}

func newFlowResult(flow string) *FlowResult {
	return &FlowResult{Flow: flow, Steps: []string{}, Created: []string{}, Updated: []string{}, Calls: []string{}, Notifications: []Notification{}}
}

// flowConnector is the connector a flow's on: names ("" for model events and flows without on:).
func flowConnector(e *Engine, f *meta.Flow) string {
	if head, _, ok := strings.Cut(f.On, "."); ok && e.Reg().Models[head] == nil {
		return head
	}
	return ""
}

// flowIn runs f's steps inside o's transaction, as o's actor, from top-level step start.
func (e *Engine) flowIn(o *op, f *meta.Flow, event map[string]any, start int, vars map[string]any, out *FlowResult) error {
	a := o.actor
	if event == nil {
		event = map[string]any{}
	}
	if vars == nil {
		vars = map[string]any{}
	}
	vars["event"] = event
	prevInFlow := o.inFlow
	o.inFlow = true
	defer func() { o.inFlow = prevInFlow }()
	r := &flowRun{o: o, e: e, a: a, f: f, vars: vars, out: out}
	if c := flowConnector(e, f); start == 0 && c != "" && len(event) > 0 {
		if err := store.Audit(o.ctx, o.tx, a.Org, a.ID, "connector.event", "connector/"+c, f.On+" → flow "+f.Name, nil); err != nil {
			return err
		}
	}
	if start == 0 && f.If != "" {
		v, err := cel.Eval(f.If, r.env())
		if err != nil || !cel.Truthy(v) {
			out.Skipped = true
			return nil
		}
	}
	if start == 0 && f.Idempotency != "" {
		dup, err := r.claimKey()
		if err != nil || dup {
			return err
		}
	}
	var list []any
	for _, st := range f.Steps {
		list = append(list, map[string]any(st))
	}
	if err := r.steps(list, "", start); err != nil {
		return err
	}
	if r.key != "" { // the documents this run made: the key is free again once none of them is alive
		if err := store.SetFlowKeyDocs(o.ctx, o.tx, a.Org, f.Name, r.key, r.keyDocs()); err != nil {
			return err
		}
	}
	return store.Audit(o.ctx, o.tx, a.Org, a.ID, "flow.run", "flow/"+f.Name, fmt.Sprintf("%d step(s), %d created, %d notification(s)", len(out.Steps), len(out.Created), len(out.Notifications)), nil)
}

// claimKey enforces the flow's idempotency: (contract 0002 §A.2): the same key never runs the flow twice in an org.
// dup is true (the run is skipped) when a committed run holds the key. A key is a non-empty string or a whole
// number. An empty string or null means the event has no key: the run goes on without dedupe, and the flow
// handles it (a payment notification without its gateway id tells the owner and records nothing). Anything else
// (a list, a map, a fraction, true/false, an expression error) fails the run: a flow that dedupes money must not
// skip a payment because two different events gave the same text.
func (r *flowRun) claimKey() (dup bool, err error) {
	v, err := cel.Eval(r.f.Idempotency, r.env())
	if err == nil && (v == nil || v == "") {
		return false, nil
	}
	key := ""
	if err == nil {
		key = flowKey(v)
	}
	if key == "" {
		msg := "flow " + r.f.Name + ": idempotency: harus berupa teks atau bilangan bulat"
		if err != nil {
			msg += " (" + err.Error() + ")"
		}
		return false, &StatusError{422, "idempotency_key", msg}
	}
	fresh, docs, err := store.ClaimFlowKey(r.o.ctx, r.o.tx, r.a.Org, r.f.Name, key)
	if err != nil {
		return false, err
	}
	if !fresh {
		alive, err := r.anyAlive(docs)
		if err != nil {
			return false, err
		}
		if len(docs) == 0 || alive {
			r.out.Skipped = true
			return true, store.Audit(r.o.ctx, r.o.tx, r.a.Org, r.a.ID, "flow.duplicate", "flow/"+r.f.Name, "idempotency key sudah dipakai: "+key, nil)
		}
		// every document the earlier run made was deleted or cancelled: the event is recorded again
		if err := store.Audit(r.o.ctx, r.o.tx, r.a.Org, r.a.ID, "flow.key_reused", "flow/"+r.f.Name, "idempotency key "+key+" dipakai lagi: dokumen run sebelumnya sudah dihapus atau dibatalkan ("+strings.Join(docs, ", ")+")", nil); err != nil {
			return false, err
		}
	}
	r.key = key
	return false, nil
}

// anyAlive reports whether one of docs (<model>/<name>) still exists and, for a submittable model, is not cancelled.
func (r *flowRun) anyAlive(docs []string) (bool, error) {
	for _, d := range docs {
		mn, name, _ := strings.Cut(d, "/")
		m := r.e.Reg().Model(mn)
		if m == nil {
			continue
		}
		rec, err := store.Get(r.o.ctx, r.o.tx, r.e.Reg(), r.a.Org, m, name)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		if m.IsSubmittable && toInt(rec["docstatus"]) == 2 {
			continue
		}
		return true, nil
	}
	return false, nil
}

// keyDocs are the documents a run's idempotency key stands for: the submittable records it created, or every
// record it created when it made none (a lead and its activity).
func (r *flowRun) keyDocs() []string {
	var sub, all []string
	for _, c := range r.out.Created {
		all = append(all, c)
		mn, _, _ := strings.Cut(c, "/")
		if m := r.e.Reg().Model(mn); m != nil && m.IsSubmittable {
			sub = append(sub, c)
		}
	}
	if len(sub) > 0 {
		return sub
	}
	return all
}

// flowKey is the stored form of an idempotency value, typed so that 5 and "5" never meet: "s:<text>" or
// "i:<integer>", and "h:<sha256>" of the typed form when it is longer than 200 characters. "" when v is no key.
func flowKey(v any) string {
	var k string
	switch x := v.(type) {
	case string:
		if x == "" {
			return ""
		}
		k = "s:" + x
	case int64:
		k = "i:" + strconv.FormatInt(x, 10)
	case int:
		k = "i:" + strconv.Itoa(x)
	case float64:
		if x != math.Trunc(x) || math.IsInf(x, 0) || math.Abs(x) > 1<<53 {
			return ""
		}
		k = "i:" + strconv.FormatInt(int64(x), 10)
	default:
		return ""
	}
	if len(k) > 200 {
		sum := sha256.Sum256([]byte(k))
		k = "h:" + hex.EncodeToString(sum[:])
	}
	return k
}

// maxForEach bounds one for_each step (one flow run is one transaction).
var maxForEach = 2000 // a var so a test can lower it

type flowRun struct {
	o    *op
	e    *Engine
	a    Actor
	f    *meta.Flow
	vars map[string]any
	out  *FlowResult
	stop bool
	key  string // the idempotency key this run claimed
}

func (r *flowRun) env() *cel.Env {
	return &cel.Env{Today: r.e.Today, Now: r.e.Now, Apps: r.e.Reg().IsInstalled, Vars: func(n string) (any, bool) {
		if v, ok := r.vars[n]; ok {
			return v, true
		}
		return GlobalVar(r.e.Reg(), r.a, n)
	}}
}

func (r *flowRun) setValues(st map[string]any, i string) (map[string]any, error) {
	input := map[string]any{}
	set := meta.AsMap(st["set"])
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		src, isStr := set[k].(string)
		if !isStr {
			input[k] = set[k]
			continue
		}
		v, err := cel.Eval(src, r.env())
		if err != nil {
			return nil, fmt.Errorf("step %s set %s: %w", i, k, err)
		}
		input[k] = cel.Unwrap(v)
	}
	return input, nil
}

func record(model string, rec store.Record) map[string]any {
	m := map[string]any(rec)
	m["_model"] = model
	return m
}

// steps runs a step list; label is the position prefix ("2.then.").
func (r *flowRun) steps(list []any, label string, start int) error {
	o, e, a := r.o, r.e, r.a
	for idx := start; idx < len(list) && !r.stop; idx++ {
		st := meta.AsMap(list[idx])
		i := fmt.Sprintf("%s%d", label, idx+1)
		as, _ := st["as"].(string)
		if cond, ok := st["if"].(string); ok && cond != "" {
			v, err := cel.Eval(cond, r.env())
			if err != nil {
				return fmt.Errorf("step %s if: %w", i, err)
			}
			if _, isIfStep := st["then"]; isIfStep {
				branch := "then"
				if !cel.Truthy(v) {
					branch = "else"
				}
				r.out.Steps = append(r.out.Steps, i+": if → "+branch)
				if err := r.steps(meta.AsList(st[branch]), i+"."+branch+".", 0); err != nil {
					return err
				}
				continue
			}
			if !cel.Truthy(v) {
				r.out.Steps = append(r.out.Steps, i+": skipped")
				continue
			}
		}
		switch {
		case st["find"] != nil || st["find_all"] != nil:
			all := st["find_all"] != nil
			mn := str(st["find"])
			if all {
				mn = str(st["find_all"])
			}
			m, err := e.model(mn)
			if err != nil {
				return err
			}
			readable := e.decide(a, e.permModel(m.Name), "read")
			if !readable.maybe() {
				return e.Deny(e.permModel(m.Name).Name, "read")
			}
			// contract 0025: a flow that reads a model holds that model's lock (per org) until its unit of work ends, so
			// two flows that check the same model and then write ("is this retainer month billed yet?") run one after
			// the other, and the second reads the first one's commit; other orgs and other models are not held up
			if err := o.lockFlowRead(m.Name); err != nil {
				return err
			}
			recs, err := store.All(o.ctx, o.tx, e.Reg(), a.Org, m, false)
			if err != nil {
				return err
			}
			if srt, _ := st["sort"].(string); srt != "" {
				desc := strings.HasPrefix(srt, "-")
				k := strings.TrimPrefix(srt, "-")
				sort.SliceStable(recs, func(x, y int) bool {
					c := compareValues(recs[x][k], recs[y][k])
					if desc {
						return c > 0
					}
					return c < 0
				})
			}
			limit := int(toInt(st["limit"]))
			where, _ := st["where"].(string)
			var found []any
			for _, rec := range recs {
				e.Output(m, rec)
				if !e.recordAllowed(o.ctx, o.tx, a, m, readable, rec, o.links) {
					continue
				}
				if where != "" {
					sc := o.scope(m, rec)
					sc.extra = r.vars
					v, err := sc.eval(where)
					if err != nil || !cel.Truthy(v) {
						continue
					}
				}
				found = append(found, record(mn, rec))
				if !all || (limit > 0 && len(found) >= limit) {
					break
				}
			}
			var val any
			if all {
				if found == nil {
					found = []any{}
				}
				val = found
			} else if len(found) > 0 {
				val = found[0]
			}
			if as != "" {
				r.vars[as] = val
			}
			kind := "find"
			if all {
				kind = "find_all"
			}
			r.out.Steps = append(r.out.Steps, fmt.Sprintf("%s: %s %s → %d", i, kind, mn, len(found)))
		case st["create"] != nil:
			mn := str(st["create"])
			input, err := r.setValues(st, i)
			if err != nil {
				return err
			}
			res, err := o.create(mn, input, CreateOpts{authorize: true})
			if err != nil {
				return fmt.Errorf("step %s create %s: %w", i, mn, err)
			}
			if as != "" {
				r.vars[as] = record(mn, res.Record)
			}
			r.out.Created = append(r.out.Created, mn+"/"+str(res.Record["name"]))
			r.out.Steps = append(r.out.Steps, fmt.Sprintf("%s: create %s %s", i, mn, res.Record["name"]))
		case st["update"] != nil:
			target := meta.AsMap(r.vars[str(st["update"])])
			mn, name := str(target["_model"]), str(target["name"])
			if mn == "" || name == "" {
				return badRequest(fmt.Sprintf("flow %s step %s: update needs a variable holding a record found or created earlier", r.f.Name, i))
			}
			input, err := r.setValues(st, i)
			if err != nil {
				return err
			}
			res, err := o.update(mn, name, input, true, false)
			if err != nil {
				return fmt.Errorf("step %s update %s %s: %w", i, mn, name, err)
			}
			r.vars[str(st["update"])] = record(mn, res.Record)
			r.out.Updated = append(r.out.Updated, mn+"/"+name)
			r.out.Steps = append(r.out.Steps, fmt.Sprintf("%s: update %s %s", i, mn, name))
		case st["notify"] != nil:
			to := str(st["notify"])
			ch := str(st["channel"])
			if ch == "" {
				ch = "email"
			}
			tmpl := ""
			switch t := st["template"].(type) {
			case string:
				tmpl = t
			case map[string]any:
				tmpl = meta.LabelOf(t).Text()
			}
			n := Notification{Channel: ch, To: to, Recipients: recipients(to, r.vars), Text: cel.Interpolate(tmpl, r.env())}
			r.out.Notifications = append(r.out.Notifications, n)
			if err := store.Audit(o.ctx, o.tx, a.Org, a.ID, "notify."+ch, "flow/"+r.f.Name, n.Text, n); err != nil {
				return err
			}
			o.tx.AfterCommit(func() { e.Log("notify[%s] %s %v: %s", ch, to, n.Recipients, n.Text) }) // once, not per retried attempt
			r.out.Steps = append(r.out.Steps, fmt.Sprintf("%s: notify %s via %s", i, to, ch))
		case st["call"] != nil:
			call := str(st["call"])
			conn, action, _ := strings.Cut(call, ".")
			input := map[string]any{}
			with := meta.AsMap(st["with"])
			for _, k := range sortedKeys(with) {
				if src, ok := with[k].(string); ok {
					v, err := cel.Eval(src, r.env())
					if err != nil {
						return fmt.Errorf("step %s with %s: %w", i, k, err)
					}
					input[k] = cel.Unwrap(v)
				} else {
					input[k] = with[k]
				}
			}
			// the call runs as a queued job after commit (contract 0002 §A.3): the job holds the action, input, org and
			// connector, never a rendered request (contract 0017 §6)
			if e.Queue != nil && e.CallTarget != nil {
				c, act, opts, err := e.CallTarget(e.Reg(), call, input)
				if err != nil {
					return fmt.Errorf("step %s call %s: %w", i, call, err)
				}
				if opts.UniqueKey != "" {
					opts.UniqueKey = "call:" + a.Org + ":" + c + "." + act + ":" + opts.UniqueKey
				}
				id, err := e.Queue.Enqueue(o.ctx, o.tx, "call", a.Org, map[string]any{"connector": c, "action": act, "input": input, "flow": r.f.Name}, opts)
				if err != nil {
					return err
				}
				if err := store.Audit(o.ctx, o.tx, a.Org, a.ID, "connector.queued", "connector/"+c, act+" from flow "+r.f.Name, map[string]any{"job": id}); err != nil {
					return err
				}
				if as != "" {
					r.vars[as] = map[string]any{"status": "queued", "connector": c, "action": act, "job": id}
				}
				r.out.Calls = append(r.out.Calls, call)
				r.out.Steps = append(r.out.Steps, fmt.Sprintf("%s: call %s (queued, job %d)", i, call, id))
				continue
			}
			if err := store.Audit(o.ctx, o.tx, a.Org, a.ID, "connector.call", "connector/"+conn, action+" from flow "+r.f.Name, input); err != nil {
				return err
			}
			if as != "" {
				r.vars[as] = map[string]any{"status": "recorded", "connector": conn, "action": action}
			}
			r.out.Calls = append(r.out.Calls, call)
			r.out.Steps = append(r.out.Steps, fmt.Sprintf("%s: call %s (recorded)", i, call))
		case st["for_each"] != nil:
			// for_each: <list expression>, as: <var>, do: [steps]. The steps run once per item with the item in the
			// variable, in the flow's one transaction (a failing item rolls the whole run back); wait and approve are
			// not allowed inside. A list longer than maxForEach fails the run instead of holding locks for minutes.
			src, _ := st["for_each"].(string)
			if as == "" {
				return badRequest(fmt.Sprintf("flow %s step %s: for_each needs as:", r.f.Name, i))
			}
			v, err := cel.Eval(src, r.env())
			if err != nil {
				return fmt.Errorf("step %s for_each: %w", i, err)
			}
			items, ok := cel.Unwrap(v).([]any)
			if !ok && cel.Unwrap(v) != nil {
				return fmt.Errorf("step %s for_each: needs a list, got %T", i, cel.Unwrap(v))
			}
			if len(items) > maxForEach {
				return badRequest(fmt.Sprintf("flow %s step %s: for_each over %d items (at most %d per run)", r.f.Name, i, len(items), maxForEach))
			}
			prev, had := r.vars[as]
			r.out.Steps = append(r.out.Steps, fmt.Sprintf("%s: for_each → %d", i, len(items)))
			for k, it := range items {
				if r.stop {
					break
				}
				r.vars[as] = it
				if err := r.steps(meta.AsList(st["do"]), fmt.Sprintf("%s.%d.", i, k+1), 0); err != nil {
					return err
				}
			}
			if had {
				r.vars[as] = prev
			} else {
				delete(r.vars, as)
			}
		case st["wait"] != nil:
			if label != "" {
				return badRequest(fmt.Sprintf("flow %s step %s: wait is only allowed at the top level of a flow", r.f.Name, i))
			}
			d, err := r.waitFor(st["wait"])
			if err != nil {
				return fmt.Errorf("step %s wait: %w", i, err)
			}
			if d == 0 {
				r.out.Steps = append(r.out.Steps, i+": wait over")
				continue
			}
			if e.Queue == nil {
				return &StatusError{503, "no_queue", "wait needs the queue"}
			}
			next := idx + 1
			if _, isUntil := st["wait"].(map[string]any); isUntil {
				next = idx // check the condition again
			}
			id, err := e.Queue.Enqueue(o.ctx, o.tx, "flow", a.Org, map[string]any{"flow": r.f.Name, "event": r.vars["event"], "resume": next, "vars": r.saved(), "actor": a.JobArgs(), "manual": o.manualRun},
				EnqueueOpts{RunAt: e.Now().Add(d)})
			if err != nil {
				return err
			}
			r.out.WaitingJob = id
			r.out.Steps = append(r.out.Steps, fmt.Sprintf("%s: wait %s (job %d)", i, d, id))
			r.stop = true
		case st["approve"] != nil:
			if label != "" {
				return badRequest(fmt.Sprintf("flow %s step %s: approve is only allowed at the top level of a flow", r.f.Name, i))
			}
			spec := meta.AsMap(st["approve"])
			if when, _ := spec["when"].(string); when != "" {
				v, err := cel.Eval(when, r.env())
				if err == nil && !cel.Truthy(v) {
					r.out.Steps = append(r.out.Steps, i+": skipped")
					continue
				} // a when that cannot be evaluated requires approval (fail closed, as for model policies)
			}
			id, err := r.requestApproval(spec, i, idx+1, as)
			if err != nil {
				return err
			}
			r.out.WaitingApproval = id
			r.out.Steps = append(r.out.Steps, fmt.Sprintf("%s: approve (%s)", i, id))
			r.stop = true
		default:
			return badRequest(fmt.Sprintf("flow %s step %s: unknown step", r.f.Name, i))
		}
	}
	return nil
}

// saved is the flow's variables without the event, as stored to resume a flow.
func (r *flowRun) saved() map[string]any {
	out := map[string]any{}
	for k, v := range r.vars {
		if k != "event" {
			out[k] = v
		}
	}
	return out
}

// requestApproval inserts the pending request of an approve step (contract 0007 §4). It always waits: the
// flow's actor never approves it in passing (ruling R2). Approve resumes at top-level step resume.
func (r *flowRun) requestApproval(spec map[string]any, i string, resume int, as string) (string, error) {
	o, a := r.o, r.a
	var label map[string]any
	if l := meta.AsMap(spec["label"]); len(l) > 0 {
		label = map[string]any{}
		for k, v := range l {
			label[k] = v
		}
	}
	policy := str(label["id"])
	if policy == "" {
		policy = "step " + i
	}
	approvers := []string{}
	for _, x := range meta.AsList(spec["approvers"]) {
		approvers = append(approvers, str(x))
	}
	vars, event := r.saved(), r.vars["event"]
	roles := append([]string{}, a.Roles...)
	ap := store.Approval{Org: a.Org, Verb: "flow:" + r.f.Name, Policy: policy, Label: label, Approvers: approvers,
		State: "pending", Snapshot: hashJSON(map[string]any{"event": event, "vars": vars}),
		Args:        map[string]any{"resume": resume, "vars": vars, "event": event, "as": as, "manual": o.manualRun},
		RequestedBy: a.ID, Requester: a.requesterArgs(roles)}
	if err := store.InsertApproval(o.ctx, o.tx, &ap); err != nil {
		return "", err
	}
	if err := o.approvalEvent(&ap, "approval.requested", nil); err != nil {
		return "", err
	}
	return ap.ID, nil
}

// waitFor returns how long to wait: a duration ('10m', '2d'), or for {until: cel} 0 when true and one
// minute otherwise.
func (r *flowRun) waitFor(w any) (time.Duration, error) {
	switch x := w.(type) {
	case string:
		return cel.ParseDuration(x)
	case map[string]any:
		v, err := cel.Eval(str(x["until"]), r.env())
		if err != nil {
			return 0, err
		}
		if cel.Truthy(v) {
			return 0, nil
		}
		return time.Minute, nil
	}
	return 0, fmt.Errorf("wait needs a duration or {until: …}")
}

// recipients resolves notify: <target>: a variable holding a record or list of records, or a field of the
// records found by earlier steps (notify: owner → the owners of the activities found).
func recipients(target string, vars map[string]any) []string {
	seen := map[string]bool{}
	var out []string
	add := func(v any) {
		if s := str(cel.Unwrap(v)); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if v, ok := vars[target]; ok {
		switch x := v.(type) {
		case []any:
			for _, r := range x {
				add(meta.AsMap(r)["name"])
			}
		case map[string]any:
			add(x["name"])
		default:
			add(x)
		}
		return out
	}
	names := make([]string, 0, len(vars))
	for k := range vars {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if l, ok := vars[k].([]any); ok {
			for _, r := range l {
				if rm := meta.AsMap(r); rm != nil {
					if v, ok := rm[target]; ok {
						add(v)
					}
				}
			}
		} else if rm := meta.AsMap(vars[k]); rm != nil {
			if v, ok := rm[target]; ok {
				add(v)
			}
		}
	}
	if out == nil {
		out = []string{target}
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// recordFlows runs every installed flow whose on: is <model>.<event>, in the write's transaction and as the
// writer. The event is a copy of the record's fields at top level (flows written for 0.4 read event.<field>),
// with model, name, op, record (the record) and before (the stored record before the write; null on created)
// on top. Writes made by a flow do not start flows again. ran reports whether a flow ran (not skipped by if:).
func (o *op) recordFlows(m *meta.Model, before, rec store.Record, event string) (ran bool, err error) {
	if o.inFlow {
		return false, nil
	}
	reg := o.e.Reg()
	want := m.Name + "." + event
	names := make([]string, 0, len(reg.Flows))
	for n, f := range reg.Flows {
		if f.On == want && reg.IsInstalled(f.App) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		ev := map[string]any{}
		for k, v := range rec {
			ev[k] = v
		}
		var prev any
		if before != nil {
			prev = map[string]any(copyRecord(before))
		}
		ev["model"], ev["name"], ev["op"] = m.Name, str(rec["name"]), event
		ev["record"], ev["before"] = map[string]any(copyRecord(rec)), prev
		out := newFlowResult(n)
		if err := o.e.flowIn(o, reg.Flows[n], ev, 0, nil, out); err != nil {
			return ran, fmt.Errorf("flow %s: %w", n, err)
		}
		ran = ran || !out.Skipped
	}
	return ran, nil
}
