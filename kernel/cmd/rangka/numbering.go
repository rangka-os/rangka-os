package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
)

// numberingOut is where `rangka numbering` reports what it did (tests swap it; nil is stdout).
var numberingOut io.Writer

const numberingUsage = `usage: rangka numbering set <org> <series> <next> [--period P] [--apps <dir>] [--site <dir>] [--db <url>]
Sets the next number a series issues for an org, once at go-live, to continue the client's own numbering
(contract 0009 §4). It never lowers a counter and never reissues an existing name. --period targets another
period than the current one (2026 for a yearly series, 2026-11 for a monthly one,
never or "" for a series that never resets).`

// numberingArgs are the parsed arguments of `rangka numbering set`.
type numberingArgs struct {
	Org, Series string
	Next        int64
	Period      string // "" = the current period
	c           common
}

// parseNumberingArgs reads the arguments after `rangka numbering`: set, then org, series and next (positional,
// anywhere among the flags). Every error is a usageError.
func parseNumberingArgs(args []string) (*numberingArgs, error) {
	if len(args) == 0 || args[0] != "set" {
		return nil, usageError{"unknown or missing subcommand (want: set)"}
	}
	na := &numberingArgs{}
	fs := flag.NewFlagSet("numbering set", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	na.c.flags(fs)
	fs.StringVar(&na.Period, "period", "", "period to set (default: the current one in the site timezone)")
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
	if len(positional) != 3 {
		return nil, usageError{fmt.Sprintf("want <org> <series> <next>, got %d argument(s)", len(positional))}
	}
	na.Org, na.Series = positional[0], positional[1]
	n, err := strconv.ParseInt(positional[2], 10, 64)
	if err != nil || n < 1 {
		return nil, usageError{fmt.Sprintf("next must be a whole number of 1 or more, got %q", positional[2])}
	}
	na.Next = n
	return na, nil
}

func cmdNumbering(args []string) int {
	err := runNumbering(args)
	var ue usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ue):
		fmt.Fprintln(os.Stderr, "rangka numbering:", err)
		fmt.Fprintln(os.Stderr, numberingUsage)
		return 2
	default:
		return fail(err)
	}
}

// runNumbering runs `rangka numbering set` against the validated metadata and the database.
func runNumbering(args []string) error {
	na, err := parseNumberingArgs(args)
	if err != nil {
		return err
	}
	if na.c.db == "" {
		return usageError{"missing --db (or set DATABASE_URL)"}
	}
	reg, err := na.c.registry()
	if err != nil {
		return err
	}
	ctx := context.Background()
	db, err := na.c.pool(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	e := engine.New(db, reg)
	name, period, err := e.SetNextNumber(ctx, na.Org, na.Series, na.Next, na.Period)
	if err != nil {
		return err
	}
	if period == "" {
		period = "never"
	}
	out := numberingOut
	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintf(out, "next number of series %s in org %s (period %s): %s\n", na.Series, na.Org, period, name)
	return nil
}
