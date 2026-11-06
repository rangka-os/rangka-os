package engine

import (
	"context"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Audit 2026-11-07: the summary's currency is the amount's own (a currency field is rupiah even on a document in
// USD), and a non-money "amount" is not shown as money.
func TestApprovalSummaryCurrency(t *testing.T) {
	e, owner := approvalSetupFiles(t, map[string]string{"crm/models/fx_doc.rk": `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json
name: fx_doc
label: {id: Dokumen valas}
naming: FX-{####}
fields:
  - {name: currency, type: data, label: {id: Mata uang}}
  - {name: grand_total, type: currency, label: {id: Total}}
`, "crm/models/unit_doc.rk": `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json
name: unit_doc
label: {id: Dokumen unit}
naming: UD-{####}
fields:
  - {name: amount, type: int, label: {id: Jumlah unit}}
`}, "")
	ctx := context.Background()
	res, err := e.Create(ctx, owner, "fx_doc", map[string]any{"currency": "USD", "grand_total": 632700})
	if err != nil {
		t.Fatal(err)
	}
	sum := e.ApprovalSummary(ctx, owner, &store.Approval{Model: "fx_doc", Name: str(res.Record["name"]), Verb: "submit"})
	if sum["currency"] != "IDR" || sum["amount"] == nil {
		t.Fatalf("summary of a USD document's rupiah total = %v", sum)
	}
	res, err = e.Create(ctx, owner, "unit_doc", map[string]any{"amount": 12})
	if err != nil {
		t.Fatal(err)
	}
	if sum := e.ApprovalSummary(ctx, owner, &store.Approval{Model: "unit_doc", Name: str(res.Record["name"]), Verb: "submit"}); sum["amount"] != nil {
		t.Fatalf("a count shown as money: %v", sum)
	}
}
