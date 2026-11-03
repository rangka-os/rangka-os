package engine

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// whole-token field names (dpp, not ppn_dpp or dpp_x)
var (
	dppToken         = regexp.MustCompile(`\bdpp\b`)
	downPaymentToken = regexp.MustCompile(`\bdown_payment\b`)
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
			lines := strings.Split(string(b), "\n")
			for i, l := range lines {
				l = dppToken.ReplaceAllString(l, "dpp_base")
				if !strings.Contains(l, "name: kind,") { // kind keeps its down_payment option
					l = downPaymentToken.ReplaceAllString(l, "dp_deduction")
				}
				lines[i] = l
			}
			s := strings.Join(lines, "\n")
			if !strings.Contains(s, "name: dp_deduction,") || !strings.Contains(s, "name: dpp_base,") || !strings.Contains(s, "value: down_payment") ||
				dppToken.MatchString(s) {
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
