package store

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

var counterRe = regexp.MustCompile(`\{(#+)\}`)

// ExpandPattern turns INV-{YYYY}-{#####} into the prefix "INV-2026-" and the counter width 5.
func ExpandPattern(pattern string, now time.Time) (prefix string, digits int) {
	p := strings.NewReplacer("{YYYY}", now.Format("2006"), "{YY}", now.Format("06"), "{MM}", now.Format("01")).Replace(pattern)
	m := counterRe.FindStringSubmatchIndex(p)
	if m == nil {
		return p, 4
	}
	return p[:m[0]], m[3] - m[2]
}

// NextName allocates the next name for a model in an org. The counter starts after the highest existing
// name with the same prefix, so seeded or imported numbers are never reused.
func NextName(ctx context.Context, q pg.Querier, org, model, pattern string, now time.Time) (string, error) {
	if pattern == "" {
		pattern = strings.ToUpper(model) + "-{#####}"
	}
	prefix, digits := ExpandPattern(pattern, now)
	like := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix) + "%"
	sql := fmt.Sprintf(`insert into rk_naming (org_id, prefix, last)
		values ($1, $2, coalesce((select max(substring(name from $3::int)::bigint) from %s
			where org_id = $1 and name like $4 and substring(name from $3::int) ~ '^[0-9]{1,18}$'), 0) + 1)
		on conflict (org_id, prefix) do update set last = rk_naming.last + 1
		returning last`, pg.QuoteIdent(TableName(model)))
	res, err := q.Query(ctx, sql, org, prefix, len([]rune(prefix))+1, like)
	if err != nil {
		return "", err
	}
	n := res.Rows[0][0].(int64)
	return fmt.Sprintf("%s%0*d", prefix, digits, n), nil
}

// ResetCounters forgets an org's counters so they restart from the highest existing names (after a seed).
func ResetCounters(ctx context.Context, q pg.Querier, org string) error {
	_, err := q.Query(ctx, "delete from rk_naming where org_id = $1", org)
	return err
}
