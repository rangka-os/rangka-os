package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// Receivables, payables and payment allocation (S8c). Money is whole rupiah in int64 (moneyIn/moneyOut).

func registerLedgerHooks(e *Engine) {
	e.Hooks["ledger.post_receivable"] = postReceivable
	e.Hooks["invoicing.reverse_receivable"] = reverseReceivable
	e.Hooks["procurement.post_payable"] = postPayable
	e.Hooks["procurement.reverse_payable"] = reversePayable
	e.Hooks["payments.allocate"] = allocatePayment
	e.Hooks["payments.unallocate"] = unallocatePayment
}

// amountDue is what a document adds to its party's outstanding: amount_due, or grand_total for invoice
// models that do not have one (rangka-apps before S8).
func amountDue(h *HookCtx) int64 {
	if has(h.Model, "amount_due") {
		return moneyIn(h.Record["amount_due"])
	}
	return moneyIn(h.Record["grand_total"])
}

// addOutstanding adds delta to a party's outstanding (customer or vendor), with the row locked.
func addOutstanding(h *HookCtx, model, name string, delta int64) error {
	if name == "" || delta == 0 || !has(h.op.e.Reg().Model(model), "outstanding") {
		return nil
	}
	party, err := h.Lock(model, name)
	if err != nil {
		return err
	}
	_, err = h.SetSubmitted(model, name, map[string]any{"outstanding": moneyOut(moneyIn(party["outstanding"]) + delta)})
	return err
}

// addInvoiced adds an invoice's subtotal to its customer_order.invoiced_amount.
func addInvoiced(h *HookCtx, sign int64) error {
	order := str(h.Record["customer_order"])
	om := h.op.e.Reg().Model("customer_order")
	if order == "" || !has(om, "invoiced_amount") {
		return nil
	}
	o, err := h.Lock("customer_order", order)
	if err != nil {
		return err
	}
	_, err = h.SetSubmitted("customer_order", order, map[string]any{"invoiced_amount": moneyOut(moneyIn(o["invoiced_amount"]) + sign*moneyIn(h.Record["subtotal"]))})
	return err
}

// fieldOr is name when m has it, else old (catalogues from before a field was renamed).
func fieldOr(m *meta.Model, name, old string) string {
	if has(m, name) || !has(m, old) {
		return name
	}
	return old
}

// postReceivable is ledger.post_receivable (sales_invoice on_submit).
func postReceivable(h *HookCtx) (map[string]any, error) {
	inv := h.Record
	name := str(inv["name"])
	if dp := str(inv["dp_invoice"]); dp != "" && has(h.Model, "dp_invoice") {
		dpInv, err := h.Lock("sales_invoice", dp) // serialises invoices deducting the same DP
		if err != nil {
			return nil, err
		}
		deducting, err := h.Find("sales_invoice", map[string]any{"dp_invoice": dp, "docstatus": 1})
		if err != nil {
			return nil, err
		}
		deduction, base := fieldOr(h.Model, "down_payment", "dp_deduction"), fieldOr(h.Model, "dpp", "dpp_base")
		var sum int64
		for _, r := range deducting {
			sum += moneyIn(r[deduction])
		}
		if sum > moneyIn(dpInv[base]) {
			return nil, refuse("dp_over_deducted", fmt.Sprintf("Total potongan uang muka %s melebihi DPP faktur uang muka %s", rupiah(sum), dp))
		}
	}
	if orig := str(inv["retention_of"]); orig != "" && has(h.Model, "retention_of") {
		if _, err := h.Lock("sales_invoice", orig); err != nil {
			return nil, err
		}
		billed, err := h.Find("sales_invoice", map[string]any{"retention_of": orig, "docstatus": 1})
		if err != nil {
			return nil, err
		}
		for _, r := range billed {
			if str(r["name"]) != name {
				return nil, refuse("retention_billed", "Retensi faktur "+orig+" sudah ditagih di "+str(r["name"]))
			}
		}
	}
	due := amountDue(h)
	if err := addOutstanding(h, "customer", str(inv["customer"]), due); err != nil {
		return nil, err
	}
	if err := addInvoiced(h, 1); err != nil {
		return nil, err
	}
	return map[string]any{"message": "Piutang " + str(inv["customer"]) + " bertambah " + rupiah(due)}, nil
}

// reverseReceivable is invoicing.reverse_receivable (sales_invoice on_cancel).
func reverseReceivable(h *HookCtx) (map[string]any, error) {
	inv := h.Record
	name := str(inv["name"])
	if moneyIn(inv["paid_amount"]) > 0 {
		return nil, refuse("has_payments", "Faktur "+name+" sudah dibayar; batalkan pembayarannya dulu")
	}
	for _, f := range []string{"dp_invoice", "retention_of"} {
		if !has(h.Model, f) {
			continue
		}
		deps, err := h.Find("sales_invoice", map[string]any{f: name, "docstatus": 1})
		if err != nil {
			return nil, err
		}
		if len(deps) > 0 {
			return nil, refuse("has_dependents", "Faktur "+str(deps[0]["name"])+" masih merujuk faktur ini; batalkan faktur itu dulu")
		}
	}
	due := amountDue(h)
	if err := addOutstanding(h, "customer", str(inv["customer"]), -due); err != nil {
		return nil, err
	}
	if err := addInvoiced(h, -1); err != nil {
		return nil, err
	}
	return map[string]any{"message": "Piutang " + str(inv["customer"]) + " berkurang " + rupiah(due)}, nil
}

// postPayable is procurement.post_payable (vendor_bill on_submit).
func postPayable(h *HookCtx) (map[string]any, error) {
	due := amountDue(h)
	if err := addOutstanding(h, "vendor", str(h.Record["vendor"]), due); err != nil {
		return nil, err
	}
	return map[string]any{"message": "Utang ke " + str(h.Record["vendor"]) + " bertambah " + rupiah(due)}, nil
}

// reversePayable is procurement.reverse_payable (vendor_bill on_cancel).
func reversePayable(h *HookCtx) (map[string]any, error) {
	if moneyIn(h.Record["paid_amount"]) > 0 {
		return nil, refuse("has_payments", "Tagihan "+str(h.Record["name"])+" sudah dibayar; batalkan pembayarannya dulu")
	}
	due := amountDue(h)
	if err := addOutstanding(h, "vendor", str(h.Record["vendor"]), -due); err != nil {
		return nil, err
	}
	return map[string]any{"message": "Utang ke " + str(h.Record["vendor"]) + " berkurang " + rupiah(due)}, nil
}

// paymentSide is what a payment allocates against: sales invoices of a customer (receive) or vendor bills
// of a vendor (pay).
type paymentSide struct {
	target, link, partyModel, party string
}

func sideOf(pay map[string]any) paymentSide {
	if str(pay["direction"]) == "pay" {
		return paymentSide{target: "vendor_bill", link: "vendor_bill", partyModel: "vendor", party: str(pay["vendor"])}
	}
	return paymentSide{target: "sales_invoice", link: "sales_invoice", partyModel: "customer", party: str(pay["customer"])}
}

// allocation is one allocation row. Rows and payment.amount are always IDR: a foreign payment converts once
// at the header (contract 0013 money twin), so the hook applies the row amount as is.
type allocation struct {
	row      map[string]any
	target   string
	applied  int64 // cash applied, IDR
	withheld int64 // PPh 23 withheld, IDR
}

func allocations(h *HookCtx, side paymentSide) ([]allocation, error) {
	var out []allocation
	var issues []Issue
	for i, row := range rows(h.Record["allocations"]) {
		t := str(row[side.link])
		if t == "" {
			issues = append(issues, Issue{Field: fmt.Sprintf("allocations.%d.%s", i, side.link), Code: "required",
				Message: fmt.Sprintf("Alokasi baris %d: pilih tagihan yang dibayar", i+1)})
			continue
		}
		out = append(out, allocation{row: row, target: t, applied: moneyIn(row["amount"]), withheld: moneyIn(row["withheld"])})
	}
	if len(issues) > 0 {
		return nil, validation(issues, nil)
	}
	return out, nil
}

// lockTargets locks every allocated invoice or bill in name order (one order for every payment, so two
// payments over the same invoices cannot deadlock) and returns their stored values.
func lockTargets(h *HookCtx, side paymentSide, allocs []allocation) (map[string]map[string]any, error) {
	names := []string{}
	seen := map[string]bool{}
	for _, a := range allocs {
		if !seen[a.target] {
			seen[a.target] = true
			names = append(names, a.target)
		}
	}
	sort.Strings(names)
	out := map[string]map[string]any{}
	for _, n := range names {
		r, err := h.Lock(side.target, n)
		if err != nil {
			return nil, err
		}
		out[n] = r
	}
	return out, nil
}

// allocatePayment is payments.allocate (payment on_submit).
func allocatePayment(h *HookCtx) (map[string]any, error) {
	pay := h.Record
	side := sideOf(pay)
	if h.op.e.Reg().Model(side.target) == nil {
		return map[string]any{"message": side.target + " is not installed"}, nil
	}
	allocs, err := allocations(h, side)
	if err != nil {
		return nil, err
	}
	var cash int64
	for i, a := range allocs {
		if a.applied <= 0 || a.withheld < 0 {
			return nil, refuse("negative_allocation", fmt.Sprintf("Alokasi baris %d harus lebih dari 0 dan potongan tidak boleh negatif", i+1))
		}
		cash += a.applied
	}
	if cash > moneyIn(pay["amount"]) {
		return nil, refuse("over_payment", "Alokasi melebihi jumlah pembayaran")
	}
	targets, err := lockTargets(h, side, allocs)
	if err != nil {
		return nil, err
	}
	var total int64
	rowsIDR := map[string]map[string]any{}
	for _, a := range allocs {
		t := targets[a.target]
		if toInt(t["docstatus"]) != 1 {
			return nil, refuse("not_submitted", "Tagihan "+a.target+" belum diajukan atau sudah dibatalkan")
		}
		if str(t[side.partyModel]) != side.party {
			return nil, refuse("wrong_party", "Tagihan "+a.target+" bukan milik "+side.party)
		}
		if a.withheld > 0 && side.target == "sales_invoice" && t["pph23_applies"] == true {
			return nil, refuse("pph23_already_deducted", "PPh 23 faktur "+a.target+" sudah dipotong di faktur; jangan potong lagi di pembayaran")
		}
		settle := a.applied + a.withheld
		if settle > moneyIn(t["outstanding"]) { // the stored outstanding, after earlier rows on the same invoice
			return nil, refuse("over_allocation", "Alokasi melebihi sisa tagihan "+a.target)
		}
		upd, err := h.SetSubmitted(side.target, a.target, map[string]any{"paid_amount": moneyOut(moneyIn(t["paid_amount"]) + settle)})
		if err != nil {
			return nil, err
		}
		targets[a.target] = upd // a second row on the same invoice sees the first
		total += settle
		rowsIDR[str(a.row["name"])] = map[string]any{"applied_idr": a.applied}
	}
	if err := addOutstanding(h, side.partyModel, side.party, -total); err != nil {
		return nil, err
	}
	if am := h.op.e.Reg().Model("payment_allocation"); has(am, "applied_idr") && len(rowsIDR) > 0 {
		if _, err := h.SetSubmitted(h.Model.Name, str(pay["name"]), map[string]any{"allocations": rowsIDR}); err != nil {
			return nil, err
		}
	}
	return map[string]any{"message": fmt.Sprintf("%d tagihan dialokasikan, %s", len(allocs), rupiah(total))}, nil
}

// unallocatePayment is payments.unallocate (payment on_cancel): the exact inverse of allocate, from the
// stored applied_idr of each row.
func unallocatePayment(h *HookCtx) (map[string]any, error) {
	pay := h.Record
	side := sideOf(pay)
	if h.op.e.Reg().Model(side.target) == nil {
		return map[string]any{"message": side.target + " is not installed"}, nil
	}
	allocs, err := allocations(h, side)
	if err != nil {
		return nil, err
	}
	stored := has(h.op.e.Reg().Model("payment_allocation"), "applied_idr")
	targets, err := lockTargets(h, side, allocs)
	if err != nil {
		return nil, err
	}
	var total int64
	for _, a := range allocs {
		applied := a.applied
		if stored {
			applied = toInt(a.row["applied_idr"])
		}
		settle := applied + a.withheld
		t := targets[a.target]
		upd, err := h.SetSubmitted(side.target, a.target, map[string]any{"paid_amount": moneyOut(moneyIn(t["paid_amount"]) - settle)})
		if err != nil {
			return nil, err
		}
		targets[a.target] = upd
		total += settle
	}
	if err := addOutstanding(h, side.partyModel, side.party, total); err != nil {
		return nil, err
	}
	return map[string]any{"message": fmt.Sprintf("Alokasi %d tagihan dibatalkan, %s", len(allocs), rupiah(total))}, nil
}

// rupiah formats whole rupiah as "Rp 1.250.000".
func rupiah(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprint(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-Rp " + b.String()
	}
	return "Rp " + b.String()
}
