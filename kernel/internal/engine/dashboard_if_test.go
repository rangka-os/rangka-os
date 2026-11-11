package engine

import (
	"context"
	"testing"
)

// A widget guarded by if: app_installed() is hidden, not an error, when its app (and model) is not installed:
// one dashboard can total sales from the cashier app where it is installed and from invoices elsewhere (cards
// t_e5635594, t_299dd306; schema dashboard.json "optional apps: guard with app_installed()").
func TestDashboardWidgetIfBeforeModel(t *testing.T) {
	files := map[string]string{"crm/dashboards/gabungan.rk": `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/dashboard.json
name: gabungan
label: {id: Penjualan gabungan}
widgets:
  - {name: faktur, type: number, label: {id: Faktur}, model: sales_invoice, aggregate: count, span: 3, if: "app_installed('sales')"}
  - {name: prospek, type: number, label: {id: Prospek}, model: lead, aggregate: count, span: 3}
`}
	e, owner := approvalSetupFiles(t, files, "")
	ctx := context.Background()
	d, _, err := e.Dashboard(ctx, owner, "gabungan", 0)
	if err != nil {
		t.Fatal(err)
	}
	if w := widgetOut(t, d, "faktur"); w["error"] != nil || w["hidden"] == true {
		t.Fatalf("installed app: faktur = %v, want a value", w)
	}
	e.Reg().Installed["sales"] = false // the site without the optional app
	d, _, err = e.Dashboard(ctx, owner, "gabungan", 0)
	if err != nil {
		t.Fatal(err)
	}
	if w := widgetOut(t, d, "faktur"); w["hidden"] != true || w["error"] != nil {
		t.Fatalf("app not installed: faktur = %v, want hidden without error", w)
	}
	if w := widgetOut(t, d, "prospek"); w["error"] != nil || w["hidden"] == true {
		t.Fatalf("prospek = %v, want a value", w)
	}
}

func widgetOut(t *testing.T, d map[string]any, name string) map[string]any {
	t.Helper()
	for _, x := range d["widgets"].([]any) {
		if w, _ := x.(map[string]any); w != nil && w["name"] == name {
			return w
		}
	}
	t.Fatalf("widget %s missing: %v", name, d["widgets"])
	return nil
}
