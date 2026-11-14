package engine

import (
	"context"
	"sync"
)

// regGate keeps a live metadata reload out of a running unit of work (card t_ae2bdfa5, audit 14 Nov F1-F5). Pinning
// the registry on the op covers the lookups that go through it, but hooks (stock, ledger), scopes, numbering and the
// row scope also read Engine.Reg; a reload between two of those reads mixed two registries in one transaction (a
// submit without its stock moves, a line computed with the new formula). So every unit of work enters the gate and
// SetRegistry waits until none is running, while new ones wait for the swap: Engine.Reg is the same registry from
// the first read of a unit of work to its commit. A unit of work started inside another (its ctx carries the gate)
// does not enter again, so a nested call cannot wait on a swap that waits on its parent. A unit of work started inside
// another with a ctx not derived from the outer one would wait on a pending swap that waits on the outer one: start
// nested work from o.ctx (card t_da045e65 N3). Waiting honours ctx, so a cancelled request leaves the queue (N1).
type regGate struct {
	mu       sync.Mutex
	active   int
	swapping bool
	swapDone chan struct{} // closed when the pending swap has run
	idle     chan struct{} // closed when the last running unit of work leaves during a pending swap
}

// gateKey marks a ctx that is inside a unit of work of this gate; per gate, so another engine's gate (the preview
// engine, api/preview.go) is entered on its own (card t_da045e65 N4).
type gateKey struct{ g *regGate }

// enter admits a unit of work; leave must be called when it ends (a deferred call, also on panic). Nested units of
// work pass through. While a swap is pending it waits, and returns ctx.Err() when ctx ends first.
func (g *regGate) enter(ctx context.Context) (context.Context, func(), error) {
	key := gateKey{g}
	if ctx.Value(key) != nil {
		return ctx, func() {}, nil
	}
	g.mu.Lock()
	for g.swapping {
		done := g.swapDone
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx, func() {}, ctx.Err()
		case <-done:
		}
		g.mu.Lock()
	}
	g.active++
	g.mu.Unlock()
	var once sync.Once
	return context.WithValue(ctx, key, true), func() {
		once.Do(func() {
			g.mu.Lock()
			g.active--
			if g.active == 0 && g.idle != nil {
				close(g.idle)
				g.idle = nil
			}
			g.mu.Unlock()
		})
	}, nil
}

// swap runs fn once no unit of work is running, holding new ones back until it returns; a panic in fn still opens the
// gate again (card t_da045e65 N2).
func (g *regGate) swap(fn func()) {
	g.mu.Lock()
	for g.swapping {
		done := g.swapDone
		g.mu.Unlock()
		<-done
		g.mu.Lock()
	}
	g.swapping = true
	g.swapDone = make(chan struct{})
	var idle chan struct{}
	if g.active > 0 {
		g.idle = make(chan struct{})
		idle = g.idle
	}
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.swapping = false
		close(g.swapDone)
		g.mu.Unlock()
	}()
	if idle != nil {
		<-idle
	}
	fn()
}
