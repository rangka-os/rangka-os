package engine

import (
	"context"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/cel"
)

const hourStatModel = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json
name: hour_stat
label: {id: Jam outlet}
naming: HS-{#####}
title_field: outlet
fields:
  - {name: outlet, type: select, label: {id: Outlet}, required: true, options: [{value: a, label: {id: Outlet A}}, {value: b, label: {id: Outlet B}}]}
  - {name: day, type: date, label: {id: Tanggal}, required: true}
  - {name: hour, type: int, label: {id: Jam}, required: true}
  - {name: net, type: currency, label: {id: Penjualan}}
  - {name: receipts, type: int, label: {id: Struk}}
`

const hourStatDash = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/dashboard.json
name: banding
label: {id: Banding}
widgets:
  - name: tabel
    type: table
    model: hour_stat
    period: day
    group_by: outlet
    compare: previous_period
    sort: -net
    columns:
      - {name: net, label: {id: Penjualan}, fn: sum, field: net}
      - {name: receipts, label: {id: Struk}, fn: sum, field: receipts}
      - {name: avg_ticket, label: {id: Rata-rata}, expr: "net / receipts"}
  - name: peta
    type: heatmap
    model: hour_stat
    period: day
    group_by: outlet
    series_by: hour
    series_range: [0, 23]
    aggregate: sum
    field: net
`

func TestDashboardTableAndHeatmap(t *testing.T) {
	files := map[string]string{"crm/models/hour_stat.rk": hourStatModel, "crm/dashboards/banding.rk": hourStatDash}
	e, owner := approvalSetupFiles(t, files, "")
	ctx := context.Background()
	today := e.Today()
	prev := cel.AddDays(today, -40) // outside the 30 day window, inside the one before it
	for _, r := range []map[string]any{
		{"outlet": "a", "day": today, "hour": 12, "net": 100000, "receipts": 4},
		{"outlet": "a", "day": today, "hour": 19, "net": 50000, "receipts": 2},
		{"outlet": "b", "day": today, "hour": 12, "net": 30000, "receipts": 3},
		{"outlet": "a", "day": prev, "hour": 12, "net": 75000, "receipts": 3},
		{"outlet": "b", "day": prev, "hour": 9, "net": 60000, "receipts": 6},
	} {
		if _, err := e.Create(ctx, owner, "hour_stat", r); err != nil {
			t.Fatal(err)
		}
	}
	d, _, err := e.Dashboard(ctx, owner, "banding", 30)
	if err != nil {
		t.Fatal(err)
	}
	tb := widgetOut(t, d, "tabel")
	rows := tb["rows"].([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["key"] != "a" { // sorted by -net: A (150000) before B (30000)
		t.Fatalf("table rows = %v", rows)
	}
	a := rows[0].(map[string]any)
	av := a["values"].(map[string]any)
	if av["net"] != 150000.0 || av["receipts"] != 6.0 || av["avg_ticket"] != 25000.0 {
		t.Errorf("outlet a values = %v", av)
	}
	if a["delta"].(map[string]any)["net"] != "100%" || a["trend"].(map[string]any)["net"] != "up" { // 75000 -> 150000
		t.Errorf("outlet a delta = %v trend = %v", a["delta"], a["trend"])
	}
	b := rows[1].(map[string]any)
	if b["delta"].(map[string]any)["net"] != "50%" || b["trend"].(map[string]any)["net"] != "down" { // 60000 -> 30000
		t.Errorf("outlet b delta = %v", b["delta"])
	}
	tot := tb["total"].(map[string]any)["values"].(map[string]any)
	if tot["net"] != 180000.0 || tot["receipts"] != 9.0 || tot["avg_ticket"] != 20000.0 { // average over all, not summed
		t.Errorf("total = %v", tot)
	}
	cols := tb["columns"].([]any)
	if cols[0].(map[string]any)["format"] != "currency" || cols[1].(map[string]any)["format"] != "number" {
		t.Errorf("column formats = %v", cols)
	}

	hm := widgetOut(t, d, "peta")
	if len(hm["cols"].([]any)) != 24 || len(hm["rows"].([]any)) != 2 {
		t.Fatalf("heatmap shape = %d cols x %d rows", len(hm["cols"].([]any)), len(hm["rows"].([]any)))
	}
	va := hm["values"].([]any)[0].([]any)
	if va[12] != 100000.0 || va[19] != 50000.0 || va[3] != nil {
		t.Errorf("heatmap outlet a: 12h=%v 19h=%v 3h=%v", va[12], va[19], va[3])
	}
	if hm["max"] != 100000.0 {
		t.Errorf("heatmap max = %v", hm["max"])
	}
}

// A change that rounds to 0% is flat, never a red or green arrow.
func TestTableDeltaRoundsToFlat(t *testing.T) {
	trend, delta, ok := tableDelta(34076.0, 34090.0)
	if !ok || trend != "flat" || delta != "0%" {
		t.Errorf("got %q %v %v; want flat 0%%", trend, delta, ok)
	}
	if trend, delta, _ := tableDelta(150.0, 100.0); trend != "up" || delta != "50%" {
		t.Errorf("got %q %v; want up 50%%", trend, delta)
	}
}
