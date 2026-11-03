package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// userOut is where `rangka user` reports what it did (tests swap it).
var userOut io.Writer = os.Stdout

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
		fmt.Fprintln(os.Stderr, "usage: rangka user add --db <url> --org <org> --email <e> --password <p> --role <role> [--name <full name>] [--record <USR-…>] [--reset-password] [--create-org]")
		return 2
	default:
		return fail(err)
	}
}

// runUser runs `rangka user <subcommand>`. Today only `add`: create a Rangka ID account (or reuse an existing one)
// and set its membership (one role per member) in an existing organisation. An existing account keeps its password
// unless --reset-password is given; an unknown organisation is refused unless --create-org is given.
func runUser(args []string) error {
	if len(args) == 0 || args[0] != "add" {
		return usageError{"unknown or missing subcommand (want: add)"}
	}
	fs := flag.NewFlagSet("user add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	db := fs.String("db", os.Getenv("DATABASE_URL"), "Postgres URL (default $DATABASE_URL)")
	org := fs.String("org", os.Getenv("RANGKA_ORG"), "organisation id (default $RANGKA_ORG)")
	email := fs.String("email", "", "email address (stored in lower case)")
	password := fs.String("password", "", "password for a new account (or the new one with --reset-password)")
	role := fs.String("role", "", "role in the organisation (owner, sales_manager, sales_rep, …)")
	name := fs.String("name", "", "full name")
	record := fs.String("record", "", "core user record this member is (USR-…)")
	reset := fs.Bool("reset-password", false, "set --password on an account that already exists")
	createOrg := fs.Bool("create-org", false, "create the organisation when it does not exist")
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
	addr := strings.ToLower(strings.TrimSpace(*email))
	ctx := context.Background()
	pool, err := pg.NewPool(ctx, *db, 2)
	if err != nil {
		return err
	}
	defer pool.Close()

	res, err := pool.Query(ctx, "select 1 from rk_orgs where id = $1", *org)
	if err != nil {
		return err
	}
	if len(res.Rows) == 0 {
		if !*createOrg {
			return fmt.Errorf("organisation %q does not exist (pass --create-org to create it)", *org)
		}
		if err := auth.EnsureOrg(ctx, pool, *org, titleCase(*org)); err != nil {
			return err
		}
		fmt.Fprintf(userOut, "created organisation %s\n", *org)
	}

	res, err = pool.Query(ctx, `select u.id, coalesce(m.role, '') from rk_users u
		left join rk_members m on m.user_id = u.id and m.org_id = $2 where u.email = $1`, addr, *org)
	if err != nil {
		return err
	}
	exists, oldRole := len(res.Rows) > 0, ""
	if exists {
		oldRole = res.Rows[0][1].(string)
	}

	id, err := auth.EnsureUser(ctx, pool, addr, *name, *password, *org, *role, *record)
	if err != nil {
		return err
	}
	if exists {
		if *reset {
			hash, err := auth.HashPassword(*password)
			if err != nil {
				return err
			}
			if _, err := pool.Query(ctx, "update rk_users set password_hash = $1 where id = $2", hash, id); err != nil {
				return err
			}
			fmt.Fprintf(userOut, "account %s exists; password reset\n", addr)
		} else {
			fmt.Fprintf(userOut, "account %s exists; password unchanged\n", addr)
		}
	}
	if oldRole != "" && oldRole != *role {
		fmt.Fprintf(userOut, "role %s → %s\n", oldRole, *role)
	}
	fmt.Fprintf(userOut, "user %s is %s in %s\n", addr, *role, *org)
	return nil
}
