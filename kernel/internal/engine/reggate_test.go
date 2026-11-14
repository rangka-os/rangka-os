package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

// waitSwapping returns once a swap is pending on g.
func waitSwapping(t *testing.T, g *regGate) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		g.mu.Lock()
		s := g.swapping
		g.mu.Unlock()
		if s {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("swap never became pending")
		}
		time.Sleep(time.Millisecond)
	}
}

// Card t_da045e65 N1: a unit of work waiting on a pending swap leaves when its ctx ends; the swap then still runs.
func TestGateEnterHonoursContext(t *testing.T) {
	var g regGate
	_, leave, err := g.enter(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	swapped := make(chan struct{})
	go g.swap(func() { close(swapped) })
	waitSwapping(t, &g)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, l2, err := g.enter(ctx)
	l2()
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("enter during a pending swap = %v after %v, want the ctx deadline", err, time.Since(start))
	}
	leave()
	select {
	case <-swapped:
	case <-time.After(5 * time.Second):
		t.Fatal("the swap did not run after the running unit of work left")
	}
	if _, l3, err := g.enter(context.Background()); err != nil {
		t.Fatal(err)
	} else {
		l3()
	}
}

// Card t_da045e65 N2: a panic in the swap opens the gate again.
func TestGateSwapPanicReopens(t *testing.T) {
	var g regGate
	func() {
		defer func() { _ = recover() }()
		g.swap(func() { panic("reload failed") })
	}()
	done := make(chan error, 1)
	go func() {
		_, leave, err := g.enter(context.Background())
		leave()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a unit of work waits forever after a swap panicked")
	}
}

// Card t_da045e65 N4: a ctx inside one engine's unit of work does not pass another engine's gate.
func TestGateKeyPerGate(t *testing.T) {
	var g1, g2 regGate
	ctx, leave, err := g1.enter(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer leave()
	_, l, _ := g1.enter(ctx) // nested in g1: passes, active stays 1
	l()
	g1.mu.Lock()
	if g1.active != 1 {
		t.Errorf("nested enter changed g1.active to %d", g1.active)
	}
	g1.mu.Unlock()
	_, l2, err := g2.enter(ctx)
	if err != nil {
		t.Fatal(err)
	}
	g2.mu.Lock()
	if g2.active != 1 {
		t.Errorf("g2 did not count a unit of work whose ctx came from g1 (active %d)", g2.active)
	}
	g2.mu.Unlock()
	l2()
}
