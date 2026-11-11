package engine

import (
	"context"
	"testing"
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
