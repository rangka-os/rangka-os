package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
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

// Contract 0056 §3: the seed never points its owner at a user record that is another account's (record_taken).
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

// A business seeded without sample data has no USR-001: `rangka seed` without --user-record (RecordOptional) still
// makes the owner, and the employee record sync creates the owner's record; an explicit record still must exist.
func TestSeedDefaultRecordWithoutFixtures(t *testing.T) {
	e := newEnv(t, false, nil)
	ctx := context.Background()
	db := e.s.DB
	reg, err := LoadRegistry(e.s.Cfg)
	if err != nil {
		t.Fatal(err)
	}
	opts := SeedOptions{Org: "usaha-baru", FixturesDir: t.TempDir(), Email: "owner@x.test", Password: "rangka-demo",
		UserRecord: "USR-001", Now: fixedNow}
	if _, err := Seed(ctx, db, reg, opts); err == nil || !strings.Contains(err.Error(), "unknown_record") {
		t.Fatalf("explicit missing record: err = %v, want unknown_record", err)
	}
	opts.RecordOptional = true
	for i := 0; i < 2; i++ { // and seeding again (a provision retry) is fine
		if _, err := Seed(ctx, db, reg, opts); err != nil {
			t.Fatalf("seed %d with the default record and no fixtures: %v", i+1, err)
		}
	}
	res, err := db.Query(ctx, "select m.role, m.record from rk_members m join rk_users u on u.id = m.user_id where u.email = 'owner@x.test' and m.org_id = 'usaha-baru'")
	if err != nil || len(res.Rows) != 1 || res.Rows[0][0] != "owner" {
		t.Fatalf("owner membership: %v %v", res, err)
	}
	if rec, _ := res.Rows[0][1].(string); rec == "" {
		t.Fatalf("owner record = %q, want the record the sync created", rec)
	}
}

// Contract 0076 §2: `rangka migrate` syncs config.rk's orgs before `rangka seed` runs. The defaults wait for an org's
// seed, which loads the fixtures first, so the fixtures keep their numbers (OUT-001…) and the defaults only fill what
// they left empty (kernel CI on 25 Nov 2026: demo-fnb's units were replaced by the defaults).
func TestDefaultsWaitForTheSeedOfANewOrg(t *testing.T) {
	e := newEnv(t, false, func(apps, _ string) {
		os.MkdirAll(filepath.Join(apps, "core", "defaults"), 0o755)
		os.WriteFile(filepath.Join(apps, "core", "defaults", "outlet.rk"),
			[]byte("outlet:\n  - {outlet_name: Pusat}\n  - {outlet_name: Gudang}\n"), 0o644)
	})
	ctx := context.Background()
	db := e.s.DB
	reg, err := LoadRegistry(e.s.Cfg)
	if err != nil {
		t.Fatal(err)
	}
	outlets := func(org string) []string {
		res, err := db.Query(pg.WithOrg(ctx, org), fmt.Sprintf("select name from %s where org_id = $1 order by name", pg.QuoteIdent(store.TableName("outlet"))), org)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range res.Rows {
			out = append(out, r[0].(string))
		}
		return out
	}
	for _, org := range []string{"dengan-data", "tanpa-data"} {
		if err := auth.EnsureOrg(ctx, db, org, org); err != nil {
			t.Fatal(err)
		}
	}
	// migrate / serve: neither org has been seeded, so neither gets anything yet
	if err := SeedDefaultsAll(ctx, db, reg, nil); err != nil {
		t.Fatal(err)
	}
	if got := append(outlets("dengan-data"), outlets("tanpa-data")...); len(got) != 0 {
		t.Fatalf("unseeded orgs got defaults: %v", got)
	}
	// seed with fixtures: the fixture's outlet keeps OUT-001 and the defaults add nothing to a filled table
	fx := t.TempDir()
	os.WriteFile(filepath.Join(fx, "outlet.rk"), []byte("outlet:\n  - {name: OUT-001, outlet_name: Dago}\n"), 0o644)
	seed := func(org, dir string) {
		if _, err := Seed(ctx, db, reg, SeedOptions{Org: org, FixturesDir: dir, Email: "owner-" + org + "@x.test",
			Password: "rangka-demo", RecordOptional: true, Now: fixedNow}); err != nil {
			t.Fatalf("seed %s: %v", org, err)
		}
	}
	seed("dengan-data", fx)
	if got := outlets("dengan-data"); !reflect.DeepEqual(got, []string{"OUT-001"}) {
		t.Fatalf("seeded with fixtures: outlets %v, want only the fixture's", got)
	}
	// seed without fixtures: the defaults
	seed("tanpa-data", t.TempDir())
	if got := outlets("tanpa-data"); len(got) != 2 {
		t.Fatalf("seeded without fixtures: outlets %v, want the two defaults", got)
	}
	// a later migrate adds nothing to either
	if err := SeedDefaultsAll(ctx, db, reg, nil); err != nil {
		t.Fatal(err)
	}
	if a, b := outlets("dengan-data"), outlets("tanpa-data"); len(a) != 1 || len(b) != 2 {
		t.Fatalf("after another migrate: %v %v", a, b)
	}
}
