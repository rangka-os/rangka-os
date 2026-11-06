package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A client filter whose link load fails on the database (here a cancelled context) errors instead of reading the
// record as missing: `customer.customer_name == ""` must not match every row while the database is unreachable.
func TestClientFilterDatabaseErrorFails(t *testing.T) {
	e, owner := setup(t)
	ctx := context.Background()
	invs, _, err := e.List(ctx, owner, "sales_invoice", ListOpts{Limit: 1})
	if err != nil || len(invs) == 0 {
		t.Fatalf("need an invoice: %v", err)
	}
	m := e.Reg().Models["sales_invoice"]
	dead, cancel := context.WithCancel(ctx)
	cancel()
	v, err := e.newClientScope(dead, owner, m, invs[0], e.RowScope(owner), nil).eval(`customer.customer_name == ""`)
	if err == nil {
		t.Fatalf("filter on a failed link load = %v, want an error", v)
	}
}

// A rule that cannot be evaluated blocks the save (fail closed) and says it could not be checked.
func TestRuleErrorFailsClosed(t *testing.T) {
	e, a := approvalSetupFiles(t, map[string]string{"crm/models/memo_guard.rk": `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json
name: memo_guard
label: {id: Memo dijaga}
naming: MG-{#####}
fields:
  - {name: code, type: data, label: {id: Kode}}
rules:
  - name: code_third_char
    on: save
    check: "['x', 'y', 'X'][size(code)] != 'X'"
    message: {id: Kode dua huruf tidak diizinkan}
`}, "")
	ctx := context.Background()
	if _, err := e.Create(ctx, a, "memo_guard", map[string]any{"code": "A"}); err != nil {
		t.Fatalf("a rule that evaluates true lets the save through: %v", err)
	}
	if _, err := e.Create(ctx, a, "memo_guard", map[string]any{"code": "AB"}); err == nil {
		t.Fatal("a failing rule must block")
	}
	// "ABC" indexes past the list: the rule cannot be evaluated and now blocks
	_, err := e.Create(ctx, a, "memo_guard", map[string]any{"code": "ABC"})
	var ve *ValidationError
	if err == nil || !errors.As(err, &ve) || len(ve.Errors) == 0 || ve.Errors[0].Code != "rule_error" || !strings.Contains(ve.Errors[0].Message, "tidak bisa diperiksa") {
		t.Fatalf("rule error = %v, want a rule_error issue", err)
	}
}
