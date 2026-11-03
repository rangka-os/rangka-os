package backup

import "testing"

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		archive, binary string
		want            bool
	}{
		{"99.0.0", "0.4.0-dev", true},
		{"0.4.1", "0.4.0", true},
		{"0.5.0-alpha.1", "0.4.9", true},
		{"0.4.0", "0.4.0-dev", false},
		{"0.4.0-dev", "0.4.0", false},
		{"v0.4.0", "0.4.1", false},
		{"0.4.0", "0.10.0", false},
		{"99.0.0", "dev", false},
	} {
		if got := newer(c.archive, c.binary); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.archive, c.binary, got, c.want)
		}
	}
}
