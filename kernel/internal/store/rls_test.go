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
	reg, rep := meta.ValidateOpts(meta.Options{AppsDir: testdb.Sibling(t, "rangka-apps/apps"), SiteDir: testdb.Sibling(t, "rangka-apps/sites/kopi-senja")}, nil, schema.Embedded())
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
	// transactions get their connection through the same hook
	tx, err := plain.Begin(pg.WithOrg(ctx, "b"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := tx.Query(ctx, "select name from r_customer")
	tx.Rollback(ctx)
	if err != nil || len(res.Rows) != 1 || res.Rows[0][0] != "Budi" {
		t.Fatalf("transaction under org b: %v %v", res, err)
	}
	// child tables are covered too
	if _, err := admin.Exec(ctx, "insert into r_sales_invoice_item (org_id, name, parent, parent_field) values ('a', 'i1', 'x', 'items'), ('b', 'i2', 'x', 'items')"); err != nil {
		t.Fatal(err)
	}
	res, err = plain.Query(pg.WithOrg(ctx, "a"), "select name from r_sales_invoice_item")
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("child table under org a: %v %v", res, err)
	}
}

// TestRLSStepRollsBack: a table without row-level security gets it in one low-risk, non-destructive step with a
// reverse; --dry-run shows it, rollback removes it without --allow-destructive, and the next migrate adds it back.
func TestRLSStepRollsBack(t *testing.T) {
	e := newMigrateEnv(t)
	e.mustMigrate(MigrateOpts{Source: "test"})
	rls := func() int64 {
		return e.count("select count(*) from pg_class where relname = 'r_deal' and relrowsecurity and relforcerowsecurity")
	}
	if rls() != 1 {
		t.Fatal("r_deal has no row-level security after migrate")
	}
	if _, err := e.db.Exec(e.ctx, `drop policy rk_org on r_deal; alter table r_deal no force row level security; alter table r_deal disable row level security`); err != nil {
		t.Fatal(err)
	}
	dry := e.mustMigrate(MigrateOpts{Source: "test", DryRun: true})
	if len(dry.Steps) != 1 || dry.Steps[0].Risk != RiskLow || dry.Steps[0].Destructive || dry.Steps[0].Reverse == "" ||
		!strings.Contains(dry.Steps[0].SQL, `create policy rk_org on "r_deal"`) || rls() != 0 {
		t.Fatalf("dry run: %+v", dry.Steps)
	}
	if res := e.mustMigrate(MigrateOpts{Source: "test"}); len(res.Steps) != 1 || rls() != 1 {
		t.Fatalf("migrate: %+v", res.Steps)
	}
	if _, err := Rollback(e.ctx, e.db, RollbackOpts{N: 1}); err != nil {
		t.Fatal(err)
	}
	if rls() != 0 || e.count("select count(*) from pg_policies where tablename = 'r_deal' and policyname = 'rk_org'") != 0 {
		t.Fatal("rollback left row-level security on r_deal")
	}
	if res := e.mustMigrate(MigrateOpts{Source: "test"}); len(res.Steps) != 1 || rls() != 1 {
		t.Fatalf("migrate after rollback: %+v", res.Steps)
	}
	if res := e.mustMigrate(MigrateOpts{Source: "test"}); len(res.Steps) != 0 {
		t.Fatalf("second migrate: %+v", res.Steps)
	}
}
