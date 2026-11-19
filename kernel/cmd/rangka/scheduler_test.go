package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

func TestParseSchedulerArgs(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	for _, bad := range [][]string{nil, {"stop"}, {"pause"}, {"pause", "--until", "besok"}, {"pause", "--until", "2026-11-19T09:00:00Z"},
		{"pause", "--until", "2026-12-20T10:00:01Z"}, {"resume", "--until", "2026-11-20T00:00:00Z"}, {"status", "--reason", "x"},
		{"status", "extra"}, {"pause", "--until", "2026-11-20"}} {
		if _, err := parseSchedulerArgs(bad, now); err == nil {
			t.Errorf("%v: no error", bad)
		} else if _, ok := err.(usageError); !ok {
			t.Errorf("%v: %T, want usageError", bad, err)
		}
	}
	sa, err := parseSchedulerArgs([]string{"pause", "--until", "2026-11-20T00:00:00+07:00", "--reason", "menunggak", "--by", "staf"}, now)
	if err != nil || !sa.until.Equal(time.Date(2026, 11, 19, 17, 0, 0, 0, time.UTC)) || sa.Reason != "menunggak" || sa.By != "staf" {
		t.Fatalf("%+v %v", sa, err)
	}
	if sa, err = parseSchedulerArgs([]string{"status", "--json"}, now); err != nil || !sa.JSON {
		t.Fatalf("status: %+v %v", sa, err)
	}
	if code := cmdScheduler([]string{"pause"}); code != 2 {
		t.Fatalf("pause without --until: exit %d, want 2", code)
	}
}

func runSchedulerOut(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	prev := schedulerOut
	schedulerOut = &buf
	defer func() { schedulerOut = prev }()
	err := runScheduler(args)
	return buf.String(), err
}

// pause, status --json and resume against a migrated database, as Rangka Cloud runs them (contract 0070 §1).
func TestSchedulerCommands(t *testing.T) {
	url := testdb.New(t)
	ctx := context.Background()
	db, err := pg.NewPool(ctx, url, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Exec(ctx, store.SystemSQL()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL", url)
	status := func() map[string]any {
		t.Helper()
		out, err := runSchedulerOut(t, "status", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(out), &m); err != nil {
			t.Fatalf("status --json: %q: %v", out, err)
		}
		return m
	}
	if m := status(); m["paused"] != false || m["until"] != nil || m["since"] != nil || m["reason"] != "" {
		t.Fatalf("fresh status: %v", m)
	}
	until := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
	out, err := runSchedulerOut(t, "pause", "--until", until, "--reason", "menunggak", "--by", "staf@rangka.cloud")
	if err != nil || !strings.Contains(out, "paused until "+until) {
		t.Fatalf("pause: %q %v", out, err)
	}
	m := status()
	if m["paused"] != true || m["until"] != until || m["reason"] != "menunggak" || m["since"] == nil {
		t.Fatalf("paused status: %v", m)
	}
	if _, err := time.Parse(time.RFC3339, m["since"].(string)); err != nil {
		t.Fatalf("since: %v", err)
	}
	if code := cmdScheduler([]string{"resume", "--by", "staf@rangka.cloud"}); code != 0 {
		t.Fatalf("resume: exit %d", code)
	}
	if m := status(); m["paused"] != false || m["until"] != nil || m["since"] == nil {
		t.Fatalf("resumed status: %v", m)
	}
	if out, err := runSchedulerOut(t, "resume"); err != nil || !strings.Contains(out, "was not paused") {
		t.Fatalf("second resume: %q %v", out, err)
	}
	l, err := store.ListAudit(ctx, db, store.AuditFilter{Kind: "scheduler."})
	if err != nil || len(l) != 2 || l[0].Kind != "scheduler.resumed" || l[1].Kind != "scheduler.paused" || l[1].Actor != "staf@rangka.cloud" {
		t.Fatalf("audit: %+v %v", l, err)
	}
}
