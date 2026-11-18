package api

import "testing"

func TestParseHostConnectors(t *testing.T) {
	got, err := ParseHostConnectors(" rangka_pay, rangka_wa ,,")
	if err != nil || len(got) != 2 || !got["rangka_pay"] || !got["rangka_wa"] {
		t.Fatalf("ParseHostConnectors: %v %v", got, err)
	}
	if got, err := ParseHostConnectors(""); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
	for _, bad := range []string{"x_pay", "Rangka", "pay-gw", "1pay"} {
		if _, err := ParseHostConnectors(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
