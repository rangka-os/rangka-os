// Package testdb gives tests a fresh database on the server named by RANGKA_TEST_DATABASE_URL
// (make test starts a throwaway Postgres and sets it). Tests skip when it is not set.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// New creates an empty database and returns its URL; it is dropped when the test ends.
func New(t testing.TB) string {
	t.Helper()
	base := os.Getenv("RANGKA_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("RANGKA_TEST_DATABASE_URL not set (make test starts a temporary Postgres)")
	}
	ctx := context.Background()
	admin, err := pg.NewPool(ctx, base, 2)
	if err != nil {
		t.Fatalf("connect %s: %v", base, err)
	}
	b := make([]byte, 4)
	rand.Read(b)
	name := "rk_test_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "create database "+name); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(base)
	u.Path = "/" + name
	t.Cleanup(func() {
		admin.Exec(context.Background(), "drop database if exists "+name+" with (force)")
		admin.Close()
	})
	return u.String()
}
