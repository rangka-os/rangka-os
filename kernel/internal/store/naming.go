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
	// the usual case: the counter exists, take the next number without reading the model's table (the insert
	// below evaluates its max() even when it then conflicts, a scan of every name with the prefix per new record:
	// submitting a sale that posts 100 stock moves read the whole stock_move table 100 times, card t_58a60f6b)
	res, err := q.Query(ctx, `update rk_naming set last = last + 1 where org_id = $1 and prefix = $2 returning last`, org, prefix)
	if err != nil {
		return "", err
	}
	if len(res.Rows) == 1 {
		return fmt.Sprintf("%s%0*d", prefix, digits, res.Rows[0][0].(int64)), nil
	}
	like := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix) + "%"
	sql := fmt.Sprintf(`insert into rk_naming (org_id, prefix, last)
		values ($1, $2, coalesce((select max(substring(name from $3::int)::bigint) from %s
			where org_id = $1 and name like $4 and substring(name from $3::int) ~ '^[0-9]{1,18}$'), 0) + 1)
		on conflict (org_id, prefix) do update set last = rk_naming.last + 1
		returning last`, pg.QuoteIdent(TableName(model)))
	res, err = q.Query(ctx, sql, org, prefix, len([]rune(prefix))+1, like)
	if err != nil {
		return "", err
	}
	n := res.Rows[0][0].(int64)
	return fmt.Sprintf("%s%0*d", prefix, digits, n), nil
}

// NoteName moves an org's v0 counters past a name the kernel writes as given instead of allocating it (an
// imported or seeded record that keeps its own number): every counter whose prefix the name starts with, followed
// only by digits, continues after that number. It reads rk_naming alone (the org's counter rows), never the
// model's table, so allocation stays one counter update per record (card t_58a60f6b); a counter that is already
// past the number is left as it is. Without it a counter at 1 would reissue 0002 after an import of 0500, and the
// insert fails on the name once it reaches 0500 (card t_32af87a2).
func NoteName(ctx context.Context, q pg.Querier, org, name string) error {
	_, err := q.Query(ctx, `update rk_naming set last = n.v
		from (select prefix as p, case when substring($2 from char_length(prefix) + 1) ~ '^[0-9]{1,18}$'
			then substring($2 from char_length(prefix) + 1)::bigint end as v
			from rk_naming where org_id = $1 and left($2, char_length(prefix)) = prefix) n
		where org_id = $1 and prefix = n.p and n.v > last`, org, name)
	return err
}

// ResetCounters forgets an org's counters so they restart from the highest existing names (after a seed).
func ResetCounters(ctx context.Context, q pg.Querier, org string) error {
	_, err := q.Query(ctx, "delete from rk_naming where org_id = $1", org)
	return err
}
