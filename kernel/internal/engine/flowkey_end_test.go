package engine

import (
	"context"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// A run that never finishes frees its idempotency key: a rejected approval, a job dead-lettered for good. A replay
// of the dead job is skipped, and the key is no flow variable a step could overwrite (audit
// flowkey-release putaran 2 N1, N2; card t_aa8d76cd).
func TestFlowKeyReleasedWhenRunEnds(t *testing.T) {
	files := map[string]string{}
	for k, v := range memoFromRefFiles {
		files[k] = v
	}
	files["crm/flows/memo_ok_first.rk"] = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: memo_ok_first
label: {id: Setujui dulu}
idempotency: event.ref
steps:
  - approve: {label: {id: Setujui memo}, approvers: [owner]}
  - create: memo_doc
    set: {memo_key: "''"}
`
	files["crm/flows/memo_ref_then_ok.rk"] = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: memo_ref_then_ok
label: {id: Rujukan lalu setujui}
idempotency: event.ref
steps:
  - create: memo_ref
    set: {doc: "''"}
  - approve: {label: {id: Setujui memo}, approvers: [owner]}
`
	files["crm/flows/memo_later.rk"] = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: memo_later
label: {id: Memo nanti}
idempotency: event.ref
steps:
  - wait: 1m
  - create: memo_doc
    set: {memo_key: "''"}
`
	e, owner := approvalSetupFiles(t, files, "")
	q := &recordingQueue{}
	e.Queue = q
	ctx := context.Background()
	ev := func(ref string) map[string]any { return map[string]any{"ref": ref} }
	run := func(flow, ref string) *FlowResult {
		t.Helper()
		out, err := e.RunFlow(ctx, owner, flow, ev(ref))
		if err != nil {
			t.Fatalf("%s %s: %v", flow, ref, err)
		}
		return out
	}

	// rejected before the run made anything: the same event runs again
	first := run("memo_ok_first", "R-1")
	if first.WaitingApproval == "" {
		t.Fatalf("first run did not wait: %+v", first)
	}
	if again := run("memo_ok_first", "R-1"); !again.Skipped {
		t.Fatalf("same event while waiting: %+v", again)
	}
	if _, err := e.Reject(ctx, owner, first.WaitingApproval, "salah kirim"); err != nil {
		t.Fatal(err)
	}
	second := run("memo_ok_first", "R-1")
	if second.Skipped || second.WaitingApproval == "" {
		t.Fatalf("same event after the reject: %+v", second)
	}
	if _, err := e.Approve(ctx, owner, second.WaitingApproval); err != nil {
		t.Fatal(err)
	}
	if again := run("memo_ok_first", "R-1"); !again.Skipped {
		t.Fatalf("same event after the approved run: %+v", again)
	}

	// rejected after the run made a memo_ref: that document still holds the key until it is gone
	r := run("memo_ref_then_ok", "R-2")
	if _, err := e.Reject(ctx, owner, r.WaitingApproval, "tidak jadi"); err != nil {
		t.Fatal(err)
	}
	if again := run("memo_ref_then_ok", "R-2"); !again.Skipped {
		t.Fatalf("same event while the rejected run's memo_ref lives: %+v", again)
	}
	refs, _ := store.All(ctx, e.DB, e.Reg(), owner.Org, e.Reg().Model("memo_ref"), false)
	for _, x := range refs {
		if err := e.Delete(ctx, owner, "memo_ref", str(x["name"])); err != nil {
			t.Fatal(err)
		}
	}
	if again := run("memo_ref_then_ok", "R-2"); again.Skipped {
		t.Fatalf("same event after the memo_ref was deleted: %+v", again)
	}

	// a wait job that fails for good releases the key; its replay is skipped
	memos := func() int {
		t.Helper()
		recs, err := store.All(ctx, e.DB, e.Reg(), owner.Org, e.Reg().Model("memo_doc"), false)
		if err != nil {
			t.Fatal(err)
		}
		return len(recs)
	}
	w := run("memo_later", "R-3")
	if w.WaitingJob == 0 {
		t.Fatalf("memo_later did not wait: %+v", w)
	}
	deadArgs := roundTrip(q.jobs[len(q.jobs)-1]["args"]).(map[string]any)
	if _, leak := meta.AsMap(deadArgs["vars"])[flowKeyVar]; leak {
		t.Fatalf("the key is in the flow variables: %v", deadArgs["vars"])
	}
	if again := run("memo_later", "R-3"); !again.Skipped {
		t.Fatalf("same event while the job waits: %+v", again)
	}
	if err := e.ReleaseFlowJob(ctx, owner.Org, deadArgs); err != nil {
		t.Fatal(err)
	}
	w2 := run("memo_later", "R-3")
	if w2.Skipped || w2.WaitingJob == 0 {
		t.Fatalf("same event after the dead job: %+v", w2)
	}
	liveArgs := roundTrip(q.jobs[len(q.jobs)-1]["args"]).(map[string]any)
	resume := func(args map[string]any) *FlowResult {
		t.Helper()
		out, err := e.RunFlowResume(ctx, owner, "memo_later", meta.AsMap(args["event"]), int(args["resume"].(float64)), meta.AsMap(args["vars"]), args["manual"] == true, meta.AsMap(args["flow_key"]))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := memos()
	if out := resume(deadArgs); !out.Skipped || memos() != before {
		t.Fatalf("replay of the dead job: %+v, memos %d -> %d", out, before, memos())
	}
	if out := resume(liveArgs); out.Skipped || memos() != before+1 {
		t.Fatalf("live job resume: %+v, memos %d -> %d", out, before, memos())
	}
	// the document of the resumed run holds the key
	if again := run("memo_later", "R-3"); !again.Skipped {
		t.Fatalf("same event after the resumed run made its memo: %+v", again)
	}
}
