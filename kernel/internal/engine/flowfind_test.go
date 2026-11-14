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
  - {find_all: deal, where: "expected_close >= '2026-12-01' && expected_close < event.next && '2026-12-01' == expected_close", as: dated}
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
	res, err := e.RunFlow(ctx, a, "find_probe", map[string]any{"customer": "CUST-0012", "custs": []any{"CUST-0012", "CUST-0031"}, "missing": "CUST-9999", "next": "2026-12-02"})
	if err != nil {
		t.Fatal(err)
	}
	steps := strings.Join(res.Steps, " | ")
	for _, want := range []string{"find_all deal → 230", "find deal → 1", "find_all deal → 3", "find_all deal → 1", "find_all deal → 0", "find_all deal → 236"} { // 236: the 235 created plus the fixture deal closing 2026-12-01
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
		// t_0dba5917: a date column compared to a YYYY-MM-DD text, field on the left (== also flipped)
		{"expected_close == '2026-12-01'", nil, []string{`"expected_close" = $2::date`}, []any{"2026-12-01"}},
		{"'2026-12-01' == expected_close", nil, []string{`"expected_close" = $2::date`}, []any{"2026-12-01"}},
		{"expected_close >= d && expected_close < e", map[string]any{"d": "2026-11-01", "e": "2026-12-01"},
			[]string{`"expected_close" >= $2::date`, `("expected_close" < $3::date or "expected_close" is null)`}, []any{"2026-11-01", "2026-12-01"}},
		{"expected_close <= '2026-12-01' && expected_close > '2026-11-30'", nil,
			[]string{`("expected_close" <= $2::date or "expected_close" is null)`, `"expected_close" > $3::date`}, []any{"2026-12-01", "2026-11-30"}},
		// not pushed: an ordering with the field on the right, !=, an empty or partial or timestamp value
		{"'2026-12-01' < expected_close", nil, nil, nil},
		{"expected_close != '2026-12-01'", nil, nil, nil},
		{"expected_close == ''", nil, nil, nil},
		{"expected_close < '2026-12'", nil, nil, nil},
		{"expected_close < '2026-12-01T00:00:00Z'", nil, nil, nil},
		{"expected_close < 5", nil, nil, nil},
		{"expected_close < '0000-01-01'", nil, nil, nil},
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

const findDateProbeFlow = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: find_date_probe
label: {id: Uji find tanggal}
on: deal.updated
steps:
  - {find_all: deal, where: "notes == 'k1probe' && expected_close < '2026-12-01'", as: lt}
  - {find_all: deal, where: "notes == 'k1probe' && [1].exists(x, expected_close < '2026-12-01')", as: lt_cel}
  - {find_all: deal, where: "notes == 'k1probe' && expected_close <= '2026-12-01'", as: le}
  - {find_all: deal, where: "notes == 'k1probe' && [1].exists(x, expected_close <= '2026-12-01')", as: le_cel}
  - {find_all: deal, where: "notes == 'k1probe' && expected_close > '2026-01-01'", as: gt}
  - {find_all: deal, where: "notes == 'k1probe' && [1].exists(x, expected_close > '2026-01-01')", as: gt_cel}
`

// Audit t_0dba5917 K1: an empty date is NULL in SQL but "" in CEL, which sorts before any date; a pushed < or <=
// finds the same rows as CEL alone (the empty ones too), and > the same as well.
func TestFlowFindDateEmptyMatchesCEL(t *testing.T) {
	e, a := approvalSetupFiles(t, map[string]string{"crm/flows/find_date_probe.rk": findDateProbeFlow}, "")
	ctx := context.Background()
	for i, d := range []string{"", "", "2026-11-15", "2026-12-15"} {
		in := map[string]any{"deal_name": fmt.Sprintf("D%d", i), "customer": "CUST-0012", "value": 1, "notes": "k1probe"}
		if d != "" {
			in["expected_close"] = d
		} else {
			in["stage"], in["lost_reason"] = "lost", "uji" // the deal rule needs a close date unless won or lost
		}
		if _, err := e.Create(ctx, a, "deal", in); err != nil {
			t.Fatal(err)
		}
	}
	res, err := e.RunFlow(ctx, a, "find_date_probe", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]string{}
	for _, s := range res.Steps {
		if i := strings.Index(s, "→ "); i > 0 {
			counts[strings.SplitN(s, ":", 2)[0]] = s[i+len("→ "):]
		}
	}
	for _, p := range [][2]string{{"1", "2"}, {"3", "4"}, {"5", "6"}} {
		if counts[p[0]] != counts[p[1]] {
			t.Errorf("pushed step %s found %s, CEL alone (step %s) %s: %v", p[0], counts[p[0]], p[1], counts[p[1]], res.Steps)
		}
	}
	if counts["1"] != "3" || counts["5"] != "2" {
		t.Errorf("counts %v, want < 3 (two empty + 2026-11-15) and > 2", counts)
	}
}
