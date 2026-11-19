package server

import (
	"context"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// On a host with two businesses, the scheduled flow (crm morning_follow_up, 08:00 Jakarta on weekdays) is enqueued
// and run once for each of them, not only for the default org (rangka-os#263 slice a).
func TestScheduledFlowRunsForEveryOrg(t *testing.T) {
	e := newEnv(t, false, nil)
	ctx := pg.WithOrg(context.Background(), "")
	if err := auth.EnsureOrg(ctx, e.s.DB, "toko-dua", "Toko Dua"); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Jakarta")
	e.s.Sched.SetNow(func() time.Time { return time.Date(2026, 10, 30, 7, 59, 0, 0, loc) })
	e.s.Sched.SetFlows(e.s.Engine.Reg(), "kopi-senja")
	e.s.Sched.Tick(ctx)
	e.s.Sched.SetNow(func() time.Time { return time.Date(2026, 10, 30, 8, 0, 30, 0, loc) })
	e.s.Sched.Tick(ctx)
	e.s.Sched.Tick(ctx)

	states := map[string][]string{}
	for i := 0; i < 200; i++ {
		res, err := e.s.DB.Query(ctx, `select org_id, state from rk_jobs where kind = 'flow' and args->>'flow' = 'morning_follow_up'
			order by org_id, id`)
		if err != nil {
			t.Fatal(err)
		}
		states = map[string][]string{}
		done := len(res.Rows) > 0
		for _, r := range res.Rows {
			org, st := r[0].(string), r[1].(string)
			states[org] = append(states[org], st)
			if st != "completed" {
				done = false
			}
		}
		if done && len(states) == 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, org := range []string{"kopi-senja", "toko-dua"} {
		if got := states[org]; len(got) != 1 || got[0] != "completed" {
			t.Fatalf("org %s: scheduled flow jobs %v, want one completed (all: %v)", org, got, states)
		}
	}
	if st := e.s.Sched.Status(ctx, "toko-dua"); len(st) != 1 || st[0]["last_status"] != "completed" {
		t.Fatalf("status of toko-dua = %v", st)
	}
}
