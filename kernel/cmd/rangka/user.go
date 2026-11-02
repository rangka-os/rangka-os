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
)

// userArgs are the parsed arguments of `rangka user add|remove`.
type userArgs struct {
	Sub       string // add or remove
	Email     string
	Org       string
	Role      string
	Outlets   []string
	SetOutlet bool // --outlets was given (an empty value clears the outlets)
	Record    string
	Name      string
	DB        string
}

var errUserUsage = errors.New("usage: rangka user add <email> --org <id> --role <role> [--outlets a,b] [--record USR-001] [--name \"...\"] [--db <url>]\n       rangka user remove <email> --org <id> [--db <url>]")

// parseUserArgs reads the arguments after `rangka user`: the sub command, the email (anywhere among the
// flags) and the flags.
func parseUserArgs(args []string) (*userArgs, error) {
	if len(args) == 0 || (args[0] != "add" && args[0] != "remove") {
		return nil, errUserUsage
	}
	ua := &userArgs{Sub: args[0]}
	fs := flag.NewFlagSet("user "+ua.Sub, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&ua.Org, "org", os.Getenv("RANGKA_ORG"), "organisation id (default $RANGKA_ORG)")
	fs.StringVar(&ua.DB, "db", os.Getenv("DATABASE_URL"), "Postgres URL (default $DATABASE_URL)")
	var outlets string
	if ua.Sub == "add" {
		fs.StringVar(&ua.Role, "role", "", "role of the member")
		fs.StringVar(&outlets, "outlets", "", "outlet codes the member works at, comma separated (empty clears)")
		fs.StringVar(&ua.Record, "record", "", "core user record (USR-001)")
		fs.StringVar(&ua.Name, "name", "", "full name of a new user")
	}
	var positional []string
	rest := args[1:]
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return nil, fmt.Errorf("%v\n%w", err, errUserUsage)
		}
		rest = fs.Args()
		if len(rest) > 0 {
			positional = append(positional, rest[0])
			rest = rest[1:]
		}
	}
	if len(positional) != 1 || strings.TrimSpace(positional[0]) == "" {
		return nil, fmt.Errorf("an email is required\n%w", errUserUsage)
	}
	ua.Email = strings.ToLower(strings.TrimSpace(positional[0]))
	if ua.Org == "" {
		return nil, fmt.Errorf("--org is required\n%w", errUserUsage)
	}
	if ua.Sub == "add" {
		if ua.Role == "" {
			return nil, fmt.Errorf("--role is required\n%w", errUserUsage)
		}
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "outlets" {
				ua.SetOutlet = true
			}
		})
		for _, o := range strings.Split(outlets, ",") {
			if o = strings.TrimSpace(o); o != "" {
				ua.Outlets = append(ua.Outlets, o)
			}
		}
	}
	return ua, nil
}

func cmdUser(args []string) int {
	ua, err := parseUserArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rangka:", err)
		return 2
	}
	password := os.Getenv("RANGKA_USER_PASSWORD")
	ctx := context.Background()
	c := common{db: ua.DB}
	if c.db == "" {
		return fail(errNoDB)
	}
	db, err := c.pool(ctx)
	if err != nil {
		return fail(err)
	}
	defer db.Close()
	res, err := db.Query(ctx, "select id from rk_users where email = $1", ua.Email)
	if err != nil {
		return fail(err)
	}
	exists := len(res.Rows) > 0
	if ua.Sub == "remove" {
		if !exists {
			return fail(fmt.Errorf("no user %s", ua.Email))
		}
		if err := auth.RemoveMember(ctx, db, ua.Org, res.Rows[0][0].(int64)); err != nil {
			return fail(err)
		}
		fmt.Printf("removed %s from %s\n", ua.Email, ua.Org)
		return 0
	}
	if !exists && password == "" {
		fmt.Fprintln(os.Stderr, "rangka: RANGKA_USER_PASSWORD is required for a new user")
		return 2
	}
	if !exists && ua.Name == "" {
		ua.Name = ua.Email
	}
	if exists {
		// the password of an existing user is never changed; a throwaway value satisfies EnsureUser
		password = auth.RandomToken("unused", 16)
	}
	if err := auth.EnsureOrg(ctx, db, ua.Org, titleCase(ua.Org)); err != nil {
		return fail(err)
	}
	id, err := auth.EnsureUser(ctx, db, ua.Email, ua.Name, password, ua.Org, ua.Role, ua.Record)
	if err != nil {
		return fail(err)
	}
	if ua.SetOutlet {
		if err := auth.SetOutlets(ctx, db, ua.Org, id, ua.Outlets); err != nil {
			return fail(err)
		}
	}
	fmt.Printf("added %s to %s as %s\n", ua.Email, ua.Org, ua.Role)
	return 0
}
