package cel

import "testing"

func TestAddMonthsDaysBetween(t *testing.T) {
	e := env(map[string]any{"start": "2026-01-31", "empty": "", "ts": "2026-11-08T10:00:00+07:00"})
	cases := []struct {
		src  string
		want any
	}{
		{"add_months('2026-11-08', 1)", "2026-12-08"},
		{"add_months('2026-11-08', 2)", "2027-01-08"},
		{"add_months(start, 1)", "2026-02-28"},
		{"add_months('2028-01-31', 1)", "2028-02-29"},
		{"add_months(start, 3)", "2026-04-30"},
		{"add_months(start, 12)", "2027-01-31"},
		{"add_months('2026-03-31', -1)", "2026-02-28"},
		{"add_months(start, 0)", "2026-01-31"},
		{"add_months(empty, 1)", ""},
		{"days_between('1900-01-01', '2300-01-01')", int64(146097)},
		{"add_months('2026-01-31', 12000)", "3026-01-31"},
		{"days_between('2026-11-01', '2026-12-01')", int64(30)},
		{"days_between('2026-02-01', '2026-03-01')", int64(28)},
		{"days_between('2026-11-15', '2026-11-01')", int64(-14)},
		{"days_between(empty, '2026-11-01')", int64(0)},
		{"days_between('2026-03-01', '2026-04-01')", int64(31)}, // no DST in UTC dates
		{"add_months(today(), 1) == '2026-11-30'", true},
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
	for _, bad := range []string{"add_months('2026-13-01', 1)", "add_months('x', 1)", "add_months('2026-11-01')", "add_months('2026-11-01', 'a')", "days_between(1, '2026-11-01')", "add_months(ts, 1)", "days_between(ts, '2026-11-09')", "add_months('2026-11-08xyz', 1)", "add_months('2026-11-08', 12001)", "add_months('2026-11-08', -9223372036854775807)"} {
		if _, err := Eval(bad, e); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
}
