package engine

import (
	"math"
	"sort"

	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Stock (S8c): stock.post_moves / stock.reverse_moves for goods_receipt, stock_transfer, stock_count,
// stock_adjustment and outlet_sales. Quantities are in the item's stock_uom (a line's stock_qty, else its
// qty); values are whole rupiah at the moving average cost. Stock never blocks: a balance may go negative,
// and the move then has negative = true (the stock_balance computes its own negative flag).

func registerStockHooks(e *Engine) {
	e.Hooks["stock.post_moves"] = postStockMoves
	e.Hooks["stock.reverse_moves"] = reverseStockMoves
}

func roundQty(q float64) float64 { return math.Round(q*1e6) / 1e6 }

// stockPost writes the moves of one document.
type stockPost struct {
	h       *HookCtx
	date    string
	voucher string
	vtype   string
	items   map[string]store.Record
}

func newStockPost(h *HookCtx, date string) *stockPost {
	return &stockPost{h: h, date: date, voucher: str(h.Record["name"]), vtype: h.Model.Name, items: map[string]store.Record{}}
}

func (s *stockPost) item(name string) (store.Record, error) {
	if r, ok := s.items[name]; ok {
		return r, nil
	}
	r, err := s.h.op.load(s.h.op.e.Reg().Model("item"), name)
	if err != nil {
		return nil, err
	}
	s.items[name] = r
	return r, nil
}

// balance returns the locked stock_balance of an outlet and item, creating it on the first move.
func (s *stockPost) balance(outlet, item string) (store.Record, error) {
	if err := s.h.LockKey("stock_balance|" + outlet + "|" + item); err != nil {
		return nil, err
	}
	found, err := s.h.FindForUpdate("stock_balance", map[string]any{"outlet": outlet, "item": item})
	if err != nil {
		return nil, err
	}
	if len(found) > 0 {
		return found[0], nil
	}
	r, _, err := s.h.Create("stock_balance", map[string]any{"outlet": outlet, "item": item, "qty": 0, "value": moneyOut(0), "avg_cost": moneyOut(0)})
	return r, err
}

// move posts qty (signed, stock uom) of item at outlet. Incoming moves are valued at cost; outgoing ones at
// the current average (item.standard_cost when there is none yet). fixed, when set, is the value of a
// reversal (the original move's value, unsigned) and cost its unit cost. It returns the move.
func (s *stockPost) move(outlet, item string, qty float64, cost int64, fixed *int64, reason string) (store.Record, error) {
	bal, err := s.balance(outlet, item)
	if err != nil {
		return nil, err
	}
	bq, bv, avg := qtyOf(bal["qty"]), moneyIn(bal["value"]), moneyIn(bal["avg_cost"])
	var value, unit int64
	if qty > 0 {
		in := mulRound(qty, cost)
		if fixed != nil {
			in = *fixed
		}
		unit, value = cost, in
		if nq := roundQty(bq + qty); nq > 0 {
			avg = divRound(bv+in, nq)
			bq, bv = nq, mulRound(nq, avg)
		} else {
			if avg == 0 {
				avg = cost
			}
			bq, bv = nq, bv+in
		}
	} else {
		out := -qty
		if fixed != nil {
			// reversal of an incoming move: take its value out again and let the average follow
			unit, value = cost, -*fixed
			bq, bv = roundQty(bq-out), bv-*fixed
			if bq > 0 {
				avg = divRound(bv, bq)
			}
		} else {
			a := avg
			if a == 0 {
				it, err := s.item(item)
				if err != nil {
					return nil, err
				}
				a = moneyIn(it["standard_cost"])
			}
			v := mulRound(out, a)
			unit, value = a, -v
			bq, bv = roundQty(bq-out), bv-v
		}
	}
	mv, _, err := s.h.Create("stock_move", map[string]any{
		"posting_date": s.date, "outlet": outlet, "item": item, "qty": qty, "unit_cost": moneyOut(unit), "value": moneyOut(value),
		"balance_qty": bq, "balance_value": moneyOut(bv), "avg_cost": moneyOut(avg), "negative": bq < 0,
		"voucher_type": s.vtype, "voucher": s.voucher, "reason": reason,
	})
	if err != nil {
		return nil, err
	}
	fields := map[string]any{"qty": bq, "value": moneyOut(bv), "avg_cost": moneyOut(avg)}
	if has(s.h.op.e.Reg().Model("stock_balance"), "last_move") {
		fields["last_move"] = mv["name"]
	}
	if _, err := s.h.SetSubmitted("stock_balance", str(bal["name"]), fields); err != nil {
		return nil, err
	}
	return mv, nil
}

// out posts an outgoing quantity at the average and returns the value taken (positive) and the unit cost.
func (s *stockPost) out(outlet, item string, qty float64, reason string) (int64, int64, error) {
	mv, err := s.move(outlet, item, -qty, 0, nil, reason)
	if err != nil {
		return 0, 0, err
	}
	return -moneyIn(mv["value"]), moneyIn(mv["unit_cost"]), nil
}

// lineQty is a line's quantity in stock uom: stock_qty when the line model computes it, else qty.
func lineQty(h *HookCtx, table string, row map[string]any) float64 {
	if f := h.Model.Field(table); f != nil && has(h.op.e.Reg().Models[f.Of], "stock_qty") {
		return qtyOf(row["stock_qty"])
	}
	return qtyOf(row["qty"])
}

func postStockMoves(h *HookCtx) (map[string]any, error) {
	if h.op.e.Reg().Model("stock_move") == nil || h.op.e.Reg().Model("stock_balance") == nil {
		return map[string]any{"message": "stock is not installed"}, nil
	}
	rec := h.Record
	switch h.Model.Name {
	case "goods_receipt":
		s := newStockPost(h, str(rec["received_on"]))
		for _, l := range rows(rec["items"]) {
			if q := lineQty(h, "items", l); q > qtyEps {
				if _, err := s.move(str(rec["outlet"]), str(l["item"]), q, moneyIn(l["unit_cost"]), nil, "receipt"); err != nil {
					return nil, err
				}
			}
		}
		if err := receivedOnPO(h, 1); err != nil {
			return nil, err
		}
	case "stock_transfer":
		s := newStockPost(h, str(rec["transfer_date"]))
		for _, l := range rows(rec["items"]) {
			q := lineQty(h, "items", l)
			if q <= qtyEps {
				continue
			}
			v, unit, err := s.out(str(rec["from_outlet"]), str(l["item"]), q, "transfer_out")
			if err != nil {
				return nil, err
			}
			if _, err := s.move(str(rec["to_outlet"]), str(l["item"]), q, unit, &v, "transfer_in"); err != nil {
				return nil, err
			}
		}
	case "stock_count":
		s := newStockPost(h, str(rec["count_date"]))
		lines := map[string]map[string]any{}
		for _, l := range rows(rec["items"]) {
			item := str(l["item"])
			bal, err := s.balance(str(rec["outlet"]), item)
			if err != nil {
				return nil, err
			}
			system := qtyOf(bal["qty"])
			diff := roundQty(qtyOf(l["counted_qty"]) - system)
			lines[str(l["name"])] = map[string]any{"system_qty": system, "difference": diff}
			switch {
			case diff > qtyEps:
				cost := moneyIn(bal["avg_cost"])
				if cost == 0 {
					it, err := s.item(item)
					if err != nil {
						return nil, err
					}
					cost = moneyIn(it["standard_cost"])
				}
				if _, err := s.move(str(rec["outlet"]), item, diff, cost, nil, "count"); err != nil {
					return nil, err
				}
			case diff < -qtyEps:
				if _, _, err := s.out(str(rec["outlet"]), item, -diff, "count"); err != nil {
					return nil, err
				}
			}
		}
		if len(lines) > 0 {
			if _, err := h.SetSubmitted(h.Model.Name, s.voucher, map[string]any{"items": lines}); err != nil {
				return nil, err
			}
		}
	case "stock_adjustment":
		s := newStockPost(h, str(rec["adjustment_date"]))
		reason := "adjustment"
		if str(rec["reason"]) == "waste" {
			reason = "waste"
		}
		for _, l := range rows(rec["items"]) {
			if q := lineQty(h, "items", l); q > qtyEps {
				if _, _, err := s.out(str(rec["outlet"]), str(l["item"]), q, reason); err != nil {
					return nil, err
				}
			}
		}
	case "outlet_sales":
		return postOutletSales(h)
	default:
		return map[string]any{"message": "stock.post_moves has no rule for " + h.Model.Name}, nil
	}
	return map[string]any{"message": "Stok diperbarui dari " + str(rec["name"])}, nil
}

// postOutletSales consumes each sales line through the item's active recipe (the item itself when it has no
// recipe and is stocked), writes the line cogs and the header cogs_total.
func postOutletSales(h *HookCtx) (map[string]any, error) {
	rec := h.Record
	outlet, date := str(rec["outlet"]), str(rec["date"])
	if str(rec["source"]) == "form" {
		if err := h.LockKey("outlet_sales|" + outlet + "|" + date); err != nil {
			return nil, err
		}
		same, err := h.Find(h.Model.Name, map[string]any{"outlet": outlet, "date": date, "source": "form", "docstatus": 1})
		if err != nil {
			return nil, err
		}
		for _, r := range same {
			if str(r["name"]) != str(rec["name"]) {
				return nil, refuse("duplicate_sales", "Penjualan outlet "+outlet+" tanggal "+date+" sudah dicatat di "+str(r["name"]))
			}
		}
	}
	s := newStockPost(h, date)
	recipes := map[string][]materialUse{}
	lines := map[string]map[string]any{}
	var total int64
	for _, l := range rows(rec["lines"]) {
		item := str(l["item"])
		uses, ok := recipes[item]
		if !ok {
			var err error
			if uses, err = s.consumption(item); err != nil {
				return nil, err
			}
			recipes[item] = uses
		}
		var cogs int64
		for _, u := range uses {
			q := roundQty(qtyOf(l["qty"]) * u.per)
			if q <= qtyEps {
				continue
			}
			v, _, err := s.out(outlet, u.material, q, "sale")
			if err != nil {
				return nil, err
			}
			cogs += v
		}
		lines[str(l["name"])] = map[string]any{"cogs": moneyOut(cogs)}
		total += cogs
	}
	fields := map[string]any{"lines": lines}
	if has(h.Model, "cogs_total") {
		fields["cogs_total"] = moneyOut(total)
	}
	if _, err := h.SetSubmitted(h.Model.Name, s.voucher, fields); err != nil {
		return nil, err
	}
	return map[string]any{"message": "HPP " + str(rec["name"]) + ": " + rupiah(total)}, nil
}

// materialUse is what one sold unit of a product consumes (per = stock qty per sold unit).
type materialUse struct {
	material string
	per      float64
}

// consumption is the active recipe of a product (most recently modified when several), merged per material;
// without one, a good, material or product consumes itself and a service nothing.
func (s *stockPost) consumption(item string) ([]materialUse, error) {
	if s.h.op.e.Reg().Model("recipe") != nil {
		found, err := s.h.Find("recipe", map[string]any{"product": item})
		if err != nil {
			return nil, err
		}
		var active []store.Record
		for _, r := range found {
			if r["active"] == true {
				active = append(active, r)
			}
		}
		sort.SliceStable(active, func(i, j int) bool { return str(active[i]["modified_at"]) > str(active[j]["modified_at"]) })
		if len(active) > 0 {
			r := active[0]
			y := qtyOf(r["yield_qty"])
			if y <= 0 {
				y = 1
			}
			var out []materialUse
			idx := map[string]int{}
			for _, rl := range rows(r["lines"]) {
				m := str(rl["material"])
				if i, ok := idx[m]; ok {
					out[i].per += qtyOf(rl["qty"]) / y
					continue
				}
				idx[m] = len(out)
				out = append(out, materialUse{material: m, per: qtyOf(rl["qty"]) / y})
			}
			return out, nil
		}
	}
	it, err := s.item(item)
	if err != nil {
		return nil, err
	}
	switch str(it["kind"]) {
	case "good", "material", "product":
		return []materialUse{{material: item, per: 1}}, nil
	}
	return nil, nil
}

// reverseStockMoves is stock.reverse_moves: one reversal move per original move of the document, with the
// opposite quantity and value at the original unit cost. A goods receipt also takes its quantities off the
// purchase order's received_qty; line cogs and count lines keep their values (history).
func reverseStockMoves(h *HookCtx) (map[string]any, error) {
	if h.op.e.Reg().Model("stock_move") == nil {
		return map[string]any{"message": "stock is not installed"}, nil
	}
	moves, err := h.Find("stock_move", map[string]any{"voucher_type": h.Model.Name, "voucher": str(h.Record["name"])})
	if err != nil {
		return nil, err
	}
	s := newStockPost(h, h.op.e.Today())
	n := 0
	for _, mv := range moves {
		if str(mv["reason"]) == "reversal" {
			continue
		}
		v := moneyIn(mv["value"])
		if v < 0 {
			v = -v
		}
		if _, err := s.move(str(mv["outlet"]), str(mv["item"]), -qtyOf(mv["qty"]), moneyIn(mv["unit_cost"]), &v, "reversal"); err != nil {
			return nil, err
		}
		n++
	}
	if h.Model.Name == "goods_receipt" {
		if err := receivedOnPO(h, -1); err != nil {
			return nil, err
		}
	}
	return map[string]any{"message": formatInt(int64(n)) + " mutasi stok dibalik"}, nil
}

// receivedOnPO adds (sign 1) or removes (sign -1) a goods receipt's quantities on its purchase order lines:
// each receipt line goes to the PO lines of the same item in order, converted through stock qty into the PO
// line's own unit; received_pct = 100 × Σ min(received, qty) / Σ qty.
func receivedOnPO(h *HookCtx, sign float64) error {
	poName := str(h.Record["purchase_order"])
	pm := h.op.e.Reg().Model("purchase_order")
	if poName == "" || pm == nil {
		return nil
	}
	po, err := h.Lock("purchase_order", poName)
	if err != nil {
		return err
	}
	lines := rows(po["items"])
	got := make([]float64, len(lines))
	for i, l := range lines {
		got[i] = qtyOf(l["received_qty"])
	}
	factor := func(l map[string]any) float64 {
		if q := qtyOf(l["qty"]); q > 0 && qtyOf(l["stock_qty"]) > 0 {
			return qtyOf(l["stock_qty"]) / q
		}
		return 1
	}
	for _, gl := range rows(h.Record["items"]) {
		rest := lineQty(h, "items", gl) // stock uom
		var match []int
		for i, l := range lines {
			if str(l["item"]) == str(gl["item"]) {
				match = append(match, i)
			}
		}
		for k, i := range match {
			if rest <= qtyEps {
				break
			}
			f := factor(lines[i])
			units := rest / f
			if sign > 0 && k < len(match)-1 {
				units = math.Min(units, math.Max(qtyOf(lines[i]["qty"])-got[i], 0))
			}
			if sign < 0 && k < len(match)-1 {
				units = math.Min(units, got[i])
			}
			got[i] = math.Max(roundQty(got[i]+sign*units), 0)
			rest = roundQty(rest - units*f)
		}
	}
	upd := map[string]map[string]any{}
	var done, total float64
	for i, l := range lines {
		upd[str(l["name"])] = map[string]any{"received_qty": got[i]}
		done += math.Min(got[i], qtyOf(l["qty"]))
		total += qtyOf(l["qty"])
	}
	fields := map[string]any{"items": upd}
	if has(pm, "received_pct") && total > 0 {
		fields["received_pct"] = math.Round(done/total*10000) / 100
	}
	_, err = h.SetSubmitted("purchase_order", poName, fields)
	return err
}
