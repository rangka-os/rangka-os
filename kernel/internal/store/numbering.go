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
	Start   int64  // lowest number issued in every period; 0 = 1
}

// reset is s.Reset, else the default for its pattern.
func (s Scheme) reset() string {
	if s.Reset != "" {
		return s.Reset
	}
	return DefaultReset(s.Pattern)
}

// start is the lowest number the series issues in a period (at least 1).
func (s Scheme) start() int64 {
	if s.Start > 1 {
		return s.Start
	}
	return 1
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

// existingNames is the union over tables of the record names in $1's org that match the regular expression
// in parameter $nameParam, selecting cols from each ("name" and/or the captured number as "n").
func existingNames(tables []string, cols string, nameParam int) string {
	parts := make([]string, len(tables))
	for i, t := range tables {
		parts[i] = fmt.Sprintf("select %s from %s where org_id = $1 and name ~ $%d::text", cols, pg.QuoteIdent(t), nameParam)
	}
	return strings.Join(parts, " union all ")
}

// existingMax is the subquery that finds the highest number among the names in tables matching the
// regular expression in parameter $4, or "null" when there are no tables.
func existingMax(tables []string) string {
	if len(tables) == 0 {
		return "null"
	}
	return "(select max(n) from (" + existingNames(tables, "substring(name from $4::text)::bigint as n", 4) + ") x)"
}

// nameRe matches the names of a rendered series and captures their number.
func nameRe(prefix, suffix string) string {
	return "^" + regexp.QuoteMeta(prefix) + "([0-9]{1,18})" + regexp.QuoteMeta(suffix) + "$"
}

// NextSeriesName takes the next number of a series for an org inside q's transaction. tables are the record
// tables of every model using the series; every number continues after the highest existing
// name there that matches the rendered pattern, so imported names are skipped over, and is never below the
// series start. orgCode is used verbatim for {ORG}. now must already be in the site timezone.
func NextSeriesName(ctx context.Context, q pg.Querier, org, orgCode string, s Scheme, tables []string, now time.Time) (string, error) {
	prefix, suffix, digits := RenderSeries(s.Pattern, orgCode, now)
	sql := fmt.Sprintf(`insert into rk_number_series (org_id, series, period, last)
		values ($1, $2, $3, greatest(coalesce(%s, 0) + 1, %d))
		on conflict (org_id, series, period) do update set last = greatest(rk_number_series.last + 1, excluded.last)
		returning last`, existingMax(tables), s.start())
	args := []any{org, s.Series, Period(s.reset(), now)}
	if len(tables) > 0 {
		args = append(args, nameRe(prefix, suffix))
	}
	res, err := q.Query(ctx, sql, args...)
	if err != nil {
		return "", err
	}
	return FormatNumber(prefix, suffix, digits, res.Rows[0][0].(int64)), nil
}

var periodShape = map[string]*regexp.Regexp{
	"yearly":  regexp.MustCompile(`^[0-9]{4}$`),
	"monthly": regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`),
	"never":   regexp.MustCompile(`^$`),
}

// periodTime is a time inside period (its first day, in now's location) to render the series with; an empty
// period is the current one.
func periodTime(reset, period string, now time.Time) (time.Time, error) {
	if period == "" || period == Period(reset, now) {
		return now, nil
	}
	re := periodShape[reset]
	if re == nil || !re.MatchString(period) {
		want := map[string]string{"yearly": "YYYY", "monthly": "YYYY-MM", "never": `"" (the series never resets)`}[reset]
		return time.Time{}, fmt.Errorf("numbering: period %q does not fit a %s series (want %s)", period, reset, want)
	}
	y, m := 0, 1
	fmt.Sscanf(period, "%d-%d", &y, &m)
	return time.Date(y, time.Month(m), 1, 0, 0, 0, 0, now.Location()), nil
}

// SetNextSeries makes next the number the series issues next for an org in period ("" = the current period
// of now, which must already be in the site timezone), inside q's transaction, and returns that number
// formatted with the period it resolved. It is the one-time go-live step that continues a client's own numbering (contract 0009 §4):
// it never lowers the counter and never makes an existing name come out again. Numbers between the old
// counter and next are skipped on purpose; they live in the client's old system.
func SetNextSeries(ctx context.Context, q pg.Querier, org, orgCode string, s Scheme, tables []string, now time.Time, period string, next int64) (string, string, error) {
	if next < 1 {
		return "", "", fmt.Errorf("numbering: next must be 1 or more, got %d", next)
	}
	if next < s.start() {
		return "", "", fmt.Errorf("numbering: series %s starts at %d; next cannot be below that", s.Series, s.start())
	}
	reset := s.reset()
	at, err := periodTime(reset, period, now)
	if err != nil {
		return "", "", err
	}
	period = Period(reset, at)
	label := period
	if label == "" {
		label = "never"
	}
	// Lock the counter row first (creating it at 0 when missing): allocations wait on it, so the scan below
	// sees every name they committed and none can be issued between the checks and the update.
	if _, err := q.Exec(ctx, `insert into rk_number_series (org_id, series, period, last) values ($1, $2, $3, 0)
		on conflict (org_id, series, period) do nothing`, org, s.Series, period); err != nil {
		return "", "", err
	}
	res, err := q.Query(ctx, `select last from rk_number_series where org_id = $1 and series = $2 and period = $3 for update`, org, s.Series, period)
	if err != nil {
		return "", "", err
	}
	if last := res.Rows[0][0].(int64); last >= next {
		return "", "", fmt.Errorf("numbering: series %s of %s already issued up to %d in period %s; next can only go up", s.Series, org, last, label)
	}
	prefix, suffix, digits := RenderSeries(s.Pattern, orgCode, at)
	if len(tables) > 0 {
		res, err := q.Query(ctx, "select name, n from ("+existingNames(tables, "name, substring(name from $2::text)::bigint as n", 2)+") x order by n desc limit 1", org, nameRe(prefix, suffix))
		if err != nil {
			return "", "", err
		}
		if len(res.Rows) > 0 && res.Rows[0][1].(int64) >= next {
			return "", "", fmt.Errorf("numbering: %s already exists in series %s of %s; next must be above %d", res.Rows[0][0], s.Series, org, res.Rows[0][1])
		}
	}
	if _, err := q.Exec(ctx, `update rk_number_series set last = $4 where org_id = $1 and series = $2 and period = $3 and last < $4`,
		org, s.Series, period, next-1); err != nil {
		return "", "", err
	}
	return FormatNumber(prefix, suffix, digits, next), period, nil
}
