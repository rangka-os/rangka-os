package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/backup"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Production reports whether RANGKA_ENV=production: migrations then back up first.
func Production() bool { return os.Getenv("RANGKA_ENV") == "production" }

// BackupDir is where pre-migration archives go: RANGKA_BACKUP_DIR, else <site>/backups.
func BackupDir(siteDir string) string {
	if d := os.Getenv("RANGKA_BACKUP_DIR"); d != "" {
		return d
	}
	return filepath.Join(siteDir, "backups")
}

// PreMigrateBackup returns a Before hook for store.MigrateOpts and store.RollbackOpts. In production mode
// (RANGKA_ENV=production) and when there are steps to apply, it writes
// <dir>/rangka-<org|all>-<YYYYMMDD-HHMMSS>-pre-migrate.tar.gz and fails the migration if the backup fails.
// Otherwise it does nothing.
func PreMigrateBackup(db *pg.Pool, o backup.Options, dir string) func(ctx context.Context, steps []store.Step) error {
	return func(ctx context.Context, steps []store.Step) error {
		if !Production() || len(steps) == 0 {
			return nil
		}
		now := o.Now
		if now == nil {
			now = time.Now
		}
		who := o.Org
		if who == "" {
			who = "all"
		}
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("pre-migrate backup: %w", err)
		}
		base := fmt.Sprintf("rangka-%s-%s-pre-migrate", who, now().Format("20060102-150405"))
		out := filepath.Join(dir, base+".tar.gz")
		for i := 2; ; i++ { // never overwrite an earlier archive from the same second
			if _, err := os.Stat(out); os.IsNotExist(err) {
				break
			}
			out = filepath.Join(dir, fmt.Sprintf("%s-%d.tar.gz", base, i))
		}
		tmp := out + ".partial"
		f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return fmt.Errorf("pre-migrate backup: %w", err)
		}
		_, err = backup.Write(ctx, db, f, o)
		if err == nil {
			err = f.Sync()
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Rename(tmp, out)
		}
		if err != nil {
			os.Remove(tmp)
			return fmt.Errorf("pre-migrate backup: %w", err)
		}
		return nil
	}
}

// preBackup is the production backup hook for this server's site, apps and files directory.
func (cfg Config) preBackup(db *pg.Pool, reg *meta.Registry) func(context.Context, []store.Step) error {
	return PreMigrateBackup(db, backup.Options{SiteDir: cfg.SiteDir, AppsDir: cfg.AppsDir, FilesDir: backup.FilesDir(reg, cfg.SiteDir), Version: cfg.Version}, BackupDir(cfg.SiteDir))
}
