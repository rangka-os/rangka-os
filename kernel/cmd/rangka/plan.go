package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/plan"
)

// planOut is where `rangka plan` reports (tests swap it; nil is stdout).
var planOut io.Writer

const planUsage = `usage: rangka plan show [--json] [--apps <dir>] [--site <dir>] [--db <url>]
       rangka plan set <plan> [--by <who>] [--note "..."] [--apps <dir>] [--site <dir>] [--db <url>]
       rangka plan clear [--by <who>] [--apps <dir>] [--site <dir>] [--db <url>]
The tenant's plan (contract 0027): set is the operator's upgrade or downgrade (a plan of features.rk), clear
goes back to config.rk plan. Nothing is deleted on a downgrade: what is past a lower limit becomes read-only.
A running kernel picks the change up within 10 seconds.`

type planArgs struct {
	Sub, Plan, By, Note string
	JSON                bool
	c                   common
}

func parsePlanArgs(args []string) (*planArgs, error) {
	if len(args) == 0 || (args[0] != "show" && args[0] != "set" && args[0] != "clear") {
		return nil, usageError{"unknown or missing subcommand (want: show, set or clear)"}
	}
	pa := &planArgs{Sub: args[0]}
	fs := flag.NewFlagSet("plan "+pa.Sub, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	pa.c.flags(fs)
	fs.StringVar(&pa.By, "by", envOr("USER", "operator"), "who changes the plan (kept in rk_plan_changes)")
	fs.StringVar(&pa.Note, "note", "", "why (an invoice number, for example)")
	fs.BoolVar(&pa.JSON, "json", false, "show: print JSON")
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
	want := 0
	if pa.Sub == "set" {
		want = 1
	}
	if len(positional) != want {
		return nil, usageError{fmt.Sprintf("plan %s takes %d argument(s), got %d", pa.Sub, want, len(positional))}
	}
	if want == 1 {
		pa.Plan = positional[0]
	}
	return pa, nil
}

func cmdPlan(args []string) int {
	err := runPlan(args)
	var ue usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ue):
		fmt.Fprintln(os.Stderr, "rangka plan:", err)
		fmt.Fprintln(os.Stderr, planUsage)
		return 2
	default:
		return fail(err)
	}
}

func runPlan(args []string) error {
	pa, err := parsePlanArgs(args)
	if err != nil {
		return err
	}
	out := planOut
	if out == nil {
		out = os.Stdout
	}
	reg, err := pa.c.registry()
	if err != nil {
		return err
	}
	ctx := context.Background()
	db, err := pa.c.pool(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	switch pa.Sub {
	case "set":
		from, err := plan.Set(ctx, db, reg, pa.Plan, pa.By, pa.Note)
		if err != nil {
			return err
		}
		if from == "" {
			from = "(none)"
		}
		fmt.Fprintf(out, "plan %s -> %s\n", from, pa.Plan)
	case "clear":
		if err := plan.Clear(ctx, db, reg, pa.By); err != nil {
			return err
		}
		fmt.Fprintln(out, "plan cleared: config.rk plan applies")
	}
	now := time.Now()
	snap, err := plan.Take(ctx, db, reg, now)
	if err != nil {
		return err
	}
	rep := snap.Report(reg, plan.Period(reg, now))
	if pa.JSON {
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Fprintln(out, string(b))
		return nil
	}
	if snap.Current.Name == "" {
		fmt.Fprintln(out, "no plan: nothing is limited")
		return nil
	}
	fmt.Fprintf(out, "plan %s (%s, from %s)\n", snap.Current.Name, snap.Current.Label(), snap.Current.Source)
	meters := rep["usage"].(map[string]plan.Meter)
	for _, k := range []string{plan.Outlets, plan.Users, plan.AICredits} {
		m := meters[k]
		lim := "unlimited"
		if m.Limit != nil {
			lim = fmt.Sprint(*m.Limit)
		}
		fmt.Fprintf(out, "  %-17s %d / %s  %s\n", k, m.Used, lim, m.State)
	}
	for _, o := range keys(snap.ExcessOutlets) {
		fmt.Fprintf(out, "  read-only outlet  %s\n", o)
	}
	for _, p := range keys(snap.ExcessPeople) {
		fmt.Fprintf(out, "  read-only person  %s\n", p)
	}
	return nil
}

func keys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
