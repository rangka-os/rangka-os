package api

import "testing"

// Contract 0024 §8: a workbook number cell only when Excel shows the same text back (audit C1-3 L1).
func TestNumberCell(t *testing.T) {
	for v, want := range map[string]bool{
		"12500000": true, "-5": true, "2.5": true, "0": true, "0.25": true,
		"1.10": false, "12345678901234.56": false, "1234567890123456": false, "0812345": false, "+62812": false,
		"9000000000000001": false, "1e5": false, "": false, "12.": false, "Rp 5": false,
	} {
		if _, got := numberCell(v); got != want {
			t.Errorf("numberCell(%q) = %v, want %v", v, got, want)
		}
	}
}
