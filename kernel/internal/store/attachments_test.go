package store

import (
	"context"
	"errors"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

func TestAttachmentRows(t *testing.T) {
	url := testdb.New(t)
	ctx := context.Background()
	db, err := pg.NewPool(ctx, url, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := Migrate(ctx, db, load(t, testdb.Sibling(t, "rangka-apps/apps")), false, "test"); err != nil {
		t.Fatal(err)
	}
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	mk := func(org, name string, size int64) *Attachment {
		a := &Attachment{Org: org, Model: "sales_invoice", Record: "X", Filename: name, ContentType: "image/png", Size: size, SHA256: sha, CreatedBy: "ceo@saga.co.id"}
		if err := InsertAttachment(ctx, db, a); err != nil {
			t.Fatal(err)
		}
		if len(a.ID) != 24 || a.ID[:4] != "att_" || a.CreatedAt.IsZero() {
			t.Fatalf("id/created_at not filled: %+v", a)
		}
		return a
	}
	a1 := mk("a", "one.png", 10)
	a2 := mk("a", "two.png", 20)
	b1 := mk("b", "other.png", 5)

	list, err := ListAttachments(ctx, db, "a", "sales_invoice", "X")
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v %v", list, err)
	}
	if list[0].ID != a2.ID || list[1].ID != a1.ID || list[0].Filename != "two.png" || list[0].Size != 20 || list[0].CreatedBy != "ceo@saga.co.id" {
		t.Fatalf("order/fields: %+v", list)
	}
	if got, err := GetAttachment(ctx, db, "a", a1.ID); err != nil || got.ID != a1.ID || got.SHA256 != sha {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := GetAttachment(ctx, db, "b", a1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org get: %v", err)
	}
	if n, _ := CountBlobRefs(ctx, db, "a", sha); n != 2 {
		t.Fatalf("refs a: %d", n)
	}
	if n, _ := CountBlobRefs(ctx, db, "b", sha); n != 1 {
		t.Fatalf("refs b: %d", n)
	}
	if c, b, err := AttachmentTotals(ctx, db, "a"); err != nil || c != 2 || b != 30 {
		t.Fatalf("totals: %d %d %v", c, b, err)
	}
	if err := DeleteAttachment(ctx, db, "a", a1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := GetAttachment(ctx, db, "a", a1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	a3 := mk("a", "three.png", 30)
	del, err := DeleteRecordAttachments(ctx, db, "a", "sales_invoice", "X")
	if err != nil || len(del) != 2 {
		t.Fatalf("delete record: %v %v", del, err)
	}
	ids := map[string]bool{del[0].ID: true, del[1].ID: true}
	if !ids[a2.ID] || !ids[a3.ID] {
		t.Fatalf("deleted wrong rows: %+v", del)
	}
	if list, _ := ListAttachments(ctx, db, "b", "sales_invoice", "X"); len(list) != 1 || list[0].ID != b1.ID {
		t.Fatalf("org b affected: %+v", list)
	}
}
