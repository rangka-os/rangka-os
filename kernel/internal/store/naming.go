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
	sql := fmt.Sprintf(`insert into rk_naming (org_id, prefix, last)
		values ($1, $2, %s + 1)
		on conflict (org_id, prefix) do update set last = rk_naming.last + 1
		returning last`, highestName(model))
	res, err = q.Query(ctx, sql, org, prefix, len([]rune(prefix))+1, likePrefix(prefix))
	if err != nil {
		return "", err
	}
	n := res.Rows[0][0].(int64)
	return fmt.Sprintf("%s%0*d", prefix, digits, n), nil
}

// highestName is the SQL for the highest number among model's names in org $1 that are prefix $2 (as LIKE $4,
// counter from rune $3) followed by digits only, 0 when there is none: the one read of the model's table.
func highestName(model string) string {
	return fmt.Sprintf(`coalesce((select max(substring(name from $3::int)::bigint) from %s
			where org_id = $1 and name like $4 and substring(name from $3::int) ~ '^[0-9]{1,18}$'), 0)`, pg.QuoteIdent(TableName(model)))
}

func likePrefix(prefix string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix) + "%"
}

// SyncCounter moves the v0 counter of org and prefix past the highest existing name of model with that prefix
// (never back). The counter reads only its own row on every allocation, so a name written outside it after it
// started (rows loaded by SQL, a partial restore) is not seen; the engine calls this when the name it allocated
// turns out to be taken, then writes again (card t_32af87a2). It reads the model's table once.
func SyncCounter(ctx context.Context, q pg.Querier, org, model, prefix string) error {
	sql := fmt.Sprintf(`update rk_naming set last = greatest(last, %s) where org_id = $1 and prefix = $2`, highestName(model))
	_, err := q.Query(ctx, sql, org, prefix, len([]rune(prefix))+1, likePrefix(prefix))
	return err
}

// ResetCounters forgets an org's counters so they restart from the highest existing names (after a seed).
func ResetCounters(ctx context.Context, q pg.Querier, org string) error {
	_, err := q.Query(ctx, "delete from rk_naming where org_id = $1", org)
	return err
}
