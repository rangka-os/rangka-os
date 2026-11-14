package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/sandbox"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

const sandboxUsage = `usage: rangka sandbox create|reset|delete [--org <id>] [--fixtures <dir>] [--email <owner>] [--apps <dir>] [--site <dir>] [--db <url>]
The ruang coba (contract 0052): a sandbox org <org>-coba with demo data that sends nothing outside.
create seeds it from --fixtures (plus generated rows for company models) and makes --email its owner; reset seeds it
again (with --fixtures, else what create used); delete removes it with every row.`

// sandboxOut is where `rangka sandbox` reports (tests swap it; nil is stdout).
var sandboxOut io.Writer

func cmdSandbox(args []string) int {
	out := sandboxOut
	if out == nil {
		out = os.Stdout
	}
	if len(args) == 0 || (args[0] != "create" && args[0] != "reset" && args[0] != "delete") {
		fmt.Fprintln(os.Stderr, sandboxUsage)
		return 2
	}
	sub := args[0]
	fs := flag.NewFlagSet("sandbox "+sub, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var c common
	c.flags(fs)
	org := fs.String("org", os.Getenv("RANGKA_ORG"), "the real organisation (default $RANGKA_ORG, else the site directory name)")
	fixtures := fs.String("fixtures", os.Getenv("RANGKA_FIXTURES"), "demo fixtures directory (`rangka seed` format)")
	email := fs.String("email", os.Getenv("RANGKA_OWNER_EMAIL"), "the owner who may enter the sandbox (an owner of --org)")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, sandboxUsage)
		return 2
	}
	if err := runSandbox(context.Background(), out, sub, &c, c.orgID(*org), *fixtures, *email); err != nil {
		return fail(err)
	}
	return 0
}

func runSandbox(ctx context.Context, out io.Writer, sub string, c *common, org, fixturesDir, email string) error {
	reg, err := c.registry()
	if err != nil {
		return err
	}
	db, err := c.pool(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := store.Migrate(ctx, db, reg, false, "sandbox"); err != nil {
		return err
	}
	// the CLI is the operator: it acts as an owner; --email names the person who may enter the sandbox
	owner := sandbox.Owner{Email: "cli", Roles: []string{"owner"}}
	if email != "" {
		res, err := db.Query(ctx, "select id from rk_users where email = $1", strings.ToLower(strings.TrimSpace(email)))
		if err != nil {
			return err
		}
		if len(res.Rows) == 0 {
			return fmt.Errorf("sandbox: no account %s", email)
		}
		u, err := auth.LoadUser(ctx, db, res.Rows[0][0].(int64), org)
		if errors.Is(err, auth.ErrNoMembership) {
			return fmt.Errorf("sandbox: %s is not a member of %s", email, org)
		}
		if err != nil {
			return err
		}
		owner = sandbox.Owner{UserID: u.ID, Email: u.Email, Record: u.Record, Roles: u.Roles}
	}
	var fx sandbox.Fixtures
	if fixturesDir != "" {
		if fx, err = engine.LoadFixtures(fixturesDir); err != nil {
			return err
		}
	}
	e := engine.New(db, reg)
	switch sub {
	case "create":
		r, err := sandbox.Create(ctx, e, org, owner, fx)
		if err != nil {
			return err
		}
		return report(out, "created", r)
	case "reset":
		r, err := sandbox.Reset(ctx, e, org, owner, fx)
		if err != nil {
			return err
		}
		return report(out, "reset", r)
	default:
		if err := sandbox.Delete(ctx, e, org, owner); err != nil {
			return err
		}
		fmt.Fprintf(out, "deleted the sandbox of %s\n", org)
		return nil
	}
}

func report(out io.Writer, what string, r *sandbox.Result) error {
	b, _ := json.Marshal(r.Stats.Created)
	fmt.Fprintf(out, "%s sandbox %s: created %s\n", what, r.Org, b)
	for m, why := range r.Skipped {
		fmt.Fprintf(out, "  not generated: %s (%s)\n", m, why)
	}
	return nil
}
