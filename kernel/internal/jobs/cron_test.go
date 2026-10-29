package jobs

import (
	"testing"
	"time"
)

func TestCronNext(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Jakarta")
	c, err := ParseCron("0 8 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"2026-10-30T10:18:00+07:00": "2026-11-02T08:00:00+07:00", // Friday after 8 → Monday
		"2026-10-30T07:59:30+07:00": "2026-10-30T08:00:00+07:00",
		"2026-10-30T08:00:00+07:00": "2026-11-02T08:00:00+07:00",
	}
	for from, want := range cases {
		f, _ := time.Parse(time.RFC3339, from)
		if got := c.Next(f.In(loc)).Format(time.RFC3339); got != want {
			t.Errorf("Next(%s) = %s, want %s", from, got, want)
		}
	}
	for _, spec := range []string{"*/15 * * * *", "@daily", "0 0 1,15 * *", "30 9 * 1-6 0,6"} {
		if _, err := ParseCron(spec); err != nil {
			t.Errorf("%s: %v", spec, err)
		}
	}
	for _, bad := range []string{"* * *", "61 * * * *", "a b c d e"} {
		if _, err := ParseCron(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}
