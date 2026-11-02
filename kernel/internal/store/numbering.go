package store

import (
	"fmt"
	"strings"
	"time"
)

// Scheme is how a model's documents are numbered (contract 0009).
type Scheme struct {
	Series  string // "" = legacy prefix counter (rk_naming)
	Pattern string
	Reset   string // yearly | monthly | never; "" = DefaultReset(Pattern)
}

// DefaultReset is monthly when the pattern has {MM}, yearly when it has {YY} or {YYYY}, else never.
func DefaultReset(pattern string) string {
	switch {
	case strings.Contains(pattern, "{MM}"):
		return "monthly"
	case strings.Contains(pattern, "{YYYY}"), strings.Contains(pattern, "{YY}"):
		return "yearly"
	}
	return "never"
}

// Period is the counter period key: "" for never, "2026" for yearly, "2026-11" for monthly.
// now is already in the site timezone.
func Period(reset string, now time.Time) string {
	switch reset {
	case "yearly":
		return now.Format("2006")
	case "monthly":
		return now.Format("2006-01")
	}
	return ""
}

// RenderSeries replaces the date tokens and {ORG}, then splits the pattern at its {#…} counter.
// A pattern without a counter gets one of 4 digits at the end, like ExpandPattern.
func RenderSeries(pattern, orgCode string, now time.Time) (prefix, suffix string, digits int) {
	p := strings.NewReplacer("{YYYY}", now.Format("2006"), "{YY}", now.Format("06"), "{MM}", now.Format("01"), "{ORG}", orgCode).Replace(pattern)
	m := counterRe.FindStringSubmatchIndex(p)
	if m == nil {
		return p, "", 4
	}
	return p[:m[0]], p[m[1]:], m[3] - m[2]
}

// FormatNumber zero-pads n to digits and wraps it in prefix and suffix; a longer number is never truncated.
func FormatNumber(prefix, suffix string, digits int, n int64) string {
	return fmt.Sprintf("%s%0*d%s", prefix, digits, n, suffix)
}
