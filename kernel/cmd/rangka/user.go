package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// usageError is a missing or wrong flag: the command exits 2 instead of 1.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func cmdUser(args []string) int {
	err := runUser(args)
	var ue usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ue):
		fmt.Fprintln(os.Stderr, "rangka user:", err)
		fmt.Fprintln(os.Stderr, "usage: rangka user add --db <url> --org <org> --email <e> --password <p> --role <role> [--name <full name>] [--record <USR-…>]")
		return 2
	default:
		return fail(err)
	}
}

// runUser runs `rangka user <subcommand>`. Today only `add`: create or update a Rangka ID account and its
// membership (one role per member) in an organisation. The password is set only when the account is new.
func runUser(args []string) error {
	if len(args) == 0 || args[0] != "add" {
		return usageError{"unknown or missing subcommand (want: add)"}
	}
	fs := flag.NewFlagSet("user add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	db := fs.String("db", os.Getenv("DATABASE_URL"), "Postgres URL (default $DATABASE_URL)")
	org := fs.String("org", os.Getenv("RANGKA_ORG"), "organisation id (default $RANGKA_ORG)")
	email := fs.String("email", "", "email address (stored in lower case)")
	password := fs.String("password", "", "password for a new account")
	role := fs.String("role", "", "role in the organisation (owner, sales_manager, sales_rep, …)")
	name := fs.String("name", "", "full name")
	record := fs.String("record", "", "core user record this member is (USR-…)")
	if err := fs.Parse(args[1:]); err != nil {
		return usageError{err.Error()}
	}
	var missing []string
	for _, f := range []struct{ name, val string }{{"--db", *db}, {"--org", *org}, {"--email", *email}, {"--password", *password}, {"--role", *role}} {
		if strings.TrimSpace(f.val) == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return usageError{"missing " + strings.Join(missing, ", ")}
	}
	ctx := context.Background()
	pool, err := pg.NewPool(ctx, *db, 2)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := auth.EnsureOrg(ctx, pool, *org, titleCase(*org)); err != nil {
		return err
	}
	if _, err := auth.EnsureUser(ctx, pool, *email, *name, *password, *org, *role, *record); err != nil {
		return err
	}
	fmt.Printf("user %s is %s in %s\n", strings.ToLower(strings.TrimSpace(*email)), *role, *org)
	return nil
}
