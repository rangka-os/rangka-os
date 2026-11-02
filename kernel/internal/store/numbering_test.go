package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
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

// --- DB tests -------------------------------------------------------------------------------------

func seriesDB(t *testing.T, size int) *pg.Pool {
	t.Helper()
	ctx := context.Background()
	db, err := pg.NewPool(ctx, testdb.New(t), size)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Exec(ctx, SystemSQL()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `create table t_doc (org_id text, name text, primary key (org_id, name))`); err != nil {
		t.Fatal(err)
	}
	return db
}

func nextSeriesCode(t *testing.T, db *pg.Pool, org, code string, s Scheme, tables []string, now time.Time) string {
	t.Helper()
	got, err := NextSeriesName(context.Background(), db, org, code, s, tables, now)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func nextSeries(t *testing.T, db *pg.Pool, org string, s Scheme, tables []string, now time.Time) string {
	t.Helper()
	got, err := NextSeriesName(context.Background(), db, org, strings.ToUpper(org), s, tables, now)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func expectNames(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v; want %v", got, want)
	}
}

func TestNextSeriesNameSequence(t *testing.T) {
	db := seriesDB(t, 2)
	s := Scheme{Series: "inv", Pattern: "INV/{ORG}/{YY}{MM}/{####}", Reset: "monthly"}
	oct := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	nov := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	got := []string{
		nextSeries(t, db, "pt-a", s, nil, oct),
		nextSeries(t, db, "pt-a", s, nil, oct),
		nextSeries(t, db, "pt-a", s, nil, nov),
		nextSeries(t, db, "pt-b", s, nil, nov),
		nextSeriesCode(t, db, "pt-c", "PTC", s, nil, nov),
	}
	expectNames(t, got, "INV/PT-A/2610/0001", "INV/PT-A/2610/0002", "INV/PT-A/2611/0001", "INV/PT-B/2611/0001", "INV/PTC/2611/0001")
}

func TestNextSeriesNameYearlyContinues(t *testing.T) {
	db := seriesDB(t, 2)
	s := Scheme{Series: "d", Pattern: "{##}/D/{YYYY}", Reset: "yearly"}
	got := []string{
		nextSeries(t, db, "pt-a", s, nil, time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)),
		nextSeries(t, db, "pt-a", s, nil, time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)),
	}
	expectNames(t, got, "01/D/2026", "02/D/2026")
}

func TestNextSeriesNameAfterImported(t *testing.T) {
	db := seriesDB(t, 2)
	ctx := context.Background()
	for _, n := range []string{"INV/PT-A/2610/0007", "INV/PT-A/2610/abc"} {
		if _, err := db.Exec(ctx, `insert into t_doc values ('pt-a', $1)`, n); err != nil {
			t.Fatal(err)
		}
	}
	s := Scheme{Series: "inv", Pattern: "INV/{ORG}/{YY}{MM}/{####}", Reset: "monthly"}
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	got := []string{nextSeries(t, db, "pt-a", s, []string{"t_doc"}, now), nextSeries(t, db, "pt-a", s, []string{"t_doc"}, now)}
	expectNames(t, got, "INV/PT-A/2610/0008", "INV/PT-A/2610/0009")
}

func TestNextSeriesNameRollbackNoGap(t *testing.T) {
	db := seriesDB(t, 2)
	ctx := context.Background()
	s := Scheme{Series: "inv", Pattern: "INV-{####}", Reset: "never"}
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	tx1, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := NextSeriesName(ctx, tx1, "pt-a", "PT-A", s, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx1.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	tx2, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NextSeriesName(ctx, tx2, "pt-a", "PT-A", s, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx2.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	expectNames(t, []string{first, second}, "INV-0001", "INV-0001")
}

func TestNextSeriesNameConcurrent(t *testing.T) {
	db := seriesDB(t, 25)
	ctx := context.Background()
	s := Scheme{Series: "inv", Pattern: "{####}", Reset: "never"}
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	const workers = 20
	var (
		mu        sync.Mutex
		committed []string
		wg        sync.WaitGroup
	)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tx, err := db.Begin(ctx)
			if err != nil {
				errs <- err
				return
			}
			name, err := NextSeriesName(ctx, tx, "pt-a", "PT-A", s, []string{"t_doc"}, now)
			if err == nil {
				_, err = tx.Exec(ctx, `insert into t_doc values ('pt-a', $1)`, name)
			}
			if err != nil {
				_ = tx.Rollback(ctx)
				errs <- err
				return
			}
			if i%5 == 0 {
				if err := tx.Rollback(ctx); err != nil {
					errs <- err
				}
				return
			}
			if err := tx.Commit(ctx); err != nil {
				errs <- err
				return
			}
			mu.Lock()
			committed = append(committed, name)
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	sort.Strings(committed)
	want := make([]string, 16)
	for i := range want {
		want[i] = fmt.Sprintf("%04d", i+1)
	}
	expectNames(t, committed, want...)
}

func TestNextSeriesNameOverflow(t *testing.T) {
	db := seriesDB(t, 2)
	if _, err := db.Exec(context.Background(), `insert into t_doc values ('pt-a', 'A-99')`); err != nil {
		t.Fatal(err)
	}
	s := Scheme{Series: "a", Pattern: "A-{##}", Reset: "never"}
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	got := []string{nextSeries(t, db, "pt-a", s, []string{"t_doc"}, now), nextSeries(t, db, "pt-a", s, []string{"t_doc"}, now)}
	expectNames(t, got, "A-100", "A-101")
}
