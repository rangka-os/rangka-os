package engine

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Submitting a draft renames DRAFT-xxx; links to it (a field, a child row) follow, so they never point at a name
// that no longer exists (audit payroll 7 Nov).
func TestSubmitRenamesLinksToDraft(t *testing.T) {
	e, a := approvalSetupFiles(t, memoFiles, "")
	ctx := context.Background()
	res, err := e.Create(ctx, a, "memo_doc", map[string]any{"memo_key": "K-1"})
	if err != nil {
		t.Fatal(err)
	}
	draft := str(res.Record["name"])
	other, err := e.Create(ctx, a, "memo_doc", map[string]any{"memo_key": "K-2"})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := e.Create(ctx, a, "memo_ref", map[string]any{"doc": draft, "lines": []any{
		map[string]any{"doc": draft}, map[string]any{"doc": str(other.Record["name"])}}})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := e.Submit(ctx, a, "memo_doc", draft, false)
	if err != nil {
		t.Fatal(err)
	}
	final := str(sub.Record["name"])
	if final == draft {
		t.Fatalf("submit kept the draft name %s", draft)
	}
	got, err := e.Get(ctx, a, "memo_ref", str(ref.Record["name"]))
	if err != nil {
		t.Fatal(err)
	}
	if str(got["doc"]) != final {
		t.Fatalf("memo_ref.doc = %v, want %s", got["doc"], final)
	}
	lines := rows(got["lines"])
	if len(lines) != 2 || str(lines[0]["doc"]) != final || str(lines[1]["doc"]) != str(other.Record["name"]) {
		t.Fatalf("lines = %v, want first row %s and second untouched", lines, final)
	}
}

// memoHoldFiles add a submittable referrer whose submit and cancel wait for a sales manager, and a submittable
// model linking itself from a field and from a child row.
var memoHoldFiles = func() map[string]string {
	out := map[string]string{}
	for k, v := range memoFiles {
		out[k] = v
	}
	out["crm/models/memo_hold.rk"] = memoHeader + `name: memo_hold
label: {id: Memo tahan}
is_submittable: true
naming: MH-{#####}
fields:
  - {name: doc, type: link, to: memo_doc, label: {id: Memo}}
approvals:
  - {name: ajukan, on: submit, approvers: [sales_manager]}
  - {name: batal, on: cancel, approvers: [sales_manager]}
`
	out["crm/models/memo_two.rk"] = memoHeader + `name: memo_two
label: {id: Memo dua}
is_submittable: true
naming: MT-{#####}
fields:
  - {name: doc, type: link, to: memo_doc, label: {id: Memo}}
  - {name: note, type: data, label: {id: Catatan}}
approvals:
  - {name: p_mgr, on: submit, approvers: [sales_manager]}
  - {name: p_own, on: submit, approvers: [owner]}
`
	out["crm/models/memo_self.rk"] = memoHeader + `name: memo_self
label: {id: Memo berantai}
is_submittable: true
naming: SF-{#####}
fields:
  - {name: prev, type: link, to: memo_self, label: {id: Sebelumnya}}
  - {name: lines, type: table, of: memo_self_line, label: {id: Baris}}
`
	out["crm/models/memo_self_line.rk"] = memoHeader + `name: memo_self_line
is_child: true
fields:
  - {name: ref, type: link, to: memo_self, label: {id: Rujukan}}
`
	return out
}()

// A request held on a record that links a draft keeps its approval when the draft is submitted: the rename
// corrects the link, it is not a change of the document (audit kernel fix 1111 O1). Submit and cancel both.
func TestSubmitRenameKeepsReferrerApprovals(t *testing.T) {
	e, owner := approvalSetupFiles(t, memoHoldFiles, "")
	ctx := context.Background()
	newDraft := func(key string) string {
		res, err := e.Create(ctx, owner, "memo_doc", map[string]any{"memo_key": key})
		if err != nil {
			t.Fatal(err)
		}
		return str(res.Record["name"])
	}
	final := func(draft string) string {
		sub, err := e.Submit(ctx, owner, "memo_doc", draft, false)
		if err != nil {
			t.Fatal(err)
		}
		return str(sub.Record["name"])
	}

	// submit held, then the draft it links is submitted, then the manager approves
	d1 := newDraft("K-1")
	h, err := e.Create(ctx, rep, "memo_hold", map[string]any{"doc": d1})
	if err != nil {
		t.Fatal(err)
	}
	hn := str(h.Record["name"])
	held, err := e.Submit(ctx, rep, "memo_hold", hn, false)
	if err != nil || !held.Held {
		t.Fatalf("submit memo_hold: %+v %v", held, err)
	}
	f1 := final(d1)
	dec, err := e.Approve(ctx, mgr, held.Approvals[0].ID)
	if err != nil {
		t.Fatalf("approve after the linked draft was submitted: %v", err)
	}
	if str(dec.Record["doc"]) != f1 || toInt(dec.Record["docstatus"]) != 1 {
		t.Fatalf("approved memo_hold = %v, want doc %s and submitted", dec.Record, f1)
	}

	// cancel held on a submitted record that links a draft
	d2 := newDraft("K-2")
	h2, err := e.Create(ctx, rep, "memo_hold", map[string]any{"doc": d2})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := e.Submit(ctx, rep, "memo_hold", str(h2.Record["name"]), false)
	if err != nil || !s2.Held {
		t.Fatalf("submit memo_hold 2: %+v %v", s2, err)
	}
	d2rec, err := e.Approve(ctx, mgr, s2.Approvals[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	n2 := str(d2rec.Record["name"])
	c2, err := e.Cancel(ctx, rep, "memo_hold", n2)
	if err != nil || !c2.Held {
		t.Fatalf("cancel memo_hold: %+v %v", c2, err)
	}
	f2 := final(d2)
	dc, err := e.Approve(ctx, mgr, c2.Approvals[0].ID)
	if err != nil {
		t.Fatalf("approve cancel after the linked draft was submitted: %v", err)
	}
	if str(dc.Record["doc"]) != f2 || toInt(dc.Record["docstatus"]) != 2 {
		t.Fatalf("cancelled memo_hold = %v, want doc %s and cancelled", dc.Record, f2)
	}
}

// A record linking itself (a field, a child row) gets the final name in memory too, so on_submit hooks, flows
// and the caller see what is stored (audit kernel fix 1111 O3).
func TestSubmitRenamesSelfLinksInMemory(t *testing.T) {
	e, owner := approvalSetupFiles(t, memoHoldFiles, "")
	ctx := context.Background()
	res, err := e.Create(ctx, owner, "memo_self", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	draft := str(res.Record["name"])
	if _, err := e.Update(ctx, owner, "memo_self", draft, map[string]any{"prev": draft, "lines": []any{map[string]any{"ref": draft}}}); err != nil {
		t.Fatal(err)
	}
	sub, err := e.Submit(ctx, owner, "memo_self", draft, false)
	if err != nil {
		t.Fatal(err)
	}
	fin := str(sub.Record["name"])
	lines := rows(sub.Record["lines"])
	if str(sub.Record["prev"]) != fin || len(lines) != 1 || str(lines[0]["ref"]) != fin {
		t.Fatalf("submit returned prev=%v lines=%v, want %s", sub.Record["prev"], lines, fin)
	}
	got, err := e.Get(ctx, owner, "memo_self", fin)
	if err != nil {
		t.Fatal(err)
	}
	if str(got["prev"]) != fin || str(rows(got["lines"])[0]["ref"]) != fin {
		t.Fatalf("stored prev=%v lines=%v, want %s", got["prev"], got["lines"], fin)
	}
}

// An approval given for one content never carries another: a referrer edited by another transaction while the
// draft it links is submitted keeps its old snapshot on the manager's approved request, so a resubmit asks the
// manager again (audit kernel 1111b F1). An edit made before the submit is left alone too.
func TestSubmitRenameNeverRestampsAnEdit(t *testing.T) {
	e, owner := approvalSetupFiles(t, memoHoldFiles, "")
	ctx := context.Background()
	held := func(draft string) (string, string) {
		r, err := e.Create(ctx, rep, "memo_two", map[string]any{"doc": draft, "note": "harga 100"})
		if err != nil {
			t.Fatal(err)
		}
		name := str(r.Record["name"])
		h, err := e.Submit(ctx, rep, "memo_two", name, false)
		if err != nil || !h.Held {
			t.Fatalf("held: %+v %v", h, err)
		}
		for _, ap := range h.Approvals {
			if ap.Policy == "p_mgr" {
				if _, err := e.Approve(ctx, mgr, ap.ID); err != nil {
					t.Fatal(err)
				}
				return name, ap.ID
			}
		}
		t.Fatal("no p_mgr request")
		return "", ""
	}
	mgrAsked := func(name, oldID string) {
		t.Helper()
		h, err := e.Submit(ctx, rep, "memo_two", name, false)
		if err != nil || !h.Held {
			t.Fatalf("resubmit: %+v %v", h, err)
		}
		for _, ap := range h.Approvals {
			if ap.ID == oldID {
				t.Fatalf("the manager's approval of the old content was reused for the edited record")
			}
		}
	}
	newDraft := func(key string) string {
		d, err := e.Create(ctx, owner, "memo_doc", map[string]any{"memo_key": key})
		if err != nil {
			t.Fatal(err)
		}
		return str(d.Record["name"])
	}

	// race: tx B holds an edit of the referrer while tx A submits the draft it links
	d1 := newDraft("R-1")
	n1, p1 := held(d1)
	inB, release, doneB := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		doneB <- e.run(ctx, rep, func(o *op) error {
			if _, err := o.update("memo_two", n1, map[string]any{"note": "harga 999"}, true, true); err != nil {
				return err
			}
			close(inB)
			<-release
			return nil
		})
	}()
	<-inB
	doneA := make(chan error, 1)
	go func() {
		_, err := e.Submit(ctx, owner, "memo_doc", d1, false)
		doneA <- err
	}()
	time.Sleep(300 * time.Millisecond) // A reads the referrer, then waits on B's row lock in the rename
	close(release)
	if err := <-doneB; err != nil {
		t.Fatal(err)
	}
	if err := <-doneA; err != nil {
		t.Fatal(err)
	}
	mgrAsked(n1, p1)

	// in order: the edit commits first, then the draft is submitted
	d2 := newDraft("R-2")
	n2, p2 := held(d2)
	if _, err := e.Update(ctx, rep, "memo_two", n2, map[string]any{"note": "harga 999"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Submit(ctx, owner, "memo_doc", d2, false); err != nil {
		t.Fatal(err)
	}
	mgrAsked(n2, p2)
}

// store.RecordsLinking finds exactly the records whose field or child row links the draft, so submit loads only
// their live requests (card t_6e1fa9ae): an unrelated record of a referrer model is not looked at.
func TestRecordsLinkingOnlyTheDraft(t *testing.T) {
	e, owner := approvalSetupFiles(t, memoHoldFiles, "")
	ctx := context.Background()
	mk := func() string {
		res, err := e.Create(ctx, owner, "memo_self", map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		return str(res.Record["name"])
	}
	draft, byField, byLine, other := mk(), mk(), mk(), mk()
	if _, err := e.Update(ctx, owner, "memo_self", byField, map[string]any{"prev": draft}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Update(ctx, owner, "memo_self", byLine, map[string]any{"lines": []any{map[string]any{"ref": other}, map[string]any{"ref": draft}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Update(ctx, owner, "memo_self", other, map[string]any{"prev": byField, "lines": []any{map[string]any{"ref": byLine}}}); err != nil {
		t.Fatal(err)
	}
	got, err := store.RecordsLinking(ctx, e.DB, e.Reg(), owner.Org, e.Reg().Model("memo_self"), "memo_self", draft)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{byField, byLine}
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("RecordsLinking = %v, want %v", got, want)
	}
	if got, _ := store.RecordsLinking(ctx, e.DB, e.Reg(), "org-lain", e.Reg().Model("memo_self"), "memo_self", draft); len(got) != 0 {
		t.Fatalf("other org sees %v", got)
	}
}
