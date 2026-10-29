package jobs

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is a parsed 5-field schedule (minute hour day-of-month month day-of-week).
type Cron struct {
	min, hour, dom, month, dow [64]bool
	domStar, dowStar           bool
	Spec                       string
}

var macros = map[string]string{"@hourly": "0 * * * *", "@daily": "0 0 * * *", "@weekly": "0 0 * * 0", "@monthly": "0 0 1 * *", "@yearly": "0 0 1 1 *"}

// ParseCron parses "0 8 * * 1-5", lists, ranges, steps and @hourly/@daily/@weekly/@monthly/@yearly.
func ParseCron(spec string) (*Cron, error) {
	s := strings.TrimSpace(spec)
	if m, ok := macros[s]; ok {
		s = m
	}
	f := strings.Fields(s)
	if len(f) != 5 {
		return nil, fmt.Errorf("cron %q: need 5 fields", spec)
	}
	c := &Cron{Spec: spec, domStar: f[2] == "*", dowStar: f[4] == "*"}
	for i, def := range []struct {
		dst      *[64]bool
		min, max int
	}{{&c.min, 0, 59}, {&c.hour, 0, 23}, {&c.dom, 1, 31}, {&c.month, 1, 12}, {&c.dow, 0, 7}} {
		if err := parseField(f[i], def.min, def.max, def.dst); err != nil {
			return nil, fmt.Errorf("cron %q: %w", spec, err)
		}
	}
	if c.dow[7] {
		c.dow[0] = true
	}
	return c, nil
}

func parseField(s string, lo, hi int, dst *[64]bool) error {
	for _, part := range strings.Split(s, ",") {
		step := 1
		if base, st, ok := strings.Cut(part, "/"); ok {
			n, err := strconv.Atoi(st)
			if err != nil || n <= 0 {
				return fmt.Errorf("bad step %q", part)
			}
			step, part = n, base
		}
		a, b := lo, hi
		switch {
		case part == "*":
		case strings.Contains(part, "-"):
			x, y, _ := strings.Cut(part, "-")
			var err1, err2 error
			a, err1 = strconv.Atoi(x)
			b, err2 = strconv.Atoi(y)
			if err1 != nil || err2 != nil {
				return fmt.Errorf("bad range %q", part)
			}
		default:
			n, err := strconv.Atoi(part)
			if err != nil {
				return fmt.Errorf("bad value %q", part)
			}
			a, b = n, n
			if step > 1 {
				b = hi
			}
		}
		if a < lo || b > hi || a > b {
			return fmt.Errorf("%q out of range %d-%d", part, lo, hi)
		}
		for v := a; v <= b; v += step {
			dst[v] = true
		}
	}
	return nil
}

func (c *Cron) dayMatches(t time.Time) bool {
	d, w := c.dom[t.Day()], c.dow[int(t.Weekday())]
	switch {
	case c.domStar && c.dowStar:
		return true
	case c.domStar:
		return w
	case c.dowStar:
		return d
	}
	return d || w
}

// Next returns the first minute after t (in t's location) that matches.
func (c *Cron) Next(t time.Time) time.Time {
	t = t.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 366*24*60*5; i++ {
		if !c.month[int(t.Month())] {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !c.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !c.hour[t.Hour()] {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			continue
		}
		if !c.min[t.Minute()] {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}
