package store

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
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

// OrgCode is the org's short code for {ORG}: rk_orgs.code when set, else the org id upper-cased.
func OrgCode(ctx context.Context, q pg.Querier, org string) (string, error) {
	res, err := q.Query(ctx, "select code from rk_orgs where id = $1", org)
	if err != nil {
		return "", err
	}
	if len(res.Rows) > 0 {
		if code, ok := res.Rows[0][0].(string); ok && code != "" {
			return code, nil
		}
	}
	return strings.ToUpper(org), nil
}

// NextSeriesName takes the next number of a series for an org inside q's transaction. tables are the record
// tables of every model using the series; the first number of a period continues after the highest existing
// name there that matches the rendered pattern. now must already be in the site timezone.
func NextSeriesName(ctx context.Context, q pg.Querier, org string, s Scheme, tables []string, now time.Time) (string, error) {
	code, err := OrgCode(ctx, q, org)
	if err != nil {
		return "", err
	}
	prefix, suffix, digits := RenderSeries(s.Pattern, code, now)
	reset := s.Reset
	if reset == "" {
		reset = DefaultReset(s.Pattern)
	}
	re := "^" + regexp.QuoteMeta(prefix) + "([0-9]{1,18})" + regexp.QuoteMeta(suffix) + "$"
	start := "0"
	if len(tables) > 0 {
		parts := make([]string, len(tables))
		for i, t := range tables {
			parts[i] = fmt.Sprintf("select substring(name from $4::text)::bigint as n from %s where org_id = $1 and name ~ $4::text", pg.QuoteIdent(t))
		}
		start = "(select max(n) from (" + strings.Join(parts, " union all ") + ") x)"
	}
	sql := fmt.Sprintf(`insert into rk_number_series (org_id, series, period, last)
		values ($1, $2, $3, coalesce(%s, 0) + 1)
		on conflict (org_id, series, period) do update set last = rk_number_series.last + 1
		returning last`, start)
	args := []any{org, s.Series, Period(reset, now)}
	if len(tables) > 0 {
		args = append(args, re)
	}
	res, err := q.Query(ctx, sql, args...)
	if err != nil {
		return "", err
	}
	return FormatNumber(prefix, suffix, digits, res.Rows[0][0].(int64)), nil
}
