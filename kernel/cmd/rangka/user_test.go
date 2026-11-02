package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

func TestParseUserArgs(t *testing.T) {
	ua, err := parseUserArgs([]string{"add", "Staff@Example.test", "--org", "kopi-senja", "--role", "outlet_staff", "--outlets", "OUT-01, OUT-02", "--record", "USR-009", "--name", "Staf Satu", "--db", "postgres://x"})
	if err != nil {
		t.Fatal(err)
	}
	want := &userArgs{Sub: "add", Email: "staff@example.test", Org: "kopi-senja", Role: "outlet_staff", Outlets: []string{"OUT-01", "OUT-02"},
		SetOutlet: true, Record: "USR-009", Name: "Staf Satu", DB: "postgres://x"}
	if !reflect.DeepEqual(ua, want) {
		t.Fatalf("got %+v", ua)
	}
	// flags before the email, empty outlets clears
	ua, err = parseUserArgs([]string{"add", "--org", "o", "--role", "r", "--outlets", "", "a@b.test"})
	if err != nil || ua.Email != "a@b.test" || !ua.SetOutlet || len(ua.Outlets) != 0 {
		t.Fatalf("got %+v, %v", ua, err)
	}
	// no --outlets: leave them alone
	if ua, _ = parseUserArgs([]string{"add", "a@b.test", "--org", "o", "--role", "r"}); ua == nil || ua.SetOutlet {
		t.Fatalf("got %+v", ua)
	}
	ua, err = parseUserArgs([]string{"remove", "a@b.test", "--org", "o"})
	if err != nil || ua.Sub != "remove" || ua.Org != "o" {
		t.Fatalf("got %+v, %v", ua, err)
	}
	for _, bad := range [][]string{
		nil, {"nope"}, {"add"}, {"add", "a@b.test"}, {"add", "a@b.test", "--org", "o"}, {"add", "--org", "o", "--role", "r"},
		{"remove", "a@b.test"}, {"remove", "a@b.test", "--org", "o", "--role", "r"}, {"add", "a@b.test", "--org", "o", "--role", "r", "--bogus", "1"},
	} {
		t.Setenv("RANGKA_ORG", "")
		if _, err := parseUserArgs(bad); err == nil {
			t.Errorf("%v: want an error", bad)
		}
	}
}

func TestUserAddRemove(t *testing.T) {
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
	auth.Iterations = 1000
	t.Setenv("DATABASE_URL", url)
	t.Setenv("RANGKA_ORG", "")
	t.Setenv("RANGKA_USER_PASSWORD", "")

	add := []string{"add", "staff@example.test", "--org", "pt-baru", "--role", "outlet_staff", "--outlets", "OUT-01,OUT-02", "--name", "Staf"}
	if code := cmdUser(add); code != 2 {
		t.Fatalf("new user without password: exit %d", code)
	}
	t.Setenv("RANGKA_USER_PASSWORD", "rahasia-1")
	if code := cmdUser(add); code != 0 {
		t.Fatalf("add: exit %d", code)
	}
	row := func(q string) []any {
		t.Helper()
		res, err := db.Query(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("%s: %d rows", q, len(res.Rows))
		}
		return res.Rows[0]
	}
	if r := row("select name from rk_orgs where id = 'pt-baru'"); r[0] != "Pt Baru" {
		t.Fatalf("org: %v", r)
	}
	id, err := auth.Authenticate(ctx, db, "staff@example.test", "rahasia-1")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	u, err := auth.LoadUser(ctx, db, id, "pt-baru")
	if err != nil || !reflect.DeepEqual(u.Roles, []string{"outlet_staff"}) || !reflect.DeepEqual(u.Outlets, []string{"OUT-01", "OUT-02"}) {
		t.Fatalf("user: %+v %v", u, err)
	}
	// existing user: password stays, outlets change only with --outlets, empty clears
	t.Setenv("RANGKA_USER_PASSWORD", "lain-sekali")
	if code := cmdUser([]string{"add", "staff@example.test", "--org", "pt-baru", "--role", "outlet_staff", "--outlets", "OUT-03"}); code != 0 {
		t.Fatalf("re-add: exit %d", code)
	}
	if _, err := auth.Authenticate(ctx, db, "staff@example.test", "rahasia-1"); err != nil {
		t.Fatalf("password changed: %v", err)
	}
	if u, _ = auth.LoadUser(ctx, db, id, "pt-baru"); !reflect.DeepEqual(u.Outlets, []string{"OUT-03"}) {
		t.Fatalf("outlets: %v", u.Outlets)
	}
	t.Setenv("RANGKA_USER_PASSWORD", "")
	if code := cmdUser([]string{"add", "staff@example.test", "--org", "pt-baru", "--role", "outlet_staff"}); code != 0 {
		t.Fatalf("re-add without password: exit %d", code)
	}
	if u, _ = auth.LoadUser(ctx, db, id, "pt-baru"); !reflect.DeepEqual(u.Outlets, []string{"OUT-03"}) {
		t.Fatalf("outlets changed without --outlets: %v", u.Outlets)
	}
	if code := cmdUser([]string{"add", "staff@example.test", "--org", "pt-baru", "--role", "outlet_staff", "--outlets", ""}); code != 0 {
		t.Fatal("clear outlets")
	}
	if u, _ = auth.LoadUser(ctx, db, id, "pt-baru"); len(u.Outlets) != 0 {
		t.Fatalf("outlets not cleared: %v", u.Outlets)
	}
	if code := cmdUser([]string{"remove", "staff@example.test", "--org", "pt-baru"}); code != 0 {
		t.Fatalf("remove: exit %d", code)
	}
	if _, err := auth.LoadUser(ctx, db, id, "pt-baru"); err != auth.ErrNoMembership {
		t.Fatalf("still a member: %v", err)
	}
	if code := cmdUser([]string{"remove", "nobody@example.test", "--org", "pt-baru"}); code != 1 {
		t.Fatalf("remove unknown: exit %d", code)
	}
}
