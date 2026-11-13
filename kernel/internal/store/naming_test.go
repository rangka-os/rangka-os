package store

import (
	"context"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// NextName reads the model's table only to start a counter: once the counter exists the next number comes from
// rk_naming alone (card t_58a60f6b: every stock move a sale posted scanned all stock moves). The numbers are as
// before: the first continues after the highest existing name, the next ones count on.
func TestNextNameReadsTableOnlyToStart(t *testing.T) {
	ctx := context.Background()
	db, err := pg.NewPool(ctx, testdb.New(t), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Exec(ctx, systemSQL); err != nil {
		t.Fatal(err)
	}
	tn := TableName("naming_probe")
	if _, err := db.Exec(ctx, "create table "+pg.QuoteIdent(tn)+" (org_id text, name text)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "insert into "+pg.QuoteIdent(tn)+" values ('o1', 'NP-2026-000041'), ('o1', 'NP-2026-x'), ('o2', 'NP-2026-000900')"); err != nil {
		t.Fatal(err)
	}
	now := jakarta(t)
	next := func(org string) string {
		t.Helper()
		n, err := NextName(ctx, db, org, "naming_probe", "NP-{YYYY}-{######}", now)
		if err != nil {
			t.Fatalf("NextName %s: %v", org, err)
		}
		return n
	}
	if got := next("o1"); got != "NP-2026-000042" {
		t.Fatalf("first name = %s, want NP-2026-000042 (after the highest existing)", got)
	}
	// with the counter started, the table is not read again: renaming it away must not matter
	if _, err := db.Exec(ctx, "alter table "+pg.QuoteIdent(tn)+" rename to "+pg.QuoteIdent(tn+"_gone")); err != nil {
		t.Fatal(err)
	}
	if got := next("o1"); got != "NP-2026-000043" {
		t.Fatalf("second name = %s, want NP-2026-000043", got)
	}
	// another org starts its own counter, which reads the table: gone now, so that start fails as before
	if _, err := NextName(ctx, db, "o2", "naming_probe", "NP-{YYYY}-{######}", now); err == nil {
		t.Fatal("a new counter must read the table to start")
	}
}

// SyncCounter moves a counter that fell behind (names written outside it after it started) past the highest
// existing name, never back (card t_32af87a2).
func TestSyncCounter(t *testing.T) {
	ctx := context.Background()
	db, err := pg.NewPool(ctx, testdb.New(t), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Exec(ctx, systemSQL); err != nil {
		t.Fatal(err)
	}
	tn := pg.QuoteIdent(TableName("naming_sync"))
	if _, err := db.Exec(ctx, "create table "+tn+" (org_id text, name text)"); err != nil {
		t.Fatal(err)
	}
	now := jakarta(t)
	next := func() string {
		t.Helper()
		n, err := NextName(ctx, db, "o1", "naming_sync", "NS-{YYYY}-{####}", now)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := next(); got != "NS-2026-0001" {
		t.Fatalf("first = %s", got)
	}
	// loaded by SQL after the counter started: the counter does not see it (it never reads the table again)
	if _, err := db.Exec(ctx, "insert into "+tn+" values ('o1', 'NS-2026-0500'), ('o1', 'NS-2026-0900-R'), ('o2', 'NS-2026-0999')"); err != nil {
		t.Fatal(err)
	}
	if got := next(); got != "NS-2026-0002" {
		t.Fatalf("second = %s", got)
	}
	if err := SyncCounter(ctx, db, "o1", "naming_sync", "NS-2026-"); err != nil {
		t.Fatal(err)
	}
	if got := next(); got != "NS-2026-0501" {
		t.Fatalf("after SyncCounter = %s, want NS-2026-0501", got)
	}
	// never back: the counter is past every name now
	if err := SyncCounter(ctx, db, "o1", "naming_sync", "NS-2026-"); err != nil {
		t.Fatal(err)
	}
	if got := next(); got != "NS-2026-0502" {
		t.Fatalf("after a second SyncCounter = %s, want NS-2026-0502", got)
	}
}
