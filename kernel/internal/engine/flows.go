package engine

import (
	"context"
	"fmt"
	"sort"
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
	Flow          string         `json:"flow"`
	Skipped       bool           `json:"skipped,omitempty"`
	Steps         []string       `json:"steps"`
	Created       []string       `json:"created"`
	Updated       []string       `json:"updated"`
	Calls         []string       `json:"calls"`
	Notifications []Notification `json:"notifications"`
	WaitingJob    int64          `json:"waiting_job,omitempty"` // a wait: step put the rest on the queue
}

// RunFlow runs a flow from its first step. event is the trigger payload (flows with on:).
func (e *Engine) RunFlow(ctx context.Context, a Actor, flowName string, event map[string]any) (*FlowResult, error) {
	return e.RunFlowFrom(ctx, a, flowName, event, 0, nil)
}

// RunFlowFrom runs a flow's steps in one transaction from top-level step start with variables vars (a
// resumed wait). Steps: find, find_all, create, update, notify (log driver), call (connector action; the
// kernel records the call, it has no connector runtime yet), if/then/else and wait (the remaining steps are
// queued for later). Every step may have if: and as:.
func (e *Engine) RunFlowFrom(ctx context.Context, a Actor, flowName string, event map[string]any, start int, vars map[string]any) (*FlowResult, error) {
	f := e.Reg().Flows[flowName]
	if f == nil || !e.Reg().IsInstalled(f.App) {
		return nil, notFound("flow " + flowName)
	}
	if event == nil {
		event = map[string]any{}
	}
	connector := ""
	if head, _, ok := strings.Cut(f.On, "."); ok && e.Reg().Models[head] == nil {
		connector = head
	}
	out := &FlowResult{Flow: flowName, Steps: []string{}, Created: []string{}, Updated: []string{}, Calls: []string{}, Notifications: []Notification{}}
	err := e.run(ctx, a, func(o *op) error {
		if vars == nil {
			vars = map[string]any{}
		}
		vars["event"] = event
		r := &flowRun{o: o, e: e, a: a, f: f, vars: vars, out: out}
		if start == 0 && connector != "" && len(event) > 0 {
			if err := store.Audit(o.ctx, o.tx, a.Org, a.ID, "connector.event", "connector/"+connector, f.On+" → flow "+flowName, nil); err != nil {
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
		steps := f.Steps
		var list []any
		for _, st := range steps {
			list = append(list, map[string]any(st))
		}
		if err := r.steps(list, "", start); err != nil {
			return err
		}
		return store.Audit(o.ctx, o.tx, a.Org, a.ID, "flow.run", "flow/"+flowName, fmt.Sprintf("%d step(s), %d created, %d notification(s)", len(out.Steps), len(out.Created), len(out.Notifications)), nil)
	})
	if err != nil && connector != "" {
		store.Audit(context.WithoutCancel(ctx), e.DB, a.Org, a.ID, "connector.error", "connector/"+connector, "flow "+flowName+": "+err.Error(), nil)
	}
	return out, err
}

type flowRun struct {
	o    *op
	e    *Engine
	a    Actor
	f    *meta.Flow
	vars map[string]any
	out  *FlowResult
	stop bool
}

func (r *flowRun) env() *cel.Env {
	return &cel.Env{Today: r.e.Today, Now: r.e.Now, Apps: r.e.Reg().IsInstalled, Vars: func(n string) (any, bool) {
		if v, ok := r.vars[n]; ok {
			return v, true
		}
		if n == "user" {
			return UserVar(r.a), true
		}
		return nil, false
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
			res, err := o.create(mn, input, CreateOpts{})
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
			res, err := o.update(mn, name, input)
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
			e.Log("notify[%s] %s %v: %s", ch, to, n.Recipients, n.Text)
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
			if err := store.Audit(o.ctx, o.tx, a.Org, a.ID, "connector.call", "connector/"+conn, action+" from flow "+r.f.Name, input); err != nil {
				return err
			}
			if as != "" {
				r.vars[as] = map[string]any{"status": "recorded", "connector": conn, "action": action}
			}
			r.out.Calls = append(r.out.Calls, call)
			r.out.Steps = append(r.out.Steps, fmt.Sprintf("%s: call %s (recorded)", i, call))
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
			saved := map[string]any{}
			for k, v := range r.vars {
				if k != "event" {
					saved[k] = v
				}
			}
			id, err := e.Queue.Enqueue(o.ctx, o.tx, "flow", a.Org, map[string]any{"flow": r.f.Name, "event": r.vars["event"], "resume": next, "vars": saved},
				EnqueueOpts{RunAt: e.Now().Add(d)})
			if err != nil {
				return err
			}
			r.out.WaitingJob = id
			r.out.Steps = append(r.out.Steps, fmt.Sprintf("%s: wait %s (job %d)", i, d, id))
			r.stop = true
		default:
			return badRequest(fmt.Sprintf("flow %s step %s: unknown step", r.f.Name, i))
		}
	}
	return nil
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
