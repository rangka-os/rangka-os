package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

func load(t *testing.T, apps string) *meta.Registry {
	reg, rep := meta.Validate(apps, "", nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("metadata errors: %v", rep.All())
	}
	return reg
}

func TestMigrateIdempotentAndAddsColumns(t *testing.T) {
	url := testdb.New(t)
	ctx := context.Background()
	db, err := pg.NewPool(ctx, url, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	apps := filepath.Join(t.TempDir(), "apps")
	if err := exec.Command("cp", "-r", "../../../../rangka-apps/apps", apps).Run(); err != nil {
		t.Fatal(err)
	}
	res, err := Migrate(ctx, db, load(t, apps), false, "test")
	if err != nil {
		t.Fatal(err)
	}
	creates := 0
	for _, s := range res.Statements {
		if strings.HasPrefix(s, "create table") {
			creates++
		}
	}
	if creates != 9 || res.Files != 18 {
		t.Fatalf("first migrate: %d tables, %d files", creates, res.Files)
	}
	if !strings.Contains(strings.Join(res.Statements, "\n"), `create unique index if not exists "r_lead_phone_uniq"`) ||
		!strings.Contains(strings.Join(res.Statements, "\n"), `"r_sales_invoice_docstatus_idx" on "r_sales_invoice" (org_id, docstatus)`) {
		t.Fatalf("indexes missing:\n%s", strings.Join(res.Statements, "\n"))
	}
	res, err = Migrate(ctx, db, load(t, apps), false, "test")
	if err != nil || len(res.Statements) != 0 || len(res.Changed) != 0 {
		t.Fatalf("second migrate not idempotent: %v %v", res.Statements, err)
	}
	// add a field: only an ALTER TABLE (and its index) is planned
	p := filepath.Join(apps, "crm", "models", "deal.rk")
	b, _ := os.ReadFile(p)
	b = []byte(strings.Replace(string(b), "rules:\n", "  - name: region\n    type: link\n    to: outlet\n    label: {id: Wilayah}\nrules:\n", 1))
	os.WriteFile(p, b, 0o644)
	res, err = Migrate(ctx, db, load(t, apps), false, "test")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`alter table "r_deal" add column "region" text`, `create index if not exists "r_deal_region_idx" on "r_deal" (org_id, "region")`}
	if strings.Join(res.Statements, ";") != strings.Join(want, ";") || len(res.Changed) != 1 || res.Changed[0] != "apps/crm/models/deal.rk" {
		t.Fatalf("diff = %q changed %v", res.Statements, res.Changed)
	}
	// removing it again keeps the column and reports drift
	os.WriteFile(p, []byte(strings.Replace(string(b), "  - name: region\n    type: link\n    to: outlet\n    label: {id: Wilayah}\n", "", 1)), 0o644)
	res, _ = Migrate(ctx, db, load(t, apps), true, "test")
	if len(res.Statements) != 0 || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "r_deal.region is no longer in the model") {
		t.Fatalf("drift: %v %v", res.Statements, res.Notes)
	}
}

func TestExpandPattern(t *testing.T) {
	p, d := ExpandPattern("INV-{YYYY}-{#####}", mustDate("2026-03-04"))
	if p != "INV-2026-" || d != 5 {
		t.Errorf("got %q %d", p, d)
	}
}

func mustDate(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}
