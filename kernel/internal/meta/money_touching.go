package meta

import (
	"sort"
	"strings"
)

// Money-touching connectors (contract 0033 §4). One rule, three callers: Registry.MoneyTouching, the validator's
// money_unflagged warning and the change-set risk table (changeset.AssessWith). rangka_validate.py money_touching
// is the Python twin.

// SensitiveField is a money field (money: true; currency and money typed fields unless money: false) or a stock
// field (stock: true).
func SensitiveField(f map[string]any) bool {
	if b, ok := f["money"].(bool); ok {
		if b {
			return true
		}
	} else if t, _ := f["type"].(string); t == "currency" || t == "money" {
		return true
	}
	return f["stock"] == true
}

// SensitiveModel reports whether the model's field list (raw `fields:`) holds a money or stock field.
func SensitiveModel(fields any) bool {
	for _, f := range AsList(fields) {
		if SensitiveField(AsMap(f)) {
			return true
		}
	}
	return false
}

// moneyPorts are the `provides` port prefixes that make a connector money-touching (contract 0002 §B).
var moneyPorts = []string{"payment.", "bank.", "tax."}

// MoneyDeclared is the part of the rule that needs no flows: `money: true`, or a provided port under payment.,
// bank. or tax.
func MoneyDeclared(c map[string]any) bool {
	if c["money"] == true {
		return true
	}
	ports := make([]string, 0)
	for k := range AsMap(c["provides"]) {
		ports = append(ports, k)
	}
	sort.Strings(ports)
	for _, p := range ports {
		for _, pre := range moneyPorts {
			if strings.HasPrefix(p, pre) {
				return true
			}
		}
	}
	return false
}

// MoneyByFlow is the flow part of the rule: a flow with `on: <connector>.<trigger>` or a `call: <connector>.<action>`
// step that creates or updates a model for which sensitive returns true. An update names a variable; it counts when
// the variable was bound (find, find_all, create) to such a model earlier in the same flow. An update of a variable
// the flow does not bind to a model counts as no write here (the risk table rates it high on its own).
func MoneyByFlow(connector string, flows []map[string]any, sensitive func(model string) bool) bool {
	prefix := connector + "."
	for _, f := range flows {
		on, _ := f["on"].(string)
		touches := strings.HasPrefix(on, prefix)
		w := &flowMoney{prefix: prefix, vars: map[string]map[string]bool{}}
		if head, _, ok := strings.Cut(on, "."); ok && !touches {
			w.bind("doc", head) // a record event: doc is that record (a non-model head is no sensitive model)
		}
		w.walk(AsList(f["steps"]))
		if !touches && !w.calls {
			continue
		}
		for _, m := range w.written {
			if sensitive(m) {
				return true
			}
		}
	}
	return false
}

type flowMoney struct {
	prefix  string
	calls   bool
	vars    map[string]map[string]bool
	written []string
}

func (w *flowMoney) bind(v, model string) {
	if v == "" || model == "" {
		return
	}
	if w.vars[v] == nil {
		w.vars[v] = map[string]bool{}
	}
	w.vars[v][model] = true
}

func (w *flowMoney) walk(steps []any) {
	for _, s := range steps {
		st := AsMap(s)
		as, _ := st["as"].(string)
		if call, _ := st["call"].(string); strings.HasPrefix(call, w.prefix) {
			w.calls = true
		}
		for _, k := range []string{"find", "find_all"} {
			if m, _ := st[k].(string); m != "" {
				w.bind(as, m)
			}
		}
		if m, _ := st["create"].(string); m != "" {
			w.written = append(w.written, m)
			w.bind(as, m)
		}
		if v, _ := st["update"].(string); v != "" {
			ms := make([]string, 0, len(w.vars[v]))
			for m := range w.vars[v] {
				ms = append(ms, m)
			}
			sort.Strings(ms)
			w.written = append(w.written, ms...)
		}
		for _, k := range []string{"then", "else", "do"} {
			w.walk(AsList(st[k]))
		}
	}
}

// MoneyTouchingOf is the whole rule over plain maps: connector name, its file, the flows (raw files) and a
// sensitive-model lookup.
func MoneyTouchingOf(name string, c map[string]any, flows []map[string]any, sensitive func(model string) bool) bool {
	return MoneyDeclared(c) || MoneyByFlow(name, flows, sensitive)
}

// MoneyTouching reports whether a loaded connector is money-touching (contract 0033 §4).
func (r *Registry) MoneyTouching(connector string) bool {
	d := r.Connectors[connector]
	if d == nil {
		return false
	}
	return MoneyTouchingOf(connector, AsMap(d.Data), r.flowRaws(), r.sensitiveModel)
}

func (r *Registry) flowRaws() []map[string]any {
	names := make([]string, 0, len(r.Flows))
	for n := range r.Flows {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]map[string]any, 0, len(names))
	for _, n := range names {
		out = append(out, r.Flows[n].Raw)
	}
	return out
}

func (r *Registry) sensitiveModel(name string) bool {
	m := r.Models[name]
	return m != nil && SensitiveModel(m.Raw["fields"])
}

// WriteActions are the names of a connector's actions whose request method is not GET, in file order (contract 0033 §4).
func WriteActions(c map[string]any) []string {
	out := []string{}
	for _, a := range AsList(c["actions"]) {
		am := AsMap(a)
		if m, _ := AsMap(am["request"])["method"].(string); strings.ToUpper(m) != "GET" {
			if n, _ := am["name"].(string); n != "" {
				out = append(out, n)
			}
		}
	}
	return out
}
