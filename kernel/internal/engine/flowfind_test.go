package engine

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const findProbeFlow = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: find_probe
label: {id: Uji find}
on: deal.updated
steps:
  - {find_all: deal, where: "customer == event.customer && notes == 'x'", as: all}
  - {find: deal, where: "customer == event.customer", as: newest}
  - update: newest
    set: {notes: "'newest'"}
  - {find_all: deal, where: "customer in event.custs && notes == 'x' && value > 0", limit: 3, as: three}
  - {find_all: deal, where: "customer == event.customer && notes == 'x'", sort: "-value", limit: 1, as: top}
  - {find: deal, where: "size(top) == 1 && name == top[0].name", as: t1}
  - update: t1
    set: {notes: "'top'"}
  - {find_all: deal, where: "docstatus == 0 && customer == event.missing", as: none}
`

// t_451714a9: a flow find reads only the rows its where: can match (SQL), in chunks, and stops at its first match or
// at limit:; what it finds is unchanged (newest first, sort:, the whole where: still checked).
func TestFlowFindPushdown(t *testing.T) {
	e, a := approvalSetupFiles(t, map[string]string{"crm/flows/find_probe.rk": findProbeFlow}, "")
	ctx := context.Background()
	var names []string
	for i := 0; i < 230; i++ {
		res, err := e.Create(ctx, a, "deal", map[string]any{"deal_name": fmt.Sprintf("F%03d", i), "customer": "CUST-0012", "value": i + 1, "notes": "x", "expected_close": "2026-12-01"})
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, str(res.Record["name"]))
	}
	for i := 0; i < 5; i++ {
		if _, err := e.Create(ctx, a, "deal", map[string]any{"deal_name": fmt.Sprintf("G%d", i), "customer": "CUST-0031", "value": 5, "notes": "x", "expected_close": "2026-12-01"}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := e.RunFlow(ctx, a, "find_probe", map[string]any{"customer": "CUST-0012", "custs": []any{"CUST-0012", "CUST-0031"}, "missing": "CUST-9999"})
	if err != nil {
		t.Fatal(err)
	}
	steps := strings.Join(res.Steps, " | ")
	for _, want := range []string{"find_all deal → 230", "find deal → 1", "find_all deal → 3", "find_all deal → 1", "find_all deal → 0"} {
		if !strings.Contains(steps, want) {
			t.Fatalf("steps %q lack %q", steps, want)
		}
	}
	newest, _ := e.Get(ctx, a, "deal", names[len(names)-1])
	top, _ := e.Get(ctx, a, "deal", names[len(names)-2])
	if str(newest["notes"]) != "newest" || str(top["notes"]) != "top" {
		t.Fatalf("newest %v, top %v", newest["notes"], top["notes"])
	}

	m, _ := e.model("deal")
	cases := []struct {
		where string
		vars  map[string]any
		conds []string
		args  []any
	}{
		{"customer == event.customer && notes == 'x'", map[string]any{"event": map[string]any{"customer": "CUST-0012"}},
			[]string{`"customer" = $2`, `"notes" = $3`}, []any{"CUST-0012", "x"}},
		{"event.customer == customer", map[string]any{"event": map[string]any{"customer": "CUST-0012"}}, []string{`"customer" = $2`}, []any{"CUST-0012"}},
		{"name == d.name", map[string]any{"d": map[string]any{"name": "DEAL-1"}}, []string{`"name" = $2`}, []any{"DEAL-1"}},
		// not pushed: != on text, an empty value, a value read from the record, a shadowed name, a computed field,
		// a non-text column, a macro, an ||
		{"customer != 'CUST-0012'", nil, nil, nil},
		{"customer == event.customer", map[string]any{"event": map[string]any{"customer": ""}}, nil, nil},
		{"customer == notes", nil, nil, nil},
		{"name == 'X'", map[string]any{"name": "Y"}, nil, nil},
		{"weighted_value == '5'", nil, nil, nil},
		{"value == 5", nil, nil, nil},
		{"customer == 'A' || customer == 'B'", nil, nil, nil},
		{"[1].exists(x, customer == 'A')", nil, nil, nil},
		{"customer in ['A', 1]", nil, nil, nil},
		{"notes in ['x', '']", nil, nil, nil},
		{"customer == f.customer", map[string]any{"f": nil}, nil, nil},
	}
	err = e.run(ctx, a, func(o *op) error {
		for _, c := range cases {
			conds, args := flowFindSQL(o, m, c.where, c.vars)
			if len(conds) == 0 && len(c.conds) == 0 {
				continue
			}
			if !reflect.DeepEqual(conds, c.conds) || !reflect.DeepEqual(args, c.args) {
				t.Errorf("%s: %v %v, want %v %v", c.where, conds, args, c.conds, c.args)
			}
		}
		conds, _ := flowFindSQL(o, m, "customer in ['A', 'B']", nil)
		if len(conds) != 1 || conds[0] != `"customer" = any($2)` {
			t.Errorf("in list: %v", conds)
		}
		// docstatus of a submittable model, == and !=; a flow variable named docstatus is not the column
		inv, _ := e.model("sales_invoice")
		conds, args := flowFindSQL(o, inv, "docstatus == 1 && customer == c && docstatus != 2", map[string]any{"c": "CUST-0012"})
		if !reflect.DeepEqual(conds, []string{"docstatus = $2", `"customer" = $3`, "docstatus <> $4"}) || !reflect.DeepEqual(args, []any{int64(1), "CUST-0012", int64(2)}) {
			t.Errorf("docstatus: %v %v", conds, args)
		}
		if conds, _ := flowFindSQL(o, inv, "docstatus == 1", map[string]any{"docstatus": 0}); len(conds) != 0 {
			t.Errorf("shadowed docstatus: %v", conds)
		}
		if conds, _ := flowFindSQL(o, m, "docstatus == 1", nil); len(conds) != 0 {
			t.Errorf("docstatus of a model that is not submittable: %v", conds)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
