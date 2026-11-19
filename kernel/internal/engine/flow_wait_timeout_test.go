package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// timedQueue records each job with the time it is due.
type timedQueue struct{ jobs []timedJob }

type timedJob struct {
	args  map[string]any
	runAt time.Time
}

func (q *timedQueue) Enqueue(_ context.Context, _ pg.Querier, _, _ string, args map[string]any, opts EnqueueOpts) (int64, error) {
	q.jobs = append(q.jobs, timedJob{args: roundTrip(args).(map[string]any), runAt: opts.RunAt})
	return int64(len(q.jobs)), nil
}

func (q *timedQueue) last(t *testing.T) timedJob {
	t.Helper()
	if len(q.jobs) == 0 {
		t.Fatal("no job queued")
	}
	return q.jobs[len(q.jobs)-1]
}

// A wait: {until, timeout} step re-checks its condition every minute until it holds or the deadline set when the
// wait started passes; then the run ends (flow.timed_out, idempotency key released) and the steps after the wait
// never run. Without timeout the wait has no end.
func TestFlowWaitUntilTimeout(t *testing.T) {
	files := map[string]string{}
	for k, v := range memoFiles {
		files[k] = v
	}
	// the wait is the second step: a re-check resumes at its own index, and a resume at index 0 is taken for a
	// fresh run (it claims the idempotency key again), a separate issue
	flow := func(name, wait string) string {
		return `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: ` + name + `
label: {id: Memo saat siap}
idempotency: event.ref
steps:
  - wait: 0s
  - wait: ` + wait + `
  - create: memo_doc
    set: {memo_key: "''"}
`
	}
	files["crm/flows/memo_when_ready.rk"] = flow("memo_when_ready", `{until: "event.ready", timeout: 3m}`)
	files["crm/flows/memo_ready_forever.rk"] = flow("memo_ready_forever", `{until: "event.ready"}`)
	e, owner := approvalSetupFiles(t, files, "")
	q := &timedQueue{}
	e.Queue = q
	t0 := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	clock := t0
	e.Now = func() time.Time { return clock }
	ctx := context.Background()
	ev := func(ref string, ready bool) map[string]any { return map[string]any{"ref": ref, "ready": ready} }
	start := func(flow, ref string) *FlowResult {
		t.Helper()
		out, err := e.RunFlow(ctx, owner, flow, ev(ref, false))
		if err != nil {
			t.Fatalf("%s %s: %v", flow, ref, err)
		}
		return out
	}
	// resume runs a queued job the way the server does: args as stored (JSON), deadline read from them
	resume := func(flow string, j timedJob, ready bool) *FlowResult {
		t.Helper()
		ref := str(meta.AsMap(j.args["event"])["ref"])
		out, err := e.RunFlowResume(ctx, owner, flow, ev(ref, ready), int(j.args["resume"].(float64)), meta.AsMap(j.args["vars"]), j.args["manual"] == true, meta.AsMap(j.args["flow_key"]), WaitDeadline(j.args))
		if err != nil {
			t.Fatalf("resume %s: %v", flow, err)
		}
		return out
	}
	memos := func() int {
		t.Helper()
		recs, err := store.All(ctx, e.DB, e.Reg(), owner.Org, e.Reg().Model("memo_doc"), false)
		if err != nil {
			t.Fatal(err)
		}
		return len(recs)
	}
	deadline := t0.Add(3 * time.Minute)

	// the condition holds before the deadline: the run goes on
	clock = t0
	if out := start("memo_when_ready", "A"); out.WaitingJob == 0 {
		t.Fatalf("did not wait: %+v", out)
	}
	j := q.last(t)
	if got := WaitDeadline(j.args); !got.Equal(deadline) {
		t.Fatalf("deadline %v, want %v (args %v)", got, deadline, j.args)
	}
	if !j.runAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("first re-check at %v", j.runAt)
	}
	clock = t0.Add(time.Minute)
	before := memos()
	if out := resume("memo_when_ready", j, true); out.TimedOut || out.WaitingJob != 0 || memos() != before+1 {
		t.Fatalf("condition met before the deadline: %+v, memos %d -> %d", out, before, memos())
	}

	// the deadline passes: the run ends, nothing after the wait runs, the same event can run again
	clock = t0
	start("memo_when_ready", "B")
	j = q.last(t)
	clock = t0.Add(time.Minute)
	if out := resume("memo_when_ready", j, false); out.WaitingJob == 0 || out.TimedOut {
		t.Fatalf("re-check before the deadline: %+v", out)
	}
	j = q.last(t)
	if got := WaitDeadline(j.args); !got.Equal(deadline) {
		t.Fatalf("the re-check moved the deadline: %v, want %v", got, deadline)
	}
	clock = t0.Add(2*time.Minute + 30*time.Second)
	resume("memo_when_ready", j, false)
	j = q.last(t)
	if !j.runAt.Equal(deadline) {
		t.Fatalf("last re-check at %v, want the deadline %v", j.runAt, deadline)
	}
	clock = deadline
	jobs := len(q.jobs)
	before = memos()
	out := resume("memo_when_ready", j, false)
	if !out.TimedOut || out.WaitingJob != 0 || len(q.jobs) != jobs || memos() != before {
		t.Fatalf("at the deadline: %+v, jobs %d -> %d, memos %d -> %d", out, jobs, len(q.jobs), before, memos())
	}
	if len(out.Steps) == 0 || !strings.Contains(out.Steps[len(out.Steps)-1], "timed out") {
		t.Fatalf("steps: %v", out.Steps)
	}
	got, err := store.ListAudit(ctx, e.DB, store.AuditFilter{Orgs: []string{owner.Org}, Kind: "flow.timed_out"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Target != "flow/memo_when_ready" || !strings.Contains(got[0].Summary, "3m") {
		t.Fatalf("flow.timed_out audit: %+v", got)
	}
	if again := start("memo_when_ready", "B"); again.Skipped || again.WaitingJob == 0 {
		t.Fatalf("same event after the timed-out run: %+v", again)
	}

	// without timeout the wait never ends (the old behaviour)
	clock = t0
	start("memo_ready_forever", "C")
	j = q.last(t)
	if _, has := j.args[waitDeadlineArg]; has {
		t.Fatalf("wait without timeout has a deadline: %v", j.args)
	}
	clock = t0.Add(30 * 24 * time.Hour)
	if out := resume("memo_ready_forever", j, false); out.TimedOut || out.WaitingJob == 0 || !q.last(t).runAt.Equal(clock.Add(time.Minute)) {
		t.Fatalf("wait without timeout after 30 days: %+v", out)
	}
	before = memos()
	if out := resume("memo_ready_forever", q.last(t), true); out.TimedOut || memos() != before+1 {
		t.Fatalf("wait without timeout, condition met: %+v", out)
	}
}

func TestWaitDeadlineArg(t *testing.T) {
	if !WaitDeadline(nil).IsZero() || !WaitDeadline(map[string]any{waitDeadlineArg: "nope"}).IsZero() {
		t.Fatal("missing or bad wait_deadline must be zero")
	}
	want := time.Date(2026, 11, 19, 9, 3, 0, 0, time.UTC)
	if got := WaitDeadline(map[string]any{waitDeadlineArg: want.Format(time.RFC3339Nano)}); !got.Equal(want) {
		t.Fatalf("got %v", got)
	}
}
