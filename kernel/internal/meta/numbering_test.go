package meta

import (
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

func TestModelNamingMapAndString(t *testing.T) {
	m := parseModel("x", &rk.Doc{Data: map[string]any{"name": "credit_note",
		"naming": map[string]any{"series": "invoice", "pattern": "CN-{####}", "reset": "never"}}})
	if m.Naming != "" || m.NamingSeries != "invoice" || m.NamingPattern != "CN-{####}" || m.NamingReset != "never" {
		t.Fatalf("map naming parsed as %q %q %q %q", m.Naming, m.NamingSeries, m.NamingPattern, m.NamingReset)
	}
	m = parseModel("x", &rk.Doc{Data: map[string]any{"name": "lead", "naming": "LEAD-{#####}"}})
	if m.Naming != "LEAD-{#####}" || m.NamingSeries != "" || m.NamingPattern != "" {
		t.Fatalf("string naming parsed as %q %q %q", m.Naming, m.NamingSeries, m.NamingPattern)
	}
}
