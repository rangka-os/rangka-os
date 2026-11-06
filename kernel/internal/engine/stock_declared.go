package engine

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// Declared stock moves (contract 0021): a submittable model with on_submit stock.post_moves and a stock: block
// moves stock by its declaration instead of a Go case. The six built-in documents keep their Go rules.

// LegacyStockModels are the documents stock.post_moves has a Go rule for; they may not declare stock:.
var LegacyStockModels = map[string]bool{"goods_receipt": true, "stock_transfer": true, "stock_count": true,
	"stock_adjustment": true, "stock_transfer_receipt": true, "outlet_sales": true}

type stockRule struct {
	lines, outlet, item, qty, cond, reason, cogs string
	in, explode, lineOutlet                      bool
	cost, costField                              string // average | unit | amount | source | consumed
}

type stockSpec struct {
	date, cogsTotal string
	refuseNegative  bool
	rules           []stockRule
}

// stockSpecOf reads a model's stock: block (nil when it has none).
func stockSpecOf(m *meta.Model) *stockSpec {
	raw := meta.AsMap(m.Raw["stock"])
	if raw == nil {
		return nil
	}
	sp := &stockSpec{date: str(raw["date"]), cogsTotal: str(raw["cogs_total"]), refuseNegative: str(raw["negative"]) == "refuse"}
	if sp.date == "" {
		for _, f := range m.Fields {
			if f.Type == "date" && (f.Required || sp.date == "") {
				if sp.date == "" || f.Required {
					sp.date = f.Name
				}
				if f.Required {
					break
				}
			}
		}
	}
	for _, rv := range meta.AsList(raw["moves"]) {
		r := meta.AsMap(rv)
		ru := stockRule{lines: str(r["lines"]), item: str(r["item"]), qty: str(r["qty"]), cond: str(r["if"]), reason: str(r["reason"]),
			cogs: str(r["cogs"]), explode: str(r["explode"]) == "recipe", cost: "average"}
		if ru.item == "" {
			ru.item = "item"
		}
		ru.outlet, ru.in = str(r["out"]), false
		if v := str(r["in"]); v != "" {
			ru.outlet, ru.in = v, true
		}
		if f, ok := strings.CutPrefix(ru.outlet, "line."); ok {
			ru.outlet, ru.lineOutlet = f, true
		}
		switch c := r["cost"].(type) {
		case string:
			ru.cost = c
		case map[string]any:
			for _, k := range []string{"unit", "amount", "source"} {
				if v := str(c[k]); v != "" {
					ru.cost, ru.costField = k, v
				}
			}
			if c["consumed"] == true {
				ru.cost, ru.costField = "consumed", str(c["split"])
			}
		}
		sp.rules = append(sp.rules, ru)
	}
	return sp
}

// declaredLine is one row of a rule that moves stock.
type declaredLine struct {
	rule   int
	row    map[string]any
	outlet string
	item   string
	qty    float64
	uses   []materialUse // explode: recipe
}

// postDeclaredMoves is stock.post_moves for a model with a stock: block: every rule's rows are evaluated and
// their (outlet, item) balances locked in sorted order; then all out rules post (at the outlet average), then
// all in rules (at their cost), so a consumed cost sees what the document took out; line cogs and cogs_total are
// written with one SetSubmitted.
func postDeclaredMoves(h *HookCtx, sp *stockSpec) (map[string]any, error) {
	rec := h.Record
	date := dateOnly(rec[sp.date])
	if f := h.Model.Field(sp.date); f != nil && f.Type == "datetime" {
		date = localDate(h.op.e, rec[sp.date])
	}
	if date == "" {
		date = h.op.e.Today()
	}
	s := newStockPost(h, date)
	sc := h.op.scope(h.Model, rec)
	var lines []declaredLine
	var keys [][2]string
	recipes := map[string][]materialUse{}
	for i, ru := range sp.rules {
		lm := h.op.e.Reg().Models[h.Model.Field(ru.lines).Of]
		for _, row := range rows(rec[ru.lines]) {
			q := qtyOf(row[ru.qty])
			if ru.qty == "" {
				if has(lm, "stock_qty") {
					q = qtyOf(row["stock_qty"])
				} else {
					q = qtyOf(row["qty"])
				}
			}
			if q <= qtyEps {
				continue
			}
			if ru.cond != "" {
				sc.extra = map[string]any{"doc": map[string]any(rec), "line": row}
				v, err := sc.eval(ru.cond)
				if err != nil {
					return nil, &StatusError{422, "stock_if", fmt.Sprintf("stock.moves[%d].if of %s: %v", i, h.Model.Name, err)}
				}
				b, ok := v.(bool)
				if !ok {
					return nil, &StatusError{422, "stock_if", fmt.Sprintf("stock.moves[%d].if of %s must be true or false, got %v", i, h.Model.Name, v)}
				}
				if !b {
					continue
				}
			}
			dl := declaredLine{rule: i, row: row, item: str(row[ru.item]), qty: q, outlet: str(rec[ru.outlet])}
			if ru.lineOutlet {
				dl.outlet = str(row[ru.outlet])
			}
			if dl.item == "" {
				return nil, refuse("stock_item_missing", fmt.Sprintf("Baris %v %s belum diisi barangnya", row["idx"], ru.lines))
			}
			if dl.outlet == "" {
				return nil, refuse("stock_outlet_missing", fmt.Sprintf("Outlet %s untuk baris %v %s belum diisi", ru.outlet, row["idx"], ru.lines))
			}
			if ru.explode {
				uses, ok := recipes[dl.item]
				if !ok {
					var err error
					if uses, err = s.consumption(dl.item); err != nil {
						return nil, err
					}
					recipes[dl.item] = uses
				}
				dl.uses = uses
				for _, u := range uses {
					if roundQty(q*u.per) > qtyEps {
						keys = append(keys, [2]string{dl.outlet, u.material})
					}
				}
			} else {
				keys = append(keys, [2]string{dl.outlet, dl.item})
			}
			lines = append(lines, dl)
		}
	}
	sc.extra = nil
	if err := s.lockAll(keys); err != nil {
		return nil, err
	}
	cogs := map[string]int64{}
	hasCogs := map[string]bool{}
	var consumed, total int64
	out := func(dl declaredLine, outlet, item string, q float64, reason string) (int64, error) {
		mv, err := s.move(outlet, item, -q, 0, nil, reason)
		if err != nil {
			return 0, err
		}
		if sp.refuseNegative && mv["negative"] == true {
			return 0, refuse("negative_stock", fmt.Sprintf("Stok %s di %s tidak cukup: sisa %s", item, outlet, formatQty(qtyOf(mv["balance_qty"]))))
		}
		return -moneyIn(mv["value"]), nil
	}
	for _, dl := range lines {
		ru := sp.rules[dl.rule]
		if ru.in {
			continue
		}
		var v int64
		if ru.explode {
			for _, u := range dl.uses {
				q := roundQty(dl.qty * u.per)
				if q <= qtyEps {
					continue
				}
				x, err := out(dl, dl.outlet, u.material, q, ru.reason)
				if err != nil {
					return nil, err
				}
				v += x
			}
		} else {
			x, err := out(dl, dl.outlet, dl.item, dl.qty, ru.reason)
			if err != nil {
				return nil, err
			}
			v = x
		}
		consumed += v
		if ru.cogs != "" {
			k := ru.lines + "|" + ru.cogs + "|" + str(dl.row["name"])
			cogs[k] += v
			hasCogs[k] = true
			total += v
		}
	}
	// consumed: the out value split once over the lines of every consumed rule (outputs and by-products share one
	// pool), by qty or a share field, the last of those lines taking the remainder so the values add up to the
	// rupiah. A document that took value out for a consumed rule with no line to put it on is refused.
	shares := map[int]int64{}
	var pool []int
	var weight float64
	hasConsumed := false
	for j, dl := range lines {
		if ru := sp.rules[dl.rule]; ru.in && ru.cost == "consumed" {
			pool = append(pool, j)
			weight += splitWeight(dl, ru)
		}
	}
	for _, ru := range sp.rules {
		hasConsumed = hasConsumed || (ru.in && ru.cost == "consumed")
	}
	if hasConsumed && len(pool) == 0 && consumed != 0 {
		return nil, refuse("consumed_nowhere", "Bahan senilai "+rupiah(consumed)+" keluar, tetapi tidak ada baris hasil untuk menampung nilainya")
	}
	rest := consumed
	for k, j := range pool {
		v := rest
		if k < len(pool)-1 {
			v = 0
			if weight > 0 {
				v = mulRound(splitWeight(lines[j], sp.rules[lines[j].rule])/weight, consumed)
			}
		}
		shares[j] = v
		rest -= v
	}
	for j, dl := range lines {
		ru := sp.rules[dl.rule]
		if !ru.in {
			continue
		}
		var err error
		switch ru.cost {
		case "unit":
			_, err = s.move(dl.outlet, dl.item, dl.qty, moneyIn(dl.row[ru.costField]), nil, ru.reason)
		case "amount":
			v := moneyIn(dl.row[ru.costField])
			unit := moneyIn(dl.row["unit_cost"])
			if _, ok := dl.row["unit_cost"]; !ok {
				unit = divRound(v, dl.qty)
			}
			_, err = s.move(dl.outlet, dl.item, dl.qty, unit, &v, ru.reason)
		case "consumed":
			v := shares[j]
			_, err = s.move(dl.outlet, dl.item, dl.qty, divRound(v, dl.qty), &v, ru.reason)
		case "source":
			var unit int64
			var ok bool
			if unit, ok, err = sourceUnitCost(h, ru.costField, dl.item); err == nil {
				if !ok {
					unit, err = s.averageIn(dl.outlet, dl.item)
				}
				if err == nil {
					_, err = s.move(dl.outlet, dl.item, dl.qty, unit, nil, ru.reason)
				}
			}
		default: // average
			var unit int64
			if unit, err = s.averageIn(dl.outlet, dl.item); err == nil {
				_, err = s.move(dl.outlet, dl.item, dl.qty, unit, nil, ru.reason)
			}
		}
		if err != nil {
			return nil, err
		}
	}
	fields := map[string]any{}
	for _, ru := range sp.rules {
		if ru.cogs == "" {
			continue
		}
		tbl, _ := fields[ru.lines].(map[string]map[string]any)
		if tbl == nil {
			tbl = map[string]map[string]any{}
			fields[ru.lines] = tbl
		}
		for _, row := range rows(rec[ru.lines]) {
			k := ru.lines + "|" + ru.cogs + "|" + str(row["name"])
			if hasCogs[k] {
				if tbl[str(row["name"])] == nil {
					tbl[str(row["name"])] = map[string]any{}
				}
				tbl[str(row["name"])][ru.cogs] = moneyOut(cogs[k])
			}
		}
	}
	if sp.cogsTotal != "" {
		fields[sp.cogsTotal] = moneyOut(total)
	}
	if len(fields) > 0 {
		if _, err := h.SetSubmitted(h.Model.Name, s.voucher, fields); err != nil {
			return nil, err
		}
	}
	msg := "Stok diperbarui dari " + str(rec["name"])
	if sp.cogsTotal != "" {
		msg += "; HPP " + rupiah(total)
	}
	return map[string]any{"message": msg}, nil
}

func splitWeight(dl declaredLine, ru stockRule) float64 {
	if ru.costField == "" || ru.costField == "qty" {
		return dl.qty
	}
	return qtyOf(dl.row[ru.costField])
}

// averageIn is the unit cost of an incoming move valued at the receiving outlet's average (the item's
// standard_cost when the outlet has none yet), as a stock count values found stock.
func (s *stockPost) averageIn(outlet, item string) (int64, error) {
	bal, err := s.balance(outlet, item)
	if err != nil {
		return 0, err
	}
	if c := moneyIn(bal["avg_cost"]); c != 0 {
		return c, nil
	}
	it, err := s.item(item)
	if err != nil {
		return 0, err
	}
	return moneyIn(it["standard_cost"]), nil
}

// sourceUnitCost is the unit cost at which the document linked by field took item out: round(Σ|value| / Σ|qty|) of
// its moves for item (reversals excluded), else, when it consumed item through a recipe, the cogs per unit of its
// lines of item. ok is false when it took none. A linked document that is not submitted (a draft, or cancelled:
// its stock went back) is refused, so a return cannot bring in stock that never left.
func sourceUnitCost(h *HookCtx, field, item string) (int64, bool, error) {
	f := h.Model.Field(field)
	name := str(h.Record[field])
	if f == nil || name == "" {
		return 0, false, nil
	}
	sm := h.op.e.Reg().Model(f.To)
	if sm == nil {
		return 0, false, nil
	}
	src, err := h.op.load(sm, name)
	if err != nil {
		return 0, false, err
	}
	if sm.IsSubmittable && toInt(src["docstatus"]) != 1 {
		return 0, false, refuse("source_not_submitted", "Dokumen sumber "+name+" belum diajukan atau sudah dibatalkan; retur tidak bisa memakai biayanya")
	}
	moves, err := h.Find("stock_move", map[string]any{"voucher_type": f.To, "voucher": name})
	if err != nil {
		return 0, false, err
	}
	var v int64
	var q float64
	moved := false
	for _, mv := range moves {
		if str(mv["reason"]) == "reversal" {
			continue
		}
		moved = true
		if str(mv["item"]) != item {
			continue
		}
		v += abs64(moneyIn(mv["value"]))
		q += math.Abs(qtyOf(mv["qty"]))
	}
	if q > qtyEps {
		return divRound(v, q), true, nil
	}
	if !moved {
		return 0, false, nil // the source moved no stock (an invoice made from a delivery note): the average
	}
	// exploded through a recipe: the source's moves are of the materials; its line cogs are per item sold
	type cogsRule struct{ lines, item, qty, cogs string }
	var rules []cogsRule
	if sp := stockSpecOf(sm); sp != nil && !LegacyStockModels[sm.Name] {
		for _, ru := range sp.rules {
			if !ru.in && ru.cogs != "" {
				rules = append(rules, cogsRule{ru.lines, ru.item, ru.qty, ru.cogs})
			}
		}
	} else if sm.Name == "outlet_sales" {
		rules = []cogsRule{{"lines", "item", "", "cogs"}}
	}
	for _, ru := range rules {
		lm := h.op.e.Reg().Models[sm.Field(ru.lines).Of]
		for _, row := range rows(src[ru.lines]) {
			if str(row[ru.item]) != item {
				continue
			}
			rq := qtyOf(row[ru.qty])
			if ru.qty == "" {
				rq = qtyOf(row["qty"])
				if has(lm, "stock_qty") {
					rq = qtyOf(row["stock_qty"])
				}
			}
			v += moneyIn(row[ru.cogs])
			q += rq
		}
	}
	if q > qtyEps {
		return divRound(v, q), true, nil
	}
	return 0, false, nil
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// localDate is a datetime value's date in the organisation time zone (the store keeps datetimes in UTC).
func localDate(e *Engine, v any) string {
	switch d := v.(type) {
	case time.Time:
		return d.In(e.Location()).Format("2006-01-02")
	case string:
		if t, err := time.Parse(time.RFC3339, d); err == nil {
			return t.In(e.Location()).Format("2006-01-02")
		}
	}
	return dateOnly(v)
}

func formatQty(q float64) string {
	s := fmt.Sprintf("%.6f", q)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return strings.Replace(s, ".", ",", 1)
}
