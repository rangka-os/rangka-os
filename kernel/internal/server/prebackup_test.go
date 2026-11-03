package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/backup"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

func TestPreMigrateBackup(t *testing.T) {
	url := testdb.New(t)
	ctx := context.Background()
	db, err := pg.NewPool(ctx, url, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dir := t.TempDir()
	apps := filepath.Join(dir, "apps")
	if err := exec.Command("cp", "-r", filepath.Join(workspace, "rangka-apps/apps"), apps).Run(); err != nil {
		t.Fatal(err)
	}
	site := filepath.Join(dir, "site")
	os.MkdirAll(site, 0o755)
	cfg := Config{AppsDir: apps, SiteDir: site, Schemas: schema.Embedded()}
	bdir := filepath.Join(dir, "backups")
	opts := backup.Options{SiteDir: site, AppsDir: apps, Version: "test"}
	run := func() error {
		reg, err := LoadRegistry(cfg)
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.MigrateWith(ctx, db, reg, store.MigrateOpts{Source: "test", Before: PreMigrateBackup(db, opts, bdir)})
		return err
	}
	count := func() int {
		m, _ := filepath.Glob(filepath.Join(bdir, "rangka-all-*-pre-migrate.tar.gz"))
		return len(m)
	}
	// not production: the first migrate (many steps) writes nothing
	if err := run(); err != nil || count() != 0 {
		t.Fatalf("no env: %v, %d archives", err, count())
	}
	t.Setenv("RANGKA_ENV", "production")
	// nothing pending: nothing written
	if err := run(); err != nil || count() != 0 {
		t.Fatalf("no steps: %v, %d archives", err, count())
	}
	// a pending column: one archive
	p := filepath.Join(apps, "crm", "models", "deal.rk")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(b), "rules:\n", "  - name: region\n    type: link\n    to: outlet\n    label: {id: Wilayah}\nrules:\n", 1)), 0o644)
	if err := run(); err != nil || count() != 1 {
		t.Fatalf("pending column: %v, %d archives", err, count())
	}
	// a failing backup stops the migration
	os.WriteFile(p, []byte(strings.Replace(string(b), "rules:\n", "  - name: area\n    type: data\n    label: {id: Area}\nrules:\n", 1)), 0o644)
	os.WriteFile(filepath.Join(dir, "blocker"), nil, 0o644)
	failing := PreMigrateBackup(db, opts, filepath.Join(dir, "blocker", "sub"))
	reg, _ := LoadRegistry(cfg)
	if _, err := store.MigrateWith(ctx, db, reg, store.MigrateOpts{Source: "test", Before: failing}); err == nil {
		t.Fatal("migration ran although the backup failed")
	}
	res, err := db.Query(ctx, `select 1 from information_schema.columns where table_name='r_deal' and column_name='area'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 {
		t.Fatal("column added despite failed backup")
	}
}
