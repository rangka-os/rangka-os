package engine

import (
	"context"
	"testing"
)

// A record that a live record still links (a field or a child row) is not deleted: 409 linked, so a later save
// or a flow creating from the referrer never fails link_not_found far from the delete (audit moka N3, card
// t_5a50f661). A self link, the record's own child rows and a cancelled referrer do not hold it.
func TestDeleteRefusesLinkedRecord(t *testing.T) {
	e, owner := approvalSetupFiles(t, memoHoldFiles, "")
	ctx := context.Background()
	mk := func(set map[string]any) string {
		res, err := e.Create(ctx, owner, "memo_self", set)
		if err != nil {
			t.Fatal(err)
		}
		return str(res.Record["name"])
	}
	// a draft linking itself from a field and a child row deletes
	self := mk(map[string]any{})
	if _, err := e.Update(ctx, owner, "memo_self", self, map[string]any{"prev": self, "lines": []any{map[string]any{"ref": self}}}); err != nil {
		t.Fatal(err)
	}
	if err := e.Delete(ctx, owner, "memo_self", self); err != nil {
		t.Fatalf("delete self-linked draft: %v", err)
	}

	target := mk(map[string]any{})
	byField := mk(map[string]any{"prev": target})
	if err := e.Delete(ctx, owner, "memo_self", target); !isCode(err, "linked") {
		t.Fatalf("delete linked by a draft field = %v, want 409 linked", err)
	}
	if err := e.Delete(ctx, owner, "memo_self", byField); err != nil {
		t.Fatal(err)
	}
	byLine := mk(map[string]any{"lines": []any{map[string]any{"ref": target}}})
	if err := e.Delete(ctx, owner, "memo_self", target); !isCode(err, "linked") {
		t.Fatalf("delete linked by a child row = %v, want 409 linked", err)
	}
	// a submitted referrer holds it too; once cancelled it does not
	sub, err := e.Submit(ctx, owner, "memo_self", byLine, false)
	if err != nil {
		t.Fatal(err)
	}
	fin := str(sub.Record["name"])
	if err := e.Delete(ctx, owner, "memo_self", target); !isCode(err, "linked") {
		t.Fatalf("delete linked by a submitted document = %v, want 409 linked", err)
	}
	if _, err := e.Cancel(ctx, owner, "memo_self", fin); err != nil {
		t.Fatal(err)
	}
	if err := e.Delete(ctx, owner, "memo_self", target); err != nil {
		t.Fatalf("delete linked only by a cancelled document: %v", err)
	}
	if _, err := e.Get(ctx, owner, "memo_self", target); err == nil {
		t.Fatal("record still there after delete")
	}
}
