package engine

import (
	"strings"
	"testing"
)

// An idempotency value is a key only when it is a non-empty text or a whole number, typed so 5 and "5" differ
// (audit kernel gelombang 1 F1, F2).
func TestFlowKey(t *testing.T) {
	long := strings.Repeat("x", 300)
	for _, c := range []struct {
		v    any
		want string
	}{
		{"INV-1", "s:INV-1"}, {int64(5), "i:5"}, {5.0, "i:5"}, {"5", "s:5"},
		{"", ""}, {nil, ""}, {[]any{}, ""}, {map[string]any{}, ""}, {1.5, ""}, {true, ""},
	} {
		if got := flowKey(c.v); got != c.want {
			t.Errorf("flowKey(%#v) = %q, want %q", c.v, got, c.want)
		}
	}
	h := flowKey(long)
	if !strings.HasPrefix(h, "h:") || len(h) != 66 || h == flowKey(strings.TrimPrefix(h, "h:")) || flowKey(long+"y") == h {
		t.Errorf("long key = %q", h)
	}
}
