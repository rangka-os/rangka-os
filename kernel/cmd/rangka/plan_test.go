package main

import (
	"context"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

func TestParsePlanArgs(t *testing.T) {
	for _, bad := range [][]string{nil, {"naik"}, {"set"}, {"show", "x"}, {"set", "a", "b"}} {
		if _, err := parsePlanArgs(bad); err == nil {
			t.Errorf("%v: no error", bad)
		}
	}
	pa, err := parsePlanArgs([]string{"set", "--note", "JRG/2026/11/0001", "tumbuh", "--by", "ops"})
	if err != nil || pa.Plan != "tumbuh" || pa.Note != "JRG/2026/11/0001" || pa.By != "ops" {
		t.Fatalf("%+v %v", pa, err)
	}
	// grants and caps (contract 0057 §2)
	for _, bad := range [][]string{{"grant", "db_mb"}, {"grant", "db_mb", "satu"}, {"grant", "db_mb", "+1", "--until", "besok"},
		{"cap", "db_mb"}, {"revoke"}, {"uncap"}, {"set", "x", "--until", "2026-12-01"}} {
		if _, err := parsePlanArgs(bad); err == nil {
			t.Errorf("%v: no error", bad)
		}
	}
	pa, err = parsePlanArgs([]string{"grant", "db_mb", "+1024", "--until", "2027-02-14", "--ref", "ADDON-7"})
	if err != nil || pa.Limit != "db_mb" || pa.Amount != 1024 || pa.Until != "2027-02-14" || pa.Ref != "ADDON-7" {
		t.Fatalf("grant: %+v %v", pa, err)
	}
	pa, err = parsePlanArgs([]string{"cap", "files_mb", "2048", "--reason", "pool"})
	if err != nil || pa.Limit != "files_mb" || pa.Amount != 2048 || pa.Reason != "pool" {
		t.Fatalf("cap: %+v %v", pa, err)
	}
	if pa, err = parsePlanArgs([]string{"revoke", "g1a2b3c4d"}); err != nil || pa.Grant != "g1a2b3c4d" {
		t.Fatalf("revoke: %+v %v", pa, err)
	}
}

// user add refuses a new person past the people limit rk_site_plan holds (contract 0028 §2)
func TestUserAddPlanLimit(t *testing.T) {
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
	if _, err := db.Exec(ctx, `insert into rk_site_plan (plan, source, limits) values ('mulai', 'operator', '{"users": 2}')`); err != nil {
		t.Fatal(err)
	}
	auth.Iterations = 1000
	t.Setenv("DATABASE_URL", url)
	t.Setenv("RANGKA_USER_PASSWORD", "rahasia-1")
	for _, e := range []string{"owner@kopi.test", "staf@kopi.test"} {
		if _, err := runUserOut(t, "add", e, "--org", "kopi", "--role", "owner", "--create-org"); err != nil {
			t.Fatalf("%s: %v", e, err)
		}
	}
	_, err = runUserOut(t, "add", "ketiga@kopi.test", "--org", "kopi", "--role", "owner")
	if err == nil || !strings.Contains(err.Error(), "Paket mulai berlaku untuk 2 pengguna") {
		t.Fatalf("third person: %v", err)
	}
	if code := cmdUser([]string{"add", "ketiga@kopi.test", "--org", "kopi", "--role", "owner"}); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	// an existing member may change role or org: not a new person
	if _, err := runUserOut(t, "add", "staf@kopi.test", "--org", "kopi", "--role", "cashier"); err != nil {
		t.Fatalf("existing member: %v", err)
	}
}
