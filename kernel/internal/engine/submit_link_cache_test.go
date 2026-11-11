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
