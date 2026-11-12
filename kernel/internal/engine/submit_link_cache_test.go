package engine

import (
	"context"
	"testing"
)

// memoCreditFiles: a submittable document whose check compute reads its customer's credit and its lines' total
// (like delivery_note.credit_over_seen in rangka-apps), with an on_submit hook.
var memoCreditFiles = map[string]string{
	"crm/models/memo_credit.rk": memoHeader + `name: memo_credit
label: {id: Memo kredit}
is_submittable: true
naming: MK-{#####}
on_submit: [crm.mark_won]
fields:
  - {name: customer, type: link, to: customer, label: {id: Pelanggan}}
  - {name: lines, type: table, of: memo_credit_line, label: {id: Baris}}
  - {name: total, type: currency, label: {id: Total}, compute: "lines.map(l, l.qty).sum()"}
  - {name: over_seen, type: check, label: {id: Lewat batas saat diajukan}, compute: "customer.credit_limit > 0 && customer.outstanding + total > customer.credit_limit"}
`,
	"crm/models/memo_credit_line.rk": memoHeader + `name: memo_credit_line
is_child: true
fields:
  - {name: qty, type: currency, label: {id: Jumlah}}
  - {name: cost, type: currency, label: {id: Biaya}, read_only: true}
`,
}

// A submit reads a linked record once: a hook that sets a submitted row (stock cogs) recomputes the computes that
// depend on it with the linked values the submit saw, not with a commit another transaction made meanwhile.
// rangka-os#135 emptied the whole link cache at the draft rename, so delivery_note.credit_over_seen read the
// customer again after the other delivery committed, and the flow that re-checks credit under its lock let both
// deliveries through (card t_48e24a91, rangka-apps day_one scenario_grosir_credit_race).
func TestSubmitRenameKeepsLinksReadBySubmit(t *testing.T) {
	e, owner := approvalSetupFiles(t, memoCreditFiles, "")
	ctx := context.Background()
	cust, err := e.Get(ctx, owner, "customer", "CUST-0012")
	if err != nil {
		t.Fatal(err)
	}
	out := int(num(cust["outstanding"]))
	if _, err := e.Update(ctx, owner, "customer", "CUST-0012", map[string]any{"credit_limit": out + 1000}); err != nil {
		t.Fatal(err)
	}
	res, err := e.Create(ctx, owner, "memo_credit", map[string]any{"customer": "CUST-0012", "lines": []any{map[string]any{"qty": 600}}})
	if err != nil {
		t.Fatal(err)
	}
	draft := str(res.Record["name"])
	row := str(rows(res.Record["lines"])[0]["name"])
	e.Hooks["crm.mark_won"] = func(h *HookCtx) (map[string]any, error) {
		// another unit of work commits a write to the customer while this submit runs (another delivery took room)
		if _, err := e.Update(ctx, owner, "customer", "CUST-0012", map[string]any{"credit_limit": out + 100}); err != nil {
			return nil, err
		}
		// the stock hook sets the line's cost: computes reading lines are recomputed
		_, err := h.SetSubmitted("memo_credit", str(h.Record["name"]), map[string]any{"lines": map[string]map[string]any{row: {"cost": 1}}})
		return map[string]any{}, err
	}
	if res.Record["over_seen"] != false {
		t.Fatalf("draft over_seen = %v, want false", res.Record["over_seen"])
	}
	sub, err := e.Submit(ctx, owner, "memo_credit", draft, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.Get(ctx, owner, "memo_credit", str(sub.Record["name"]))
	if err != nil {
		t.Fatal(err)
	}
	if got["over_seen"] != false {
		t.Fatalf("over_seen = %v after the hook, want false (the customer as the submit read it)", got["over_seen"])
	}
}

// memoAcctFiles: memo_credit's customer replaced by an account that links documents back (a field and a child
// row), as a site's x_ link or a table on customer could.
var memoAcctFiles = map[string]string{
	"crm/models/memo_acct.rk": memoHeader + `name: memo_acct
label: {id: Akun memo}
naming: MA-{#####}
fields:
  - {name: limit_amt, type: currency, label: {id: Batas}}
  - {name: used, type: currency, label: {id: Terpakai}}
  - {name: last_memo, type: link, to: memo_acct_doc, label: {id: Memo terakhir}}
  - {name: notes, type: table, of: memo_acct_note, label: {id: Catatan}}
`,
	"crm/models/memo_acct_note.rk": memoHeader + `name: memo_acct_note
is_child: true
fields:
  - {name: memo, type: link, to: memo_acct_doc, label: {id: Memo}}
`,
	"crm/models/memo_acct_doc.rk": memoHeader + `name: memo_acct_doc
label: {id: Memo akun}
is_submittable: true
naming: MD2-{#####}
on_submit: [crm.mark_won]
fields:
  - {name: acct, type: link, to: memo_acct, label: {id: Akun}}
  - {name: acct2, type: link, to: memo_acct, label: {id: Akun lain}}
  - {name: lines, type: table, of: memo_credit_line, label: {id: Baris}}
  - {name: total, type: currency, label: {id: Total}, compute: "lines.map(l, l.qty).sum()"}
  - {name: over_seen, type: check, label: {id: Lewat batas saat diajukan}, compute: "acct.limit_amt > 0 && acct.used + total > acct.limit_amt"}
  - {name: b_limit, type: currency, label: {id: Batas akun lain}, compute: "acct2.limit_amt"}
  - {name: back, type: data, label: {id: Rujukan balik}, read_only: true}
`,
}

// The rename does not read a referrer again either: an account that links the document back keeps the values the
// submit read, with the link names moved to the number (audit credit-race 1). B is read by a compute before the
// rename, so the hook sees the cache entry the rename corrected in place (credit-race putaran 2 temuan 3).
func TestSubmitRenameKeepsReferrerReadBySubmit(t *testing.T) {
	files := map[string]string{}
	for k, v := range memoCreditFiles {
		files[k] = v
	}
	for k, v := range memoAcctFiles {
		files[k] = v
	}
	e, owner := approvalSetupFiles(t, files, "")
	ctx := context.Background()
	// A is read for credit and could link documents back (none links this draft); B links the draft back
	acct, err := e.Create(ctx, owner, "memo_acct", map[string]any{"limit_amt": 1000, "used": 0})
	if err != nil {
		t.Fatal(err)
	}
	an := str(acct.Record["name"])
	b2, err := e.Create(ctx, owner, "memo_acct", map[string]any{"limit_amt": 0})
	if err != nil {
		t.Fatal(err)
	}
	bn := str(b2.Record["name"])
	res, err := e.Create(ctx, owner, "memo_acct_doc", map[string]any{"acct": an, "acct2": bn, "lines": []any{map[string]any{"qty": 600}}})
	if err != nil {
		t.Fatal(err)
	}
	draft := str(res.Record["name"])
	row := str(rows(res.Record["lines"])[0]["name"])
	if _, err := e.Update(ctx, owner, "memo_acct", bn, map[string]any{"last_memo": draft, "notes": []any{map[string]any{"memo": draft}}}); err != nil {
		t.Fatal(err)
	}
	var back string
	var cachedBefore bool
	e.Hooks["crm.mark_won"] = func(h *HookCtx) (map[string]any, error) {
		// b_limit read B while submit computed, before the rename: the rename must have moved B's cached links in
		// place, not dropped B so the read below loads it fresh (audit credit-race putaran 2 temuan 3)
		_, cachedBefore = h.op.links["memo_acct/"+bn]
		// another unit of work uses A's room while this submit runs (A's row is not locked by the submit)
		if _, err := e.Update(ctx, owner, "memo_acct", an, map[string]any{"used": 600}); err != nil {
			return nil, err
		}
		// what B's cached links say now (an expression reading acct2.last_memo / acct2.notes)
		v, err := h.op.scope(h.Model, h.Record).eval("acct2.last_memo.name + '|' + acct2.notes[0].memo")
		if err != nil {
			return nil, err
		}
		back = str(v)
		_, err = h.SetSubmitted("memo_acct_doc", str(h.Record["name"]), map[string]any{"lines": map[string]map[string]any{row: {"cost": 1}}})
		return map[string]any{}, err
	}
	sub, err := e.Submit(ctx, owner, "memo_acct_doc", draft, false)
	if err != nil {
		t.Fatal(err)
	}
	fin := str(sub.Record["name"])
	got, err := e.Get(ctx, owner, "memo_acct_doc", fin)
	if err != nil {
		t.Fatal(err)
	}
	if got["over_seen"] != false {
		t.Fatalf("over_seen = %v after the hook, want false (the account as the submit read it)", got["over_seen"])
	}
	if !cachedBefore {
		t.Fatalf("account B was not in the link cache when the hook ran: the test does not exercise the in-place rename")
	}
	if back != fin+"|"+fin {
		t.Fatalf("cached account links = %q, want %s|%s", back, fin, fin)
	}
}
