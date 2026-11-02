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

func TestUserAdd(t *testing.T) {
	url := testdb.New(t)
	auth.Iterations = 1000
	ctx := context.Background()
	db, err := pg.NewPool(ctx, url, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ctx, store.SystemSQL()); err != nil {
		t.Fatal(err)
	}

	if err := runUser([]string{"add", "--db", url, "--org", "t", "--email", "M@X.test", "--password", "pw-123456", "--role", "sales_manager"}); err != nil {
		t.Fatalf("user add: %v", err)
	}
	svc := &auth.Service{DB: db}
	_, u, err := svc.Login(ctx, "m@x.test", "pw-123456", "t")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if u.Org != "t" || len(u.Roles) != 1 || u.Roles[0] != "sales_manager" {
		t.Fatalf("user = %+v, want org t with roles [sales_manager]", u)
	}

	err = runUser([]string{"add", "--db", url, "--org", "t", "--email", "r@x.test", "--password", "pw-123456"})
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
