package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Lots and serial numbers on stock moves (contract 0070). Lot and serial are quantity dimensions of stock: the
// value stays per outlet and item at the moving average (stockPost.move), and the kernel keeps beside it
//   - stock_lot_balance: qty per (outlet, item, lot); the row with an empty lot is the no-lot bucket, always
//     stock_balance.qty minus the real lots, so stock from before tracking (or moved without a lot) sits there;
//   - serial_no: one per (item, serial) with its state and outlet.
// Both are active only when the stock module ships the models and item.tracking says so; an item without
// tracking moves exactly as before.

const (
	trackLot       = "lot"
	trackLotExpiry = "lot_expiry"
	trackSerial    = "serial"
)

// Expired-lot modes of an out move (contract 0070 §5).
const (
	expiredSkip   = "skip"
	expiredAllow  = "allow"
	expiredRefuse = "refuse"
	expiredFirst  = "first"
)

// lotIn is the lot of an incoming line: number and dates, and whether the document carries lot fields at all
// (a document that does not puts lot items in the no-lot bucket and refuses nothing).
type lotIn struct {
	no, expiry, made string
	declared         bool
}

// lotOut is how an outgoing line picks lots: an explicit stock_lot, else FEFO under the expired mode.
type lotOut struct {
	pick, expired string
	bucket        bool // take from the no-lot bucket only (a count line without a lot number)
}

// serialSpec is the serials of a line; declared is false when the document has no serial field (serial items
// then move by quantity only and the register is not touched).
type serialSpec struct {
	list     []string
	declared bool
}

// lotTake is one part of a line: qty from lot ("" = the no-lot bucket).
type lotTake struct {
	lot string
	qty float64
}

func (s *stockPost) lotsOn() bool {
	r := s.h.op.e.Reg()
	l, b := r.Model("stock_lot"), r.Model("stock_lot_balance")
	return has(l, "item") && has(l, "lot_no") && has(b, "outlet") && has(b, "item") && has(b, "lot") && has(b, "qty") &&
		has(r.Model("stock_move"), "lot") // a reversal finds the lot of each move there
}

func (s *stockPost) serialsOn() bool {
	m := s.h.op.e.Reg().Model("serial_no")
	return has(m, "item") && has(m, "serial") && has(m, "state") && has(m, "outlet") &&
		has(s.h.op.e.Reg().Model("stock_move"), "serial_nos")
}

// tracking is the item's tracking mode when the registry can record it, else "".
func (s *stockPost) tracking(item string) (string, error) {
	if !has(s.h.op.e.Reg().Model("item"), "tracking") {
		return "", nil
	}
	it, err := s.item(item)
	if err != nil {
		return "", err
	}
	switch t := str(it["tracking"]); t {
	case trackLot, trackLotExpiry:
		if s.lotsOn() {
			return t, nil
		}
	case trackSerial:
		if s.serialsOn() {
			return t, nil
		}
	}
	return "", nil
}

// problem refuses with code, or, on a device write (contract 0058: stock follows sales, never refused), records
// an exception and returns ok = false so the caller carries on without the lot or serial.
func (s *stockPost) problem(code, msg, outlet string, detail map[string]any) (bool, error) {
	if deviceWriteOf(s.h.op.ctx) != nil {
		d := map[string]any{"code": code, "message": msg}
		for k, v := range detail {
			d[k] = v
		}
		s.h.op.except("stock_"+code, outlet, "", d)
		return false, nil
	}
	return false, refuse(code, msg)
}

// ---------- lots

func (s *stockPost) lotRecord(name string) (store.Record, error) {
	if r, ok := s.lots[name]; ok {
		return r, nil
	}
	r, err := s.h.op.load(s.h.op.e.Reg().Model("stock_lot"), name)
	if err != nil {
		return nil, err
	}
	s.lots[name] = r
	return r, nil
}

// lotFor finds or creates the stock_lot of item with number li.no. A lot already recorded with another expiry
// is refused; a new lot of a lot_expiry item needs its expiry. ok is false on a device write that was flagged.
func (s *stockPost) lotFor(outlet, item, tracking string, li lotIn) (string, bool, error) {
	no := strings.TrimSpace(li.no)
	if err := s.h.LockKey("stock_lot|" + item + "|" + no); err != nil {
		return "", false, err
	}
	found, err := s.h.FindForUpdate("stock_lot", map[string]any{"item": item, "lot_no": no})
	if err != nil {
		return "", false, err
	}
	exp := dateOnly(li.expiry)
	if len(found) > 0 {
		l := found[0]
		if have := dateOnly(l["expiry_date"]); exp != "" && have != "" && exp != have {
			return s.problemLot("lot_expiry_mismatch", fmt.Sprintf("Lot %s sudah tercatat kedaluwarsa %s, bukan %s", no, have, exp), outlet, item, no)
		}
		s.lots[str(l["name"])] = l
		return str(l["name"]), true, nil
	}
	if tracking == trackLotExpiry && exp == "" {
		return s.problemLot("expiry_required", "Lot "+no+" baru: isi tanggal kedaluwarsanya", outlet, item, no)
	}
	in := map[string]any{"item": item, "lot_no": no}
	lm := s.h.op.e.Reg().Model("stock_lot")
	if exp != "" && has(lm, "expiry_date") {
		in["expiry_date"] = exp
	}
	if made := dateOnly(li.made); made != "" && has(lm, "made_date") {
		in["made_date"] = made
	}
	if has(lm, "first_voucher_type") {
		in["first_voucher_type"] = s.vtype
	}
	if has(lm, "first_voucher") {
		in["first_voucher"] = s.voucher
	}
	r, _, err := s.h.Create("stock_lot", in)
	if err != nil {
		return "", false, err
	}
	s.lots[str(r["name"])] = r
	return str(r["name"]), true, nil
}

func (s *stockPost) problemLot(code, msg, outlet, item, no string) (string, bool, error) {
	ok, err := s.problem(code, msg, outlet, map[string]any{"item": item, "lot_no": no})
	return "", ok, err
}

// lotBalances are the stock_lot_balance rows of (outlet, item) with a real lot. The caller holds the
// stock_balance lock of the pair, which covers its lot rows.
func (s *stockPost) lotBalances(outlet, item string) ([]store.Record, error) {
	all, err := s.h.FindForUpdate("stock_lot_balance", map[string]any{"outlet": outlet, "item": item})
	if err != nil {
		return nil, err
	}
	var out []store.Record
	for _, r := range all {
		if str(r["lot"]) != "" {
			out = append(out, r)
		}
	}
	return out, nil
}

// lotQty is the quantity of lot at (outlet, item); lot "" is the no-lot bucket.
func (s *stockPost) lotQty(outlet, item, lot string) (float64, error) {
	if lot == "" {
		bal, err := s.balance(outlet, item)
		if err != nil {
			return 0, err
		}
		rows, err := s.lotBalances(outlet, item)
		if err != nil {
			return 0, err
		}
		q := qtyOf(bal["qty"])
		for _, r := range rows {
			q -= qtyOf(r["qty"])
		}
		return roundQty(q), nil
	}
	found, err := s.h.FindForUpdate("stock_lot_balance", map[string]any{"outlet": outlet, "item": item, "lot": lot})
	if err != nil || len(found) == 0 {
		return 0, err
	}
	return qtyOf(found[0]["qty"]), nil
}

// addLot adds qty (signed) to the balance of a real lot, creating the row on its first move.
func (s *stockPost) addLot(outlet, item, lot string, qty float64) error {
	if lot == "" {
		return nil
	}
	found, err := s.h.FindForUpdate("stock_lot_balance", map[string]any{"outlet": outlet, "item": item, "lot": lot})
	if err != nil {
		return err
	}
	if len(found) == 0 {
		_, _, err = s.h.Create("stock_lot_balance", map[string]any{"outlet": outlet, "item": item, "lot": lot, "qty": roundQty(qty)})
		return err
	}
	_, err = s.h.SetSubmitted("stock_lot_balance", str(found[0]["name"]), map[string]any{"qty": roundQty(qtyOf(found[0]["qty"]) + qty)})
	return err
}

// syncBucket writes the no-lot bucket of (outlet, item): stock_balance.qty minus the real lots. The row is created
// only when it is not zero.
func (s *stockPost) syncBucket(outlet, item string) error {
	q, err := s.lotQty(outlet, item, "")
	if err != nil {
		return err
	}
	found, err := s.h.FindForUpdate("stock_lot_balance", map[string]any{"outlet": outlet, "item": item, "lot": ""})
	if err != nil {
		return err
	}
	if len(found) == 0 {
		if math.Abs(q) <= qtyEps {
			return nil
		}
		_, _, err = s.h.Create("stock_lot_balance", map[string]any{"outlet": outlet, "item": item, "qty": q})
		return err
	}
	if qtyOf(found[0]["qty"]) == q {
		return nil
	}
	_, err = s.h.SetSubmitted("stock_lot_balance", str(found[0]["name"]), map[string]any{"qty": q})
	return err
}

// fefo splits an outgoing qty over lots (contract 0070 §5): the picked lot when lo.pick is set, else the lots with
// stock ordered by expiry (empty last), then age, then number, under the expired mode; what the lots do not cover
// comes out of the no-lot bucket, which may go negative. Stock is never refused for quantity.
func (s *stockPost) fefo(outlet, item string, qty float64, lo lotOut) ([]lotTake, error) {
	var takes []lotTake
	rest := qty
	if lo.bucket {
		return []lotTake{{"", qty}}, nil
	}
	if lo.pick != "" {
		l, err := s.lotRecord(lo.pick)
		if err != nil {
			return nil, err
		}
		if str(l["item"]) != item {
			return nil, refuse("lot_wrong_item", "Lot "+str(l["lot_no"])+" bukan lot barang "+item)
		}
		if exp := dateOnly(l["expiry_date"]); lo.expired == expiredRefuse && exp != "" && exp < s.date {
			ok, err := s.problem("lot_expired", "Lot "+str(l["lot_no"])+" sudah kedaluwarsa "+exp, outlet, map[string]any{"item": item, "lot": lo.pick})
			if err != nil {
				return nil, err
			}
			if !ok { // a flagged device write posts by quantity, out of the no-lot bucket (§9)
				return []lotTake{{"", qty}}, nil
			}
		}
		have, err := s.lotQty(outlet, item, lo.pick)
		if err != nil {
			return nil, err
		}
		if take := math.Min(rest, math.Max(have, 0)); take > qtyEps {
			takes = append(takes, lotTake{lo.pick, roundQty(take)})
			rest = roundQty(rest - take)
		}
		if rest > qtyEps {
			takes = append(takes, lotTake{"", rest})
		}
		return takes, nil
	}
	rows, err := s.lotBalances(outlet, item)
	if err != nil {
		return nil, err
	}
	type cand struct {
		lot, exp, created, no string
		qty                   float64
		expired               bool
	}
	var cs []cand
	for _, r := range rows {
		if qtyOf(r["qty"]) <= qtyEps {
			continue
		}
		l, err := s.lotRecord(str(r["lot"]))
		if err != nil {
			return nil, err
		}
		exp := dateOnly(l["expiry_date"])
		cs = append(cs, cand{lot: str(r["lot"]), exp: exp, created: fmt.Sprint(l["created_at"]), no: str(l["lot_no"]), qty: qtyOf(r["qty"]), expired: exp != "" && exp < s.date})
	}
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if lo.expired == expiredFirst && a.expired != b.expired {
			return a.expired
		}
		if a.exp != b.exp {
			if a.exp == "" || b.exp == "" {
				return b.exp == ""
			}
			return a.exp < b.exp
		}
		if a.created != b.created {
			return a.created < b.created
		}
		if a.no != b.no {
			return a.no < b.no
		}
		return a.lot < b.lot
	})
	var usable []cand
	var expiredQty, freshQty float64
	for _, c := range cs {
		if c.expired {
			expiredQty += c.qty
			if lo.expired == expiredSkip || lo.expired == expiredRefuse || lo.expired == "" {
				continue
			}
		} else {
			freshQty += c.qty
		}
		usable = append(usable, c)
	}
	if lo.expired == expiredRefuse && expiredQty > qtyEps {
		bucket, err := s.lotQty(outlet, item, "")
		if err != nil {
			return nil, err
		}
		if freshQty+math.Max(bucket, 0) < qty-qtyEps {
			if ok, err := s.problem("lot_expired", fmt.Sprintf("Stok %s yang belum kedaluwarsa tinggal %s; sisanya sudah kedaluwarsa", item, formatQty(freshQty+math.Max(bucket, 0))), outlet, map[string]any{"item": item}); !ok && err != nil {
				return nil, err
			}
		}
	}
	for _, c := range usable {
		if rest <= qtyEps {
			break
		}
		take := math.Min(rest, c.qty)
		takes = append(takes, lotTake{c.lot, roundQty(take)})
		rest = roundQty(rest - take)
	}
	if rest > qtyEps {
		takes = append(takes, lotTake{"", rest})
	}
	return takes, nil
}

// ---------- locks

// wantSerialLocks and wantLotLock note the advisory keys a document will take for its serials and lots; lockWanted
// takes them in sorted order after lockAll, so two documents touching the same serials or lots in another line order
// cannot deadlock (serialRecord and lotFor take the same keys again, which is a no-op in the transaction).
func (s *stockPost) wantSerialLocks(item string, serials []string) {
	for _, sn := range serials {
		s.wantKey("serial_no|" + item + "|" + sn)
	}
}

func (s *stockPost) wantLotLock(item, no string) {
	if no = strings.TrimSpace(no); no != "" {
		s.wantKey("stock_lot|" + item + "|" + no)
	}
}

func (s *stockPost) wantKey(k string) {
	if s.want == nil {
		s.want = map[string]bool{}
	}
	s.want[k] = true
}

func (s *stockPost) lockWanted() error {
	keys := make([]string, 0, len(s.want))
	for k := range s.want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := s.h.LockKey(k); err != nil {
			return err
		}
	}
	s.want = nil
	return nil
}

// wantLineLocks notes the lot and serial keys of a Go-rule document's lines (conventional fields, lineLots).
func (s *stockPost) wantLineLocks(model, table string) {
	for _, l := range rows(s.h.Record[table]) {
		li, _, sp := lineLots(s.h, model, table, l)
		if li.declared {
			s.wantLotLock(str(l["item"]), li.no)
		}
		if sp.declared {
			s.wantSerialLocks(str(l["item"]), sp.list)
		}
	}
}

// ---------- serials

// parseSerials is a serial field: one serial per line (commas and semicolons also separate), trimmed, empty
// lines dropped.
func parseSerials(v any) []string {
	var out []string
	for _, f := range strings.FieldsFunc(str(v), func(r rune) bool { return r == '\n' || r == '\r' || r == ',' || r == ';' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// serialState is the state an out move leaves a serial in, by reason (contract 0070 §6).
func serialState(reason string) string {
	switch reason {
	case "transfer_out":
		return "in_transit"
	case "production_out":
		return "consumed"
	case "return_out":
		return "returned"
	case "waste", "adjustment", "count":
		return "scrapped"
	}
	return "sold"
}

// checkSerialCount: a serial line's qty is a whole number equal to its count of distinct serials.
func (s *stockPost) checkSerialCount(outlet, item string, qty float64, sp serialSpec) (bool, error) {
	seen := map[string]bool{}
	for _, sn := range sp.list {
		if seen[sn] {
			return s.problem("serial_duplicate", "Nomor seri "+sn+" tertulis dua kali", outlet, map[string]any{"item": item, "serial": sn})
		}
		seen[sn] = true
	}
	if math.Abs(qty-math.Round(qty)) > qtyEps || int(math.Round(qty)) != len(sp.list) {
		return s.problem("serial_count_mismatch", fmt.Sprintf("Barang %s: jumlah %s, nomor seri %d", item, formatQty(qty), len(sp.list)), outlet, map[string]any{"item": item})
	}
	return true, nil
}

func (s *stockPost) serialRecord(item, sn string) (store.Record, error) {
	if err := s.h.LockKey("serial_no|" + item + "|" + sn); err != nil {
		return nil, err
	}
	found, err := s.h.FindForUpdate("serial_no", map[string]any{"item": item, "serial": sn})
	if err != nil || len(found) == 0 {
		return nil, err
	}
	return found[0], nil
}

// serialWrite sets a serial's state and outlet after a forward move (creating it on its first move), keeping
// prev_state, the last voucher and the dates when the model has them, and pushing "<type>/<name>|<state before>" on
// its history (contract 0070 §8), which a reversal pops.
func (s *stockPost) serialWrite(cur store.Record, item, sn, state, outlet, lot string, in bool) error {
	m := s.h.op.e.Reg().Model("serial_no")
	f := map[string]any{"state": state, "outlet": outlet}
	if has(m, "prev_state") && cur != nil {
		f["prev_state"] = str(cur["state"])
	}
	if has(m, "history") {
		f["history"] = pushHistory(str(cur["history"]), voucherRef(s.vtype, s.voucher)+"|"+str(cur["state"]))
	}
	if has(m, "last_voucher_type") {
		f["last_voucher_type"] = s.vtype
	}
	if has(m, "last_voucher") {
		f["last_voucher"] = s.voucher
	}
	if in && has(m, "in_date") {
		f["in_date"] = s.date
	}
	if !in && has(m, "out_date") {
		f["out_date"] = s.date
	}
	if lot != "" && has(m, "lot") {
		f["lot"] = lot
	}
	if cur == nil {
		f["item"], f["serial"] = item, sn
		_, _, err := s.h.Create("serial_no", f)
		return err
	}
	_, err := s.h.SetSubmitted("serial_no", str(cur["name"]), f)
	return err
}

// A serial's history is one entry per forward move still standing, oldest first: "<type>/<name>|<state before>".
func pushHistory(h, entry string) string {
	if h == "" {
		return entry
	}
	return h + "\n" + entry
}

// topHistory is the last entry of a history (the document that moved the serial last, the state before it) and
// the history without it.
func topHistory(h string) (voucher, before, rest string) {
	if h == "" {
		return "", "", ""
	}
	i := strings.LastIndex(h, "\n")
	top := h[i+1:]
	if i >= 0 {
		rest = h[:i]
	}
	voucher, before, _ = strings.Cut(top, "|")
	return voucher, before, rest
}

// serialsIn brings serials into outlet: each must not be in stock or on its way anywhere (a transfer receipt
// takes only serials in transit). It returns the serials recorded on the move.
func (s *stockPost) serialsIn(outlet, item, reason string, qty float64, sp serialSpec, lot string) ([]string, error) {
	if ok, err := s.checkSerialCount(outlet, item, qty, sp); !ok {
		return nil, err
	}
	var done []string
	for _, sn := range sortedCopy(sp.list) {
		cur, err := s.serialRecord(item, sn)
		if err != nil {
			return nil, err
		}
		st := str(cur["state"])
		switch {
		case reason == "transfer_in" && st != "in_transit":
			if ok, err := s.problem("serial_not_sent", "Nomor seri "+sn+" tidak sedang dalam perjalanan", outlet, map[string]any{"item": item, "serial": sn}); !ok {
				if err != nil {
					return nil, err
				}
				continue
			}
		case reason != "transfer_in" && (st == "in_stock" || st == "in_transit"):
			if ok, err := s.problem("serial_in_stock", "Nomor seri "+sn+" sudah tercatat ada di "+str(cur["outlet"]), outlet, map[string]any{"item": item, "serial": sn}); !ok {
				if err != nil {
					return nil, err
				}
				continue
			}
		}
		if err := s.serialWrite(cur, item, sn, "in_stock", outlet, lot, true); err != nil {
			return nil, err
		}
		done = append(done, sn)
	}
	return orderLike(done, sp.list), nil
}

// serialsOut takes serials out of outlet: each must be in stock there.
func (s *stockPost) serialsOut(outlet, item, reason string, qty float64, sp serialSpec) ([]string, error) {
	if ok, err := s.checkSerialCount(outlet, item, qty, sp); !ok {
		return nil, err
	}
	var done []string
	for _, sn := range sortedCopy(sp.list) {
		cur, err := s.serialRecord(item, sn)
		if err != nil {
			return nil, err
		}
		if str(cur["state"]) != "in_stock" || str(cur["outlet"]) != outlet {
			if ok, err := s.problem("serial_not_here", "Nomor seri "+sn+" tidak ada di stok "+outlet, outlet, map[string]any{"item": item, "serial": sn}); !ok {
				if err != nil {
					return nil, err
				}
				continue
			}
		}
		if err := s.serialWrite(cur, item, sn, serialState(reason), outlet, "", false); err != nil {
			return nil, err
		}
		done = append(done, sn)
	}
	return orderLike(done, sp.list), nil
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// orderLike keeps the serials of done in the order the line wrote them.
func orderLike(done, order []string) []string {
	in := map[string]bool{}
	for _, d := range done {
		in[d] = true
	}
	var out []string
	for _, o := range order {
		if in[o] {
			out = append(out, o)
		}
	}
	return out
}

// ---------- lines

// outLine takes qty of item out of outlet at the outlet average, by lots when the item is lot-tracked (one move per
// lot, values split by qty with the last part taking the remainder, so they add up to the single move of an
// untracked item) and checking serials when the document carries them. It returns the value taken (positive),
// the unit cost and the moves written.
func (s *stockPost) outLine(outlet, item string, qty float64, reason string, lo lotOut, sp serialSpec) (int64, int64, []store.Record, error) {
	tr, err := s.tracking(item)
	if err != nil {
		return 0, 0, nil, err
	}
	var serials []string
	if tr == trackSerial && sp.declared {
		if serials, err = s.serialsOut(outlet, item, reason, qty, sp); err != nil {
			return 0, 0, nil, err
		}
	}
	if tr != trackLot && tr != trackLotExpiry {
		mv, err := s.moveX(outlet, item, -qty, 0, nil, reason, moveOpt{serials: serials})
		if err != nil {
			return 0, 0, nil, err
		}
		return -moneyIn(mv["value"]), moneyIn(mv["unit_cost"]), []store.Record{mv}, nil
	}
	takes, err := s.fefo(outlet, item, qty, lo)
	if err != nil {
		return 0, 0, nil, err
	}
	bal, err := s.balance(outlet, item)
	if err != nil {
		return 0, 0, nil, err
	}
	avg0 := moneyIn(bal["avg_cost"])
	a := avg0
	if a == 0 {
		it, err := s.item(item)
		if err != nil {
			return 0, 0, nil, err
		}
		a = moneyIn(it["standard_cost"])
	}
	total := mulRound(qty, a)
	var mvs []store.Record
	var sum int64
	for i, t := range takes {
		v := mulRound(t.qty, a)
		if (total >= 0 && v > total-sum) || (total < 0 && v < total-sum) { // never past the line's value: no part flips sign
			v = total - sum
		}
		mo := moveOpt{lot: t.lot}
		if i == len(takes)-1 {
			v = total - sum
			mo.keepAvg = &avg0
		}
		sum += v
		mv, err := s.moveX(outlet, item, -t.qty, a, &v, reason, mo)
		if err != nil {
			return 0, 0, nil, err
		}
		if err := s.addLot(outlet, item, t.lot, -t.qty); err != nil {
			return 0, 0, nil, err
		}
		mvs = append(mvs, mv)
	}
	if err := s.syncBucket(outlet, item); err != nil {
		return 0, 0, nil, err
	}
	return total, a, mvs, nil
}

// inLine brings qty of item into outlet (cost/fixed as stockPost.move) into the line's lot, recording serials when
// the item is serial-tracked and the document carries them. A lot item with no lot number goes to the no-lot bucket
// (tracking lot, or a document without lot fields); a lot_expiry item needs one.
func (s *stockPost) inLine(outlet, item string, qty float64, cost int64, fixed *int64, reason string, li lotIn, sp serialSpec) (store.Record, error) {
	tr, err := s.tracking(item)
	if err != nil {
		return nil, err
	}
	var mo moveOpt
	switch tr {
	case trackSerial:
		if sp.declared {
			if mo.serials, err = s.serialsIn(outlet, item, reason, qty, sp, ""); err != nil {
				return nil, err
			}
		}
	case trackLot, trackLotExpiry:
		switch {
		case strings.TrimSpace(li.no) != "":
			lot, ok, err := s.lotFor(outlet, item, tr, li)
			if err != nil {
				return nil, err
			}
			if ok {
				mo.lot = lot
			}
		case tr == trackLotExpiry && li.declared:
			if _, err := s.problem("lot_required", "Barang "+item+" dilacak per lot dengan kedaluwarsa: isi nomor lotnya", outlet, map[string]any{"item": item}); err != nil {
				return nil, err
			}
		}
	}
	mv, err := s.moveX(outlet, item, qty, cost, fixed, reason, mo)
	if err != nil {
		return nil, err
	}
	if tr == trackLot || tr == trackLotExpiry {
		if err := s.addLot(outlet, item, mo.lot, qty); err != nil {
			return nil, err
		}
		if err := s.syncBucket(outlet, item); err != nil {
			return nil, err
		}
	}
	return mv, nil
}

// inParts brings qty of item into outlet in the given lot parts (a transfer receipt following the lots that left),
// valued at value in total, split by qty with the last part taking the remainder.
func (s *stockPost) inParts(outlet, item string, parts []lotTake, unit, value int64, reason string, serials []string) error {
	tr, err := s.tracking(item)
	if err != nil {
		return err
	}
	lots := tr == trackLot || tr == trackLotExpiry
	if !lots || len(parts) == 0 {
		var q float64
		for _, p := range parts {
			q += p.qty
		}
		_, err := s.moveX(outlet, item, roundQty(q), unit, &value, reason, moveOpt{serials: serials})
		return err
	}
	var sum int64
	for i, p := range parts {
		v := mulRound(p.qty, unit)
		if v > value-sum {
			v = value - sum
		}
		if i == len(parts)-1 {
			v = value - sum
		}
		sum += v
		mo := moveOpt{lot: p.lot}
		if i == 0 {
			mo.serials = serials
		}
		if _, err := s.moveX(outlet, item, p.qty, unit, &v, reason, mo); err != nil {
			return err
		}
		if err := s.addLot(outlet, item, p.lot, p.qty); err != nil {
			return err
		}
	}
	return s.syncBucket(outlet, item)
}

// lineLots reads the conventional lot and serial fields of a line of a Go-rule document (goods_receipt,
// stock_adjustment, stock_transfer, stock_count): lot_no, expiry_date, made_date in; lot out; serial_nos both.
func lineLots(h *HookCtx, model, table string, row map[string]any) (lotIn, lotOut, serialSpec) {
	var lm = h.op.e.Reg().Models[h.op.e.Reg().Model(model).Field(table).Of]
	li := lotIn{no: str(row["lot_no"]), expiry: dateOnly(row["expiry_date"]), made: dateOnly(row["made_date"]), declared: has(lm, "lot_no")}
	lo := lotOut{expired: expiredSkip}
	if f := lm.Field("lot"); f != nil && f.Type == "link" {
		lo.pick = str(row["lot"])
	}
	sp := serialSpec{list: parseSerials(row["serial_nos"]), declared: has(lm, "serial_nos")}
	return li, lo, sp
}

// postStockCount is stock.post_moves on a stock_count: each line's counted_qty against the system quantity, the
// difference posted as a count move (in at the outlet average, out at the average). For a lot item whose line
// model has lot_no, the line counts one lot (system_qty is that lot's quantity, a new lot is created like an
// incoming one) or, with no lot number, the no-lot bucket; lots not on the count are untouched (contract 0070 §7).
func postStockCount(h *HookCtx) (map[string]any, error) {
	rec := h.Record
	outlet := str(rec["outlet"])
	s := newStockPost(h, str(rec["count_date"]))
	var countKeys [][2]string // every counted line reads its balance, so all are locked
	for _, l := range rows(rec["items"]) {
		countKeys = append(countKeys, [2]string{outlet, str(l["item"])})
	}
	s.wantLineLocks(h.Model.Name, "items")
	if err := s.lockAll(countKeys); err != nil {
		return nil, err
	}
	lines := map[string]map[string]any{}
	for _, l := range rows(rec["items"]) {
		item := str(l["item"])
		bal, err := s.balance(outlet, item)
		if err != nil {
			return nil, err
		}
		tr, err := s.tracking(item)
		if err != nil {
			return nil, err
		}
		li, lo, _ := lineLots(h, h.Model.Name, "items", l)
		lo.expired = expiredAllow
		byLot := (tr == trackLot || tr == trackLotExpiry) && li.declared
		system := qtyOf(bal["qty"])
		if byLot {
			lot := ""
			if no := strings.TrimSpace(li.no); no != "" {
				found, err := h.Find("stock_lot", map[string]any{"item": item, "lot_no": no})
				if err != nil {
					return nil, err
				}
				if len(found) == 0 {
					lot = "\x00new" // not seen before: system 0, created by the incoming difference
				} else {
					lot = str(found[0]["name"])
				}
			} else {
				li.declared = false // the bucket: no lot number is needed
				lo.bucket = true
			}
			if lot != "\x00new" {
				if system, err = s.lotQty(outlet, item, lot); err != nil {
					return nil, err
				}
			} else {
				system = 0
			}
			if lot != "" && lot != "\x00new" {
				lo.pick = lot
			}
		}
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
			if _, err := s.inLine(outlet, item, diff, cost, nil, "count", li, serialSpec{}); err != nil {
				return nil, err
			}
		case diff < -qtyEps:
			if _, _, _, err := s.outLine(outlet, item, -diff, "count", lo, serialSpec{}); err != nil {
				return nil, err
			}
		}
	}
	if len(lines) > 0 {
		if _, err := h.SetSubmitted(h.Model.Name, s.voucher, map[string]any{"items": lines}); err != nil {
			return nil, err
		}
	}
	return map[string]any{"message": "Stok diperbarui dari " + str(rec["name"])}, nil
}

// serialsReverse undoes what a move did to its serials (contract 0070 §8). With a history on serial_no, the
// cancelled document must be the one that moved the serial last (its entry on top), else serial_moved; the entry is
// popped and the serial gets back the state it had before (a new serial: cancelled) and, for an out move, its place
// in stock at outlet. Without a history only the state is checked: an out move's reversal needs the state that move
// left, an in move's reversal needs the serial in stock at outlet and leaves it in transit (a transfer receipt) or
// cancelled.
func (s *stockPost) serialsReverse(outlet, item, reason string, q float64, serials []string) error {
	m := s.h.op.e.Reg().Model("serial_no")
	history := has(m, "history")
	for _, sn := range sortedCopy(serials) {
		cur, err := s.serialRecord(item, sn)
		if err != nil {
			return err
		}
		if cur == nil {
			continue
		}
		st := str(cur["state"])
		moved := refuse("serial_moved", "Nomor seri "+sn+" sudah berpindah sejak dokumen ini; batalkan dokumen sesudahnya dulu")
		f := map[string]any{}
		if history {
			v, before, rest := topHistory(str(cur["history"]))
			if v != voucherRef(s.vtype, s.voucher) {
				return moved
			}
			if before == "" {
				before = "cancelled"
			}
			f["state"], f["history"] = before, rest
			pv, _, _ := topHistory(rest)
			vt, vn := splitVoucherRef(pv)
			if has(m, "last_voucher_type") {
				f["last_voucher_type"] = vt
			}
			if has(m, "last_voucher") {
				f["last_voucher"] = vn
			}
		} else {
			switch {
			case q < 0 && st != serialState(reason):
				return moved
			case q < 0:
				f["state"] = "in_stock"
			case st != "in_stock" || str(cur["outlet"]) != outlet:
				return moved
			case reason == "transfer_in":
				f["state"] = "in_transit"
			default:
				f["state"] = "cancelled"
			}
		}
		if q < 0 {
			f["outlet"] = outlet
		}
		if has(m, "prev_state") {
			f["prev_state"] = st
		}
		if _, err := s.h.SetSubmitted("serial_no", str(cur["name"]), f); err != nil {
			return err
		}
	}
	return nil
}

// voucherRef is "<type>/<name>" ("" when there is none); splitVoucherRef reads it back.
func voucherRef(vtype, voucher string) string {
	if voucher == "" {
		return ""
	}
	return vtype + "/" + voucher
}

func splitVoucherRef(ref string) (string, string) {
	vt, v, ok := strings.Cut(ref, "/")
	if !ok {
		return "", ""
	}
	return vt, v
}
