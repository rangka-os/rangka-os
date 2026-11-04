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

func TestNextSeriesNameSkipsImportedMidPeriod(t *testing.T) {
	db := seriesDB(t, 2)
	ctx := context.Background()
	s := Scheme{Series: "inv", Pattern: "INV-{YYYY}-{####}", Reset: "yearly"}
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	tables := []string{"t_doc"}
	got := []string{nextSeries(t, db, "pt-a", s, tables, now)}
	if _, err := db.Exec(ctx, `insert into t_doc values ('pt-a', 'INV-2026-0005')`); err != nil {
		t.Fatal(err)
	}
	got = append(got, nextSeries(t, db, "pt-a", s, tables, now), nextSeries(t, db, "pt-a", s, tables, now))
	expectNames(t, got, "INV-2026-0001", "INV-2026-0006", "INV-2026-0007")
}

func TestNextSeriesNameSharedSeriesTwoTables(t *testing.T) {
	db := seriesDB(t, 2)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `create table t_doc2 (org_id text, name text, primary key (org_id, name))`); err != nil {
		t.Fatal(err)
	}
	s := Scheme{Series: "inv", Pattern: "INV-{YYYY}-{####}", Reset: "yearly"}
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	tables := []string{"t_doc", "t_doc2"}
	got := []string{nextSeries(t, db, "pt-a", s, tables, now)}
	if _, err := db.Exec(ctx, `insert into t_doc2 values ('pt-a', 'INV-2026-0003')`); err != nil {
		t.Fatal(err)
	}
	got = append(got, nextSeries(t, db, "pt-a", s, tables, now))
	expectNames(t, got, "INV-2026-0001", "INV-2026-0004")
}

func TestSeriesStart(t *testing.T) {
	db := seriesDB(t, 2)
	s := Scheme{Series: "inv", Pattern: "INV-{YYYY}-{####}", Reset: "yearly", Start: 1001}
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	tables := []string{"t_doc"}
	got := []string{nextSeries(t, db, "pt-a", s, tables, now)}
	if _, err := db.Exec(context.Background(), `insert into t_doc values ('pt-a', 'INV-2026-1005')`); err != nil {
		t.Fatal(err)
	}
	got = append(got, nextSeries(t, db, "pt-a", s, tables, now), nextSeries(t, db, "pt-a", s, tables, time.Date(2027, 1, 2, 9, 0, 0, 0, time.UTC)))
	expectNames(t, got, "INV-2026-1001", "INV-2026-1006", "INV-2027-1001")
}

// setNext runs SetNextSeries in its own transaction, like the engine does.
func setNext(db *pg.Pool, org string, s Scheme, tables []string, now time.Time, period string, next int64) (string, error) {
	ctx := context.Background()
	tx, err := db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	got, err := SetNextSeries(ctx, tx, org, strings.ToUpper(org), s, tables, now, period, next)
	if err != nil {
		return "", err
	}
	return got, tx.Commit(ctx)
}

func TestSetNextSeries(t *testing.T) {
	db := seriesDB(t, 2)
	s := Scheme{Series: "inv", Pattern: "INV/{ORG}/{YY}/{####}", Reset: "yearly"}
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	tables := []string{"t_doc"}
	if got, err := setNext(db, "pt-a", s, tables, now, "", 154); err != nil || got != "INV/PT-A/26/0154" {
		t.Fatalf("set 154 = %q, %v", got, err)
	}
	// running the same go-live command twice is harmless
	if got, err := setNext(db, "pt-a", s, tables, now, "", 154); err != nil || got != "INV/PT-A/26/0154" {
		t.Fatalf("set 154 again = %q, %v", got, err)
	}
	expectNames(t, []string{nextSeries(t, db, "pt-a", s, tables, now)}, "INV/PT-A/26/0154")
	_, err := setNext(db, "pt-a", s, tables, now, "", 100)
	if err == nil || err.Error() != "numbering: series inv of pt-a already issued up to 154 in period 2026; next can only go up" {
		t.Fatalf("lowering: %v", err)
	}
	// an existing name at or above next is never issued again
	if _, err := db.Exec(context.Background(), `insert into t_doc values ('pt-b', 'INV/PT-B/26/0200')`); err != nil {
		t.Fatal(err)
	}
	_, err = setNext(db, "pt-b", s, tables, now, "", 150)
	if err == nil || !strings.Contains(err.Error(), "INV/PT-B/26/0200") {
		t.Fatalf("existing name: %v", err)
	}
	// another period: rendered inside that period, the current one untouched
	if got, err := setNext(db, "pt-a", s, tables, now, "2027", 50); err != nil || got != "INV/PT-A/27/0050" {
		t.Fatalf("set 2027 = %q, %v", got, err)
	}
	expectNames(t, []string{nextSeries(t, db, "pt-a", s, tables, now), nextSeries(t, db, "pt-a", s, tables, time.Date(2027, 2, 1, 9, 0, 0, 0, time.UTC))},
		"INV/PT-A/26/0155", "INV/PT-A/27/0050")
	for _, bad := range []struct {
		period string
		next   int64
	}{{"2026-11", 300}, {"26", 300}, {"2026", 0}} {
		if _, err := setNext(db, "pt-c", s, tables, now, bad.period, bad.next); err == nil {
			t.Errorf("period %q next %d: want an error", bad.period, bad.next)
		}
	}
	// below the series start the counter would not move: refused
	if _, err := setNext(db, "pt-c", Scheme{Series: "k", Pattern: "K-{####}", Start: 1001}, nil, now, "", 500); err == nil {
		t.Error("next below start: want an error")
	}
	m := Scheme{Series: "m", Pattern: "M/{YY}{MM}/{###}", Reset: "monthly"}
	if got, err := setNext(db, "pt-c", m, nil, now, "2026-12", 7); err != nil || got != "M/2612/007" {
		t.Fatalf("monthly set = %q, %v", got, err)
	}
}

func TestSetNextSeriesConcurrent(t *testing.T) {
	db := seriesDB(t, 25)
	ctx := context.Background()
	s := Scheme{Series: "inv", Pattern: "{####}", Reset: "never"}
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	tables := []string{"t_doc"}
	const workers, each = 10, 5
	var (
		mu       sync.Mutex
		before   []string // allocations that may have started before the set committed
		after    []string // allocations started after it
		setDone  bool
		wg       sync.WaitGroup
		setErr   error
		errCount int
	)
	errs := make(chan error, workers*each)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < each; j++ {
				mu.Lock()
				started := setDone
				mu.Unlock()
				tx, err := db.Begin(ctx)
				if err != nil {
					errs <- err
					return
				}
				name, err := NextSeriesName(ctx, tx, "pt-a", "PT-A", s, tables, now)
				if err == nil {
					_, err = tx.Exec(ctx, `insert into t_doc values ('pt-a', $1)`, name)
				}
				if err == nil {
					err = tx.Commit(ctx)
				}
				if err != nil {
					_ = tx.Rollback(ctx)
					errs <- err
					return
				}
				mu.Lock()
				if started {
					after = append(after, name)
				} else {
					before = append(before, name)
				}
				mu.Unlock()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, setErr = setNext(db, "pt-a", s, tables, now, "", 500)
		mu.Lock()
		setDone = true
		mu.Unlock()
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		errCount++
		t.Error(err)
	}
	if setErr != nil || errCount > 0 {
		t.Fatalf("set: %v", setErr)
	}
	all := append(append([]string{}, before...), after...)
	seen := map[string]bool{}
	low, high := 0, 0
	for _, n := range all {
		if seen[n] {
			t.Fatalf("duplicate %s in %v", n, all)
		}
		seen[n] = true
		if n < "0500" {
			low++
		} else {
			high++
		}
	}
	for _, n := range after {
		if n < "0500" {
			t.Fatalf("%s allocated after the set (all: %v)", n, all)
		}
	}
	// both runs are gap free: 0001…low, then 0500…
	for i := 1; i <= low; i++ {
		if !seen[fmt.Sprintf("%04d", i)] {
			t.Fatalf("gap at %04d: %v", i, all)
		}
	}
	for i := 0; i < high; i++ {
		if !seen[fmt.Sprintf("%04d", 500+i)] {
			t.Fatalf("gap at %04d: %v", 500+i, all)
		}
	}
}
