package auth

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// legacyHash was produced by the PBKDF2 HashPassword (1000 iterations) for "hunter2-legacy".
const legacyHash = "pbkdf2-sha256$1000$3WGHqTvqsv1jPLDI6JwVvg$UcjkmVocxfgJPvrPBCq1TbE27M6br1Qqk8t2FLCN7xU"

func lowCost(t *testing.T) {
	old := BcryptCost
	BcryptCost = bcrypt.MinCost
	t.Cleanup(func() { BcryptCost = old })
}

func TestBcryptHash(t *testing.T) {
	lowCost(t)
	h, err := HashPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$2") {
		t.Fatalf("hash %q is not bcrypt", h)
	}
	if !CheckPassword(h, "s3cret") || CheckPassword(h, "other") {
		t.Fatal("CheckPassword wrong for bcrypt hash")
	}
	if NeedsRehash(h) {
		t.Fatal("fresh bcrypt hash at current cost should not need rehash")
	}
	BcryptCost = bcrypt.MinCost + 1
	if !NeedsRehash(h) {
		t.Fatal("lower-cost bcrypt hash should need rehash")
	}
}

func TestLegacyPBKDF2(t *testing.T) {
	if !CheckPassword(legacyHash, "hunter2-legacy") || CheckPassword(legacyHash, "nope") {
		t.Fatal("legacy hash verification broken")
	}
	if !NeedsRehash(legacyHash) {
		t.Fatal("pbkdf2 hash must need rehash")
	}
	if CheckPassword("garbage", "x") || CheckPassword("", "x") {
		t.Fatal("garbage hash must not verify")
	}
}

func TestTooLong(t *testing.T) {
	lowCost(t)
	if _, err := HashPassword(strings.Repeat("a", 73)); err == nil || !strings.Contains(err.Error(), "password longer than 72 bytes") {
		t.Fatalf("want too-long error, got %v", err)
	}
	if _, err := HashPassword(strings.Repeat("a", 72)); err != nil {
		t.Fatal(err)
	}
}

func TestRehashOnLogin(t *testing.T) {
	lowCost(t)
	ctx := context.Background()
	pool, err := pg.NewPool(ctx, testdb.New(t), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, store.SystemSQL()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "insert into rk_users (email, password_hash) values ('a@b.co', $1)", legacyHash); err != nil {
		t.Fatal(err)
	}
	if _, err := Authenticate(ctx, pool, "a@b.co", "wrong"); err != ErrBadLogin {
		t.Fatalf("want ErrBadLogin, got %v", err)
	}
	res, _ := pool.Query(ctx, "select password_hash from rk_users where email = 'a@b.co'")
	if res.Rows[0][0].(string) != legacyHash {
		t.Fatal("hash must not change after a failed login")
	}
	if _, err := Authenticate(ctx, pool, "a@b.co", "hunter2-legacy"); err != nil {
		t.Fatal(err)
	}
	res, _ = pool.Query(ctx, "select password_hash from rk_users where email = 'a@b.co'")
	h := res.Rows[0][0].(string)
	if !strings.HasPrefix(h, "$2") || !CheckPassword(h, "hunter2-legacy") {
		t.Fatalf("stored hash not upgraded: %q", h)
	}
}
