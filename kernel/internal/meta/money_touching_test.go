package meta

import (
	"reflect"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// moneyReg is a registry by hand: the connectors, flows and models are plain maps.
func moneyReg(conns map[string]map[string]any, flows map[string]map[string]any, models map[string][]any) *Registry {
	r := &Registry{Connectors: map[string]*rk.Doc{}, Flows: map[string]*Flow{}, Models: map[string]*Model{}}
	for n, c := range conns {
		r.Connectors[n] = &rk.Doc{Data: c}
	}
	for n, f := range flows {
		r.Flows[n] = &Flow{Name: n, Raw: f}
	}
	for n, fs := range models {
		r.Models[n] = &Model{Name: n, Raw: map[string]any{"fields": fs}}
	}
	return r
}

func field(name, typ string) map[string]any { return map[string]any{"name": name, "type": typ} }

var payModels = map[string][]any{
	"x_omilo_payment": {field("amount", "money")},
	"deal":            {field("title", "data")},
}

func TestMoneyTouchingFlag(t *testing.T) {
	r := moneyReg(map[string]map[string]any{"x_a": {"money": true}, "x_b": {"money": false}}, nil, nil)
	if !r.MoneyTouching("x_a") || r.MoneyTouching("x_b") || r.MoneyTouching("nope") {
		t.Error("money: true must be the only flag")
	}
}

func TestMoneyTouchingPort(t *testing.T) {
	for port, want := range map[string]bool{"payment.qris.create": true, "bank.statement.list": true, "tax.efaktur.issue": true, "message.send": false} {
		r := moneyReg(map[string]map[string]any{"x_a": {"provides": map[string]any{port: "act"}}}, nil, nil)
		if got := r.MoneyTouching("x_a"); got != want {
			t.Errorf("provides %s: got %v want %v", port, got, want)
		}
	}
}

func TestMoneyTouchingByFlow(t *testing.T) {
	conn := map[string]map[string]any{"x_omilo": {"name": "x_omilo"}}
	trig := map[string]any{"on": "x_omilo.paid", "steps": []any{map[string]any{"create": "x_omilo_payment", "set": map[string]any{"amount": "1"}}}}
	call := map[string]any{"on": "deal.updated", "steps": []any{
		map[string]any{"call": "x_omilo.pay"},
		map[string]any{"if": "true", "then": []any{map[string]any{"create": "x_omilo_payment"}}}}}
	upd := map[string]any{"on": "x_omilo.paid", "steps": []any{
		map[string]any{"find": "x_omilo_payment", "where": "true", "as": "p"},
		map[string]any{"update": "p", "set": map[string]any{"amount": "2"}}}}
	for name, f := range map[string]map[string]any{"trigger": trig, "call": call, "update": upd} {
		r := moneyReg(conn, map[string]map[string]any{"f": f}, payModels)
		if !r.MoneyTouching("x_omilo") {
			t.Errorf("%s flow must make the connector money-touching", name)
		}
	}
	// a flow that never names the connector, or writes a model without money, does not count
	other := map[string]any{"on": "deal.updated", "steps": []any{map[string]any{"create": "x_omilo_payment"}}}
	plain := map[string]any{"on": "x_omilo.paid", "steps": []any{map[string]any{"create": "deal"}}}
	for name, f := range map[string]map[string]any{"unrelated": other, "plain model": plain} {
		if moneyReg(conn, map[string]map[string]any{"f": f}, payModels).MoneyTouching("x_omilo") {
			t.Errorf("%s flow must not count", name)
		}
	}
}

func TestMoneyTouchingReadOnlyCRM(t *testing.T) {
	r := moneyReg(map[string]map[string]any{"x_crm": {"name": "x_crm", "actions": []any{map[string]any{"name": "list", "request": map[string]any{"method": "GET"}}}}},
		map[string]map[string]any{"f": {"on": "x_crm.changed", "steps": []any{map[string]any{"create": "deal"}}}}, payModels)
	if r.MoneyTouching("x_crm") {
		t.Error("a read-only CRM connector is not money-touching")
	}
}

func TestWriteActions(t *testing.T) {
	c := map[string]any{"actions": []any{
		map[string]any{"name": "list", "request": map[string]any{"method": "GET"}},
		map[string]any{"name": "pay", "request": map[string]any{"method": "POST"}},
		map[string]any{"name": "drop", "request": map[string]any{"method": "delete"}},
		map[string]any{"name": "fetch_email", "mail": "fetch"}, // contract 0067: a fetch reads, a send writes
		map[string]any{"name": "send_email", "mail": "send"}}}
	if got, want := WriteActions(c), []string{"pay", "drop", "send_email"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
	if got := WriteActions(map[string]any{}); got == nil || len(got) != 0 {
		t.Errorf("no actions: got %#v", got)
	}
}

// An update whose target the flow cannot resolve fails closed (a flow that triggers on or calls the connector).
func TestMoneyTouchingUnresolvedUpdate(t *testing.T) {
	conn := map[string]map[string]any{"x_omilo": {"name": "x_omilo"}}
	upd := []any{map[string]any{"update": "ghost", "set": map[string]any{"a": "1"}}}
	for name, f := range map[string]map[string]any{"trigger": {"on": "x_omilo.paid", "steps": upd},
		"call": {"on": "deal.updated", "steps": append([]any{map[string]any{"call": "x_omilo.pay"}}, upd...)}} {
		if !moneyReg(conn, map[string]map[string]any{"f": f}, payModels).MoneyTouching("x_omilo") {
			t.Errorf("%s: unresolved update must count", name)
		}
	}
	// the same flow not tied to the connector does not count
	if moneyReg(conn, map[string]map[string]any{"f": {"on": "deal.updated", "steps": upd}}, payModels).MoneyTouching("x_omilo") {
		t.Error("unrelated flow must not count")
	}
	// a resolved update to a plain model still does not count
	ok := map[string]any{"on": "x_omilo.paid", "steps": []any{map[string]any{"find": "deal", "where": "true", "as": "d"}, map[string]any{"update": "d"}}}
	if moneyReg(conn, map[string]map[string]any{"f": ok}, payModels).MoneyTouching("x_omilo") {
		t.Error("resolved plain update must not count")
	}
}

// Fix wave item 3: for_each over a find_all variable binds as: to that variable's models, so a loop that updates a
// plain model is not money-touching and one over a money model is; any other list expression still fails closed.
func TestMoneyTouchingForEach(t *testing.T) {
	conn := map[string]map[string]any{"x_omilo": {"name": "x_omilo"}}
	loop := func(model, over string) map[string]any {
		return map[string]any{"on": "x_omilo.changed", "steps": []any{
			map[string]any{"find_all": model, "where": "true", "as": "rows"},
			map[string]any{"for_each": over, "as": "d", "do": []any{map[string]any{"update": "d", "set": map[string]any{"a": "1"}}}}}}
	}
	for name, c := range map[string]struct {
		f    map[string]any
		want bool
	}{
		"plain model loop": {loop("deal", "rows"), false},
		"money model loop": {loop("x_omilo_payment", "rows"), true},
		"expression":       {loop("deal", "rows.filter(r, true)"), true},
		"unbound list":     {loop("deal", "others"), true},
		"event list":       {loop("deal", "event.items"), true},
	} {
		if got := moneyReg(conn, map[string]map[string]any{"f": c.f}, payModels).MoneyTouching("x_omilo"); got != c.want {
			t.Errorf("%s: got %v want %v", name, got, c.want)
		}
	}
}
