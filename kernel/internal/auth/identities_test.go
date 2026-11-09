package auth

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

func identEnv(t *testing.T) (context.Context, *pg.Pool) {
	t.Helper()
	ctx := context.Background()
	db, err := pg.NewPool(ctx, testdb.New(t), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Exec(ctx, store.SystemSQL()); err != nil {
		t.Fatal(err)
	}
	if err := EnsureOrg(ctx, db, "pt_alpha", "PT Alpha"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureOrg(ctx, db, "pt_beta", "PT Beta"); err != nil {
		t.Fatal(err)
	}
	return ctx, db
}

func mustUser(t *testing.T, ctx context.Context, db *pg.Pool, email string) int64 {
	t.Helper()
	id, _, err := UpsertMember(ctx, db, email, "", "pt_alpha", "sales_rep", nil)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestIdentityRoundTrip(t *testing.T) {
	ctx, db := identEnv(t)
	uid := mustUser(t, ctx, db, "a@x.id")
	if _, err := UserByIdentity(ctx, db, "https://id.example", "sub-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := LinkIdentity(ctx, db, "https://id.example", "sub-1", uid); err != nil {
		t.Fatal(err)
	}
	if err := LinkIdentity(ctx, db, "https://id.example", "sub-1", uid); err != nil {
		t.Fatalf("relink same user must be idempotent: %v", err)
	}
	got, err := UserByIdentity(ctx, db, "https://id.example", "sub-1")
	if err != nil || got != uid {
		t.Fatalf("got %d, %v", got, err)
	}
	if _, err := UserByIdentity(ctx, db, "https://other.example", "sub-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("issuer must be part of the key, got %v", err)
	}
}

func TestLinkIdentityConflict(t *testing.T) {
	ctx, db := identEnv(t)
	u1 := mustUser(t, ctx, db, "a@x.id")
	u2 := mustUser(t, ctx, db, "b@x.id")
	if err := LinkIdentity(ctx, db, "iss", "sub", u1); err != nil {
		t.Fatal(err)
	}
	if err := LinkIdentity(ctx, db, "iss", "sub", u2); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	if got, _ := UserByIdentity(ctx, db, "iss", "sub"); got != u1 {
		t.Fatalf("link moved to %d", got)
	}
}

func TestLinkIdentityConflictInsideTx(t *testing.T) {
	ctx, db := identEnv(t)
	u1 := mustUser(t, ctx, db, "a@x.id")
	u2 := mustUser(t, ctx, db, "b@x.id")
	if err := LinkIdentity(ctx, db, "iss", "sub", u1); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := LinkIdentity(ctx, tx, "iss", "sub", u2); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict from a transaction, got %v", err)
	}
	// the conflict must not abort the transaction: the caller can still run statements
	if _, err := tx.Query(ctx, "select 1"); err != nil {
		t.Fatalf("transaction aborted by the conflict: %v", err)
	}
}

func TestIdentityCascadesOnUserDelete(t *testing.T) {
	ctx, db := identEnv(t)
	uid := mustUser(t, ctx, db, "a@x.id")
	if err := LinkIdentity(ctx, db, "iss", "sub", uid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "delete from rk_users where id = $1", uid); err != nil {
		t.Fatal(err)
	}
	if _, err := UserByIdentity(ctx, db, "iss", "sub"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound after cascade, got %v", err)
	}
}

func TestUpsertMemberCreatesPasswordless(t *testing.T) {
	ctx, db := identEnv(t)
	id, created, err := UpsertMember(ctx, db, "  Dewi@X.id ", "Dewi", "pt_alpha", "sales_rep", []string{"JKT"})
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	res, err := db.Query(ctx, "select email, full_name, password_hash is null from rk_users where id = $1", id)
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Rows[0]; r[0] != "dewi@x.id" || r[1] != "Dewi" || r[2] != true {
		t.Fatalf("row %v", r)
	}
	u, err := LoadUser(ctx, db, id, "pt_alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(u.Roles, []string{"sales_rep"}) || !reflect.DeepEqual(u.Outlets, []string{"JKT"}) {
		t.Fatalf("%+v", u)
	}
	if _, err := Authenticate(ctx, db, "dewi@x.id", ""); !errors.Is(err, ErrBadLogin) {
		t.Fatalf("passwordless user must not authenticate, got %v", err)
	}
}

func TestUpsertMemberUpdatesAndKeepsPassword(t *testing.T) {
	lowCost(t)
	ctx, db := identEnv(t)
	id, err := EnsureUser(ctx, db, "a@x.id", "", "rahasia-123", "pt_alpha", "sales_rep", "USR-001")
	if err != nil {
		t.Fatal(err)
	}
	id2, created, err := UpsertMember(ctx, db, "A@x.id", "Ayu", "pt_alpha", "sales_manager", []string{"JKT", "BDG"})
	if err != nil || created || id2 != id {
		t.Fatalf("id=%d created=%v err=%v", id2, created, err)
	}
	if _, _, err := UpsertMember(ctx, db, "a@x.id", "Lain", "pt_alpha", "sales_manager", nil); err != nil {
		t.Fatal(err)
	}
	res, _ := db.Query(ctx, "select full_name from rk_users where id = $1", id)
	if res.Rows[0][0] != "Ayu" {
		t.Fatalf("name %v: set only when empty", res.Rows[0][0])
	}
	if _, err := Authenticate(ctx, db, "a@x.id", "rahasia-123"); err != nil {
		t.Fatalf("password must survive: %v", err)
	}
	u, _ := LoadUser(ctx, db, id, "pt_alpha")
	if !reflect.DeepEqual(u.Roles, []string{"sales_manager"}) || len(u.Outlets) != 0 || u.Record != "USR-001" {
		t.Fatalf("%+v", u)
	}
}

func TestUpsertMemberProtectsOwner(t *testing.T) {
	ctx, db := identEnv(t)
	if _, _, err := UpsertMember(ctx, db, "new@x.id", "N", "pt_alpha", "owner", nil); !errors.Is(err, ErrOwnerProtected) {
		t.Fatalf("owner role for a new member: %v", err)
	}
	if res, _ := db.Query(ctx, "select 1 from rk_users where email = 'new@x.id'"); len(res.Rows) != 0 {
		t.Fatal("refused owner must not create the user")
	}
	own, err := EnsureUser(ctx, db, "o@x.id", "", "rahasia-123", "pt_alpha", "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := UpsertMember(ctx, db, "o@x.id", "X", "pt_alpha", "sales_rep", []string{"JKT"}); !errors.Is(err, ErrOwnerProtected) {
		t.Fatalf("demoting an owner: %v", err)
	}
	if _, _, err := UpsertMember(ctx, db, "o@x.id", "X", "pt_alpha", "owner", nil); !errors.Is(err, ErrOwnerProtected) {
		t.Fatalf("owner to owner: %v", err)
	}
	u, _ := LoadUser(ctx, db, own, "pt_alpha")
	if !reflect.DeepEqual(u.Roles, []string{"owner"}) || len(u.Outlets) != 0 {
		t.Fatalf("owner changed: %+v", u)
	}
	// an owner in another org does not block a member role here
	if _, _, err := UpsertMember(ctx, db, "o@x.id", "X", "pt_beta", "sales_rep", nil); err != nil {
		t.Fatalf("owner of another org: %v", err)
	}
	// inside a transaction the refusal leaves the tx usable
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, _, err := UpsertMember(ctx, tx, "o@x.id", "X", "pt_alpha", "sales_rep", nil); !errors.Is(err, ErrOwnerProtected) {
		t.Fatal(err)
	}
	if _, err := tx.Query(ctx, "select 1"); err != nil {
		t.Fatalf("tx aborted: %v", err)
	}
}

func TestUpsertMemberLeavesOtherOrgAlone(t *testing.T) {
	ctx, db := identEnv(t)
	id, _, err := UpsertMember(ctx, db, "a@x.id", "A", "pt_beta", "sales_manager", []string{"SBY"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := UpsertMember(ctx, db, "a@x.id", "A", "pt_alpha", "sales_rep", nil); err != nil {
		t.Fatal(err)
	}
	u, err := LoadUser(ctx, db, id, "pt_beta")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(u.Roles, []string{"sales_manager"}) || !reflect.DeepEqual(u.Outlets, []string{"SBY"}) {
		t.Fatalf("other org changed: %+v", u)
	}
}
