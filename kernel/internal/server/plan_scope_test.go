package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scopedOutlets gives the canvas outlet a location type and declares limit_scope: {outlets: is_store} (contract
// 0057 §5) on a plan of 1 outlet.
func scopedOutlets(root string) {
	app := func(p, s string) {
		f, _ := os.OpenFile(filepath.Join(root, p), os.O_APPEND|os.O_WRONLY, 0o644)
		f.WriteString(s)
		f.Close()
	}
	app("modules/outlets/module.rk", "limit_scope: {outlets: is_store}\n")
	app("modules/outlets/models/outlet.rk", `  - name: location_type
    type: select
    label: {id: Jenis lokasi}
    default: "'store'"
    options: [{value: store, label: {id: Toko}}, {value: warehouse, label: {id: Gudang}}, {value: vehicle, label: {id: Mobil kanvas}}]
  - name: is_store
    type: check
    label: {id: Toko}
    hidden: true
    compute: "location_type == '' || location_type == 'store'"
`)
	app("site/features.rk", "    limits: {outlets: 1}\n")
}

func TestPlanLimitScope(t *testing.T) {
	e, _ := canvasEnv(t, scopedOutlets)
	// one store and three locations that are not stores: no refusal
	e.must(201, "POST", "/api/v1/r/outlet", map[string]any{"outlet_name": "Toko Kemang"})
	for _, n := range []string{"Gudang Utama", "Gudang Beku"} {
		e.must(201, "POST", "/api/v1/r/outlet", map[string]any{"outlet_name": n, "location_type": "warehouse"})
	}
	van := e.must(201, "POST", "/api/v1/r/outlet", map[string]any{"outlet_name": "Mobil Kanvas 1", "location_type": "vehicle"})
	refreshPlan(e)
	rep := e.must(200, "GET", "/api/v1/plan", nil).JSON
	if get(rep, "usage", "outlets", "used") != float64(1) || get(rep, "usage", "outlets", "state") != "full" {
		t.Fatalf("usage counts stores only %v", rep["usage"])
	}
	// a second store is refused
	r := e.must(403, "POST", "/api/v1/r/outlet", map[string]any{"outlet_name": "Toko Depok"})
	if errCode(r) != "plan_limit" || !strings.Contains(errMsg(r), "berlaku untuk 1 outlet") {
		t.Fatalf("second store: %s", r.Body)
	}
	// so is retyping a warehouse as a store; another change to it is not
	name := van.JSON["name"].(string)
	if r := e.must(403, "PATCH", "/api/v1/r/outlet/"+name, map[string]any{"location_type": "store"}); errCode(r) != "plan_limit" {
		t.Fatalf("retype to store: %s", r.Body)
	}
	e.must(200, "PATCH", "/api/v1/r/outlet/"+name, map[string]any{"city": "Bekasi"})
	if n := len(e.must(200, "GET", "/api/v1/r/outlet", nil).JSON["data"].([]any)); n != 4 {
		t.Fatalf("outlets after the refusals: %d", n)
	}
	// a downgrade to 0 outlets would make only stores read-only: warehouses keep working (contract 0028 §4)
	ro, _ := get(rep, "read_only", "outlets").([]any)
	if len(ro) != 0 {
		t.Fatalf("read-only %v", ro)
	}
}
