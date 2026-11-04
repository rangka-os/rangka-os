package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// TestListAuditLimit: a limit above 500 caps at 500 rather than falling back to the default 50.
func TestListAuditLimit(t *testing.T) {
	ctx := context.Background()
	db, err := pg.NewPool(ctx, testdb.New(t), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ctx, systemSQL); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 510; i++ {
		if err := Audit(ctx, db, "o1", "u1", "test.entry", fmt.Sprintf("t%d", i), "entry", nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ limit, want int }{{0, 50}, {-1, 50}, {120, 120}, {500, 500}, {501, 500}, {10000, 500}} {
		list, err := ListAudit(ctx, db, AuditFilter{Orgs: []string{"o1"}, Org: "o1", Limit: c.limit})
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != c.want {
			t.Errorf("limit %d: %d entries, want %d", c.limit, len(list), c.want)
		}
	}
}
