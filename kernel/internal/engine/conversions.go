package engine

import (
	"math"

	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Document conversions (S8c): actions that make the next document as a draft and return
// {"open": {"model", "name"}, "message"}. Each reuses an existing draft for the same source instead of
// making a second one.

func registerConversionHooks(e *Engine) {
	e.Hooks["quotation.revise"] = reviseQuotation
	e.Hooks["quotation.mark_outcome"] = markOutcome
	e.Hooks["orders.from_quotation"] = orderFromQuotation
	e.Hooks["invoicing.from_order"] = invoiceFromOrder
	e.Hooks["procurement.make_po"] = makePO
	e.Hooks["procurement.receive"] = receivePO
}

const qtyEps = 1e-9

func openResult(model, name, msg string) map[string]any {
	return map[string]any{"message": msg, "open": map[string]any{"model": model, "name": name}}
}

// createDraft creates a draft in the hook's transaction, leaving the named required fields for the user.
func (h *HookCtx) createDraft(model string, input map[string]any, allowMissing ...string) (store.Record, error) {
	r, err := h.op.create(model, input, CreateOpts{AllowMissing: allowMissing})
	if err != nil {
		return nil, err
	}
	return r.Record, nil
}

// draftFor returns the first draft of model whose field names the source, or nil.
func draftFor(h *HookCtx, model, field, source string) (store.Record, error) {
	if !has(h.op.e.Reg().Model(model), field) {
		return nil, nil
	}
	found, err := h.Find(model, map[string]any{field: source, "docstatus": 0})
	if err != nil || len(found) == 0 {
		return nil, err
	}
	return found[0], nil
}

// needOpen refuses a quotation whose outcome is decided (” left by amend counts as open).
func needOpen(q map[string]any) error {
	switch str(q["outcome"]) {
	case "accepted", "lost":
		return refuse("not_open", "Penawaran "+str(q["name"])+" sudah "+str(q["outcome"]))
	}
	return nil
}

func needSubmitted(rec map[string]any, what string) error {
	if toInt(rec["docstatus"]) != 1 {
		return refuse("not_submitted", what+" "+str(rec["name"])+" harus sudah diajukan")
	}
	return nil
}

// reviseQuotation is quotation.revise: cancel the submitted quotation and amend it into a draft with
// revision + 1 and outcome open (Amend zeroes read-only fields, so both are set explicitly).
func reviseQuotation(h *HookCtx) (map[string]any, error) {
	q := h.Record
	name := str(q["name"])
	if err := needSubmitted(q, "Penawaran"); err != nil {
		return nil, err
	}
	if err := needOpen(q); err != nil {
		return nil, err
	}
	if _, err := h.op.cancel(h.Model.Name, name); err != nil {
		return nil, err
	}
	am, err := h.op.amend(h.Model.Name, name)
	if err != nil {
		return nil, err
	}
	patch := map[string]any{"revision": toInt(q["revision"]) + 1}
	if has(h.Model, "outcome") {
		patch["outcome"] = "open"
	}
	if has(h.Model, "lost_reason") {
		patch["lost_reason"] = ""
	}
	draft, err := h.Update(h.Model.Name, str(am.Record["name"]), patch)
	if err != nil {
		return nil, err
	}
	return openResult(h.Model.Name, str(draft["name"]), "Penawaran "+name+" dibatalkan; revisi "+formatInt(toInt(patch["revision"]))+" dibuat sebagai draf"), nil
}

// markOutcome is quotation.mark_outcome: sets outcome (open|accepted|lost) and lost_reason on a submitted
// quotation from the action's args.
func markOutcome(h *HookCtx) (map[string]any, error) {
	q := h.Record
	if err := needSubmitted(q, "Penawaran"); err != nil {
		return nil, err
	}
	if err := needOpen(q); err != nil {
		return nil, err
	}
	outcome := str(h.Args["outcome"])
	switch outcome {
	case "open", "accepted", "lost":
	default:
		return nil, refuse("bad_outcome", "Hasil penawaran harus open, accepted atau lost")
	}
	reason := str(h.Args["lost_reason"])
	if outcome == "lost" && reason == "" {
		return nil, refuse("lost_reason_required", "Isi alasan kalah")
	}
	if outcome != "lost" {
		reason = ""
	}
	fields := map[string]any{"outcome": outcome}
	if has(h.Model, "lost_reason") {
		fields["lost_reason"] = reason
	}
	rec, err := h.setSubmitted(h.Model.Name, str(q["name"]), fields, []string{"outcome", "lost_reason"})
	if err != nil {
		return nil, err
	}
	return map[string]any{"record": rec, "message": "Hasil penawaran " + str(q["name"]) + ": " + outcome}, nil
}

// orderFromQuotation is orders.from_quotation: a draft customer_order with the quotation's customer and lines
// and the customer's PO number and date from the args.
func orderFromQuotation(h *HookCtx) (map[string]any, error) {
	q := h.Record
	name := str(q["name"])
	poNumber, poDate := str(h.Args["po_number"]), str(h.Args["po_date"])
	if poNumber == "" || poDate == "" {
		return nil, &StatusError{422, "po_required", "Nomor dan tanggal PO pelanggan wajib diisi"}
	}
	if h.op.e.Reg().Model("customer_order") == nil {
		return map[string]any{"message": "orders is not installed"}, nil
	}
	if err := needSubmitted(q, "Penawaran"); err != nil {
		return nil, err
	}
	if d, err := draftFor(h, "customer_order", "quotation", name); err != nil || d != nil {
		if err != nil {
			return nil, err
		}
		return openResult("customer_order", str(d["name"]), "Draf pesanan "+str(d["name"])+" sudah ada untuk penawaran ini"), nil
	}
	var items []any
	for _, l := range rows(q["items"]) {
		rate := moneyIn(l["rate"])
		if d := num(l["discount"]); d != 0 { // the order line has no discount: carry the net rate
			rate = percentOff(rate, d)
		}
		items = append(items, map[string]any{"item": l["item"], "description": l["description"], "qty": l["qty"], "uom": l["uom"], "rate": moneyOut(rate)})
	}
	o, err := h.createDraft("customer_order", map[string]any{"customer": q["customer"], "quotation": name, "po_number": poNumber, "po_date": poDate, "items": items})
	if err != nil {
		return nil, err
	}
	return openResult("customer_order", str(o["name"]), "Draf pesanan "+str(o["name"])+" dibuat dari penawaran "+name), nil
}

// invoiceFromOrder is invoicing.from_order: a draft sales_invoice for what the order has not invoiced yet,
// per item (quantities of submitted invoices of the order are taken from the order lines in order).
func invoiceFromOrder(h *HookCtx) (map[string]any, error) {
	so := h.Record
	name := str(so["name"])
	if h.op.e.Reg().Model("sales_invoice") == nil {
		return map[string]any{"message": "invoicing is not installed"}, nil
	}
	if toInt(so["docstatus"]) != 1 {
		return nil, refuse("order_not_submitted", "Pesanan "+name+" harus sudah diajukan")
	}
	if d, err := draftFor(h, "sales_invoice", "customer_order", name); err != nil || d != nil {
		if err != nil {
			return nil, err
		}
		return openResult("sales_invoice", str(d["name"]), "Draf faktur "+str(d["name"])+" sudah ada untuk pesanan ini"), nil
	}
	invoiced := map[string]float64{}
	done, err := h.Find("sales_invoice", map[string]any{"customer_order": name, "docstatus": 1})
	if err != nil {
		return nil, err
	}
	for _, inv := range done {
		for _, l := range rows(inv["items"]) {
			invoiced[str(l["item"])] += qtyOf(l["qty"])
		}
	}
	var items []any
	for _, l := range rows(so["items"]) {
		item := str(l["item"])
		qty := qtyOf(l["qty"])
		used := math.Min(qty, invoiced[item])
		invoiced[item] -= used
		if rest := qty - used; rest > qtyEps {
			items = append(items, map[string]any{"item": item, "description": l["description"], "qty": rest, "uom": l["uom"], "rate": l["rate"]})
		}
	}
	if len(items) == 0 {
		return nil, refuse("nothing_to_invoice", "Semua baris pesanan "+name+" sudah ditagih")
	}
	in := map[string]any{"customer": so["customer"], "customer_order": name, "kind": "regular", "items": items}
	if has(h.op.e.Reg().Model("sales_invoice"), "po_number") {
		in["po_number"] = so["po_number"]
	}
	inv, err := h.createDraft("sales_invoice", in)
	if err != nil {
		return nil, err
	}
	return openResult("sales_invoice", str(inv["name"]), "Draf faktur "+str(inv["name"])+" dibuat dari pesanan "+name), nil
}

// makePO is procurement.make_po: a draft purchase_order with the request's lines; the vendor comes from the
// args or is left for the user. The request's state becomes ordered.
func makePO(h *HookCtx) (map[string]any, error) {
	pr := h.Record
	name := str(pr["name"])
	if h.op.e.Reg().Model("purchase_order") == nil {
		return map[string]any{"message": "purchase_order is not installed"}, nil
	}
	if err := needSubmitted(pr, "Permintaan pembelian"); err != nil {
		return nil, err
	}
	if d, err := draftFor(h, "purchase_order", "purchase_request", name); err != nil || d != nil {
		if err != nil {
			return nil, err
		}
		return openResult("purchase_order", str(d["name"]), "Draf PO "+str(d["name"])+" sudah ada untuk permintaan ini"), nil
	}
	var items []any
	for _, l := range rows(pr["items"]) {
		items = append(items, map[string]any{"item": l["item"], "qty": l["qty"], "uom": l["uom"], "packing": l["packing"], "rate": l["est_rate"]})
	}
	in := map[string]any{"purchase_request": name, "outlet": pr["outlet"], "items": items}
	if v := str(h.Args["vendor"]); v != "" {
		in["vendor"] = v
	}
	po, err := h.createDraft("purchase_order", in, "vendor")
	if err != nil {
		return nil, err
	}
	if has(h.Model, "state") {
		if _, err := h.SetSubmitted(h.Model.Name, name, map[string]any{"state": "ordered"}); err != nil {
			return nil, err
		}
	}
	return openResult("purchase_order", str(po["name"]), "Draf PO "+str(po["name"])+" dibuat dari permintaan "+name), nil
}

// receivePO is procurement.receive: a draft goods_receipt for what the purchase order has not received yet.
func receivePO(h *HookCtx) (map[string]any, error) {
	po := h.Record
	name := str(po["name"])
	if h.op.e.Reg().Model("goods_receipt") == nil {
		return map[string]any{"message": "goods_receipt is not installed"}, nil
	}
	if err := needSubmitted(po, "PO"); err != nil {
		return nil, err
	}
	if d, err := draftFor(h, "goods_receipt", "purchase_order", name); err != nil || d != nil {
		if err != nil {
			return nil, err
		}
		return openResult("goods_receipt", str(d["name"]), "Draf penerimaan "+str(d["name"])+" sudah ada untuk PO ini"), nil
	}
	var items []any
	for _, l := range rows(po["items"]) {
		if rest := qtyOf(l["qty"]) - qtyOf(l["received_qty"]); rest > qtyEps {
			items = append(items, map[string]any{"item": l["item"], "qty": rest, "uom": l["uom"], "packing": l["packing"], "rate": l["rate"]})
		}
	}
	if len(items) == 0 {
		return nil, refuse("nothing_to_receive", "Semua barang PO "+name+" sudah diterima")
	}
	gr, err := h.createDraft("goods_receipt", map[string]any{"purchase_order": name, "vendor": po["vendor"], "outlet": po["outlet"], "items": items}, "outlet")
	if err != nil {
		return nil, err
	}
	return openResult("goods_receipt", str(gr["name"]), "Draf penerimaan "+str(gr["name"])+" dibuat dari PO "+name), nil
}
