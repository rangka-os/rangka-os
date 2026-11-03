package engine

import (
	"fmt"
	"math"

	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Two-step stock transfers (S8e): when the registry has stock_transfer_receipt, submitting a stock_transfer
// only takes the goods out of from_outlet (transfer_out, at its average) and records each line's unit_cost;
// the transfer is then in_transit. The receiving outlet posts a stock_transfer_receipt, which brings goods in
// at the transfer's unit cost (transfer_in) and adds to the lines' received_qty. Without the receipt model a
// transfer moves both sides at once, as before.
//
// Valuation: per item, a receipt is valued round(qty × unit_cost), except the receipt that takes the item's
// whole remainder, which gets the exact remaining value, so the in-values of an item add up to its out-value.
// Receipts are checked per item (lines of the same item add up); received_qty fills the item's lines in order.

// twoStepTransfers reports whether transfers wait for a receipt.
func twoStepTransfers(h *HookCtx) bool { return h.op.e.Reg().Model("stock_transfer_receipt") != nil }

func transferItemModelOK(h *HookCtx) error {
	tf := h.op.e.Reg().Model("stock_transfer").Field("items")
	if tf == nil {
		return fmt.Errorf("stock_transfer has no items table")
	}
	lm := h.op.e.Reg().Models[tf.Of]
	for _, f := range []string{"unit_cost", "received_qty"} {
		if !has(lm, f) {
			return fmt.Errorf("two-step transfers need %s.%s (read_only)", tf.Of, f)
		}
	}
	return nil
}

// transferLineQty is a transfer line's quantity in stock uom.
func transferLineQty(h *HookCtx, l map[string]any) float64 {
	tf := h.op.e.Reg().Model("stock_transfer").Field("items")
	if tf != nil && has(h.op.e.Reg().Models[tf.Of], "stock_qty") {
		return qtyOf(l["stock_qty"])
	}
	return qtyOf(l["qty"])
}

// postTransferOut is stock.post_moves on a two-step stock_transfer.
func postTransferOut(h *HookCtx) (map[string]any, error) {
	if err := transferItemModelOK(h); err != nil {
		return nil, err
	}
	rec := h.Record
	from := str(rec["from_outlet"])
	s := newStockPost(h, str(rec["transfer_date"]))
	var keys [][2]string
	for _, l := range rows(rec["items"]) {
		if transferLineQty(h, l) > qtyEps {
			keys = append(keys, [2]string{from, str(l["item"])})
		}
	}
	if err := s.lockAll(keys); err != nil {
		return nil, err
	}
	lines := map[string]map[string]any{}
	for _, l := range rows(rec["items"]) {
		var unit int64
		if q := transferLineQty(h, l); q > qtyEps {
			_, u, err := s.out(from, str(l["item"]), q, "transfer_out")
			if err != nil {
				return nil, err
			}
			unit = u
		}
		lines[str(l["name"])] = map[string]any{"unit_cost": moneyOut(unit), "received_qty": 0.0}
	}
	fields := map[string]any{"items": lines}
	if has(h.Model, "state") {
		fields["state"] = "in_transit"
	}
	if _, err := h.SetSubmitted("stock_transfer", s.voucher, fields); err != nil {
		return nil, err
	}
	return map[string]any{"message": "Stok " + str(rec["name"]) + " keluar dari " + from + ", menunggu diterima di " + str(rec["to_outlet"])}, nil
}

// sentItem is one item of a transfer: quantity sent and received (stock uom), the value that went out, and
// the unit cost it comes in at.
type sentItem struct {
	sent, received float64
	value, unit    int64
}

func transferItems(h *HookCtx, tr store.Record) (map[string]*sentItem, []string) {
	out := map[string]*sentItem{}
	var order []string
	mixed := map[string]bool{}
	for _, l := range rows(tr["items"]) {
		q := transferLineQty(h, l)
		if q <= qtyEps {
			continue
		}
		it := str(l["item"])
		u := moneyIn(l["unit_cost"])
		si := out[it]
		if si == nil {
			si = &sentItem{unit: u}
			out[it] = si
			order = append(order, it)
		} else if si.unit != u {
			mixed[it] = true
		}
		si.sent = roundQty(si.sent + q)
		si.received = roundQty(si.received + qtyOf(l["received_qty"]))
		si.value += mulRound(q, u) // exactly what the transfer_out move took
	}
	for it := range mixed { // lines of one item left at different averages: come in at their mean
		out[it].unit = divRound(out[it].value, out[it].sent)
	}
	return out, order
}

// receivedValues sums, per item, the values already brought in by the transfer's receipts (reversals net out).
func receivedValues(h *HookCtx, transfer, except string) (map[string]int64, error) {
	got, err := h.Find("stock_transfer_receipt", map[string]any{"stock_transfer": transfer})
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, r := range got {
		if str(r["name"]) == except {
			continue
		}
		mv, err := h.Find("stock_move", map[string]any{"voucher_type": "stock_transfer_receipt", "voucher": str(r["name"])})
		if err != nil {
			return nil, err
		}
		for _, m := range mv {
			out[str(m["item"])] += moneyIn(m["value"])
		}
	}
	return out, nil
}

// postTransferReceipt is stock.post_moves on a stock_transfer_receipt.
func postTransferReceipt(h *HookCtx) (map[string]any, error) {
	if err := transferItemModelOK(h); err != nil {
		return nil, err
	}
	rec := h.Record
	name, trName, outlet := str(rec["name"]), str(rec["stock_transfer"]), str(rec["outlet"])
	tr, err := h.Lock("stock_transfer", trName)
	if err != nil {
		return nil, err
	}
	if outlet != str(tr["to_outlet"]) {
		return nil, refuse("wrong_outlet", "Transfer "+trName+" dikirim ke "+str(tr["to_outlet"])+", bukan ke "+outlet)
	}
	if toInt(tr["docstatus"]) != 1 || (str(tr["state"]) != "in_transit" && str(tr["state"]) != "partly_received") {
		return nil, refuse("not_in_transit", "Transfer "+trName+" tidak sedang dalam perjalanan")
	}
	items, _ := transferItems(h, tr)
	want := map[string]float64{}
	for _, l := range rows(rec["items"]) {
		if q := qtyOf(l["qty"]); q > qtyEps {
			want[str(l["item"])] = roundQty(want[str(l["item"])] + q)
		}
	}
	for _, l := range rows(rec["items"]) {
		it := str(l["item"])
		si := items[it]
		left := 0.0
		if si != nil {
			left = roundQty(si.sent - si.received)
		}
		if want[it] > left+qtyEps {
			return nil, refuse("over_receipt", fmt.Sprintf("Barang %s: diterima %s, sisa kiriman %s", it, fmtQty(want[it]), fmtQty(left)))
		}
	}
	s := newStockPost(h, str(rec["received_on"]))
	var keys [][2]string
	for it := range want {
		keys = append(keys, [2]string{outlet, it})
	}
	if err := s.lockAll(keys); err != nil {
		return nil, err
	}
	var prior map[string]int64   // values brought in by the transfer's other receipts, loaded when needed
	posted := map[string]int64{} // values brought in by this receipt so far
	for _, l := range rows(rec["items"]) {
		q := qtyOf(l["qty"])
		if q <= qtyEps {
			continue
		}
		it := str(l["item"])
		si := items[it]
		value := mulRound(q, si.unit)
		if roundQty(si.sent-si.received-q) <= qtyEps { // the whole remainder: the exact remaining value
			if prior == nil {
				if prior, err = receivedValues(h, trName, name); err != nil {
					return nil, err
				}
			}
			value = si.value - prior[it] - posted[it]
		}
		if _, err := s.move(outlet, it, q, si.unit, &value, "transfer_in"); err != nil {
			return nil, err
		}
		si.received = roundQty(si.received + q)
		posted[it] += value
	}
	if err := receivedOnTransfer(h, tr, 1); err != nil {
		return nil, err
	}
	return map[string]any{"message": "Transfer " + trName + " diterima di " + outlet}, nil
}

// receivedOnTransfer adds (sign 1) or removes (sign -1) a receipt's quantities on the transfer's lines of the
// same item, in line order (removing from the last), and sets the transfer's state.
func receivedOnTransfer(h *HookCtx, tr store.Record, sign float64) error {
	lines := rows(tr["items"])
	got := make([]float64, len(lines))
	for i, l := range lines {
		got[i] = qtyOf(l["received_qty"])
	}
	for _, rl := range rows(h.Record["items"]) {
		rest := qtyOf(rl["qty"])
		var match []int
		for i, l := range lines {
			if str(l["item"]) == str(rl["item"]) && transferLineQty(h, l) > qtyEps {
				match = append(match, i)
			}
		}
		if sign < 0 { // take back from the last line first
			for a, b := 0, len(match)-1; a < b; a, b = a+1, b-1 {
				match[a], match[b] = match[b], match[a]
			}
		}
		for k, i := range match {
			if rest <= qtyEps {
				break
			}
			units := rest
			if k < len(match)-1 {
				if sign > 0 {
					units = math.Min(units, math.Max(transferLineQty(h, lines[i])-got[i], 0))
				} else {
					units = math.Min(units, got[i])
				}
			}
			got[i] = math.Max(roundQty(got[i]+sign*units), 0)
			rest = roundQty(rest - units)
		}
	}
	upd := map[string]map[string]any{}
	all, some := true, false
	for i, l := range lines {
		upd[str(l["name"])] = map[string]any{"received_qty": got[i]}
		if got[i] > qtyEps {
			some = true
		}
		if got[i] < transferLineQty(h, l)-qtyEps {
			all = false
		}
	}
	fields := map[string]any{"items": upd}
	if has(h.op.e.Reg().Model("stock_transfer"), "state") {
		switch {
		case all:
			fields["state"] = "received"
		case some:
			fields["state"] = "partly_received"
		default:
			fields["state"] = "in_transit"
		}
	}
	_, err := h.SetSubmitted("stock_transfer", str(tr["name"]), fields)
	return err
}

// fmtQty prints a quantity without trailing zeros.
func fmtQty(q float64) string { return fmt.Sprintf("%g", roundQty(q)) }
