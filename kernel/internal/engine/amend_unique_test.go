package engine

import (
	"context"
	"errors"
	"testing"
)

// Amending a cancelled document whose unique value is still held (the cancelled original keeps it) is a
// validation error (422 unique), not a database error answered 500 (audit payroll 7 Nov, temuan 9).
func TestAmendUniqueViolationIsValidation(t *testing.T) {
	e, a := approvalSetupFiles(t, memoFiles, "")
	ctx := context.Background()
	res, err := e.Create(ctx, a, "memo_doc", map[string]any{"memo_key": "RUN-1:P-1"})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := e.Submit(ctx, a, "memo_doc", str(res.Record["name"]), false)
	if err != nil {
		t.Fatal(err)
	}
	name := str(sub.Record["name"])
	if _, err := e.Cancel(ctx, a, "memo_doc", name); err != nil {
		t.Fatal(err)
	}
	_, err = e.Amend(ctx, a, "memo_doc", name)
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Errors) != 1 || ve.Errors[0].Code != "unique" || ve.Errors[0].Field != "memo_key" {
		t.Fatalf("amend over a held unique value = %v, want a unique issue on memo_key", err)
	}
}
