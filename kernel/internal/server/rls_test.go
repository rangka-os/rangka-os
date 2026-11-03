package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

func TestWarnsWhenRLSBypassed(t *testing.T) {
	ctx := context.Background()
	db, err := pg.NewPool(ctx, testdb.New(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var logged []string
	// the test cluster's user is a superuser
	warnIfBypassRLS(ctx, db, func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) })
	if len(logged) != 1 || logged[0] != "warning: connected as a superuser or BYPASSRLS role; row-level security is not enforced" {
		t.Fatalf("logged %q", logged)
	}
}
