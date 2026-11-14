package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
)

// The seed never makes an account that is linked to upstream sign-in, and not yet in the org, its owner: that
// would let whoever holds the upstream identity in as the owner (final review I1).
func TestSeedRefusesLinkedAccount(t *testing.T) {
	e := newEnv(t, false, nil)
	ctx := context.Background()
	db := e.s.DB
	if err := auth.EnsureOrg(ctx, db, "other", "Other"); err != nil {
		t.Fatal(err)
	}
	uid, _, err := auth.UpsertMember(ctx, db, "linked@x.test", "L", "other", "sales_rep", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.LinkIdentity(ctx, db, "https://cloud.example", "sub-1", uid); err != nil {
		t.Fatal(err)
	}
	reg, err := LoadRegistry(e.s.Cfg)
	if err != nil {
		t.Fatal(err)
	}
	opts := SeedOptions{Org: "kopi-senja", FixturesDir: filepath.Join(filepath.Dir(e.apps), "fixtures"),
		Email: "Linked@x.test", Password: "rangka-demo", UserRecord: "USR-001", Now: fixedNow}
	_, err = Seed(ctx, db, reg, opts)
	if err == nil || !strings.Contains(err.Error(), "linked@x.test") || !strings.Contains(err.Error(), "https://cloud.example") {
		t.Fatalf("seed with a linked account: err = %v, want a refusal naming the account and the issuer", err)
	}
	res, err := db.Query(ctx, "select count(*) from rk_members where org_id = 'kopi-senja' and user_id = $1", uid)
	if err != nil {
		t.Fatal(err)
	}
	if n := res.Rows[0][0].(int64); n != 0 {
		t.Fatal("the refused seed wrote a membership")
	}
	// the seed's own owner (no links) seeds again as before
	opts.Email = "ceo@saga.co.id"
	if _, err := Seed(ctx, db, reg, opts); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
}

// Contract 0055 §3: the seed never points its owner at a user record that is another account's (record_taken).
func TestSeedRefusesTakenRecord(t *testing.T) {
	e := newEnv(t, false, nil)
	ctx := context.Background()
	db := e.s.DB
	reg, err := LoadRegistry(e.s.Cfg)
	if err != nil {
		t.Fatal(err)
	}
	opts := SeedOptions{Org: "kopi-senja", FixturesDir: filepath.Join(filepath.Dir(e.apps), "fixtures"),
		Email: "boss@x.test", Password: "rangka-demo", UserRecord: "USR-001", Now: fixedNow}
	if _, err := Seed(ctx, db, reg, opts); err == nil || !strings.Contains(err.Error(), "record_taken") {
		t.Fatalf("seed onto the owner's record: err = %v", err)
	}
	res, err := db.Query(ctx, "select count(*) from rk_users where email = 'boss@x.test'")
	if err != nil {
		t.Fatal(err)
	}
	if n := res.Rows[0][0].(int64); n != 0 {
		t.Fatal("the refused seed wrote an account")
	}
	// a record that does not exist (in the database or the fixtures), and moving the owner off its record
	opts.Email = "ceo@saga.co.id"
	for rec, code := range map[string]string{"USR-999": "unknown_record", "USR-002": "record_taken"} {
		opts.UserRecord = rec
		if _, err := Seed(ctx, db, reg, opts); err == nil || !strings.Contains(err.Error(), code) {
			t.Fatalf("seed onto %s: err = %v, want %s", rec, err, code)
		}
	}
	res, err = db.Query(ctx, "select record from rk_members m join rk_users u on u.id = m.user_id where u.email = 'ceo@saga.co.id' and m.org_id = 'kopi-senja'")
	if err != nil || res.Rows[0][0] != "USR-001" {
		t.Fatalf("a refused seed moved the owner: %v %v", res, err)
	}
	opts.UserRecord = "USR-001"
	if _, err := Seed(ctx, db, reg, opts); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
}
