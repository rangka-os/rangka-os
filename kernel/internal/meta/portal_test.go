package meta

import "testing"

// A portal print reads related lists as the kernel, so each must be matched on the document's name (contract 0073 §2).
func TestPortalPrintAnchored(t *testing.T) {
	for _, c := range []struct {
		rel  map[string]Related
		want bool
	}{
		{nil, true},
		{map[string]Related{"cara_bayar": {Model: "cb", Match: map[string]string{"faktur": "name"}}}, true},
		{map[string]Related{"sehari": {Model: "faktur", Range: map[string][2]string{"posting_date": {"posting_date", "posting_date"}}}}, false},
		{map[string]Related{"lain": {Model: "faktur", Match: map[string]string{"customer": "customer"}}}, false},
		{map[string]Related{"a": {Match: map[string]string{"x": "name"}}, "b": {Match: map[string]string{"y": "customer"}}}, false},
	} {
		if got := PortalPrintAnchored(&Print{Related: c.rel}); got != c.want {
			t.Errorf("%v = %v, want %v", c.rel, got, c.want)
		}
	}
}
