package engine

import (
	"sort"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// WorkItem is one work screen offered to an actor (contract 0030 §3): Home marks the screen the web app opens on
// sign-in (the first item with Home wins).
type WorkItem struct {
	Screen string `json:"screen"`
	Home   bool   `json:"home"`
}

// screenEnv is the CEL environment of a screen's if: and set: user, org, tax, today() and now().
func (e *Engine) screenEnv(a Actor) *cel.Env {
	reg := e.Reg()
	return &cel.Env{Today: e.Today, Now: e.Now, Apps: reg.IsInstalled, Vars: func(n string) (any, bool) { return GlobalVar(reg, a, n) }}
}

// screenIf reports whether the screen's if: holds for a (no if: holds; an expression that fails hides the screen).
func (e *Engine) screenIf(a Actor, s *meta.Screen) bool {
	if s.If == "" {
		return true
	}
	v, err := cel.Eval(s.If, e.screenEnv(a))
	if err != nil {
		e.Log("screen %s: if: %v", s.Name, err)
		return false
	}
	return cel.Truthy(v)
}

// Work lists the screens a may open, in the order of the installed apps' work: lists: a screen bound by an app
// for one of a's roles (narrowed by the entry's roles, else the screen's own) whose if: holds. The owner may open
// every bound screen and keeps the dashboard home, so no item is Home for them.
func (e *Engine) Work(a Actor) []WorkItem {
	reg := e.Reg()
	var out []WorkItem
	at := map[string]int{}
	ifs := map[string]bool{}
	for _, w := range reg.Work() {
		s := reg.Screen(w.Screen)
		roles := w.Roles
		if len(roles) == 0 {
			roles = s.Roles
		}
		ok := unrestricted(a)
		for _, r := range roles {
			ok = ok || hasRole(a, r)
		}
		if !ok {
			continue
		}
		pass, seen := ifs[s.Name]
		if !seen {
			pass = e.screenIf(a, s)
			ifs[s.Name] = pass
		}
		if !pass {
			continue
		}
		home := w.Home && !unrestricted(a)
		if i, dup := at[s.Name]; dup {
			out[i].Home = out[i].Home || home
			continue
		}
		at[s.Name] = len(out)
		out = append(out, WorkItem{Screen: s.Name, Home: home})
	}
	return out
}

// ScreenAllowed returns the screen when a may open it (Work lists it), else nil.
func (e *Engine) ScreenAllowed(a Actor, name string) *meta.Screen {
	for _, w := range e.Work(a) {
		if w.Screen == name {
			return e.Reg().Screen(name)
		}
	}
	return nil
}

// ScreenSet evaluates the set: of the screen's type block for a, now (contract 0030 §2: set values are CEL over
// user, today() and now()). The values prefill the record the screen creates; they are not trusted more than a
// form's input.
func (e *Engine) ScreenSet(a Actor, s *meta.Screen) (map[string]any, error) {
	set := meta.AsMap(s.Block()["set"])
	out := map[string]any{}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	env := e.screenEnv(a)
	for _, k := range keys {
		src, _ := set[k].(string)
		v, err := cel.Eval(src, env)
		if err != nil {
			return nil, &StatusError{Status: 422, Code: "screen_set", Msg: "screen " + s.Name + " set." + k + ": " + err.Error()}
		}
		v = cel.Unwrap(v)
		if t, ok := v.(time.Time); ok {
			v = t.In(e.Location()).Format(time.RFC3339)
		}
		out[k] = v
	}
	return out, nil
}
