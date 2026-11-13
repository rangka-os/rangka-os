package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// A database that does not answer holds a poll tick at most pollEnqueueTimeout, and with it SetPolls (a metadata
// reload); the slot that was not enqueued is tried again on the next tick (t_3c967f78).
func TestPollTickBoundedWhenTheQueueBlocks(t *testing.T) {
	ctx := context.Background()
	db, err := pg.NewPool(ctx, testdb.New(t), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Exec(ctx, store.SystemSQL()); err != nil {
		t.Fatal(err)
	}
	old := pollEnqueueTimeout
	pollEnqueueTimeout = 300 * time.Millisecond
	t.Cleanup(func() { pollEnqueueTimeout = old })

	s := NewScheduler(New(db, 1, 1), nil)
	s.SetPolls("org_a", []PollSpec{{Name: "vendor.sales", Every: time.Minute}})
	lock, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec(ctx, "lock table rk_jobs in access exclusive mode"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	start := time.Now()
	done := make(chan struct{})
	go func() { s.tickPolls(ctx, now); close(done) }()
	time.Sleep(50 * time.Millisecond)
	s.SetPolls("org_a", []PollSpec{{Name: "vendor.sales", Every: time.Minute}}) // a reload while the tick waits
	reload := time.Since(start)
	<-done
	if took := time.Since(start); took > 3*time.Second || reload > 3*time.Second {
		t.Fatalf("tick %v, reload %v: not bounded by the enqueue timeout", took, reload)
	}
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	s.tickPolls(ctx, now)
	res, err := db.Query(ctx, "select count(*) from rk_jobs where kind = 'poll'")
	if err != nil {
		t.Fatal(err)
	}
	if n := res.Rows[0][0]; n != int64(1) {
		t.Fatalf("poll jobs after the database answered again = %v, want 1 (the slot is retried)", n)
	}
}
