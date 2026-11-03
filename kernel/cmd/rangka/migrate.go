package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
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
	fs.Parse(args)
	ctx := context.Background()
	if *rollback > 0 {
		return migrateRollback(ctx, &c, *rollback, *dry)
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
	res, err := store.MigrateWith(ctx, db, reg, store.MigrateOpts{DryRun: *dry, AllowDestructive: *destructive, Source: "migrate"})
	if err != nil && !errors.Is(err, store.ErrDestructive) {
		return fail(err)
	}
	for _, s := range res.Steps {
		mark := s.Risk
		if s.Destructive {
			mark += ", destructive"
		}
		fmt.Printf("%s;  -- %s\n", strings.TrimSpace(s.SQL), mark)
	}
	for _, s := range res.Refused {
		fmt.Printf("-- refused (needs --allow-destructive): %s\n", strings.TrimSpace(s.SQL))
	}
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

// migrateRollback undoes the last n migrations (rangka migrate --rollback n).
func migrateRollback(ctx context.Context, c *common, n int, dry bool) int {
	db, err := c.pool(ctx)
	if err != nil {
		return fail(err)
	}
	defer db.Close()
	res, err := store.Rollback(ctx, db, n, dry)
	if err != nil {
		return fail(err)
	}
	for _, s := range res.Statements {
		fmt.Println(strings.TrimSpace(s) + ";")
	}
	verb := "rolled back"
	if dry {
		verb = "would roll back"
	}
	fmt.Printf("-- %s %d migration(s) %v; %d statement(s)\n", verb, len(res.Migrations), res.Migrations, len(res.Statements))
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
