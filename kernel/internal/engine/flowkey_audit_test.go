package engine

import (
	"context"
	"strings"
	"testing"
)

func bumpFlowFiles() map[string]string {
	files := map[string]string{}
	for k, v := range memoFiles {
		files[k] = v
	}
	files["crm/models/memo_count.rk"] = memoHeader + `name: memo_count
label: {id: Hitung}
naming: CNT-{#####}
fields:
  - {name: qty, type: int, label: {id: Jumlah}}
`
	files["crm/flows/memo_bump_ok.rk"] = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: memo_bump_ok
label: {id: Tambah lalu setujui}
idempotency: event.ref
steps:
  - {find: memo_count, where: "name == event.target", as: c}
  - update: c
    set: {qty: c.qty + 1}
  - approve: {label: {id: Setujui}, approvers: [owner]}
`
	files["crm/flows/memo_bump_wait.rk"] = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: memo_bump_wait
label: {id: Tambah lalu tunggu}
idempotency: event.ref
steps:
  - {find: memo_count, where: "name == event.target", as: c}
  - update: c
    set: {qty: c.qty + 1}
  - wait: 1m
  - notify: owner
    channel: email
    template: {id: 'selesai'}
`
	return files
}

// A run that updated a record before it waited keeps its key when it is released (rejected, job dead): the same
// event must not apply the update twice (audit kernel-aman 1).
func TestFlowKeyKeptWhenRunActedBeforeWaiting(t *testing.T) {
	e, owner := approvalSetupFiles(t, bumpFlowFiles(), "")
	q := &recordingQueue{}
	e.Queue = q
	ctx := context.Background()
	c, err := e.Create(ctx, owner, "memo_count", map[string]any{"qty": 0})
	if err != nil {
		t.Fatal(err)
	}
	cn := str(c.Record["name"])
	n := func() int64 {
		r, err := e.Get(ctx, owner, "memo_count", cn)
		if err != nil {
			t.Fatal(err)
		}
		return int64(toInt(r["qty"]))
	}
	ev := map[string]any{"ref": "PAY-1", "target": cn}
	first, err := e.RunFlow(ctx, owner, "memo_bump_ok", ev)
	if err != nil || first.WaitingApproval == "" {
		t.Fatalf("first: %+v %v", first, err)
	}
	if dup, _ := e.RunFlow(ctx, owner, "memo_bump_ok", ev); !dup.Skipped {
		t.Fatalf("dup while waiting not skipped")
	}
	if _, err := e.Reject(ctx, owner, first.WaitingApproval, "tolak"); err != nil {
		t.Fatal(err)
	}
	again, err := e.RunFlow(ctx, owner, "memo_bump_ok", ev)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Skipped {
		t.Errorf("same event (ref PAY-1) applied its update twice: n=%d, want 1", n())
	}

	ev2 := map[string]any{"ref": "PAY-2", "target": cn}
	before := n()
	w, err := e.RunFlow(ctx, owner, "memo_bump_wait", ev2)
	if err != nil || w.WaitingJob == 0 {
		t.Fatalf("wait: %+v %v", w, err)
	}
	dead := roundTrip(q.jobs[len(q.jobs)-1]["args"]).(map[string]any)
	if err := e.ReleaseFlowJob(ctx, owner.Org, dead); err != nil {
		t.Fatal(err)
	}
	again2, err := e.RunFlow(ctx, owner, "memo_bump_wait", ev2)
	if err != nil {
		t.Fatal(err)
	}
	if !again2.Skipped {
		t.Errorf("same event (ref PAY-2) applied its update twice: n=%d, want %d", n(), before+1)
	}
}

// A child model shared by two parents with the same table field name: only the parent that holds the row counts,
// so a cancelled referrer does not block the delete (audit kernel-aman 3).
func TestDeleteSharedChildCountsItsOwnParent(t *testing.T) {
	files := map[string]string{}
	for k, v := range memoFiles {
		files[k] = v
	}
	files["crm/models/sh_line.rk"] = memoHeader + `name: sh_line
is_child: true
fields:
  - {name: doc, type: link, to: memo_doc, label: {id: Memo}}
`
	files["crm/models/sh_plain.rk"] = memoHeader + `name: sh_plain
label: {id: Catatan biasa}
naming: SP-{#####}
fields:
  - {name: lines, type: table, of: sh_line, label: {id: Baris}}
`
	files["crm/models/sh_sub.rk"] = memoHeader + `name: sh_sub
label: {id: Dokumen bersama}
is_submittable: true
naming: SS-{#####}
fields:
  - {name: lines, type: table, of: sh_line, label: {id: Baris}}
`
	e, owner := approvalSetupFiles(t, files, "")
	ctx := context.Background()
	d, err := e.Create(ctx, owner, "memo_doc", map[string]any{"memo_key": "SH-1"})
	if err != nil {
		t.Fatal(err)
	}
	dn := str(d.Record["name"])
	s, err := e.Create(ctx, owner, "sh_sub", map[string]any{"lines": []any{map[string]any{"doc": dn}}})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := e.Submit(ctx, owner, "sh_sub", str(s.Record["name"]), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Cancel(ctx, owner, "sh_sub", str(sub.Record["name"])); err != nil {
		t.Fatal(err)
	}
	err = e.Delete(ctx, owner, "memo_doc", dn)
	if err != nil {
		t.Errorf("delete refused though the only referrer is cancelled: %v", err)
	}
}

// The 409 names the referrer only to someone who may read it (audit kernel-aman 4).
func TestDeleteLinkedHidesUnreadableReferrer(t *testing.T) {
	files := map[string]string{}
	for k, v := range memoFiles {
		files[k] = v
	}
	files["crm/models/memo_secret.rk"] = memoHeader + `name: memo_secret
label: {id: Memo rahasia}
naming: RS-{#####}
fields:
  - {name: doc, type: link, to: memo_doc, label: {id: Memo}}
permissions:
  - {role: owner, read: true, create: true, write: true, delete: true}
`
	e, owner := approvalSetupFiles(t, files, "")
	ctx := context.Background()
	d, err := e.Create(ctx, owner, "memo_doc", map[string]any{"memo_key": "RS-1"})
	if err != nil {
		t.Fatal(err)
	}
	dn := str(d.Record["name"])
	s, err := e.Create(ctx, owner, "memo_secret", map[string]any{"doc": dn})
	if err != nil {
		t.Fatal(err)
	}
	sn := str(s.Record["name"])
	mgr := Actor{Org: owner.Org, ID: "mgr@example.test", UserName: "USR-MGR", Roles: []string{"sales_manager"}, Human: true}
	if _, err := e.Get(ctx, mgr, "memo_secret", sn); err == nil {
		t.Fatal("the test needs a referrer the manager cannot read")
	}
	err = e.Delete(ctx, mgr, "memo_doc", dn)
	if !isCode(err, "linked") || strings.Contains(err.Error(), sn) || strings.Contains(err.Error(), "rahasia") {
		t.Fatalf("delete by a member who cannot read the referrer = %v, want 409 linked without its name", err)
	}
	if err := e.Delete(ctx, owner, "memo_doc", dn); !isCode(err, "linked") || !strings.Contains(err.Error(), sn) {
		t.Fatalf("delete by the owner = %v, want 409 linked naming %s", err, sn)
	}
}

// A non-owner manual run of a .cancelled flow for a document that is not cancelled is refused (audit kernel-aman 2).
func TestManualModelEventFlowChecksState(t *testing.T) {
	files := map[string]string{}
	for k, v := range memoFiles {
		files[k] = v
	}
	files["crm/flows/memo_on_cancel.rk"] = `# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.5/flow.json
name: memo_on_cancel
label: {id: Memo batal}
on: memo_doc.cancelled
steps:
  - create: memo_ref
    set: {doc: event.name}
  - notify: owner
    channel: email
    template: {id: 'op {event.op} docstatus {event.docstatus}'}
`
	e, owner := approvalSetupFiles(t, files, "")
	ctx := context.Background()
	d, err := e.Create(ctx, owner, "memo_doc", map[string]any{"memo_key": "OP-1"})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := e.Submit(ctx, owner, "memo_doc", str(d.Record["name"]), false)
	if err != nil {
		t.Fatal(err)
	}
	mgr := Actor{Org: owner.Org, ID: "mgr@example.test", UserName: "USR-MGR", Roles: []string{"sales_manager"}, Human: true}
	out, err := e.RunFlow(ctx, mgr, "memo_on_cancel", map[string]any{"name": str(sub.Record["name"])})
	if !isCode(err, "event_state") {
		t.Fatalf("a .cancelled flow for a submitted document = %+v %v, want 409 event_state", out, err)
	}
}
