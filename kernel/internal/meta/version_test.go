package meta

import "testing"

func TestSatisfies(t *testing.T) {
	cases := []struct {
		v, r string
		ok   bool
	}{{"1.2.0", "^1.0", true}, {"2.0.0", "^1.0", false}, {"1.0.0", "^1.2", false}, {"1.2.0", ">=1.2", true},
		{"1.1.9", ">=1.2", false}, {"0.1.0", "^0.1", true}, {"1.0.0", "1.0.0", true}, {"x", "^1.0", true},
		{"1.0.0", "^2.0 || ^1.0", true}, {"1.0.0-beta", "^2.0", true}, {"١.٠.٠", "^2.0", true}}
	for _, c := range cases {
		if Satisfies(c.v, c.r) != c.ok {
			t.Errorf("Satisfies(%s, %s) != %v", c.v, c.r, c.ok)
		}
	}
}
