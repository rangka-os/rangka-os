package engine

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/money"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Money fields (contract 0013 §1, §3): the currency comes from currency_field (the row first, then the parent
// document), the rate from rate_field on the document (never a child row, whose rate is a unit price). IDR
// computes are rounded to whole rupiah, and every money field has an IDR twin <field>_idr.

var one = big.NewRat(1, 1)

// currencyOf is the ISO code of money field f: currency_field on the record, else on the parent document.
func (s *scope) currencyOf(f *meta.Field) string {
	cf := f.CurrencyField()
	if cf == "" {
		return ""
	}
	if s.m.Field(cf) != nil {
		return str(s.rec[cf])
	}
	if s.parent != nil && s.parent.m.Field(cf) != nil {
		return str(s.parent.rec[cf])
	}
	return ""
}

// docScope is the scope holding the document's rate: the record itself, or the parent document of a child row.
func (s *scope) docScope() *scope {
	if s.m.IsChild {
		return s.parent
	}
	return s
}

// rateOf is the positive IDR rate of money field f, read from rate_field on the document.
func (s *scope) rateOf(f *meta.Field) (*big.Rat, bool) {
	d := s.docScope()
	rf := f.RateField()
	if d == nil || d.m.Field(rf) == nil {
		return nil, false
	}
	r, ok := money.Rat(d.rec[rf])
	if !ok || r.Sign() <= 0 {
		return nil, false
	}
	return r, true
}

// idrRate is the rate a twin uses: 1 for IDR, the document rate otherwise (false when it is missing).
func (s *scope) idrRate(f *meta.Field) (*big.Rat, bool) {
	if money.IsIDR(s.currencyOf(f)) {
		return one, true
	}
	return s.rateOf(f)
}

// decimal is the money value of field name in rec (zero when it is not a decimal).
func decimal(rec store.Record, name string) money.Decimal {
	d, _ := rec[name].(money.Decimal)
	return d
}

// childScopes returns a scope for every row of every table field of s, linked to s as parent.
func (s *scope) childScopes(fn func(f *meta.Field, i int, cs *scope)) {
	for _, f := range s.m.Fields {
		if f.Type != "table" {
			continue
		}
		cm := s.e.Reg().Models[f.Of]
		if cm == nil {
			continue
		}
		for i, row := range rows(s.rec[f.Name]) {
			cs := s.e.newScope(s.ctx, s.q, s.actor, cm, row, s.links)
			cs.parent = s
			fn(f, i, cs)
		}
	}
}

// settleMoney sets every IDR twin of the record and its rows: round(value × rate), rate 1 for IDR. A missing
// rate or an overflow gives 0 here; check refuses that save.
func (o *op) settleMoney(m *meta.Model, rec store.Record) {
	o.scope(m, rec).settle()
}

func (s *scope) settle() {
	s.settleOwn()
	s.childScopes(func(_ *meta.Field, _ int, cs *scope) { cs.settle() })
}

// settleOwn sets the IDR twins of the record itself (not its rows).
func (s *scope) settleOwn() {
	for _, tw := range s.m.Fields {
		mf := s.m.Field(tw.MoneyOf())
		if tw.MoneyOf() == "" || mf == nil {
			continue
		}
		var n int64
		if r, ok := s.idrRate(mf); ok {
			n, _ = money.ToIDR(decimal(s.rec, mf.Name), r)
		}
		s.rec[tw.Name] = n
	}
}

const (
	msgBadCode  = "Kode mata uang harus tiga huruf besar, misalnya USD"
	msgFraction = "Rupiah tidak memakai desimal"
)

// checkMoney reports money issues of the record and its rows: an invalid currency code, a missing rate for a
// foreign currency, an entered IDR value with a fraction, and a value or twin out of range. prefix is the
// record's issue path ("items.0." for a row), docPrefix the path of its document's fields.
func (s *scope) checkMoney(prefix, docPrefix string, seen map[string]bool) []Issue {
	var out []Issue
	add := func(field, code, msg string) {
		if !seen[field+"\x00"+code] {
			seen[field+"\x00"+code] = true
			out = append(out, Issue{Field: field, Code: code, Message: msg})
		}
	}
	for _, f := range s.m.Fields {
		if f.Type != "money" {
			continue
		}
		code := s.currencyOf(f)
		cfPath := docPrefix + f.CurrencyField()
		if s.m.Field(f.CurrencyField()) != nil {
			cfPath = prefix + f.CurrencyField()
		}
		rfPath := prefix + f.RateField()
		if s.m.IsChild {
			rfPath = docPrefix + f.RateField()
		}
		d := decimal(s.rec, f.Name)
		switch {
		case !money.ValidCode(code):
			add(cfPath, "bad_value", msgBadCode)
		case money.IsIDR(code):
			if f.Compute == "" && !d.IsWhole() {
				add(prefix+f.Name, "bad_value", msgFraction)
			}
		default:
			if _, ok := s.rateOf(f); !ok {
				add(rfPath, "missing_rate", "Kurs wajib diisi untuk mata uang "+code)
			}
		}
		if f.Compute != "" {
			if err := s.computeErr(f); err != nil {
				msg := labelOr(f) + " tidak bisa dihitung"
				if isRange(err) {
					msg = labelOr(f) + " terlalu besar"
				}
				add(prefix+f.Name, "bad_value", msg)
			}
		}
		tooLarge := !money.InRange(d)
		if r, ok := s.idrRate(f); ok && money.ValidCode(code) {
			if _, err := money.ToIDR(d, r); err != nil {
				tooLarge = true
			}
		}
		if tooLarge {
			add(prefix+f.Name, "bad_value", labelOr(f)+" terlalu besar")
		}
	}
	s.childScopes(func(tf *meta.Field, i int, cs *scope) {
		out = append(out, cs.checkMoney(fmt.Sprintf("%s%s.%d.", prefix, tf.Name, i), prefix, seen)...)
	})
	return out
}

// computeErr re-evaluates computed money field f; compute stores 0 on an error, so check reports it here.
func (s *scope) computeErr(f *meta.Field) error {
	v, err := s.eval(f.Compute)
	if err != nil {
		return err
	}
	_, err = store.Coerce(f, v)
	return err
}

// isRange reports an amount outside numeric(20,4), from CEL decimal arithmetic or from store.Coerce.
func isRange(err error) bool {
	m := err.Error()
	return strings.Contains(m, money.ErrRange.Error()) || strings.HasSuffix(m, " is too large")
}
