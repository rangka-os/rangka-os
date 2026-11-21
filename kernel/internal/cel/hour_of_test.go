package cel

import (
	"testing"
	"time"
)

// TestHourWeekdayOf: hour_of and weekday_of read a datetime in the site's timezone (contract 0075 §3).
func TestHourWeekdayOf(t *testing.T) {
	jkt := time.FixedZone("WIB", 7*3600)
	for _, c := range []struct {
		expr string
		loc  *time.Location
		want any
	}{
		// 2026-11-20 is a Friday; 23:30 UTC is already Saturday 06:30 in WIB
		{"hour_of('2026-11-20T23:30:00Z')", jkt, int64(6)},
		{"weekday_of('2026-11-20T23:30:00Z')", jkt, int64(6)},
		{"hour_of('2026-11-20T23:30:00Z')", nil, int64(23)},
		{"weekday_of('2026-11-20T23:30:00Z')", nil, int64(5)},
		{"weekday_of('2026-11-22T10:00:00Z')", jkt, int64(7)}, // Sunday
		{"weekday_of('2026-11-23T00:00:00Z')", nil, int64(1)}, // Monday
		{"hour_of('2026-11-20T12:15:00+07:00')", jkt, int64(12)},
		{"weekday_of('2026-11-20')", jkt, int64(5)}, // a date keeps its own weekday
		{"hour_of('2026-11-20')", jkt, nil},         // a date has no hour
		{"hour_of('')", jkt, nil},
		{"weekday_of(null)", jkt, nil},
	} {
		got, err := Eval(c.expr, &Env{Loc: c.loc})
		if err != nil || got != c.want {
			t.Errorf("%s (loc %v) = %v, %v; want %v", c.expr, c.loc, got, err, c.want)
		}
	}
	for _, bad := range []string{"hour_of()", "hour_of(1, 2)", "weekday_of('soon')", "hour_of(12)"} {
		if _, err := Eval(bad, &Env{}); err == nil {
			t.Errorf("%s should fail", bad)
		}
	}
}
