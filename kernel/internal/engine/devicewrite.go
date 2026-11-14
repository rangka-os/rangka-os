package engine

import (
	"context"
	"encoding/json"
	"math/big"
	"sort"
	"strconv"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/money"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Replay rules of device writes (contract 0057 §10 and its conflict rules). A write that runs under WithDeviceWrite
// already happened at the outlet, so the kernel records what it would have done differently instead of refusing:
//   - clock: today(), now() and the business date (date defaults, stock posting dates, kernel numbering periods)
//     are the write's occurred_at in the org's time zone (nowIn, todayIn);
//   - money: the computed currency fields the device sent (and may see) for its own document of a naming_on:
//     device model are stored; the kernel computes its own in shadow and records edge_price_drift
//     {field, device, kernel} where they differ (DeviceMoney, visiblePins, prepare);
//   - rules: a failing save or submit rule of the write's own document of a naming_on: device model, at create or
//     submit, becomes rule_overridden {model, rule, message} (overrideRules); cancels, records a flow writes, other
//     models, permissions, the actor check, device_name_invalid and duplicate_name still refuse;
//   - voids: a cancel of a naming_on: device document made elsewhere is refused with other_device (cancelHere);
//   - approvals: a write that would wait for one is refused with approval_needed_offline before anything is saved
//     (hold); the device never offers it offline;
//   - shifts: a record linking a naming_on: device record made on another device is refused with
//     shift_other_device (deviceLinks, contract 0057 §6);
//   - stock: a move that leaves a balance below zero records negative_stock {item, outlet, balance} and is never
//     refused, not even for negative: refuse.
// Exceptions are written in the write's transaction (flushExceptions), so a write that rolls back leaves none.

// nowIn is the time of a unit of work under ctx: a device write's occurred_at, else the engine clock.
func (e *Engine) nowIn(ctx context.Context) time.Time {
	if dw := deviceWriteOf(ctx); dw != nil && !dw.OccurredAt.IsZero() {
		return dw.OccurredAt
	}
	return e.Now()
}

// todayIn is the ISO business date under ctx in the organisation time zone.
func (e *Engine) todayIn(ctx context.Context) string {
	return e.nowIn(ctx).In(e.Location()).Format("2006-01-02")
}

// DeviceMoney is the money a device charged for a record of model, read from the body it sent: every top-level key
// that is a computed field of type currency, and for each table field the same of its child model, as a list with
// one map per row (by position, the rows of the body that are objects; a row without such a key is an empty map).
// Other keys are left out. nil when the body carries none.
func (e *Engine) DeviceMoney(model string, body map[string]any) map[string]any {
	m := e.Reg().Model(model)
	if m == nil {
		return nil
	}
	return deviceMoney(e.Reg(), m, body)
}

func deviceMoney(reg *meta.Registry, m *meta.Model, rec map[string]any) map[string]any {
	var out map[string]any
	put := func(k string, v any) {
		if out == nil {
			out = map[string]any{}
		}
		out[k] = v
	}
	for _, f := range m.Fields {
		v, ok := rec[f.Name]
		if !ok || v == nil {
			continue
		}
		switch {
		case f.Type == "currency" && f.Compute != "":
			put(f.Name, v)
		case f.Type == "table":
			cm := reg.Models[f.Of]
			if cm == nil {
				continue
			}
			var list []any
			sent := false
			for _, row := range rows(v) {
				rp := deviceMoney(reg, cm, row)
				if rp == nil {
					rp = map[string]any{}
				} else {
					sent = true
				}
				list = append(list, rp)
			}
			if sent {
				put(f.Name, list)
			}
		}
	}
	return out
}

// rowPins are the pins of row i of table field of pins (nil when there are none).
func rowPins(pins map[string]any, field string, i int) map[string]any {
	l, _ := pins[field].([]any)
	if i >= len(l) {
		return nil
	}
	p, _ := l[i].(map[string]any)
	return p
}

// pinned is the device's value of computed currency field f as the kernel stores it; false when the device sent
// none or a value that is not an amount (the kernel's value is then kept).
func (s *scope) pinned(f *meta.Field) (any, bool) {
	v, ok := s.pins[f.Name]
	if !ok || f.Type != "currency" || f.Compute == "" {
		return nil, false
	}
	c, err := store.Coerce(f, v)
	if err != nil {
		return nil, false
	}
	if _, ok := exact(c); !ok {
		return nil, false
	}
	if d, ok := c.(money.Decimal); ok && money.IsIDR(s.currencyOf(f)) {
		c = d.Round(0)
	}
	return c, true
}

// amountText is an amount as a JSON number.
func amountText(r *big.Rat) json.Number {
	if r.IsInt() {
		return json.Number(r.Num().String())
	}
	return json.Number(r.FloatString(4))
}

// recordDrift records an edge_price_drift for every pinned field (and pinned row field) of rec whose kernel value
// in shadow differs.
func (o *op) recordDrift(pins map[string]any, rec, shadow store.Record, prefix string) {
	keys := make([]string, 0, len(pins))
	for k := range pins {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if l, ok := pins[k].([]any); ok {
			got, kern := rows(rec[k]), rows(shadow[k])
			for i, rp := range l {
				p, _ := rp.(map[string]any)
				if p == nil || i >= len(got) || i >= len(kern) {
					continue
				}
				o.recordDrift(p, got[i], kern[i], prefix+k+"."+strconv.Itoa(i)+".")
			}
			continue
		}
		dev, ok1 := exact(rec[k])
		kern, ok2 := exact(shadow[k])
		if !ok1 || !ok2 || dev.Cmp(kern) == 0 {
			continue
		}
		o.except("edge_price_drift", "", "", map[string]any{"field": prefix + k, "device": amountText(dev), "kernel": amountText(kern)})
	}
}

// overrideRules turns the rule issues of errs into rule_overridden exceptions while check runs over the device
// write's own document of a naming_on: device model at create or submit (o.override, contract 0057 §10): a sale
// already paid at the outlet. Any other issue stays; a cancel, another record of the unit (one a flow writes) and
// a model not named on the device keep their refusals.
func (o *op) overrideRules(m *meta.Model, errs []Issue) []Issue {
	if len(errs) == 0 || !o.override {
		return errs
	}
	var kept []Issue
	for _, is := range errs {
		if is.Rule == "" {
			kept = append(kept, is)
			continue
		}
		o.except("rule_overridden", "", m.Name+"\x00"+is.Rule, map[string]any{"model": m.Name, "rule": is.Rule, "message": is.Message})
	}
	return kept
}

// visiblePins keeps the pins of fields the actor can see (contract 0027): a hidden figure (a blind close's
// difference) is never taken from the device. Rows are kept by position.
func (o *op) visiblePins(m *meta.Model, pins map[string]any) map[string]any {
	var out map[string]any
	for k, v := range pins {
		f := m.Field(k)
		if f == nil || o.e.FieldHidden(o.actor, m, f) {
			continue
		}
		if f.Type == "table" {
			cm := o.registry().Models[f.Of]
			l, _ := v.([]any)
			if cm == nil || l == nil {
				continue
			}
			rowsOut := make([]any, len(l))
			for i, rp := range l {
				p, _ := rp.(map[string]any)
				vp := o.visiblePins(cm, p)
				if vp == nil {
					vp = map[string]any{}
				}
				rowsOut[i] = vp
			}
			v = rowsOut
		}
		if out == nil {
			out = map[string]any{}
		}
		out[k] = v
	}
	return out
}

// madeHere reports whether record name of m was made on the acting device (created_device, contract 0057 §6).
func (o *op) madeHere(m *meta.Model, name string) (bool, error) {
	if o.actor.Device == nil {
		return false, nil
	}
	dev, err := store.CreatedDevice(o.ctx, o.tx, o.actor.Org, m, name)
	return dev == o.actor.Device.ID, err
}

// cancelHere refuses a device write's cancel of a naming_on: device document another device (or head office) made
// (other_device): a void is done on the device that sold it (contract 0057 §6).
func (o *op) cancelHere(m *meta.Model, name string) error {
	if m.NamingOn != "device" || deviceWriteOf(o.ctx) == nil {
		return nil
	}
	mine, err := o.madeHere(m, name)
	if err != nil || mine {
		return err
	}
	return &StatusError{Status: 422, Code: "other_device", Msg: m.Label.Text() + " " + name + " tidak dibuat di perangkat ini, jadi tidak bisa dibatalkan di sini"}
}

// approvalNeededOffline refuses a device write that would wait for an approval (contract 0057 §10, ruling R7).
func approvalNeededOffline() error {
	return &StatusError{Status: 422, Code: "approval_needed_offline", Msg: "Perlu persetujuan, tunggu online."}
}

// deviceLinks refuses, in a device write, a record whose link points at a naming_on: device record made on another
// device (a sale on another cashier's shift, contract 0057 §6). A linked record made without a device (head office)
// or before created_device existed passes.
func (o *op) deviceLinks(m *meta.Model, rec store.Record) error {
	a := o.actor
	if a.Device == nil || deviceWriteOf(o.ctx) == nil {
		return nil
	}
	for _, f := range m.Fields {
		if f.Type != "link" {
			continue
		}
		v := str(rec[f.Name])
		tm := o.registry().Model(f.To)
		if v == "" || tm == nil || tm.NamingOn != "device" || tm.IsChild {
			continue
		}
		dev, err := store.CreatedDevice(o.ctx, o.tx, a.Org, tm, v)
		if err != nil {
			return err
		}
		if dev != "" && dev != a.Device.ID {
			return &StatusError{Status: 422, Code: "shift_other_device",
				Msg: labelOr(f) + " " + v + " dibuka di perangkat lain; perangkat ini hanya bisa memakai miliknya sendiri"}
		}
	}
	return nil
}

// pendingException is an exception of the unit, written when it commits; ref "" is the unit's target.
type pendingException struct {
	key string
	x   store.Exception
}

// except notes an exception of kind for the unit's device write; key, when set, keeps one per key; outlet "" is the
// device's outlet.
func (o *op) except(kind, outlet, key string, detail map[string]any) {
	if key != "" {
		key = kind + "\x00" + key
		for _, p := range o.exceptions {
			if p.key == key {
				return
			}
		}
	}
	o.exceptions = append(o.exceptions, pendingException{key: key, x: store.Exception{Org: o.actor.Org, Outlet: outlet, Kind: kind, Detail: detail}})
}

// flushExceptions writes the unit's exceptions with ref = target (<model>/<name> of its document).
func (o *op) flushExceptions(target string) error {
	for _, p := range o.exceptions {
		x := p.x
		if d := o.actor.Device; d != nil {
			x.Device = d.ID
			if x.Outlet == "" {
				x.Outlet = d.Outlet
			}
		}
		if x.Ref == "" {
			x.Ref = target
		}
		if err := store.RecordException(o.ctx, o.tx, x); err != nil {
			return err
		}
	}
	return nil
}
