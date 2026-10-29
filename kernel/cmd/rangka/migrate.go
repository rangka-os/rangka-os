package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/server"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

func cmdMigrate(args []string) int {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	var c common
	c.flags(fs)
	dry := fs.Bool("dry-run", false, "print the statements without running them")
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
	res, err := store.Migrate(ctx, db, reg, *dry, "migrate")
	if err != nil {
		return fail(err)
	}
	for _, s := range res.Statements {
		fmt.Println(strings.TrimSpace(s) + ";")
	}
	for _, n := range res.Notes {
		fmt.Println("-- note:", n)
	}
	verb := "applied"
	if *dry {
		verb = "would apply"
	}
	fmt.Printf("-- %s %d statement(s); %d metadata file(s), %d changed since the last migrate\n", verb, len(res.Statements), res.Files, len(res.Changed))
	return 0
}

func cmdSeed(args []string) int {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	var c common
	c.flags(fs)
	fixtures := fs.String("fixtures", "", "fixtures directory (default <apps>/../fixtures)")
	org := fs.String("org", "", "organisation id (default: the site directory name)")
	email := fs.String("email", "ceo@saga.co.id", "seed user email")
	password := fs.String("password", "rangka-demo", "seed user password")
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
