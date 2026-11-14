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
// does not enter again, so a nested call cannot wait on a swap that waits on its parent.
type regGate struct {
	mu       sync.Mutex
	cond     *sync.Cond
	active   int
	swapping bool
}

type gateKey struct{}

func (g *regGate) c() *sync.Cond {
	if g.cond == nil {
		g.cond = sync.NewCond(&g.mu)
	}
	return g.cond
}

// enter admits a unit of work; leave must be called when it ends. Nested units of work pass through.
func (g *regGate) enter(ctx context.Context) (context.Context, func()) {
	if ctx.Value(gateKey{}) != nil {
		return ctx, func() {}
	}
	g.mu.Lock()
	for g.swapping {
		g.c().Wait()
	}
	g.active++
	g.mu.Unlock()
	var once sync.Once
	return context.WithValue(ctx, gateKey{}, true), func() {
		once.Do(func() {
			g.mu.Lock()
			g.active--
			if g.active == 0 {
				g.c().Broadcast()
			}
			g.mu.Unlock()
		})
	}
}

// swap runs fn once no unit of work is running, holding new ones back until it returns.
func (g *regGate) swap(fn func()) {
	g.mu.Lock()
	for g.swapping {
		g.c().Wait()
	}
	g.swapping = true
	for g.active > 0 {
		g.c().Wait()
	}
	g.mu.Unlock()
	fn()
	g.mu.Lock()
	g.swapping = false
	g.c().Broadcast()
	g.mu.Unlock()
}
