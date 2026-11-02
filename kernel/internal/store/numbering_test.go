package store

import (
	"testing"
	"time"
)

func jakarta(t *testing.T) time.Time {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatal(err)
	}
	return time.Date(2026, 11, 2, 9, 0, 0, 0, loc)
}

func TestRenderSeries(t *testing.T) {
	now := jakarta(t)
	cases := []struct {
		pattern, org, prefix, suffix string
		digits                       int
	}{
		{"INV/{ORG}/{YY}{MM}/{####}", "PTA", "INV/PTA/2611/", "", 4},
		{"{####}/DEAL/{YYYY}", "X", "", "/DEAL/2026", 4},
		{"A-{###}", "X", "A-", "", 3},
		{"NOCOUNTER-{YY}", "X", "NOCOUNTER-26", "", 4},
	}
	for _, c := range cases {
		p, s, d := RenderSeries(c.pattern, c.org, now)
		if p != c.prefix || s != c.suffix || d != c.digits {
			t.Errorf("RenderSeries(%q) = %q, %q, %d; want %q, %q, %d", c.pattern, p, s, d, c.prefix, c.suffix, c.digits)
		}
	}
}

func TestPeriod(t *testing.T) {
	now := jakarta(t)
	for reset, want := range map[string]string{"monthly": "2026-11", "yearly": "2026", "never": ""} {
		if got := Period(reset, now); got != want {
			t.Errorf("Period(%q) = %q; want %q", reset, got, want)
		}
	}
}

func TestDefaultReset(t *testing.T) {
	for p, want := range map[string]string{"A/{YY}{MM}/{##}": "monthly", "A/{YYYY}/{##}": "yearly", "A/{YY}/{##}": "yearly", "A-{###}": "never"} {
		if got := DefaultReset(p); got != want {
			t.Errorf("DefaultReset(%q) = %q; want %q", p, got, want)
		}
	}
}

func TestFormatNumber(t *testing.T) {
	cases := []struct {
		prefix, suffix string
		digits         int
		n              int64
		want           string
	}{
		{"INV/", "", 2, 7, "INV/07"},
		{"", "/D", 2, 100, "100/D"},
		{"A-", "", 3, 1000, "A-1000"},
	}
	for _, c := range cases {
		if got := FormatNumber(c.prefix, c.suffix, c.digits, c.n); got != c.want {
			t.Errorf("FormatNumber(%q,%q,%d,%d) = %q; want %q", c.prefix, c.suffix, c.digits, c.n, got, c.want)
		}
	}
}
