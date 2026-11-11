package engine

import (
	"context"
	"sync"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/money"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Clock computes are computed fields whose value depends on the date: they call today() or now(), read
// another clock compute of their record, or read a table whose rows have clock computes. Computes run at
// save, so the stored value of a clock compute goes stale when the date passes (a certification saved while
// valid stays expired = false). Reads recompute them: Get, List (before its filter) and dashboards serve the
// value as of today. The stored column keeps the value of the last save.

var clockCache sync.Map // *meta.Model → map[string]bool

// clockFields returns the clock computes of m by name (cached per model; a registry reload makes new models).
func (e *Engine) clockFields(m *meta.Model) map[string]bool {
	return e.clockFieldsOf(m, map[*meta.Model]bool{})
}

func (e *Engine) clockFieldsOf(m *meta.Model, visiting map[*meta.Model]bool) map[string]bool {
	if v, ok := clockCache.Load(m); ok {
		return v.(map[string]bool)
	}
	visiting[m] = true
	defer delete(visiting, m)
	out := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, f := range m.Fields {
			if f.Compute == "" || out[f.Name] {
				continue
			}
			refs, err := cel.FreeVars(f.Compute)
			if err != nil {
				continue
			}
			for _, r := range refs {
				clock := false
				switch {
				case r.Kind == "fn":
					clock = r.Head == "today" || r.Head == "now"
				case out[r.Head]:
					clock = true
				default:
					if tf := m.Field(r.Head); tf != nil && tf.Type == "table" {
						if cm := e.Reg().Models[tf.Of]; cm != nil && !visiting[cm] && len(e.clockFieldsOf(cm, visiting)) > 0 {
							clock = true
						}
					}
				}
				if clock {
					out[f.Name], changed = true, true
					break
				}
			}
		}
	}
	clockCache.Store(m, out)
	return out
}

// refreshClock recomputes the clock computes of rec and of its child rows in place. children tells whether
// rec carries its child rows (a list read without children has empty tables): without them, computes that
// read a table are left as stored.
func (e *Engine) refreshClock(ctx context.Context, a Actor, m *meta.Model, rec store.Record, children bool, links map[string]map[string]any) {
	_ = e.refreshClockErr(ctx, a, m, rec, children, links)
}

// refreshClockErr is refreshClock that stops on a database error met while a compute loads a linked record
// (rec is then partly refreshed and must not be used); an expression error still leaves the field zero.
func (e *Engine) refreshClockErr(ctx context.Context, a Actor, m *meta.Model, rec store.Record, children bool, links map[string]map[string]any) error {
	clock := e.clockFields(m)
	if len(clock) == 0 && !(children && e.hasClockRows(m)) {
		return nil
	}
	s := e.newScope(ctx, e.DB, a, m, rec, links)
	return s.refreshClock(clock, children)
}

func (s *scope) refreshClock(clock map[string]bool, children bool) error {
	tables := map[string]bool{}
	for _, f := range s.m.Fields {
		if f.Type != "table" {
			continue
		}
		tables[f.Name] = true
		cm := s.e.Reg().Models[f.Of]
		if cm == nil || !children {
			continue
		}
		cc := s.e.clockFields(cm)
		if len(cc) == 0 {
			continue
		}
		for _, row := range rows(s.rec[f.Name]) {
			child := s.e.newScope(s.ctx, s.q, s.actor, cm, row, s.links)
			child.parent = s
			if err := child.refreshClock(cc, children); err != nil {
				return err
			}
		}
	}
	for _, f := range computeOrder(s.m) {
		if !clock[f.Name] {
			continue
		}
		if !children && readsTable(f, tables) {
			continue
		}
		v, err := s.eval(f.Compute)
		if err != nil && isLoadError(err) {
			return err
		}
		if err != nil || v == nil {
			s.rec[f.Name] = store.Zero(f)
			continue
		}
		c, err := store.Coerce(f, v)
		if err != nil {
			c = store.Zero(f)
		}
		if d, ok := c.(money.Decimal); ok && money.IsIDR(s.currencyOf(f)) {
			c = d.Round(0)
		}
		s.rec[f.Name] = c
		s.settleOwn()
	}
	return nil
}

// hasClockRows tells whether a table of m has rows with clock computes (m itself may have none: a worker whose
// certifications carry expired).
func (e *Engine) hasClockRows(m *meta.Model) bool {
	for _, f := range m.Fields {
		if f.Type == "table" {
			if cm := e.Reg().Models[f.Of]; cm != nil && len(e.clockFields(cm)) > 0 {
				return true
			}
		}
	}
	return false
}

func readsTable(f *meta.Field, tables map[string]bool) bool {
	refs, _ := cel.FreeVars(f.Compute)
	for _, r := range refs {
		if r.Kind == "ref" && tables[r.Head] {
			return true
		}
	}
	return false
}
