package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPilotDownPaymentOldFieldNames: a catalogue from before the rename (dp_deduction, dpp_base instead of
// down_payment, dpp) still gets the DP over-deduction check.
func TestPilotDownPaymentOldFieldNames(t *testing.T) {
	apps := filepath.Join(t.TempDir(), "apps")
	err := filepath.WalkDir(pilotApps, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(pilotApps, path)
		dst := filepath.Join(apps, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if d.Name() == "sales_invoice.rk" {
			s := strings.ReplaceAll(string(b), "dpp", "dpp_base")
			s = strings.ReplaceAll(s, "name: down_payment,", "name: dp_deduction,")
			s = strings.ReplaceAll(s, "- down_payment}", "- dp_deduction}")
			if !strings.Contains(s, "name: dp_deduction,") || !strings.Contains(s, "name: dpp_base,") {
				t.Fatalf("old names not restored:\n%s", s)
			}
			b = []byte(s)
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	downPaymentAndRetention(t, setupPilotApps(t, apps), "dp_deduction")
}
