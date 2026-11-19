package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/jobs"
)

// schedulerOut is where `rangka scheduler` reports (tests swap it; nil is stdout).
var schedulerOut io.Writer

const schedulerUsage = `usage: rangka scheduler pause --until <RFC3339> [--reason <text>] [--by <who>] [--db <url>]
       rangka scheduler resume [--by <who>] [--db <url>]
       rangka scheduler status [--json] [--db <url>]
Pauses the installation's scheduled jobs (cron flows and connector polls), contract 0070. While paused, slots that
come due are skipped and never caught up; jobs already queued still run. The pause ends by itself at --until, at
most 30 days ahead. A running kernel sees the change at its next tick.`

type schedulerArgs struct {
	Sub, Until, Reason, By string
	JSON                   bool
	until                  time.Time
	c                      common
}

func parseSchedulerArgs(args []string, now time.Time) (*schedulerArgs, error) {
	if len(args) == 0 || (args[0] != "pause" && args[0] != "resume" && args[0] != "status") {
		return nil, usageError{"unknown or missing subcommand (want: pause, resume or status)"}
	}
	sa := &schedulerArgs{Sub: args[0]}
	fs := flag.NewFlagSet("scheduler "+sa.Sub, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sa.c.flags(fs)
	fs.StringVar(&sa.Until, "until", "", "pause: when the pause ends, RFC 3339 (2026-11-20T00:00:00+07:00)")
	fs.StringVar(&sa.Reason, "reason", "", "pause: why (kept in the audit log)")
	fs.StringVar(&sa.By, "by", envOr("USER", "operator"), "who pauses or resumes (kept in the audit log)")
	fs.BoolVar(&sa.JSON, "json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return nil, usageError{err.Error()}
	}
	if fs.NArg() > 0 {
		return nil, usageError{fmt.Sprintf("scheduler %s takes no arguments, got %q", sa.Sub, fs.Args())}
	}
	if sa.Sub != "pause" && (sa.Until != "" || sa.Reason != "") {
		return nil, usageError{"--until and --reason are for pause"}
	}
	if sa.Sub == "pause" {
		if sa.Until == "" {
			return nil, usageError{fmt.Sprintf("pause needs --until (RFC 3339, at most %d days ahead)", int(jobs.MaxPause.Hours()/24))}
		}
		t, err := time.Parse(time.RFC3339, sa.Until)
		if err != nil {
			return nil, usageError{fmt.Sprintf("--until %q is not an RFC 3339 time (2026-11-20T00:00:00+07:00)", sa.Until)}
		}
		if err := jobs.CheckPause(t, sa.Reason, now); err != nil {
			return nil, usageError{err.Error()}
		}
		sa.until = t
	}
	return sa, nil
}

func cmdScheduler(args []string) int {
	err := runScheduler(args)
	var ue usageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ue):
		fmt.Fprintln(os.Stderr, "rangka scheduler:", err)
		fmt.Fprintln(os.Stderr, schedulerUsage)
		return 2
	default:
		return fail(err)
	}
}

func runScheduler(args []string) error {
	now := time.Now()
	sa, err := parseSchedulerArgs(args, now)
	if err != nil {
		return err
	}
	out := schedulerOut
	if out == nil {
		out = os.Stdout
	}
	ctx := context.Background()
	db, err := sa.c.pool(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	var st jobs.PauseState
	switch sa.Sub {
	case "pause":
		st, err = jobs.Pause(ctx, db, sa.until, sa.Reason, sa.By, now)
	case "resume":
		var changed bool
		st, changed, err = jobs.Resume(ctx, db, sa.By, now)
		if err == nil && !changed && !sa.JSON {
			fmt.Fprintln(out, "the scheduler was not paused")
		}
	case "status":
		st, err = jobs.ReadPause(ctx, db, now)
	}
	if err != nil {
		return err
	}
	if sa.JSON {
		b, _ := json.Marshal(st.JSON())
		fmt.Fprintln(out, string(b))
		return nil
	}
	if st.Paused {
		fmt.Fprintf(out, "scheduler paused until %s (since %s, by %s)", st.Until.UTC().Format(time.RFC3339), st.Since.UTC().Format(time.RFC3339), st.By)
		if st.Reason != "" {
			fmt.Fprintf(out, ": %s", st.Reason)
		}
		fmt.Fprintln(out)
		return nil
	}
	if st.Since.IsZero() {
		fmt.Fprintln(out, "scheduler running (never paused)")
		return nil
	}
	fmt.Fprintf(out, "scheduler running (last pause ended %s, by %s)\n", st.Since.UTC().Format(time.RFC3339), st.By)
	return nil
}
