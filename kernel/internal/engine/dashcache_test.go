package engine

import (
	"context"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/cache"
)

const cachedCountDash = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/dashboard.json
name: cached_count
label: {id: Hitung}
cache: 5m
widgets:
  - {name: prospek, type: number, label: {id: Prospek}, model: lead, aggregate: count, span: 3}
`

// setHookCache runs onSet once, just before a Set reaches the cache.
type setHookCache struct {
	*cache.Memory
	onSet func()
}

func (h *setHookCache) Set(k string, v any, ttl time.Duration) {
	if f := h.onSet; f != nil {
		h.onSet = nil
		f()
	}
	h.Memory.Set(k, v, ttl)
}

// Card t_0214482d A1: a write that commits while a cached dashboard is computed (here: just before its Set) does not
// leave the old count in the cache; the next read computes again and shows the write.
func TestDashboardCacheNotStaleAfterWrite(t *testing.T) {
	e, owner := approvalSetupFiles(t, map[string]string{"crm/dashboards/cached_count.rk": cachedCountDash}, "")
	hc := &setHookCache{Memory: cache.NewMemory(8)}
	e.Cache = hc
	ctx := context.Background()
	d0, _, err := e.Dashboard(ctx, owner, "cached_count", 0)
	if err != nil {
		t.Fatal(err)
	}
	before := widgetOut(t, d0, "prospek")["value"]
	e.Cache.DeletePrefix("dash:")
	hc.onSet = func() {
		if _, err := e.Create(ctx, owner, "lead", map[string]any{"lead_name": "Baru", "phone": "081299990001"}); err != nil {
			t.Error(err)
		}
	}
	if _, _, err := e.Dashboard(ctx, owner, "cached_count", 0); err != nil {
		t.Fatal(err)
	}
	d2, cached, err := e.Dashboard(ctx, owner, "cached_count", 0)
	if err != nil {
		t.Fatal(err)
	}
	after := widgetOut(t, d2, "prospek")["value"]
	if cached || after == before {
		t.Errorf("after a write during the compute: cached=%v count %v (before %v), want a fresh count", cached, after, before)
	}
	// with no write in between, the result is cached as before
	if _, cached, _ := e.Dashboard(ctx, owner, "cached_count", 0); !cached {
		t.Error("a dashboard with cache: is no longer cached")
	}
}

// Card t_0214482d A2: DashboardWith returns the registry the result was computed with.
func TestDashboardWithRegistry(t *testing.T) {
	e, owner := approvalSetupFiles(t, map[string]string{"crm/dashboards/cached_count.rk": cachedCountDash}, "")
	_, _, reg, err := e.DashboardWith(context.Background(), owner, "cached_count", 0)
	if err != nil {
		t.Fatal(err)
	}
	if reg != e.Reg() || reg.Dashboard("cached_count") == nil {
		t.Fatal("DashboardWith did not return the registry it read")
	}
}
