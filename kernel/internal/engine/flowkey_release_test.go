package engine

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// memoFromRef: a flow that drafts a memo for an outside event, deduped by the event's ref (like a payment from a
// gateway callback).
var memoFromRefFiles = func() map[string]string {
	out := map[string]string{}
	for k, v := range memoFiles {
		out[k] = v
	}
	out["crm/flows/memo_from_ref.rk"] = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: memo_from_ref
label: {id: Memo dari kejadian luar}
idempotency: event.ref
steps:
  - create: memo_doc
    set: {memo_key: "''"}
`
	return out
}()

// An idempotency key is held while a document its run made is alive; once every one is deleted or cancelled the same
// event is recorded again, still once however many arrive together (contract 0002 §A.2; rangka-apps Xendit
// callbacks after a deleted or cancelled draft, regression of rangka-os#137).
func TestFlowKeyFreedWhenDocumentsGone(t *testing.T) {
	e, owner := approvalSetupFiles(t, memoFromRefFiles, "")
	ctx := context.Background()
	ev := map[string]any{"ref": "GW-1"}
	run := func() *FlowResult {
		t.Helper()
		out, err := e.RunFlow(ctx, owner, "memo_from_ref", ev)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	live := func() []string {
		t.Helper()
		recs, err := store.All(ctx, e.DB, e.Reg(), owner.Org, e.Reg().Model("memo_doc"), false)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range recs {
			if toInt(r["docstatus"]) != 2 {
				out = append(out, str(r["name"]))
			}
		}
		return out
	}
	first := run()
	if first.Skipped || len(first.Created) != 1 {
		t.Fatalf("first run: %+v", first)
	}
	if again := run(); !again.Skipped || len(live()) != 1 {
		t.Fatalf("same event while the draft is alive: %+v, live %v", again, live())
	}
	// the draft is deleted: the event records again
	if err := e.Delete(ctx, owner, "memo_doc", live()[0]); err != nil {
		t.Fatal(err)
	}
	if again := run(); again.Skipped || len(live()) != 1 {
		t.Fatalf("same event after the delete: %+v, live %v", again, live())
	}
	// submitted (renamed), it still holds the key
	sub, err := e.Submit(ctx, owner, "memo_doc", live()[0], false)
	if err != nil {
		t.Fatal(err)
	}
	num := str(sub.Record["name"])
	if again := run(); !again.Skipped {
		t.Fatalf("same event after the submit: %+v", again)
	}
	// cancelled: free again
	if _, err := e.Cancel(ctx, owner, "memo_doc", num); err != nil {
		t.Fatal(err)
	}
	if again := run(); again.Skipped || len(live()) != 1 {
		t.Fatalf("same event after the cancel: %+v, live %v", again, live())
	}
	// deleted again, six at once: exactly one new draft
	if err := e.Delete(ctx, owner, "memo_doc", live()[0]); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.RunFlow(ctx, owner, "memo_from_ref", ev); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("parallel run: %v", err)
	}
	if n := len(live()); n != 1 {
		t.Fatalf("six parallel runs after the delete: %d live drafts, want 1 (%v)", n, live())
	}
}

// A run that waits for an approval holds its key until it finishes, and the documents it makes after the approval
// hold it too (audit flowkey-release 1); a cancelled document whose amended copy is alive holds it as well (2).
func TestFlowKeyHeldAcrossApprovalAndAmend(t *testing.T) {
	files := map[string]string{}
	for k, v := range memoFromRefFiles {
		files[k] = v
	}
	files["crm/flows/memo_after_ok.rk"] = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: memo_after_ok
label: {id: Memo setelah disetujui}
idempotency: event.ref
steps:
  - create: memo_ref
    set: {doc: "''"}
  - approve: {label: {id: Setujui memo}, approvers: [owner]}
  - create: memo_doc
    set: {memo_key: "''"}
`
	e, owner := approvalSetupFiles(t, files, "")
	ctx := context.Background()
	count := func(model string) int {
		t.Helper()
		recs, err := store.All(ctx, e.DB, e.Reg(), owner.Org, e.Reg().Model(model), false)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, r := range recs {
			if toInt(r["docstatus"]) != 2 {
				n++
			}
		}
		return n
	}
	ev := map[string]any{"ref": "OK-1"}
	res, err := e.RunFlow(ctx, owner, "memo_after_ok", ev)
	if err != nil || res.WaitingApproval == "" {
		t.Fatalf("first run: %+v %v", res, err)
	}
	// while it waits, the memo_ref it made is deleted: the run still holds the key
	refs, _ := store.All(ctx, e.DB, e.Reg(), owner.Org, e.Reg().Model("memo_ref"), false)
	if err := e.Delete(ctx, owner, "memo_ref", str(refs[0]["name"])); err != nil {
		t.Fatal(err)
	}
	if again, err := e.RunFlow(ctx, owner, "memo_after_ok", ev); err != nil || !again.Skipped {
		t.Fatalf("same event while the run waits: %+v %v", again, err)
	}
	if _, err := e.Approve(ctx, owner, res.WaitingApproval); err != nil {
		t.Fatal(err)
	}
	if count("memo_doc") != 1 {
		t.Fatalf("memo_doc after the approval: %d", count("memo_doc"))
	}
	// the memo_doc made after the approval holds the key
	if again, err := e.RunFlow(ctx, owner, "memo_after_ok", ev); err != nil || !again.Skipped {
		t.Fatalf("same event after the approved run: %+v %v", again, err)
	}

	// cancel, then amend: the live copy is the same document
	first, err := e.RunFlow(ctx, owner, "memo_from_ref", map[string]any{"ref": "AM-1"})
	if err != nil || len(first.Created) != 1 {
		t.Fatalf("memo_from_ref: %+v %v", first, err)
	}
	_, draft, _ := strings.Cut(first.Created[0], "/")
	sub, err := e.Submit(ctx, owner, "memo_doc", draft, false)
	if err != nil {
		t.Fatal(err)
	}
	num := str(sub.Record["name"])
	if _, err := e.Cancel(ctx, owner, "memo_doc", num); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Amend(ctx, owner, "memo_doc", num); err != nil {
		t.Fatal(err)
	}
	if again, err := e.RunFlow(ctx, owner, "memo_from_ref", map[string]any{"ref": "AM-1"}); err != nil || !again.Skipped {
		t.Fatalf("same event while the amended copy is alive: %+v %v", again, err)
	}
}
