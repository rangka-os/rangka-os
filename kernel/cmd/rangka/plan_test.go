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
}

// user add refuses a new person past the people limit rk_site_plan holds (contract 0027 §2)
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
