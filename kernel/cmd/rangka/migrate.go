package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/backup"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/server"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

func cmdMigrate(args []string) int {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	var c common
	c.flags(fs)
	dry := fs.Bool("dry-run", false, "print the statements and their risk without running them")
	destructive := fs.Bool("allow-destructive", false, "allow retypes that may lose data, soft deletes of removed columns/tables and drops of soft-deleted ones older than 30 days")
	rollback := fs.Int("rollback", 0, "undo the last n migrations instead of migrating")
	filesDir := fs.String("files-dir", envOr("RANGKA_FILES_DIR", ""), "attachments directory the production pre-migrate backup archives, as serve (default: system.rk files.path, else ./data/files)")
	fs.Parse(args)
	ctx := context.Background()
	rollbackSet := false
	fs.Visit(func(f *flag.Flag) { rollbackSet = rollbackSet || f.Name == "rollback" })
	if rollbackSet {
		if *rollback < 1 {
			return fail(fmt.Errorf("--rollback needs a number of migrations of at least 1"))
		}
		return migrateRollback(ctx, &c, *filesDir, store.RollbackOpts{N: *rollback, DryRun: *dry, AllowDestructive: *destructive})
	}
	reg, err := c.registry()
	if err != nil {
		return fail(err)
	}
	db, err := c.pool(ctx)
	if err != nil {
		return fail(err)
	}
	defer db.Close()
	res, err := store.MigrateWith(ctx, db, reg, store.MigrateOpts{DryRun: *dry, AllowDestructive: *destructive, Source: "migrate", Before: preBackup(db, &c, reg, *filesDir)})
	if err != nil && !errors.Is(err, store.ErrDestructive) {
		return fail(err)
	}
	printSteps(res.Steps, res.Refused)
	for _, n := range res.Notes {
		fmt.Println("-- note:", n)
	}
	verb := "applied"
	if *dry || err != nil {
		verb = "would apply"
	}
	fmt.Printf("-- risk %s: %s %d statement(s); %d metadata file(s), %d changed since the last migrate\n", res.Risk, verb, len(res.Statements), res.Files, len(res.Changed))
	if err != nil {
		return fail(err)
	}
	return 0
}

// printSteps prints each step with its risk, then the refused ones.
func printSteps(steps, refused []store.Step) {
	for _, s := range steps {
		if s.Destructive && len(refused) > 0 {
			continue // listed as refused below
		}
		mark := s.Risk
		if s.Destructive {
			mark += ", destructive"
		}
		fmt.Printf("%s;  -- %s\n", strings.TrimSpace(s.SQL), mark)
	}
	for _, s := range refused {
		fmt.Printf("-- refused (needs --allow-destructive): %s\n", strings.TrimSpace(s.SQL))
	}
}

// migrateRollback undoes the last n migrations (rangka migrate --rollback n).
func migrateRollback(ctx context.Context, c *common, filesDir string, o store.RollbackOpts) int {
	db, err := c.pool(ctx)
	if err != nil {
		return fail(err)
	}
	defer db.Close()
	if o.Before == nil {
		reg, rerr := c.registry()
		if rerr != nil && server.Production() {
			fmt.Fprintln(os.Stderr, "rangka: warning: metadata did not load, the backup uses the default files dir:", rerr)
		}
		o.Before = preBackup(db, c, reg, filesDir)
	}
	res, err := store.Rollback(ctx, db, o)
	if err != nil && (res == nil || !errors.Is(err, store.ErrDestructive)) {
		return fail(err)
	}
	printSteps(res.Steps, res.Refused)
	verb := "rolled back"
	if o.DryRun || err != nil {
		verb = "would roll back"
	}
	fmt.Printf("-- risk %s: %s %d migration(s) %v; %d statement(s)\n", store.HighestRisk(res.Steps), verb, len(res.Migrations), res.Migrations, len(res.Statements))
	if err != nil {
		return fail(err)
	}
	return 0
}

func cmdSeed(args []string) int {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	var c common
	c.flags(fs)
	fixtures := fs.String("fixtures", os.Getenv("RANGKA_FIXTURES"), "fixtures directory (default $RANGKA_FIXTURES, else <apps>/../fixtures)")
	org := fs.String("org", os.Getenv("RANGKA_ORG"), "organisation id (default $RANGKA_ORG, else the site directory name)")
	email := fs.String("email", envOr("RANGKA_OWNER_EMAIL", "ceo@saga.co.id"), "seed user email (default $RANGKA_OWNER_EMAIL)")
	password := fs.String("password", envOr("RANGKA_OWNER_PASSWORD", "rangka-demo"), "seed user password (default $RANGKA_OWNER_PASSWORD)")
	record := fs.String("user-record", "USR-001", "core user record of the seed user")
	fs.Parse(args)
	ctx := context.Background()
	reg, err := c.registry()
	if err != nil {
		return fail(err)
	}
	db, err := c.pool(ctx)
	if err != nil {
		return fail(err)
	}
	defer db.Close()
	orgID := c.orgID(*org)
	dir := *fixtures
	if dir == "" {
		dir = engine.FixturesDir(c.apps)
	}
	st, err := server.Seed(ctx, db, reg, server.SeedOptions{Org: orgID, FixturesDir: dir, Email: *email, Password: *password, UserRecord: *record})
	if err != nil {
		return fail(err)
	}
	fmt.Printf("seeded org %s: created %v, already there %v; user %s (owner)\n", orgID, st.Created, st.Skipped, *email)
	return 0
}

// preBackup is the production-mode backup before a migration or rollback (see server.PreMigrateBackup).
// filesDir is --files-dir ($RANGKA_FILES_DIR), resolved like serve.
func preBackup(db *pg.Pool, c *common, reg *meta.Registry, filesDir string) func(context.Context, []store.Step) error {
	files := backup.FilesDir(reg, filesDir) // the directory serve uses
	return server.PreMigrateBackup(db, backup.Options{SiteDir: c.site, AppsDir: c.apps, FilesDir: files, Version: Version},
		server.BackupDir(c.site))
}
