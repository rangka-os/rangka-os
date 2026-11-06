package engine

import (
	"context"
	"errors"
	"testing"
)

// crew carries pay hidden from sales reps (contract 0027); crew_pay is a dashboard only managers open.
var visibilityFiles = map[string]string{
	"crm/models/crew.rk": `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json
name: crew
label: {id: Kru}
naming: CRW-{####}
title_field: crew_name
fields:
  - {name: crew_name, type: data, label: {id: Nama}, in_list: true}
  - {name: pay, type: currency, label: {id: Upah}, in_list: true, hidden_for: [sales_rep]}
  - {name: pay_note, type: data, label: {id: Catatan upah}, hidden_for: [sales_rep]}
permissions:
  - {role: sales_rep, read: true, create: true, write: true}
  - {role: sales_manager, read: true, create: true, write: true}
`,
	"crm/dashboards/crew_overview.rk": `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/dashboard.json
name: crew_overview
label: {id: Kru}
widgets:
  - {name: crew_count, type: number, label: {id: Jumlah kru}, model: crew, aggregate: count, span: 3}
  - {name: crew_pay, type: number, label: {id: Total upah}, model: crew, aggregate: sum, field: pay, span: 3}
  - {name: crew_list, type: list, label: {id: Daftar}, model: crew, columns: [crew_name], span: 6}
`,
	"crm/dashboards/crew_pay.rk": `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/dashboard.json
name: crew_pay
label: {id: Upah kru}
roles: [sales_manager]
widgets:
  - {name: crew_count, type: number, label: {id: Jumlah kru}, model: crew, aggregate: count, span: 3}
`,
}

func TestHiddenForAndDashboardRoles(t *testing.T) {
	e, owner := approvalSetupFiles(t, visibilityFiles, "")
	ctx := context.Background()
	rep := Actor{Org: owner.Org, ID: "rep@example.test", UserName: "USR-801", Roles: []string{"sales_rep"}, Human: true}
	both := Actor{Org: owner.Org, ID: "lead@example.test", UserName: "USR-802", Roles: []string{"sales_rep", "sales_manager"}, Human: true}
	var names []string
	for i, pay := range []int{300000, 100000, 200000} {
		res, err := e.Create(ctx, owner, "crew", map[string]any{"crew_name": []string{"Budi", "Citra", "Dani"}[i], "pay": pay, "pay_note": "rahasia"})
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, str(res.Record["name"]))
	}

	// Get: the rep does not see pay; the owner and someone with another role do
	r, err := e.Get(ctx, rep, "crew", names[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r["pay"]; ok || r["pay_note"] != nil || r["pay_idr"] != nil {
		t.Fatalf("rep sees hidden fields: %v", r)
	}
	if r, _ := e.Get(ctx, owner, "crew", names[0]); r["pay"] == nil || r["pay_note"] != "rahasia" {
		t.Fatalf("owner must see pay: %v", r)
	}
	if r, _ := e.Get(ctx, both, "crew", names[0]); r["pay"] == nil {
		t.Fatalf("a role that may see the field is enough: %v", r)
	}

	// List: no hidden values; sort by pay is ignored; q and filter cannot test hidden fields
	rows, _, err := e.List(ctx, rep, "crew", ListOpts{Sort: "pay", Limit: 10})
	if err != nil || len(rows) != 3 {
		t.Fatalf("rep list: %d %v", len(rows), err)
	}
	for _, x := range rows {
		if _, ok := x["pay"]; ok {
			t.Fatalf("list row has pay: %v", x)
		}
	}
	if sorted, _, _ := e.List(ctx, owner, "crew", ListOpts{Sort: "pay", Limit: 10}); str(sorted[0]["crew_name"]) != "Citra" {
		t.Fatalf("owner sort by pay: %v", sorted[0])
	}
	if str(rows[0]["crew_name"]) == "Citra" && str(rows[1]["crew_name"]) == "Dani" {
		t.Fatal("rep list came back in pay order: the sort was applied")
	}
	if n, _, _ := e.List(ctx, rep, "crew", ListOpts{Q: "rahasia", Limit: 10}); len(n) != 0 {
		t.Fatalf("q matched a hidden field: %d", len(n))
	}
	if n, _, _ := e.List(ctx, rep, "crew", ListOpts{Filter: "pay > 150000", Limit: 10}); len(n) != 0 {
		t.Fatalf("filter tested a hidden field: %d rows", len(n))
	}
	if n, _, _ := e.List(ctx, owner, "crew", ListOpts{Filter: "pay > 150000", Limit: 10}); len(n) != 2 {
		t.Fatalf("owner filter: %d rows", len(n))
	}

	// Aggregate over a hidden field is refused
	var se *StatusError
	if _, err := e.Aggregate(ctx, rep, "crew", ListOpts{}, "", []AggFn{{Kind: "sum", Field: "pay"}}); !errors.As(err, &se) || se.Code != "field_hidden" {
		t.Fatalf("rep sum pay = %v", err)
	}
	if _, err := e.Aggregate(ctx, rep, "crew", ListOpts{}, "pay_note", nil); !errors.As(err, &se) || se.Code != "field_hidden" {
		t.Fatalf("rep group by pay_note = %v", err)
	}

	// Writes: the rep cannot set a hidden field, and the result does not show it
	res, err := e.Update(ctx, rep, "crew", names[0], map[string]any{"crew_name": "Budi S", "pay": 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.Record["pay"]; ok {
		t.Fatalf("write result shows pay to the rep: %v", res.Record)
	}
	if r, _ := e.Get(ctx, owner, "crew", names[0]); str(r["crew_name"]) != "Budi S" || toInt(r["pay"]) == 1 {
		t.Fatalf("rep changed a hidden field: %v", r)
	}

	// Dashboards: roles: opens only for its roles; a widget over a hidden field is hidden
	var pe *PermissionError
	if _, _, err := e.Dashboard(ctx, rep, "crew_pay", 0); !errors.As(err, &pe) {
		t.Fatalf("rep opens a sales_manager dashboard: %v", err)
	}
	if _, _, err := e.Dashboard(ctx, both, "crew_pay", 0); err != nil {
		t.Fatalf("manager: %v", err)
	}
	d, _, err := e.Dashboard(ctx, rep, "crew_overview", 0)
	if err != nil {
		t.Fatal(err)
	}
	ws := d["widgets"].([]any)
	if ws[1].(map[string]any)["hidden"] != true {
		t.Fatalf("pay widget shown to the rep: %v", ws[1])
	}
	if ws[0].(map[string]any)["hidden"] == true {
		t.Fatalf("count widget hidden: %v", ws[0])
	}
	for _, x := range ws[2].(map[string]any)["rows"].([]any) {
		if _, ok := x.(map[string]any)["pay"]; ok {
			t.Fatalf("list widget row shows pay: %v", x)
		}
	}
	if tw := e.Reg().Model("crew").Field("pay_idr"); tw != nil && !e.FieldHidden(rep, e.Reg().Model("crew"), tw) {
		t.Fatal("the IDR twin follows its money field")
	}
}

// Audit 2026-11-07: hidden fields of table rows survive a save by someone who cannot see them, an import does
// not set them, and a filter cannot test them through rows or option().
func TestHiddenForRowsImportsAndFilters(t *testing.T) {
	files := map[string]string{
		"crm/models/crew_job.rk": `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json
name: crew_job
label: {id: Pekerjaan kru}
naming: CJ-{####}
fields:
  - {name: title, type: data, label: {id: Judul}}
  - name: grade
    type: select
    label: {id: Kelas}
    hidden_for: [sales_rep]
    options:
      - {value: a, label: {id: A}, probability: 90}
      - {value: b, label: {id: B}, probability: 10}
  - {name: lines, type: table, of: crew_job_line, label: {id: Baris}}
permissions:
  - {role: sales_rep, read: true, create: true, write: true, import: true}
`,
		"crm/models/crew_job_line.rk": `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json
name: crew_job_line
label: {id: Baris pekerjaan}
is_child: true
fields:
  - {name: what, type: data, label: {id: Uraian}}
  - {name: cost, type: currency, label: {id: Biaya}, hidden_for: [sales_rep]}
`,
	}
	e, owner := approvalSetupFiles(t, files, "")
	ctx := context.Background()
	rep := Actor{Org: owner.Org, ID: "rep@example.test", UserName: "USR-801", Roles: []string{"sales_rep"}, Human: true}
	res, err := e.Create(ctx, owner, "crew_job", map[string]any{"title": "Pasang", "grade": "a", "lines": []any{map[string]any{"what": "kabel", "cost": 777}}})
	if err != nil {
		t.Fatal(err)
	}
	name := str(res.Record["name"])

	// the rep opens the job and saves it back with a changed description: the stored cost stays
	got, err := e.Get(ctx, rep, "crew_job", name)
	if err != nil {
		t.Fatal(err)
	}
	ls := got["lines"].([]any)
	if _, ok := ls[0].(map[string]any)["cost"]; ok {
		t.Fatalf("rep sees the row cost: %v", ls[0])
	}
	ls[0].(map[string]any)["what"] = "kabel 2"
	if _, err := e.Update(ctx, rep, "crew_job", name, map[string]any{"lines": ls}); err != nil {
		t.Fatal(err)
	}
	full, _ := e.Get(ctx, owner, "crew_job", name)
	row := full["lines"].([]any)[0].(map[string]any)
	if str(row["what"]) != "kabel 2" || toInt(row["cost"]) != 777 {
		t.Fatalf("after the rep's save: %v", row)
	}

	// filters through rows and option() read hidden values as zero/none
	for _, f := range []string{"lines.exists(l, l.cost == 777)", "option('grade', 'probability') == 90"} {
		if n, _, _ := e.List(ctx, rep, "crew_job", ListOpts{Filter: f, Limit: 10}); len(n) != 0 {
			t.Fatalf("rep filter %q matched %d rows", f, len(n))
		}
		if n, _, _ := e.List(ctx, owner, "crew_job", ListOpts{Filter: f, Limit: 10}); len(n) != 1 {
			t.Fatalf("owner filter %q matched %d rows", f, len(n))
		}
	}

	// an import by the rep does not set hidden fields
	mr, err := e.CreateMany(ctx, rep, "crew_job", []map[string]any{{"title": "Impor", "grade": "b", "lines": []any{map[string]any{"what": "x", "cost": 5}}}}, ManyOpts{})
	if err != nil || mr[0].Err != nil {
		t.Fatalf("import: %v %v", err, mr)
	}
	imp, _ := e.Get(ctx, owner, "crew_job", mr[0].Name)
	if str(imp["grade"]) == "b" || toInt(imp["lines"].([]any)[0].(map[string]any)["cost"]) == 5 {
		t.Fatalf("import set hidden fields: %v", imp)
	}
}
