package store

import (
	"context"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// TestDatetimeUTC: datetime values are formatted in UTC (Z) even when the process runs in another zone.
func TestDatetimeUTC(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	old := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = old })
	ctx := context.Background()
	db, err := pg.NewPool(ctx, testdb.New(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	r, err := db.Query(ctx, "select '2026-11-03T02:30:00Z'::timestamptz")
	if err != nil {
		t.Fatal(err)
	}
	if got := fromSQL(&meta.Field{Name: "at", Type: "datetime"}, r.Rows[0][0]); got != "2026-11-03T02:30:00Z" {
		t.Fatalf("datetime = %v, want 2026-11-03T02:30:00Z", got)
	}
}
