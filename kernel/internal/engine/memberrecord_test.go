package engine

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// Contract 0055 §2/§5: the kernel keeps the `user` record of a member in step with rk_members.

const mrOrg = "kopi-senja"

func memberRecordSetup(t *testing.T) (*Engine, context.Context) {
	t.Helper()
	e, _ := setup(t)
	ctx := context.Background()
	if err := auth.EnsureOrg(ctx, e.DB, mrOrg, "Kopi Senja"); err != nil {
		t.Fatal(err)
	}
	return e, ctx
}

func addMember(t *testing.T, e *Engine, email, name, role string, outlets []string) int64 {
	t.Helper()
	id, _, err := auth.UpsertMember(context.Background(), e.DB, email, name, mrOrg, role, outlets)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func syncRecord(t *testing.T, e *Engine, userID int64) string {
	t.Helper()
	name, err := e.SyncMemberRecord(context.Background(), mrOrg, userID)
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func userRecord(t *testing.T, e *Engine, name string) map[string]any {
	t.Helper()
	rec, err := e.Get(context.Background(), Actor{Org: mrOrg, ID: "system", System: true}, "user", name)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func recordOutlets(rec map[string]any) []string {
	out := []string{}
	for _, r := range rows(rec["outlets"]) {
		out = append(out, str(r["outlet"]))
	}
	return out
}

func memberRecordColumn(t *testing.T, e *Engine, userID int64) string {
	t.Helper()
	res, err := e.DB.Query(context.Background(), "select coalesce(record, '') from rk_members where org_id = $1 and user_id = $2", mrOrg, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) == 0 {
		return ""
	}
	return res.Rows[0][0].(string)
}

func countUsers(t *testing.T, e *Engine) int64 {
	t.Helper()
	res, err := e.DB.Query(context.Background(), `select count(*) from r_user where org_id = $1`, mrOrg)
	if err != nil {
		t.Fatal(err)
	}
	return res.Rows[0][0].(int64)
}

func TestSyncCreatesRecord(t *testing.T) {
	e, _ := memberRecordSetup(t)
	if !e.HasMemberRecord() {
		t.Fatal("catalogue user has account_id: want HasMemberRecord")
	}
	id := addMember(t, e, "kasir@example.test", "Sari Kasir", "cashier", []string{"OUT-01"})
	name := syncRecord(t, e, id)
	if name == "" {
		t.Fatal("no record name")
	}
	rec := userRecord(t, e, name)
	if toInt(rec["account_id"]) != id || str(rec["email"]) != "kasir@example.test" || str(rec["role"]) != "cashier" ||
		str(rec["access"]) != "pending" || str(rec["full_name"]) != "Sari Kasir" {
		t.Fatalf("record %+v", rec)
	}
	if got := recordOutlets(rec); !reflect.DeepEqual(got, []string{"OUT-01"}) {
		t.Fatalf("outlets %v", got)
	}
	if got := memberRecordColumn(t, e, id); got != name {
		t.Fatalf("rk_members.record = %q, want %q", got, name)
	}
}

func TestSyncActiveAfterSignIn(t *testing.T) {
	e, ctx := memberRecordSetup(t)
	id := addMember(t, e, "kasir@example.test", "Sari", "cashier", nil)
	name := syncRecord(t, e, id)
	if _, err := e.DB.Exec(ctx, "update rk_users set signed_in_at = now() where id = $1", id); err != nil {
		t.Fatal(err)
	}
	if again := syncRecord(t, e, id); again != name {
		t.Fatalf("sync moved to %q from %q", again, name)
	}
	if rec := userRecord(t, e, name); str(rec["access"]) != "active" {
		t.Fatalf("access %v", rec["access"])
	}
}

func TestSyncRemovedKeepsRecord(t *testing.T) {
	e, ctx := memberRecordSetup(t)
	id := addMember(t, e, "kasir@example.test", "Sari", "cashier", []string{"OUT-01", "OUT-02"})
	name := syncRecord(t, e, id)
	if err := auth.RemoveMember(ctx, e.DB, mrOrg, id); err != nil {
		t.Fatal(err)
	}
	if again := syncRecord(t, e, id); again != name {
		t.Fatalf("sync after removal gave %q, want %q", again, name)
	}
	rec := userRecord(t, e, name)
	if str(rec["access"]) != "removed" || len(recordOutlets(rec)) != 0 || toInt(rec["account_id"]) != id {
		t.Fatalf("record %+v", rec)
	}
}

func TestSyncReinviteReusesRecord(t *testing.T) {
	e, ctx := memberRecordSetup(t)
	id := addMember(t, e, "kasir@example.test", "Sari", "cashier", nil)
	name := syncRecord(t, e, id)
	if err := auth.RemoveMember(ctx, e.DB, mrOrg, id); err != nil {
		t.Fatal(err)
	}
	syncRecord(t, e, id)
	before := countUsers(t, e)
	addMember(t, e, "kasir@example.test", "Sari", "sales_rep", nil)
	if got := memberRecordColumn(t, e, id); got != "" {
		t.Fatalf("a new membership starts with no record, got %q", got)
	}
	if again := syncRecord(t, e, id); again != name {
		t.Fatalf("reinvite made %q, want the old record %q", again, name)
	}
	if after := countUsers(t, e); after != before {
		t.Fatalf("user records %d → %d", before, after)
	}
	rec := userRecord(t, e, name)
	if str(rec["access"]) != "pending" || str(rec["role"]) != "sales_rep" {
		t.Fatalf("record %+v", rec)
	}
}

func TestSyncTakesOverByEmailOnce(t *testing.T) {
	e, ctx := memberRecordSetup(t)
	sys := Actor{Org: mrOrg, ID: "system", System: true}
	// one record with the e-mail and no account: taken over (the e-mail is compared case-insensitively)
	var one string
	if err := e.run(ctx, sys, func(o *op) error {
		res, err := o.create("user", map[string]any{"full_name": "Budi Lama", "email": "Budi@Example.test"}, CreateOpts{})
		if err == nil {
			one = str(res.Record["name"])
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	id := addMember(t, e, "budi@example.test", "Budi", "cashier", nil)
	if got := syncRecord(t, e, id); got != one {
		t.Fatalf("took %q, want %q", got, one)
	}
	if rec := userRecord(t, e, one); toInt(rec["account_id"]) != id || str(rec["full_name"]) != "Budi Lama" {
		t.Fatalf("record %+v", rec)
	}

	// two such records: neither is taken, a new one is made
	var twins []string
	for _, n := range []string{"Ani Satu", "Ani Dua"} {
		if err := e.run(ctx, sys, func(o *op) error {
			res, err := o.create("user", map[string]any{"full_name": n, "email": "ani@example.test"}, CreateOpts{})
			if err == nil {
				twins = append(twins, str(res.Record["name"]))
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	ani := addMember(t, e, "ani@example.test", "Ani", "cashier", nil)
	got := syncRecord(t, e, ani)
	if got == twins[0] || got == twins[1] {
		t.Fatalf("took over %q of two candidates", got)
	}
	for _, n := range twins {
		if rec := userRecord(t, e, n); toInt(rec["account_id"]) != 0 || str(rec["access"]) != "none" {
			t.Fatalf("twin %s touched: %+v", n, rec)
		}
	}
}

func TestRepairBackfillsAndIsQuiet(t *testing.T) {
	e, ctx := memberRecordSetup(t)
	a := addMember(t, e, "a@example.test", "A", "cashier", []string{"OUT-01"})
	b := addMember(t, e, "b@example.test", "B", "sales_rep", nil)
	n, err := e.RepairMemberRecords(ctx, mrOrg)
	if err != nil || n != 2 {
		t.Fatalf("first repair: %d, %v", n, err)
	}
	for _, id := range []int64{a, b} {
		if memberRecordColumn(t, e, id) == "" {
			t.Fatalf("member %d has no record after repair", id)
		}
	}
	if n, err = e.RepairMemberRecords(ctx, mrOrg); err != nil || n != 0 {
		t.Fatalf("second repair: %d, %v", n, err)
	}
	// a removed member is marked by the repair too, once
	if err := auth.RemoveMember(ctx, e.DB, mrOrg, b); err != nil {
		t.Fatal(err)
	}
	if n, err = e.RepairMemberRecords(ctx, mrOrg); err != nil || n != 1 {
		t.Fatalf("repair after removal: %d, %v", n, err)
	}
	if n, err = e.RepairMemberRecords(ctx, mrOrg); err != nil || n != 0 {
		t.Fatalf("repair after removal, again: %d, %v", n, err)
	}
}

func TestNoMemberRecordModel(t *testing.T) {
	e, ctx := memberRecordSetup(t)
	// the catalogue with a user model that has no account_id (apps from before contract 0055)
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(testdb.Catalogue("apps"))); err != nil {
		t.Fatal(err)
	}
	old := "# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/model.json\n" +
		"name: user\nlabel: {id: Pengguna}\ntitle_field: full_name\nnaming: USR-{###}\nfields:\n" +
		"  - {name: full_name, type: data, label: {id: Nama}, required: true}\n" +
		"  - {name: role, type: data, label: {id: Peran}}\n"
	if err := os.WriteFile(filepath.Join(dir, "core", "models", "user.rk"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, "core", "models", "user_outlet.rk"))
	reg, rep := meta.Validate(dir, testdb.Catalogue("sites/kopi-senja"), nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("metadata errors: %v", rep.All())
	}
	e.SetRegistry(reg)
	if e.HasMemberRecord() {
		t.Fatal("user without account_id: want no member record model")
	}
	before := countUsers(t, e)
	id := addMember(t, e, "kasir@example.test", "Sari", "cashier", nil)
	if name, err := e.SyncMemberRecord(ctx, mrOrg, id); err != nil || name != "" {
		t.Fatalf("sync: %q, %v", name, err)
	}
	if n, err := e.RepairMemberRecords(ctx, mrOrg); err != nil || n != 0 {
		t.Fatalf("repair: %d, %v", n, err)
	}
	if after := countUsers(t, e); after != before || memberRecordColumn(t, e, id) != "" {
		t.Fatalf("records %d → %d, rk_members.record %q", before, after, memberRecordColumn(t, e, id))
	}
}

// Contract 0055 §5 upgrade: an account that had a session or created an API token before signed_in_at existed has
// signed in, so the migration backfills signed_in_at and the repair shows it active; an invitee without either stays
// pending (a password or an identity link alone is no proof: invitees get both).
func TestUpgradeBackfillsSignedIn(t *testing.T) {
	e, ctx := memberRecordSetup(t)
	withSession := addMember(t, e, "sesi@example.test", "Sesi", "cashier", nil)
	withToken := addMember(t, e, "token@example.test", "Token", "cashier", nil)
	invitee := addMember(t, e, "baru@example.test", "Baru", "cashier", nil)
	if _, err := e.DB.Exec(ctx, "update rk_users set password_hash = 'x' where id = $1", invitee); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Query(ctx, "insert into rk_sessions (id, user_id, org_id, created_at, expires_at) values ('h1', $1, $2, now() - interval '90 days', now() - interval '89 days')", withSession, mrOrg); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Query(ctx, "insert into rk_tokens (user_id, org_id, token_hash) values ($1, $2, 'h2')", withToken, mrOrg); err != nil {
		t.Fatal(err)
	}
	// as before the column existed: nobody has signed_in_at, then the system migration runs (as on every start)
	if _, err := e.DB.Exec(ctx, "update rk_users set signed_in_at = null"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // idempotent: runs on every start
		if _, err := e.DB.Exec(ctx, store.SystemSQL()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.RepairMemberRecords(ctx, mrOrg); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]string{withSession: "active", withToken: "active", invitee: "pending"} {
		name := memberRecordColumn(t, e, id)
		if got := str(userRecord(t, e, name)["access"]); got != want {
			t.Fatalf("account %d: access %q, want %q", id, got, want)
		}
	}
	res, err := e.DB.Query(ctx, "select signed_in_at < now() - interval '80 days' from rk_users where id = $1", withSession)
	if err != nil || res.Rows[0][0] != true {
		t.Fatalf("signed_in_at should be the first session's time: %v %v", res.Rows, err)
	}
}

// A membership whose record carries another account's account_id is not adopted: the sync falls through to the
// lookup (here: a new record), so the two accounts never take the record from each other on every repair.
func TestSyncSkipsRecordOfAnotherAccount(t *testing.T) {
	e, ctx := memberRecordSetup(t)
	sari := addMember(t, e, "sari@example.test", "Sari", "cashier", nil)
	sariRec := syncRecord(t, e, sari)
	budi := addMember(t, e, "budi@example.test", "Budi", "cashier", nil)
	if _, err := e.DB.Query(ctx, "update rk_members set record = $3 where org_id = $1 and user_id = $2", mrOrg, budi, sariRec); err != nil {
		t.Fatal(err)
	}
	budiRec := syncRecord(t, e, budi)
	if budiRec == sariRec || budiRec == "" {
		t.Fatalf("budi synced to %q (sari's is %q)", budiRec, sariRec)
	}
	if got := memberRecordColumn(t, e, budi); got != budiRec {
		t.Fatalf("budi's membership record %q, want %q", got, budiRec)
	}
	if acc := toInt(userRecord(t, e, sariRec)["account_id"]); acc != sari {
		t.Fatalf("sari's record account_id %d, want %d", acc, sari)
	}
	if n, err := e.RepairMemberRecords(ctx, mrOrg); err != nil || n != 0 {
		t.Fatalf("repair after: %d %v", n, err)
	}
}
