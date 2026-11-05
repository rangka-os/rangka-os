package engine

import (
	"math"
	"sort"
	"time"

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

// lockAll takes the balance locks of every (outlet, item) a document touches, in sorted order, before any
// move is posted, so documents moving stock in opposite directions (A→B and B→A) cannot deadlock.
func (s *stockPost) lockAll(keys [][2]string) error {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	for i, k := range keys {
		if i > 0 && k == keys[i-1] {
			continue
		}
		if _, err := s.balance(k[0], k[1]); err != nil {
			return err
		}
	}
	return nil
}

// move posts qty (signed, stock uom) of item at outlet. fixed, when set, is the move's value (unsigned):
// a receipt line's amount, the value a transfer took out, or a reversed move's value; cost is then its unit
// cost (informative). Without fixed, an incoming move is valued round(qty × cost) and an outgoing one
// round(qty × avg) (item.standard_cost when there is no average yet), each rounded once. The balance value
// is always the exact sum of move values; avg = round(value / qty) is derived when qty > 0 and kept
// otherwise. It returns the move.
func (s *stockPost) move(outlet, item string, qty float64, cost int64, fixed *int64, reason string) (store.Record, error) {
	bal, err := s.balance(outlet, item)
	if err != nil {
		return nil, err
	}
	bq, bv, avg := qtyOf(bal["qty"]), moneyIn(bal["value"]), moneyIn(bal["avg_cost"])
	var value, unit int64
	switch {
	case fixed != nil:
		unit, value = cost, *fixed
		if qty < 0 {
			value = -*fixed
		}
	case qty > 0:
		unit, value = cost, mulRound(qty, cost)
	default:
		a := avg
		if a == 0 {
			it, err := s.item(item)
			if err != nil {
				return nil, err
			}
			a = moneyIn(it["standard_cost"])
		}
		unit, value = a, -mulRound(-qty, a)
	}
	bq, bv = roundQty(bq+qty), bv+value
	switch {
	case bq > 0:
		avg = divRound(bv, bq)
	case avg == 0 && qty > 0:
		avg = unit
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
	// last_sale_date: the latest posting date of a sale out of this balance that is not cancelled, so an app can
	// list slow-moving stock (qty > 0, not sold for N days) with a plain filter. A sale dated before it leaves it
	// alone; while it is empty (first sale, or a balance from before the field) it is derived from the moves.
	if reason == "sale" && qty < 0 && has(s.h.op.e.Reg().Model("stock_balance"), "last_sale_date") {
		last := dateOnly(bal["last_sale_date"])
		if last == "" {
			if last, err = s.lastSaleDate(outlet, item); err != nil {
				return nil, err
			}
		}
		if s.date > last {
			last = s.date
		}
		fields["last_sale_date"] = last
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
	// lineKeys are the (outlet, item) pairs of the document's lines that move stock, for the given outlet
	// fields (lines with no quantity get no balance row).
	lineKeys := func(outletFields ...string) [][2]string {
		var keys [][2]string
		for _, l := range rows(rec["items"]) {
			if lineQty(h, "items", l) <= qtyEps {
				continue
			}
			for _, f := range outletFields {
				keys = append(keys, [2]string{str(rec[f]), str(l["item"])})
			}
		}
		return keys
	}
	switch h.Model.Name {
	case "goods_receipt":
		s := newStockPost(h, str(rec["received_on"]))
		if err := s.lockAll(lineKeys("outlet")); err != nil {
			return nil, err
		}
		hasAmount := has(h.op.e.Reg().Models[h.Model.Field("items").Of], "amount")
		for _, l := range rows(rec["items"]) {
			q := lineQty(h, "items", l)
			if q <= qtyEps {
				continue
			}
			var fixed *int64
			if hasAmount { // the receipt is valued at the line amount; unit_cost is informative
				v := moneyIn(l["amount"])
				fixed = &v
			}
			if _, err := s.move(str(rec["outlet"]), str(l["item"]), q, moneyIn(l["unit_cost"]), fixed, "receipt"); err != nil {
				return nil, err
			}
		}
		if err := receivedOnPO(h, 1); err != nil {
			return nil, err
		}
	case "stock_transfer":
		if twoStepTransfers(h) {
			return postTransferOut(h)
		}
		s := newStockPost(h, str(rec["transfer_date"]))
		if err := s.lockAll(lineKeys("from_outlet", "to_outlet")); err != nil {
			return nil, err
		}
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
		var countKeys [][2]string // every counted line reads its balance, so all are locked
		for _, l := range rows(rec["items"]) {
			countKeys = append(countKeys, [2]string{str(rec["outlet"]), str(l["item"])})
		}
		if err := s.lockAll(countKeys); err != nil {
			return nil, err
		}
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
		if err := s.lockAll(lineKeys("outlet")); err != nil {
			return nil, err
		}
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
	case "stock_transfer_receipt":
		return postTransferReceipt(h)
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
	var keys [][2]string
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
		for _, u := range uses {
			if roundQty(qtyOf(l["qty"])*u.per) > qtyEps {
				keys = append(keys, [2]string{outlet, u.material})
			}
		}
	}
	if err := s.lockAll(keys); err != nil {
		return nil, err
	}
	lines := map[string]map[string]any{}
	var total int64
	for _, l := range rows(rec["lines"]) {
		uses := recipes[str(l["item"])]
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
	var tr store.Record // the transfer a receipt received, locked before any balance
	switch {
	case h.Model.Name == "stock_transfer" && twoStepTransfers(h):
		got, err := h.Find("stock_transfer_receipt", map[string]any{"stock_transfer": str(h.Record["name"]), "docstatus": 1})
		if err != nil {
			return nil, err
		}
		if len(got) > 0 {
			return nil, refuse("has_receipts", "Transfer "+str(h.Record["name"])+" sudah diterima di "+str(got[0]["name"])+"; batalkan penerimaannya dulu")
		}
	case h.Model.Name == "stock_transfer_receipt":
		var err error
		if tr, err = h.Lock("stock_transfer", str(h.Record["stock_transfer"])); err != nil {
			return nil, err
		}
	}
	moves, err := h.Find("stock_move", map[string]any{"voucher_type": h.Model.Name, "voucher": str(h.Record["name"])})
	if err != nil {
		return nil, err
	}
	s := newStockPost(h, h.op.e.Today())
	var keys [][2]string
	for _, mv := range moves {
		keys = append(keys, [2]string{str(mv["outlet"]), str(mv["item"])})
	}
	if err := s.lockAll(keys); err != nil {
		return nil, err
	}
	n := 0
	var sold [][2]string
	for _, mv := range moves {
		if str(mv["reason"]) == "reversal" {
			continue
		}
		if str(mv["reason"]) == "sale" {
			sold = append(sold, [2]string{str(mv["outlet"]), str(mv["item"])})
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
	if err := s.refreshLastSale(sold); err != nil {
		return nil, err
	}
	if h.Model.Name == "goods_receipt" {
		if err := receivedOnPO(h, -1); err != nil {
			return nil, err
		}
	}
	if tr != nil {
		if err := receivedOnTransfer(h, tr, -1); err != nil {
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

// lastSaleDate is the latest posting date of a sale move out of (outlet, item) whose document is not cancelled:
// the sale moves of a voucher that has a reversal move for the same outlet and item do not count. "" when none.
func (s *stockPost) lastSaleDate(outlet, item string) (string, error) {
	sales, err := s.h.Find("stock_move", map[string]any{"outlet": outlet, "item": item, "reason": "sale"})
	if err != nil || len(sales) == 0 {
		return "", err
	}
	revs, err := s.h.Find("stock_move", map[string]any{"outlet": outlet, "item": item, "reason": "reversal"})
	if err != nil {
		return "", err
	}
	reversed := map[string]bool{}
	for _, r := range revs {
		reversed[str(r["voucher_type"])+"|"+str(r["voucher"])] = true
	}
	last := ""
	for _, m := range sales {
		if d := dateOnly(m["posting_date"]); !reversed[str(m["voucher_type"])+"|"+str(m["voucher"])] && d > last {
			last = d
		}
	}
	return last, nil
}

// refreshLastSale derives last_sale_date again for balances whose sale moves were just reversed.
func (s *stockPost) refreshLastSale(keys [][2]string) error {
	if !has(s.h.op.e.Reg().Model("stock_balance"), "last_sale_date") {
		return nil
	}
	seen := map[[2]string]bool{}
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		bal, err := s.balance(k[0], k[1])
		if err != nil {
			return err
		}
		last, err := s.lastSaleDate(k[0], k[1])
		if err != nil {
			return err
		}
		var v any
		if last != "" {
			v = last
		}
		if _, err := s.h.SetSubmitted("stock_balance", str(bal["name"]), map[string]any{"last_sale_date": v}); err != nil {
			return err
		}
	}
	return nil
}

// dateOnly is a date field as yyyy-mm-dd ("" when empty), whether the store returned text or a time.
func dateOnly(v any) string {
	switch d := v.(type) {
	case nil:
		return ""
	case time.Time:
		return d.Format("2006-01-02")
	case string:
		if len(d) >= 10 {
			return d[:10]
		}
		return d
	}
	return ""
}
