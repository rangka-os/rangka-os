package changeset

import (
	"strings"
	"testing"
)

func TestLiteralEmails(t *testing.T) {
	flow := map[string]any{"steps": []any{
		map[string]any{"notify": "head_office", "template": "ok"},
		map[string]any{"if": "x", "then": []any{map[string]any{"notify": "budi@contoh.id"}}},
	}}
	after := func(p string) (map[string]any, bool) { return flow, p == "flows/stok_minus.rk" }
	as := &Assessment{Risk: "medium"}
	literalEmails(as, []Op{{Op: "create", Path: "flows/stok_minus.rk"}}, after)
	if as.Risk != "high" || len(as.Reasons) != 1 || !strings.Contains(as.Reasons[0], "notice_literal_email: flows/stok_minus.rk steps/1/then/0 tells budi@contoh.id") {
		t.Fatalf("a literal address: %+v", as)
	}
	as = &Assessment{Risk: "medium"}
	flow["steps"] = []any{map[string]any{"notify": "head_office"}}
	literalEmails(as, []Op{{Op: "create", Path: "flows/stok_minus.rk"}, {Op: "delete", Path: "flows/old.rk"}}, after)
	if as.Risk != "medium" || len(as.Reasons) != 0 {
		t.Fatalf("roles only: %+v", as)
	}
	if !isFlowPath("modules/stock/flows/x.rk") || isFlowPath("modules/stock/models/x.rk") || !isFlowPath("flows/x.rk") {
		t.Fatal("isFlowPath")
	}
}
