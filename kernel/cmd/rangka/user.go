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
	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/plan"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// userOut is where `rangka user` reports what it did (tests swap it).
var userOut io.Writer = os.Stdout

// usageError is a missing or wrong flag: the command exits 2 instead of 1.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

const userUsage = `usage: rangka user add <email> --org <id> --role <role> [--outlets a,b] [--record USR-001] [--name "..."]
                       [--reset-password] [--create-org] [--allow-linked] [--db <url>] [--apps <dir> [--site <dir>]]
       rangka user remove <email> --org <id> [--db <url>] [--apps <dir> [--site <dir>]]
The email may also be given as --email <e>. The password comes from $RANGKA_USER_PASSWORD (or --password <p>)
and is needed only for a new account or with --reset-password. --db defaults to $DATABASE_URL, --org to $RANGKA_ORG.
Every outlet must be an existing outlet record of the organisation. With --apps (default $RANGKA_APPS) the member's
employee record is synced at once (contract 0055); without it the server syncs it within 10 minutes.`

// userArgs are the parsed arguments of `rangka user add|remove`.
type userArgs struct {
	Sub           string // add or remove
	Email         string
	Org           string
	Role          string
	Outlets       []string
	SetOutlet     bool // --outlets was given (an empty value clears the outlets)
	Record        string
	Name          string
	DB            string
	Password      string // --password, else $RANGKA_USER_PASSWORD
	ResetPassword bool
	CreateOrg     bool
	AllowLinked   bool // add an existing account that is linked to upstream sign-in and not yet in the org
	// metadata for the employee record sync (contract 0055 §2); no apps directory = no sync here
	Apps, Site, Modules, Connectors string
}

// parseUserArgs reads the arguments after `rangka user`: the sub command, the email (positional, anywhere among
// the flags, or --email) and the flags. Every error is a usageError.
func parseUserArgs(args []string) (*userArgs, error) {
	if len(args) == 0 || (args[0] != "add" && args[0] != "remove") {
		return nil, usageError{"unknown or missing subcommand (want: add or remove)"}
	}
	ua := &userArgs{Sub: args[0]}
	fs := flag.NewFlagSet("user "+ua.Sub, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&ua.Org, "org", os.Getenv("RANGKA_ORG"), "organisation id (default $RANGKA_ORG)")
	fs.StringVar(&ua.DB, "db", os.Getenv("DATABASE_URL"), "Postgres URL (default $DATABASE_URL)")
	fs.StringVar(&ua.Apps, "apps", os.Getenv("RANGKA_APPS"), "apps directory, to sync the employee record (default $RANGKA_APPS)")
	fs.StringVar(&ua.Site, "site", os.Getenv("RANGKA_SITE"), "site directory (default $RANGKA_SITE)")
	fs.StringVar(&ua.Modules, "modules", os.Getenv("RANGKA_MODULES"), "modules directory (default: modules next to the apps directory)")
	fs.StringVar(&ua.Connectors, "connectors", os.Getenv("RANGKA_CONNECTORS"), "connectors directory")
	var email, outlets string
	fs.StringVar(&email, "email", "", "email address (or give it as the first argument)")
	if ua.Sub == "add" {
		fs.StringVar(&ua.Role, "role", "", "role of the member")
		fs.StringVar(&outlets, "outlets", "", "outlet codes the member works at, comma separated (empty clears)")
		fs.StringVar(&ua.Record, "record", "", "core user record (USR-001)")
		fs.StringVar(&ua.Name, "name", "", "full name of a new user")
		fs.StringVar(&ua.Password, "password", "", "password (prefer $RANGKA_USER_PASSWORD: flags land in shell history)")
		fs.BoolVar(&ua.ResetPassword, "reset-password", false, "set the password of an account that already exists")
		fs.BoolVar(&ua.CreateOrg, "create-org", false, "create the organisation when it does not exist")
		fs.BoolVar(&ua.AllowLinked, "allow-linked", false, "add an account that is linked to upstream sign-in elsewhere (its linked identities also enter this org)")
	}
	var positional []string
	rest := args[1:]
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return nil, usageError{err.Error()}
		}
		rest = fs.Args()
		if len(rest) > 0 {
			positional = append(positional, rest[0])
			rest = rest[1:]
		}
	}
	if len(positional) > 1 {
		return nil, usageError{"more than one email given"}
	}
	if len(positional) == 1 {
		if email != "" && !strings.EqualFold(strings.TrimSpace(email), strings.TrimSpace(positional[0])) {
			return nil, usageError{"the email argument and --email differ"}
		}
		email = positional[0]
	}
	ua.Email = strings.ToLower(strings.TrimSpace(email))
	var missing []string
	if ua.Email == "" {
		missing = append(missing, "email")
	}
	if strings.TrimSpace(ua.Org) == "" {
		missing = append(missing, "--org")
	}
	if ua.Sub == "add" && strings.TrimSpace(ua.Role) == "" {
		missing = append(missing, "--role")
	}
	if len(missing) > 0 {
		return nil, usageError{"missing " + strings.Join(missing, ", ")}
	}
	if ua.Sub == "add" {
		if ua.Password == "" {
			ua.Password = os.Getenv("RANGKA_USER_PASSWORD")
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
	err := runUser(args)
	var ue usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ue):
		fmt.Fprintln(os.Stderr, "rangka user:", err)
		fmt.Fprintln(os.Stderr, userUsage)
		return 2
	case errors.Is(err, errNotMember):
		fmt.Fprintln(os.Stderr, err)
		return 1
	default:
		return fail(err)
	}
}

// errNotMember wraps `<email> is not a member of <org>` from `rangka user remove`.
var errNotMember = errors.New("not a member")

type notMemberError struct{ email, org string }

func (e notMemberError) Error() string {
	return fmt.Sprintf("%s is not a member of %s", e.email, e.org)
}
func (e notMemberError) Is(target error) bool { return target == errNotMember }

// runUser runs `rangka user add|remove`. add creates a Rangka ID account (or reuses an existing one) and sets its
// membership (one role per member, optional outlets) in an organisation. An existing account keeps its password
// unless --reset-password is given; an unknown organisation is refused unless --create-org is given. remove ends a
// membership.
func runUser(args []string) error {
	ua, err := parseUserArgs(args)
	if err != nil {
		return err
	}
	if strings.TrimSpace(ua.DB) == "" {
		return usageError{"missing --db (or set DATABASE_URL)"}
	}
	ctx := context.Background()
	pool, err := pg.NewPool(ctx, ua.DB, 2)
	if err != nil {
		return err
	}
	defer pool.Close()

	res, err := pool.Query(ctx, `select u.id, coalesce(m.role, '') from rk_users u
		left join rk_members m on m.user_id = u.id and m.org_id = $2 where u.email = $1`, ua.Email, ua.Org)
	if err != nil {
		return err
	}
	exists, oldRole := len(res.Rows) > 0, ""
	if exists {
		oldRole = res.Rows[0][1].(string)
	}

	if ua.Sub == "remove" {
		if !exists {
			return fmt.Errorf("no user %s", ua.Email)
		}
		if err := auth.RemoveMember(ctx, pool, ua.Org, res.Rows[0][0].(int64)); err != nil {
			if errors.Is(err, auth.ErrNoMembership) {
				return notMemberError{ua.Email, ua.Org}
			}
			return err
		}
		fmt.Fprintf(userOut, "removed %s from %s\n", ua.Email, ua.Org)
		syncUserRecord(ctx, pool, ua, res.Rows[0][0].(int64))
		return nil
	}

	// an account linked to upstream sign-in (by another org's member API) and not yet in this org: adding it
	// also lets whoever holds those upstream identities in here, so the operator must say so (contract 0043)
	if exists && oldRole == "" && !ua.AllowLinked {
		linked, err := auth.LinkedOutside(ctx, pool, ua.Email, ua.Org)
		if err != nil {
			return err
		}
		if len(linked) > 0 {
			return fmt.Errorf("account %s is linked to upstream sign-in (%s) and is not a member of %s: whoever holds "+
				"that sign-in would enter %s as this person; pass --allow-linked if that is intended",
				ua.Email, strings.Join(linked, ", "), ua.Org, ua.Org)
		}
	}

	if (!exists || ua.ResetPassword) && ua.Password == "" {
		why := "a new account"
		if exists {
			why = "--reset-password"
		}
		return usageError{"a password is required for " + why + ": set RANGKA_USER_PASSWORD (or pass --password)"}
	}

	res, err = pool.Query(ctx, "select 1 from rk_orgs where id = $1", ua.Org)
	if err != nil {
		return err
	}
	if len(res.Rows) == 0 {
		if !ua.CreateOrg {
			return fmt.Errorf("organisation %q does not exist (pass --create-org to create it)", ua.Org)
		}
		if err := auth.EnsureOrg(ctx, pool, ua.Org, titleCase(ua.Org)); err != nil {
			return err
		}
		fmt.Fprintf(userOut, "created organisation %s\n", ua.Org)
	}

	password := ua.Password
	if !exists && ua.Name == "" {
		ua.Name = ua.Email
	}
	if exists && !ua.ResetPassword {
		// the password of an existing account is never changed here; a throwaway value satisfies EnsureUser
		password = auth.RandomToken("unused", 16)
	}
	// contract 0055 §2: every outlet must be an outlet record of the organisation, as the member API checks
	if ua.SetOutlet {
		if err := checkOutlets(ctx, pool, ua.Org, ua.Outlets); err != nil {
			return err
		}
	}
	// contract 0028: a new person past the plan's people limit is refused (an existing member is not a new person)
	if err := plan.CheckNewPerson(ctx, pool, ua.Org, ua.Email); err != nil {
		return err
	}
	id, err := auth.EnsureUser(ctx, pool, ua.Email, ua.Name, password, ua.Org, ua.Role, ua.Record)
	if err != nil {
		return err
	}
	if exists {
		if ua.ResetPassword {
			hash, err := auth.HashPassword(ua.Password)
			if err != nil {
				return err
			}
			if _, err := pool.Query(ctx, "update rk_users set password_hash = $1 where id = $2", hash, id); err != nil {
				return err
			}
			fmt.Fprintf(userOut, "account %s exists; password reset\n", ua.Email)
		} else {
			fmt.Fprintf(userOut, "account %s exists; password unchanged\n", ua.Email)
		}
	}
	if ua.SetOutlet {
		if err := auth.SetOutlets(ctx, pool, ua.Org, id, ua.Outlets); err != nil {
			return err
		}
		if len(ua.Outlets) == 0 {
			fmt.Fprintf(userOut, "outlets cleared\n")
		} else {
			fmt.Fprintf(userOut, "outlets %s\n", strings.Join(ua.Outlets, ", "))
		}
	}
	if oldRole != "" && oldRole != ua.Role {
		fmt.Fprintf(userOut, "role %s → %s\n", oldRole, ua.Role)
	}
	fmt.Fprintf(userOut, "user %s is %s in %s\n", ua.Email, ua.Role, ua.Org)
	syncUserRecord(ctx, pool, ua, id)
	return nil
}

// checkOutlets refuses an outlet that is not an outlet record of org (unknown_outlet); without an outlet table
// (outlets not installed) every outlet is unknown.
func checkOutlets(ctx context.Context, pool *pg.Pool, org string, outlets []string) error {
	if len(outlets) == 0 {
		return nil
	}
	table := store.TableName("outlet")
	res, err := pool.Query(ctx, "select to_regclass($1) is not null", table)
	if err != nil {
		return err
	}
	installed := res.Rows[0][0].(bool)
	for _, o := range outlets {
		found := false
		if installed {
			if found, err = store.Exists(pg.WithOrg(ctx, org), pool, org, "outlet", o); err != nil {
				return err
			}
		}
		if !found {
			return fmt.Errorf("unknown_outlet: outlet %q does not exist in %s", o, org)
		}
	}
	return nil
}

// syncUserRecord keeps the member's employee record in step (contract 0055 §2) when --apps names the metadata.
// The membership is already written: a failure here is reported, not returned, and the server's repair catches up.
func syncUserRecord(ctx context.Context, pool *pg.Pool, ua *userArgs, userID int64) {
	if strings.TrimSpace(ua.Apps) == "" {
		fmt.Fprintf(userOut, "employee record: not synced here (no --apps); the server syncs it within 10 minutes\n")
		return
	}
	c := common{apps: ua.Apps, site: ua.Site, modules: ua.Modules, connectors: ua.Connectors}
	reg, err := c.registry()
	if err == nil {
		var name string
		if name, err = engine.New(pool, reg).SyncMemberRecord(ctx, ua.Org, userID); err == nil {
			if name != "" {
				fmt.Fprintf(userOut, "employee record %s\n", name)
			}
			return
		}
	}
	fmt.Fprintf(os.Stderr, "rangka user: employee record not synced: %v (the server syncs it within 10 minutes)\n", err)
}
