package server

import (
	"os"
	"path/filepath"
	"testing"
)

const lotModel = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/model.json\n" +
	"name: x_lot\nnaming: LOT-{####}\nlabel: {id: Lot}\ntitle_field: lot_name\nfields:\n" +
	"  - {name: lot_name, type: data, label: {id: Nama}, in_list: true}\n" +
	"  - {name: qty, type: float, label: {id: Qty}, in_list: true}\n" +
	"  - {name: value, type: currency, label: {id: Nilai}, in_list: true, hidden_for: [sales_rep]}\n" +
	"permissions:\n  - {role: sales_rep, read: true}\n"

const lotDashboard = "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/dashboard.json\n" +
	"name: lot_board\nlabel: {id: Lot}\nwidgets:\n" +
	"  - {name: qty_by_lot, type: chart, chart: bar, label: {id: Qty}, model: x_lot, aggregate: sum, field: qty, group_by: lot_name, sort: -value, span: 6}\n" +
	"  - {name: top_value, type: list, label: {id: Nilai}, model: x_lot, sort: -value, columns: [lot_name, qty], span: 6}\n"

// A chart's sort: -value orders its groups by their aggregate, not by a field called value: a chart of a visible field
// stays shown to a role from which a field named value is hidden, while a list sorted by that field is not (contract
// 0027 §5; audit hpp-stock-paths F4).
func TestDashboardChartSortIsNotAField(t *testing.T) {
	e := newEnv(t, false, func(apps, site string) {
		for p, c := range map[string]string{
			filepath.Join(apps, "crm", "models", "x_lot.rk"):         lotModel,
			filepath.Join(apps, "crm", "dashboards", "lot_board.rk"): lotDashboard,
		} {
			if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	})
	e.login()
	e.must(201, "POST", "/api/v1/r/x_lot", map[string]any{"lot_name": "A", "qty": 3, "value": 30000})
	rep := repEnv(t, e)
	ws := map[string]any{}
	for _, w := range rep.must(200, "GET", "/api/v1/dashboards/lot_board", nil).JSON["widgets"].([]any) {
		ws[get(w, "name").(string)] = w
	}
	if get(ws["qty_by_lot"], "hidden") == true {
		t.Fatalf("a chart of a visible field is hidden because its sort is -value: %v", ws["qty_by_lot"])
	}
	if get(ws["top_value"], "hidden") != true {
		t.Fatalf("a list sorted by a hidden field is shown: %v", ws["top_value"])
	}
}
