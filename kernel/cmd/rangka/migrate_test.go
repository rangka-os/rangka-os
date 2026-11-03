package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/files"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// TestMigrateFilesDir: rangka migrate --files-dir is the files dir the production pre-migrate backup archives;
// from the wrong place (the default ./data/files does not exist) the backup fails and nothing is migrated.
func TestMigrateFilesDir(t *testing.T) {
	url := testdb.New(t)
	ctx := context.Background()
	dir := t.TempDir()
	apps := filepath.Join(dir, "apps")
	if err := exec.Command("cp", "-r", "../../../../rangka-apps/apps", apps).Run(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RANGKA_FILES_DIR", "")
	t.Setenv("RANGKA_ENV", "")
	backups := filepath.Join(dir, "backups")
	t.Setenv("RANGKA_BACKUP_DIR", backups)
	base := []string{"--db", url, "--apps", apps}
	if code := cmdMigrate(base); code != 0 {
		t.Fatalf("first migrate exited %d", code)
	}
	// an attachment whose blob lives in a files dir only --files-dir names
	uploads := filepath.Join(dir, "uploads")
	fs, err := files.NewLocal(uploads)
	if err != nil {
		t.Fatal(err)
	}
	sum, size, err := fs.Put("o", bytes.NewReader([]byte("blob")))
	if err != nil {
		t.Fatal(err)
	}
	db, err := pg.NewPool(ctx, url, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.InsertAttachment(ctx, db, &store.Attachment{Org: "o", Model: "deal", Record: "DEAL-1", Filename: "a.pdf",
		ContentType: "application/pdf", Size: size, SHA256: sum}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(apps, "crm", "models", "deal.rk")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(b), "rules:\n", "  - name: area\n    type: data\n    label: {id: Area}\nrules:\n", 1)), 0o644)
	t.Setenv("RANGKA_ENV", "production")
	hasArea := func() bool {
		r, err := db.Query(ctx, `select 1 from information_schema.columns where table_name = 'r_deal' and column_name = 'area'`)
		if err != nil {
			t.Fatal(err)
		}
		return len(r.Rows) > 0
	}
	if code := cmdMigrate(base); code == 0 || hasArea() {
		t.Fatalf("without --files-dir: exit %d, column added %v; want a failed backup and no migration", code, hasArea())
	}
	if code := cmdMigrate(append(base, "--files-dir", uploads)); code != 0 || !hasArea() {
		t.Fatalf("with --files-dir: exit %d, column added %v", code, hasArea())
	}
	if m, _ := filepath.Glob(filepath.Join(backups, "rangka-all-*-pre-migrate.tar.gz")); len(m) != 1 {
		t.Fatalf("pre-migrate archives: %v", m)
	}
}
