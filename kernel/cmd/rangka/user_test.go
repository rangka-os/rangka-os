package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// runUserOut runs `rangka user …` and returns what it printed.
func runUserOut(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	prev := userOut
	userOut = &buf
	defer func() { userOut = prev }()
	err := runUser(args)
	return buf.String(), err
}

func TestUserAdd(t *testing.T) {
	url := testdb.New(t)
	prevIter := auth.Iterations
	auth.Iterations = 1000
	t.Cleanup(func() { auth.Iterations = prevIter })
	ctx := context.Background()
	db, err := pg.NewPool(ctx, url, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ctx, store.SystemSQL()); err != nil {
		t.Fatal(err)
	}
	svc := &auth.Service{DB: db}
	login := func(email, pw string) (*auth.User, error) {
		_, u, err := svc.Login(ctx, email, pw, "t")
		return u, err
	}

	// an unknown org is refused unless --create-org
	_, err = runUserOut(t, "add", "--db", url, "--org", "t", "--email", "M@X.test", "--password", "pw-123456", "--role", "sales_manager")
	if err == nil || !strings.Contains(err.Error(), `"t"`) {
		t.Fatalf("unknown org: err = %v, want one naming the org", err)
	}

	out, err := runUserOut(t, "add", "--db", url, "--org", "t", "--create-org", "--email", "M@X.test", "--password", "pw-123456", "--role", "sales_manager")
	if err != nil {
		t.Fatalf("user add: %v", err)
	}
	if !strings.Contains(out, "user m@x.test is sales_manager in t") {
		t.Fatalf("output %q", out)
	}
	u, err := login("m@x.test", "pw-123456")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if u.Org != "t" || len(u.Roles) != 1 || u.Roles[0] != "sales_manager" {
		t.Fatalf("user = %+v, want org t with roles [sales_manager]", u)
	}

	// existing account, new password without --reset-password: unchanged, and the role change is reported
	out, err = runUserOut(t, "add", "--db", url, "--org", "t", "--email", "m@x.test", "--password", "other-pw-1", "--role", "sales_rep")
	if err != nil {
		t.Fatalf("user add again: %v", err)
	}
	if !strings.Contains(out, "account m@x.test exists; password unchanged") || !strings.Contains(out, "role sales_manager → sales_rep") {
		t.Fatalf("output %q", out)
	}
	if _, err := login("m@x.test", "pw-123456"); err != nil {
		t.Fatalf("old password should still work: %v", err)
	}
	if _, err := login("m@x.test", "other-pw-1"); err == nil {
		t.Fatal("new password must not work without --reset-password")
	}

	// --reset-password sets it
	out, err = runUserOut(t, "add", "--db", url, "--org", "t", "--email", "m@x.test", "--password", "other-pw-1", "--role", "sales_rep", "--reset-password")
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if !strings.Contains(out, "password reset") || strings.Contains(out, "role ") {
		t.Fatalf("output %q", out)
	}
	if _, err := login("m@x.test", "other-pw-1"); err != nil {
		t.Fatalf("new password after reset: %v", err)
	}

	_, err = runUserOut(t, "add", "--db", url, "--org", "t", "--email", "r@x.test", "--password", "pw-123456")
	if err == nil || !strings.Contains(err.Error(), "--role") {
		t.Fatalf("missing --role: err = %v, want one mentioning --role", err)
	}
}

func TestUserAddMissingFlagNoDB(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	err := runUser([]string{"add", "--org", "t", "--email", "a@b.test", "--password", "x", "--role", "owner"})
	if err == nil || !strings.Contains(err.Error(), "--db") {
		t.Fatalf("err = %v, want one mentioning --db", err)
	}
	if err := runUser([]string{"remove"}); err == nil {
		t.Fatal("unknown subcommand: want an error")
	}
}
