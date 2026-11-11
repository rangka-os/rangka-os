package cel

import "testing"

// epoch() and timestamp() turn a poll cursor kept as a datetime into a vendor's since in Unix seconds and back.
func TestEpochTimestamp(t *testing.T) {
	e := env(map[string]any{"cursor": "2026-11-10T09:00:00Z", "none": nil, "empty": "", "wib": "2026-11-10T16:00:00+07:00", "secs": int64(1794301200)})
	cases := []struct {
		src  string
		want any
	}{
		{"epoch(cursor)", int64(1794301200)},
		{"epoch(wib)", int64(1794301200)},
		{"epoch('2026-11-10T09:00:00.250Z')", int64(1794301200)},
		{"epoch('2026-11-10 09:00:00')", int64(1794301200)},
		{"epoch('2026-11-10')", int64(1794268800)},
		{"epoch('1970-01-01T00:00:00Z')", int64(0)},
		{"epoch(none)", nil},
		{"epoch(empty)", nil},
		{"timestamp(secs)", "2026-11-10T09:00:00Z"},
		{"timestamp(1794301200.9)", "2026-11-10T09:00:00Z"},
		{"timestamp('1794301200')", "2026-11-10T09:00:00Z"},
		{"timestamp(0)", "1970-01-01T00:00:00Z"},
		{"timestamp(none)", nil},
		{"timestamp(empty)", nil},
		{"timestamp(epoch(wib)) == cursor", true},
		{"cursor != null ? epoch(cursor) - 600 : 0", int64(1794300600)},
	}
	for _, c := range cases {
		got, err := Eval(c.src, e)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if Unwrap(got) != c.want {
			t.Errorf("%s = %#v, want %#v", c.src, got, c.want)
		}
	}
	for _, bad := range []string{"epoch('kemarin')", "epoch(12)", "epoch()", "timestamp('x')", "timestamp(true)", "timestamp(1e20)", "timestamp(1, 2)"} {
		if _, err := Eval(bad, e); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}
