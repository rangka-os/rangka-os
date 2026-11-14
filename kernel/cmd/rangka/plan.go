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
	"strconv"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/plan"
)

// planOut is where `rangka plan` reports (tests swap it; nil is stdout).
var planOut io.Writer

const planUsage = `usage: rangka plan show [--json] [--apps <dir>] [--site <dir>] [--db <url>]
       rangka plan set <plan> [--by <who>] [--note "..."] [--apps <dir>] [--site <dir>] [--db <url>]
       rangka plan clear [--by <who>] [--apps <dir>] [--site <dir>] [--db <url>]
       rangka plan grant <limit> <+n> [--until YYYY-MM-DD] [--ref <id>] [--by <who>] [--note "..."]
       rangka plan revoke <grant id> [--by <who>]
       rangka plan cap <limit> <value> [--reason <text>] [--by <who>]
       rangka plan uncap <limit> [--by <who>]
The tenant's plan (contract 0028): set is the operator's upgrade or downgrade (a plan of features.rk), clear
goes back to config.rk plan and removes grants and caps. Nothing is deleted on a downgrade: what is past a lower
limit becomes read-only. grant adds capacity to one limit (an add-on, bought credits), until the start of
--until (UTC) or until revoked; cap sets a ceiling below the plan (a full pool), contract 0057.
A running kernel picks the change up within 10 seconds.`

type planArgs struct {
	Sub, Plan, By, Note string
	JSON                bool
	c                   common
	// grant, revoke, cap, uncap (contract 0057 §2)
	Limit, Grant, Until, Ref, Reason string
	Amount                           int64
}

var planArity = map[string]int{"show": 0, "set": 1, "clear": 0, "grant": 2, "revoke": 1, "cap": 2, "uncap": 1}

func parsePlanArgs(args []string) (*planArgs, error) {
	if len(args) == 0 {
		return nil, usageError{"unknown or missing subcommand (want: show, set, clear, grant, revoke, cap or uncap)"}
	}
	if _, ok := planArity[args[0]]; !ok {
		return nil, usageError{"unknown or missing subcommand (want: show, set, clear, grant, revoke, cap or uncap)"}
	}
	pa := &planArgs{Sub: args[0]}
	fs := flag.NewFlagSet("plan "+pa.Sub, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	pa.c.flags(fs)
	fs.StringVar(&pa.By, "by", envOr("USER", "operator"), "who changes the plan (kept in rk_plan_changes)")
	fs.StringVar(&pa.Note, "note", "", "why (an invoice number, for example)")
	fs.BoolVar(&pa.JSON, "json", false, "show: print JSON")
	fs.StringVar(&pa.Until, "until", "", "grant: the grant counts until the start of this date (UTC), YYYY-MM-DD")
	fs.StringVar(&pa.Ref, "ref", "", "grant: an invoice or add-on id")
	fs.StringVar(&pa.Reason, "reason", "", "cap: why (pool)")
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
	want := planArity[pa.Sub]
	if len(positional) != want {
		return nil, usageError{fmt.Sprintf("plan %s takes %d argument(s), got %d", pa.Sub, want, len(positional))}
	}
	switch pa.Sub {
	case "set":
		pa.Plan = positional[0]
	case "revoke":
		pa.Grant = positional[0]
	case "uncap":
		pa.Limit = positional[0]
	case "grant", "cap":
		pa.Limit = positional[0]
		n, err := strconv.ParseInt(strings.TrimPrefix(positional[1], "+"), 10, 64)
		if err != nil {
			return nil, usageError{fmt.Sprintf("plan %s: %q is not a whole number", pa.Sub, positional[1])}
		}
		pa.Amount = n
	}
	if pa.Until != "" {
		if pa.Sub != "grant" {
			return nil, usageError{"--until is for grant"}
		}
		if _, err := time.Parse("2006-01-02", pa.Until); err != nil {
			return nil, usageError{fmt.Sprintf("--until %q is not a date (YYYY-MM-DD)", pa.Until)}
		}
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
	case "grant":
		var until *time.Time
		if pa.Until != "" {
			t, _ := time.Parse("2006-01-02", pa.Until)
			until = &t
		}
		g, err := plan.AddGrant(ctx, db, pa.Limit, pa.Amount, until, pa.Ref, pa.By, pa.Note, time.Now())
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "grant %s: %s +%d\n", g.ID, g.Limit, g.Amount)
	case "revoke":
		if err := plan.RevokeGrant(ctx, db, pa.Grant, pa.By); err != nil {
			return err
		}
		fmt.Fprintf(out, "grant %s revoked\n", pa.Grant)
	case "cap":
		if err := plan.SetCap(ctx, db, pa.Limit, pa.Amount, pa.Reason, pa.By, time.Now()); err != nil {
			return err
		}
		fmt.Fprintf(out, "cap %s %d\n", pa.Limit, pa.Amount)
	case "uncap":
		if err := plan.RemoveCap(ctx, db, pa.Limit, pa.By); err != nil {
			return err
		}
		fmt.Fprintf(out, "cap on %s removed\n", pa.Limit)
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
	for _, k := range plan.ReportOrder {
		m := meters[k]
		lim := "unlimited"
		if m.Limit != nil {
			lim = fmt.Sprint(*m.Limit)
		}
		extra := ""
		if m.PlanLimit != nil && m.Limit != nil && *m.PlanLimit != *m.Limit {
			extra = fmt.Sprintf("  (plan %d)", *m.PlanLimit)
		}
		fmt.Fprintf(out, "  %-17s %d / %s  %s%s\n", k, m.Used, lim, m.State, extra)
	}
	for _, g := range plan.SortedGrants(snap.Current.Stored, now) {
		until, state := "until revoked", "active"
		if g.Until != nil {
			until = "until " + g.Until.Format("2006-01-02")
		}
		if !g.Active(now) {
			state = "ended"
		}
		fmt.Fprintf(out, "  grant %s  %s +%d %s  %s  %s\n", g.ID, g.Limit, g.Amount, until, state, g.Ref)
	}
	if st := snap.Current.Stored; st != nil {
		for _, k := range sortedCapKeys(st.Caps) {
			fmt.Fprintf(out, "  cap   %s %d  %s\n", k, st.Caps[k].Value, st.Caps[k].Reason)
		}
	}
	for _, a := range keys(snap.ExcessApps) {
		fmt.Fprintf(out, "  read-only app     %s\n", a)
	}
	for _, o := range keys(snap.ExcessOutlets) {
		fmt.Fprintf(out, "  read-only outlet  %s\n", o)
	}
	for _, p := range keys(snap.ExcessPeople) {
		fmt.Fprintf(out, "  read-only person  %s\n", p)
	}
	return nil
}

func sortedCapKeys(m map[string]plan.Cap) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func keys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
