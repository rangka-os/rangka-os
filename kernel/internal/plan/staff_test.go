package plan

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// TestStaffLimit (database): on testdata/member-apps (plan dasar: users 3, staff 5; kasir and gudang are desk: false)
// phone-only people count under staff, not users, and past either limit the newest are refused and read-only
// (contract 0074 §5).
func TestStaffLimit(t *testing.T) {
	url := testdb.New(t)
	ctx := context.Background()
	reg, rep := meta.ValidateOpts(meta.Options{AppsDir: "../../testdata/member-apps/apps", SiteDir: "../../testdata/member-apps/site"}, nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("metadata errors: %v", rep.All())
	}
	db, err := pg.NewPool(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := store.Migrate(ctx, db, reg, false, "test"); err != nil {
		t.Fatal(err)
	}
	if err := Sync(ctx, db, reg); err != nil {
		t.Fatal(err)
	}
	staff := StaffRoles(reg)
	if !staff["kasir"] || !staff["gudang"] || staff["keuangan"] || staff["owner"] {
		t.Fatalf("StaffRoles = %v", staff)
	}
	if _, err := db.Query(ctx, "insert into rk_orgs (id, name) values ('o', 'O') on conflict (id) do nothing"); err != nil {
		t.Fatal(err)
	}
	add := func(email, role string) error {
		if err := CheckNewPerson(ctx, db, "o", email, role, staff); err != nil {
			return err
		}
		res, err := db.Query(ctx, "insert into rk_users (email, full_name) values ($1, $1) returning id", email)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Query(ctx, "insert into rk_members (org_id, user_id, role) values ('o', $1, $2)", res.Rows[0][0], role); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	limitOf := func(err error) string {
		var pe *Error
		if !errors.As(err, &pe) || pe.Code != "plan_limit" {
			t.Fatalf("err = %v, want plan_limit", err)
		}
		return pe.Resource
	}
	for _, p := range [][2]string{{"owner@x.test", "owner"}, {"f1@x.test", "keuangan"}, {"f2@x.test", "keuangan"}} {
		if err := add(p[0], p[1]); err != nil {
			t.Fatalf("%s: %v", p[0], err)
		}
	}
	if got := limitOf(add("f3@x.test", "keuangan")); got != Users {
		t.Fatalf("fourth desk person refused on %s, want users", got)
	}
	for i, r := range []string{"kasir", "gudang", "kasir", "kasir", "gudang"} { // five staff fit although users is full
		if err := add("s"+string(rune('1'+i))+"@x.test", r); err != nil {
			t.Fatalf("staff %d: %v", i+1, err)
		}
	}
	if got := limitOf(add("s6@x.test", "kasir")); got != Staff {
		t.Fatalf("sixth staff refused on %s, want staff", got)
	}
	// a role change that moves an existing person between staff and users takes a place under the other limit
	// (audit 261a F2); within the same class it is not a new person
	if got := limitOf(CheckNewPerson(ctx, db, "o", "s1@x.test", "keuangan", staff)); got != Users {
		t.Fatalf("staff to desk with users full refused on %s, want users", got)
	}
	if got := limitOf(CheckNewPerson(ctx, db, "o", "f1@x.test", "kasir", staff)); got != Staff {
		t.Fatalf("desk to staff with staff full refused on %s, want staff", got)
	}
	if err := CheckNewPerson(ctx, db, "o", "s1@x.test", "gudang", staff); err != nil {
		t.Fatalf("staff to another staff role: %v", err)
	}
	if err := CheckNewPerson(ctx, db, "o", "f1@x.test", "keuangan", staff); err != nil {
		t.Fatalf("same desk role again: %v", err)
	}
	u, err := Measure(ctx, db, reg, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if u.Users != 3 || u.Staff != 5 {
		t.Fatalf("usage users %d staff %d, want 3 and 5", u.Users, u.Staff)
	}
	// a sixth phone-only person added behind the check's back is read-only, with the staff message
	res, _ := db.Query(ctx, "insert into rk_users (email, full_name) values ('s6@x.test', 'S6') returning id")
	if _, err := db.Query(ctx, "insert into rk_members (org_id, user_id, role) values ('o', $1, 'gudang')", res.Rows[0][0]); err != nil {
		t.Fatal(err)
	}
	snap, err := Take(ctx, db, reg, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.ExcessPeople) != 1 || !snap.ExcessStaff["s6@x.test"] {
		t.Fatalf("excess people %v staff %v", snap.ExcessPeople, snap.ExcessStaff)
	}
	if pe := snap.ReadOnlyPerson("s6@x.test"); pe == nil || pe.Resource != Staff {
		t.Fatalf("read-only staff = %v", pe)
	}
	if pe := snap.ReadOnlyPerson("f2@x.test"); pe != nil {
		t.Fatalf("a desk person within users is not read-only: %v", pe)
	}
	meters := snap.Report(reg, "2026-11")["usage"].(map[string]Meter)
	if m := meters[Staff]; m.Used != 6 || m.State != "over" || meters[Users].Used != 3 {
		t.Fatalf("meters staff %+v users %+v", m, meters[Users])
	}
}
