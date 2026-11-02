package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// TestRLSBlocksOtherOrg checks the row-level security backstop: a plain (non-superuser) role sees only the
// context org's rows, sees every row without an org, and cannot write a row for another org.
func TestRLSBlocksOtherOrg(t *testing.T) {
	dbURL := testdb.New(t)
	ctx := context.Background()
	admin, err := pg.NewPool(ctx, dbURL, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	reg, rep := meta.ValidateOpts(meta.Options{AppsDir: "../../../../rangka-apps/apps", SiteDir: "../../../../rangka-apps/sites/kopi-senja"}, nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("metadata errors: %v", rep.All())
	}
	if _, err := Migrate(ctx, admin, reg, false, "test"); err != nil {
		t.Fatal(err)
	}
	if res, err := Migrate(ctx, admin, reg, false, "test"); err != nil || len(res.Statements) != 0 {
		t.Fatalf("second migrate not idempotent: %v %v", res.Statements, err)
	}
	if _, err := admin.Exec(ctx, "insert into r_customer (org_id, name) values ('a', 'Ani'), ('b', 'Budi')"); err != nil {
		t.Fatal(err)
	}

	b := make([]byte, 4)
	rand.Read(b)
	role, pass := "rk_rls_"+hex.EncodeToString(b), "pw"+hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "create role "+role+" login password '"+pass+"'"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"grant usage on schema public to " + role, "grant all on all tables in schema public to " + role,
		"grant all on all sequences in schema public to " + role} {
		if _, err := admin.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	u, _ := url.Parse(dbURL)
	u.User = url.UserPassword(role, pass)
	plain, err := pg.NewPool(ctx, u.String(), 1)
	if err != nil {
		admin.Exec(ctx, "drop role "+role)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		plain.Close()
		admin.Exec(context.Background(), "drop owned by "+role)
		admin.Exec(context.Background(), "drop role if exists "+role)
	})

	names := func(ctx context.Context) string {
		t.Helper()
		res, err := plain.Query(ctx, "select name from r_customer")
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range res.Rows {
			out = append(out, r[0].(string))
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	if got := names(pg.WithOrg(ctx, "a")); got != "Ani" {
		t.Fatalf("org a sees %q, want Ani", got)
	}
	// the pool has one connection: it must be reset when reused without an org
	if got := names(ctx); got != "Ani,Budi" {
		t.Fatalf("no org sees %q, want Ani,Budi", got)
	}
	if got := names(pg.WithOrg(ctx, "b")); got != "Budi" {
		t.Fatalf("org b sees %q, want Budi", got)
	}
	if _, err := plain.Exec(pg.WithOrg(ctx, "a"), "insert into r_customer (org_id, name) values ($1, $2)", "b", "Citra"); err == nil {
		t.Fatal("insert for org b under org a succeeded")
	}
	// child tables are covered too
	if _, err := admin.Exec(ctx, "insert into r_sales_invoice_item (org_id, name, parent, parent_field) values ('a', 'i1', 'x', 'items'), ('b', 'i2', 'x', 'items')"); err != nil {
		t.Fatal(err)
	}
	res, err := plain.Query(pg.WithOrg(ctx, "a"), "select name from r_sales_invoice_item")
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("child table under org a: %v %v", res, err)
	}
}
