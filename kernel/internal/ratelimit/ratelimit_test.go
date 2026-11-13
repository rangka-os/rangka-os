package ratelimit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestWaitThenRefuse(t *testing.T) {
	// The bucket reads a clock the test moves by hand, so a slow machine refills nothing between the calls (card
	// t_63a37fe7: with the wall clock, a goroutine that started late let the bucket refill and nothing was refused).
	// The waits themselves are real timers.
	var mu sync.Mutex
	clock := time.Unix(1_800_000_000, 0)
	l := &Limiter{Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }}
	advance := func(d time.Duration) { mu.Lock(); clock = clock.Add(d); mu.Unlock() }
	ctx := context.Background()
	rate := Rate{RPS: 20, Burst: 2, MaxWait: 80 * time.Millisecond, MaxWaiting: 5}
	for i := 0; i < 2; i++ {
		if w, err := l.Wait(ctx, "t", rate); w != 0 || err != nil {
			t.Fatalf("burst %d: %s %v", i, w, err)
		}
	}
	if w, err := l.Wait(ctx, "t", rate); err != nil || w != 50*time.Millisecond {
		t.Fatalf("third waits: %s %v", w, err)
	}
	advance(50 * time.Millisecond) // the third's token came in while it waited
	// Two more at once: the first waits 50 ms, the second would wait 100 ms > 80 ms.
	done := make(chan error, 1)
	go func() { _, err := l.Wait(ctx, "t", rate); done <- err }()
	waitingIs := func(n int) bool { l.mu.Lock(); defer l.mu.Unlock(); return l.buckets["t"].waiting == n }
	for deadline := time.Now().Add(5 * time.Second); !waitingIs(1); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the fourth request never started waiting")
		}
	}
	var r *Refused
	if _, err := l.Wait(ctx, "t", rate); !errors.As(err, &r) || r.RetrySeconds() != 1 {
		t.Fatalf("over the wait: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if th, ms, ref := l.Today(); th != 2 || ms != 100 || ref != 1 {
		t.Fatalf("today: throttled %d waited %d ms refused %d", th, ms, ref)
	}
	if w, err := l.Wait(ctx, "other", rate); w != 0 || err != nil {
		t.Fatalf("own bucket per key: %s %v", w, err)
	}
}

func TestMaxWaitingAndCancel(t *testing.T) {
	l := &Limiter{}
	rate := Rate{RPS: 1, Burst: 1, MaxWait: time.Minute, MaxWaiting: 1}
	if _, err := l.Wait(context.Background(), "t", rate); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := l.Wait(ctx, "t", rate); done <- err }()
	time.Sleep(20 * time.Millisecond)
	var r *Refused
	if _, err := l.Wait(context.Background(), "t", rate); !errors.As(err, &r) {
		t.Fatalf("second waiter: %v", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDayRollsOver(t *testing.T) {
	now := time.Date(2026, 11, 13, 23, 59, 59, 0, time.Local)
	l := &Limiter{Now: func() time.Time { return now }}
	rate := Rate{RPS: 1, Burst: 1, MaxWait: 0, MaxWaiting: 0}
	l.Wait(context.Background(), "t", rate)
	l.Wait(context.Background(), "t", rate)
	if _, _, ref := l.Today(); ref != 1 {
		t.Fatalf("refused %d", ref)
	}
	now = now.Add(2 * time.Second)
	if _, _, ref := l.Today(); ref != 0 {
		t.Fatalf("refused after midnight %d", ref)
	}
}

func TestPrune(t *testing.T) {
	now := time.Unix(0, 0)
	l := &Limiter{MaxKeys: 3, Now: func() time.Time { return now }}
	rate := Rate{RPS: 10, Burst: 1, MaxWait: 0, MaxWaiting: 0}
	for _, k := range []string{"a", "b", "c"} {
		l.Wait(context.Background(), k, rate)
	}
	now = now.Add(time.Second) // all full again
	l.Wait(context.Background(), "d", rate)
	if len(l.buckets) != 1 {
		t.Fatalf("buckets %d", len(l.buckets))
	}
}
