package theme

import (
	"bytes"
	"os"
	"testing"
)

func TestTokensInSync(t *testing.T) {
	b, err := os.ReadFile("../../../web/design-system/tokens.json")
	if err != nil {
		t.Skip("design system not found")
	}
	if !bytes.Equal(b, tokensJSON) {
		t.Error("internal/theme/tokens.json differs from web/design-system/tokens.json; run make sync-schemas")
	}
}

func TestDerive(t *testing.T) {
	_, dark := Base()
	// same values as rangka_validate.py theme_colours()
	for name, want := range map[string]string{"brand:#9a5b2e": "#c9804b", "chart-1:#0c66e4": "#4790f5", "selected:#e9f2ff": "#003583"} {
		n, l := name[:len(name)-8], name[len(name)-7:]
		if got := DeriveDark(n, l, dark); got != want {
			t.Errorf("%s → %s, want %s", name, got, want)
		}
	}
}
