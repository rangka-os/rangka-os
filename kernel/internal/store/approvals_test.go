package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

func TestApprovalStore(t *testing.T) {
	ctx := context.Background()
	db, err := pg.NewPool(ctx, testdb.New(t), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ctx, systemSQL); err != nil {
		t.Fatal(err)
	}
	mk := func(model, name string) *Approval {
		return &Approval{Org: "o1", Model: model, Name: name, Verb: "submit", Policy: "big", Label: map[string]any{"id": "Besar"},
			Approvers: []string{"manager", "owner"}, State: "pending", Snapshot: "abc", Args: map[string]any{"force": true},
			RequestedBy: "u1", Requester: map[string]any{"id": "u1", "user": "ani", "roles": []any{"sales"}}}
	}
	a1, a2, a3 := mk("sales_invoice", "DRAFT-X"), mk("sales_invoice", "DRAFT-X"), mk("sales_invoice", "DRAFT-Y")
	for _, a := range []*Approval{a1, a2, a3} {
		if err := InsertApproval(ctx, db, a); err != nil {
			t.Fatal(err)
		}
		if len(a.ID) != 4+12 || a.ID[:4] != "apr_" || a.RequestedAt.IsZero() {
			t.Fatalf("insert did not fill id/time: %+v", a)
		}
	}
	rows, total, err := ListApprovals(ctx, db, ApprovalFilter{Org: "o1", State: "pending", Model: "sales_invoice", Name: "DRAFT-X"})
	if err != nil || len(rows) != 2 || total != 2 {
		t.Fatalf("list = %d rows total %d err %v", len(rows), total, err)
	}
	if _, total, _ = ListApprovals(ctx, db, ApprovalFilter{Org: "o2"}); total != 0 {
		t.Fatalf("other org sees %d", total)
	}
	if rows, total, _ = ListApprovals(ctx, db, ApprovalFilter{Org: "o1", Limit: 1}); len(rows) != 1 || total != 3 {
		t.Fatalf("limit: %d rows total %d", len(rows), total)
	}
	got, err := GetApproval(ctx, db, "o1", a1.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Approvers, a1.Approvers) || !reflect.DeepEqual(got.Label, a1.Label) ||
		got.Args["force"] != true || got.Requester["user"] != "ani" || got.Policy != "big" || got.Verb != "submit" || got.Snapshot != "abc" {
		t.Fatalf("round trip: %+v", got)
	}
	if rs, _ := got.Requester["roles"].([]any); len(rs) != 1 || rs[0] != "sales" {
		t.Fatalf("requester roles: %v", got.Requester)
	}
	at := time.Now().UTC().Truncate(time.Second)
	if err := DecideApproval(ctx, db, "o1", a1.ID, "approved", "boss@x", "", at); err != nil {
		t.Fatal(err)
	}
	got, _ = GetApproval(ctx, db, "o1", a1.ID, false)
	if got.State != "approved" || got.DecidedBy != "boss@x" || got.DecidedAt == nil || !got.DecidedAt.Equal(at) {
		t.Fatalf("decided: %+v", got)
	}
	voided, err := VoidApprovals(ctx, db, "o1", "sales_invoice", "DRAFT-X", "")
	if err != nil || len(voided) != 1 || voided[0].ID != a2.ID || voided[0].State != "void" {
		t.Fatalf("void = %+v %v", voided, err)
	}
	if v, _ := VoidApprovals(ctx, db, "o1", "sales_invoice", "DRAFT-Y", a3.ID); len(v) != 0 {
		t.Fatalf("exceptID not honoured: %+v", v)
	}
	if _, err := GetApproval(ctx, db, "o1", "apr_nope", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	if _, err := GetApproval(ctx, db, "o2", a1.ID, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other org: %v", err)
	}
}
