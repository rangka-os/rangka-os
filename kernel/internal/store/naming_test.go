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

// A name kept as given (an import or seed that brings its own number) moves the counter past it, still without
// reading the model's table (card t_32af87a2): counter at 1, an imported 000500, the next name is 000501.
func TestNextNameSkipsKeptNames(t *testing.T) {
	ctx := context.Background()
	db, err := pg.NewPool(ctx, testdb.New(t), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Exec(ctx, systemSQL); err != nil {
		t.Fatal(err)
	}
	tn := TableName("naming_kept")
	if _, err := db.Exec(ctx, "create table "+pg.QuoteIdent(tn)+" (org_id text, name text)"); err != nil {
		t.Fatal(err)
	}
	now := jakarta(t)
	next := func(org string) string {
		t.Helper()
		n, err := NextName(ctx, db, org, "naming_kept", "NK-{YYYY}-{######}", now)
		if err != nil {
			t.Fatalf("NextName %s: %v", org, err)
		}
		return n
	}
	note := func(org, name string) {
		t.Helper()
		if err := NoteName(ctx, db, org, name); err != nil {
			t.Fatalf("NoteName %s: %v", name, err)
		}
	}
	if got := next("o1"); got != "NK-2026-000001" {
		t.Fatalf("first = %s", got)
	}
	if got := next("o2"); got != "NK-2026-000001" {
		t.Fatalf("o2 first = %s", got)
	}
	// the counters exist: from here the table is never read, so it may go
	if _, err := db.Exec(ctx, "alter table "+pg.QuoteIdent(tn)+" rename to "+pg.QuoteIdent(tn+"_gone")); err != nil {
		t.Fatal(err)
	}
	note("o1", "NK-2026-000500")
	note("o1", "NK-2026-000300")       // lower: the counter does not go back
	note("o1", "NK-2026-000900-RETUR") // not the prefix followed by digits only
	note("o1", "NK-2025-009999")       // another prefix (last year) with no counter
	note("o1", "NK-2026-1x")
	note("o1", "XX-2026-009999")
	if got := next("o1"); got != "NK-2026-000501" {
		t.Fatalf("after an imported 000500: %s, want NK-2026-000501", got)
	}
	if got := next("o1"); got != "NK-2026-000502" {
		t.Fatalf("then %s", got)
	}
	// other orgs keep their own counters
	if got := next("o2"); got != "NK-2026-000002" {
		t.Fatalf("o2 second = %s, want NK-2026-000002", got)
	}
	// a counter that outgrew its width is matched in full
	note("o2", "NK-2026-1234567")
	if got := next("o2"); got != "NK-2026-1234568" {
		t.Fatalf("o2 after a wide import = %s", got)
	}
	// a name that matches no counter is a no-op, also for an org with none
	note("o3", "NK-2026-000777")
}
