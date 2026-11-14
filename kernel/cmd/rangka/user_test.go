package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

func TestParseUserArgs(t *testing.T) {
	t.Setenv("RANGKA_USER_PASSWORD", "")
	ua, err := parseUserArgs([]string{"add", "Staff@Example.test", "--org", "kopi-senja", "--role", "outlet_staff", "--outlets", "OUT-01, OUT-02", "--record", "USR-009", "--name", "Staf Satu", "--db", "postgres://x"})
	if err != nil {
		t.Fatal(err)
	}
	want := &userArgs{Sub: "add", Email: "staff@example.test", Org: "kopi-senja", Role: "outlet_staff", Outlets: []string{"OUT-01", "OUT-02"},
		SetOutlet: true, Record: "USR-009", Name: "Staf Satu", DB: "postgres://x"}
	if !reflect.DeepEqual(ua, want) {
		t.Fatalf("got %+v", ua)
	}
	// flags before the email, empty outlets clears
	ua, err = parseUserArgs([]string{"add", "--org", "o", "--role", "r", "--outlets", "", "a@b.test"})
	if err != nil || ua.Email != "a@b.test" || !ua.SetOutlet || len(ua.Outlets) != 0 {
		t.Fatalf("got %+v, %v", ua, err)
	}
	// no --outlets: leave them alone
	if ua, _ = parseUserArgs([]string{"add", "a@b.test", "--org", "o", "--role", "r"}); ua == nil || ua.SetOutlet {
		t.Fatalf("got %+v", ua)
	}
	ua, err = parseUserArgs([]string{"remove", "a@b.test", "--org", "o"})
	if err != nil || ua.Sub != "remove" || ua.Org != "o" {
		t.Fatalf("got %+v, %v", ua, err)
	}
	for _, bad := range [][]string{
		nil, {"nope"}, {"add"}, {"add", "a@b.test"}, {"add", "a@b.test", "--org", "o"}, {"add", "--org", "o", "--role", "r"},
		{"remove", "a@b.test"}, {"remove", "a@b.test", "--org", "o", "--role", "r"}, {"add", "a@b.test", "--org", "o", "--role", "r", "--bogus", "1"},
	} {
		t.Setenv("RANGKA_ORG", "")
		if _, err := parseUserArgs(bad); err == nil {
			t.Errorf("%v: want an error", bad)
		}
	}
}

func TestUserAddRemove(t *testing.T) {
	url := testdb.New(t)
	ctx := context.Background()
	db, err := pg.NewPool(ctx, url, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Exec(ctx, store.SystemSQL()); err != nil {
		t.Fatal(err)
	}
	auth.Iterations = 1000
	t.Setenv("DATABASE_URL", url)
	t.Setenv("RANGKA_ORG", "")
	t.Setenv("RANGKA_USER_PASSWORD", "")
	outletTable(t, db, "pt-baru", "OUT-01", "OUT-02", "OUT-03") // contract 0055 §2: outlets must exist

	add := []string{"add", "staff@example.test", "--org", "pt-baru", "--role", "outlet_staff", "--outlets", "OUT-01,OUT-02", "--name", "Staf", "--create-org"}
	if code := cmdUser(add); code != 2 {
		t.Fatalf("new user without password: exit %d", code)
	}
	t.Setenv("RANGKA_USER_PASSWORD", "rahasia-1")
	if code := cmdUser(add); code != 0 {
		t.Fatalf("add: exit %d", code)
	}
	row := func(q string) []any {
		t.Helper()
		res, err := db.Query(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("%s: %d rows", q, len(res.Rows))
		}
		return res.Rows[0]
	}
	if r := row("select name from rk_orgs where id = 'pt-baru'"); r[0] != "Pt Baru" {
		t.Fatalf("org: %v", r)
	}
	id, err := auth.Authenticate(ctx, db, "staff@example.test", "rahasia-1")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	u, err := auth.LoadUser(ctx, db, id, "pt-baru")
	if err != nil || !reflect.DeepEqual(u.Roles, []string{"outlet_staff"}) || !reflect.DeepEqual(u.Outlets, []string{"OUT-01", "OUT-02"}) {
		t.Fatalf("user: %+v %v", u, err)
	}
	// existing user: password stays, outlets change only with --outlets, empty clears
	t.Setenv("RANGKA_USER_PASSWORD", "lain-sekali")
	if code := cmdUser([]string{"add", "staff@example.test", "--org", "pt-baru", "--role", "outlet_staff", "--outlets", "OUT-03"}); code != 0 {
		t.Fatalf("re-add: exit %d", code)
	}
	if _, err := auth.Authenticate(ctx, db, "staff@example.test", "rahasia-1"); err != nil {
		t.Fatalf("password changed: %v", err)
	}
	if u, _ = auth.LoadUser(ctx, db, id, "pt-baru"); !reflect.DeepEqual(u.Outlets, []string{"OUT-03"}) {
		t.Fatalf("outlets: %v", u.Outlets)
	}
	t.Setenv("RANGKA_USER_PASSWORD", "")
	if code := cmdUser([]string{"add", "staff@example.test", "--org", "pt-baru", "--role", "outlet_staff"}); code != 0 {
		t.Fatalf("re-add without password: exit %d", code)
	}
	if u, _ = auth.LoadUser(ctx, db, id, "pt-baru"); !reflect.DeepEqual(u.Outlets, []string{"OUT-03"}) {
		t.Fatalf("outlets changed without --outlets: %v", u.Outlets)
	}
	if code := cmdUser([]string{"add", "staff@example.test", "--org", "pt-baru", "--role", "outlet_staff", "--outlets", ""}); code != 0 {
		t.Fatal("clear outlets")
	}
	if u, _ = auth.LoadUser(ctx, db, id, "pt-baru"); len(u.Outlets) != 0 {
		t.Fatalf("outlets not cleared: %v", u.Outlets)
	}
	if code := cmdUser([]string{"remove", "staff@example.test", "--org", "pt-baru"}); code != 0 {
		t.Fatalf("remove: exit %d", code)
	}
	if _, err := auth.LoadUser(ctx, db, id, "pt-baru"); err != auth.ErrNoMembership {
		t.Fatalf("still a member: %v", err)
	}
	if code := cmdUser([]string{"remove", "nobody@example.test", "--org", "pt-baru"}); code != 1 {
		t.Fatalf("remove unknown: exit %d", code)
	}
	// known user who is no longer a member, and an org that does not exist
	if code := cmdUser([]string{"remove", "staff@example.test", "--org", "pt-baru"}); code != 1 {
		t.Fatalf("remove non-member: exit %d", code)
	}
	if code := cmdUser([]string{"remove", "staff@example.test", "--org", "tidak-ada"}); code != 1 {
		t.Fatalf("remove from missing org: exit %d", code)
	}
}

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

// An account linked to upstream sign-in and not yet in the org is refused unless --allow-linked: adding it would
// let whoever holds that upstream identity enter the org as this person (final review I1).
func TestUserAddLinkedAccount(t *testing.T) {
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
	for _, org := range []string{"a", "b"} {
		if err := auth.EnsureOrg(ctx, db, org, org); err != nil {
			t.Fatal(err)
		}
	}
	// org a's member API linked an upstream subject to v@x.test
	uid, _, err := auth.UpsertMember(ctx, db, "v@x.test", "V", "a", "sales_rep", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.LinkIdentity(ctx, db, "https://cloud.example", "sub-1", uid); err != nil {
		t.Fatal(err)
	}
	members := func(org string) int64 {
		t.Helper()
		res, err := db.Query(ctx, "select count(*) from rk_members where org_id = $1 and user_id = $2", org, uid)
		if err != nil {
			t.Fatal(err)
		}
		return res.Rows[0][0].(int64)
	}

	_, err = runUserOut(t, "add", "--db", url, "--org", "b", "--email", "v@x.test", "--role", "sales_rep")
	if err == nil || !strings.Contains(err.Error(), "https://cloud.example") || !strings.Contains(err.Error(), "--allow-linked") {
		t.Fatalf("linked account into b: err = %v, want a refusal naming the issuer and --allow-linked", err)
	}
	if members("b") != 0 {
		t.Fatal("the refused add wrote a membership")
	}
	// a member of a already: updating that membership is not refused
	if _, err := runUserOut(t, "add", "--db", url, "--org", "a", "--email", "v@x.test", "--role", "sales_manager"); err != nil {
		t.Fatalf("existing member of a: %v", err)
	}
	out, err := runUserOut(t, "add", "--db", url, "--org", "b", "--email", "v@x.test", "--role", "sales_rep", "--allow-linked")
	if err != nil || !strings.Contains(out, "user v@x.test is sales_rep in b") {
		t.Fatalf("--allow-linked: %v %q", err, out)
	}
	if members("b") != 1 {
		t.Fatal("--allow-linked did not add the membership")
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

// Both spellings of `rangka user add` (S0's flags, S5's positional email + env password) are one command.
func TestParseUserArgsBothForms(t *testing.T) {
	t.Setenv("RANGKA_ORG", "")
	t.Setenv("RANGKA_USER_PASSWORD", "dari-env")
	ua, err := parseUserArgs([]string{"add", "--email", "A@B.test", "--org", "o", "--role", "r"})
	if err != nil || ua.Email != "a@b.test" || ua.Password != "dari-env" {
		t.Fatalf("--email form: %+v, %v", ua, err)
	}
	ua, err = parseUserArgs([]string{"add", "a@b.test", "--email", "A@B.test", "--org", "o", "--role", "r", "--password", "dari-flag", "--reset-password", "--create-org"})
	if err != nil || ua.Email != "a@b.test" || ua.Password != "dari-flag" || !ua.ResetPassword || !ua.CreateOrg {
		t.Fatalf("both forms: %+v, %v", ua, err)
	}
	if ua, err = parseUserArgs([]string{"remove", "--email", "a@b.test", "--org", "o"}); err != nil || ua.Email != "a@b.test" {
		t.Fatalf("remove --email: %+v, %v", ua, err)
	}
	for _, bad := range [][]string{
		{"add", "a@b.test", "--email", "c@d.test", "--org", "o", "--role", "r"},
		{"add", "a@b.test", "c@d.test", "--org", "o", "--role", "r"},
		{"remove", "a@b.test", "--org", "o", "--password", "x"},
	} {
		var ue usageError
		if _, err := parseUserArgs(bad); !errors.As(err, &ue) {
			t.Errorf("%v: want a usage error, got %v", bad, err)
		}
	}
	t.Setenv("DATABASE_URL", "")
	if code := cmdUser([]string{"add", "a@b.test", "--org", "o", "--role", "r"}); code != 2 {
		t.Fatalf("no db: exit %d, want 2", code)
	}
	if code := cmdUser([]string{"add", "a@b.test", "--org", "o"}); code != 2 {
		t.Fatalf("missing role: exit %d, want 2", code)
	}
}

func TestUserAddMixedForms(t *testing.T) {
	url := testdb.New(t)
	ctx := context.Background()
	db, err := pg.NewPool(ctx, url, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Exec(ctx, store.SystemSQL()); err != nil {
		t.Fatal(err)
	}
	prevIter := auth.Iterations
	auth.Iterations = 1000
	t.Cleanup(func() { auth.Iterations = prevIter })
	t.Setenv("DATABASE_URL", url)
	t.Setenv("RANGKA_ORG", "")
	t.Setenv("RANGKA_USER_PASSWORD", "rahasia-1")

	// S5 form against an unknown org: refused (exit 1), nothing created
	if code := cmdUser([]string{"add", "x@y.test", "--org", "baru", "--role", "owner"}); code != 1 {
		t.Fatalf("unknown org: exit %d, want 1", code)
	}
	if res, _ := db.Query(ctx, "select 1 from rk_users where email = 'x@y.test'"); len(res.Rows) != 0 {
		t.Fatal("user created for an unknown org")
	}
	if code := cmdUser([]string{"add", "x@y.test", "--org", "baru", "--role", "owner", "--create-org"}); code != 0 {
		t.Fatalf("create org: exit %d", code)
	}
	// S0 form for an existing account needs no password at all
	t.Setenv("RANGKA_USER_PASSWORD", "")
	if code := cmdUser([]string{"add", "--db", url, "--org", "baru", "--email", "x@y.test", "--role", "sales_rep"}); code != 0 {
		t.Fatalf("existing account without password: exit %d", code)
	}
	// --reset-password without a password is a usage error; with the env it resets
	if code := cmdUser([]string{"add", "x@y.test", "--org", "baru", "--role", "sales_rep", "--reset-password"}); code != 2 {
		t.Fatalf("reset without password: exit %d, want 2", code)
	}
	t.Setenv("RANGKA_USER_PASSWORD", "rahasia-2")
	if code := cmdUser([]string{"add", "x@y.test", "--org", "baru", "--role", "sales_rep", "--reset-password"}); code != 0 {
		t.Fatalf("reset: exit %d", code)
	}
	if _, err := auth.Authenticate(ctx, db, "x@y.test", "rahasia-2"); err != nil {
		t.Fatalf("login after reset: %v", err)
	}
	if code := cmdUser([]string{"remove", "--email", "x@y.test", "--org", "baru"}); code != 0 {
		t.Fatalf("remove --email: exit %d", code)
	}
}

// outletTable stands in for the outlet model's table (r_outlet) with the given outlet names in org.
func outletTable(t *testing.T, db *pg.Pool, org string, names ...string) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Exec(ctx, "create table if not exists r_outlet (org_id text not null, name text not null)"); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if _, err := db.Query(ctx, "insert into r_outlet (org_id, name) values ($1, $2)", org, n); err != nil {
			t.Fatal(err)
		}
	}
}

// Contract 0055 §2: `rangka user add --outlets` refuses an outlet that is not an outlet record of the org, as the
// member API does, and writes nothing.
func TestUserAddUnknownOutlet(t *testing.T) {
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
	t.Setenv("RANGKA_APPS", "")
	add := func(outlets string) error {
		_, err := runUserOut(t, "add", "--db", url, "--org", "t", "--create-org", "--email", "k@x.test", "--password", "pw-123456",
			"--role", "cashier", "--outlets", outlets)
		return err
	}
	// no outlet table: outlets are not installed, so every outlet is unknown
	if err := add("OUT-01"); err == nil || !strings.Contains(err.Error(), "unknown_outlet") {
		t.Fatalf("no outlets installed: err = %v", err)
	}
	outletTable(t, db, "t", "OUT-01")
	outletTable(t, db, "other", "OUT-09")
	if err := add("OUT-01,OUT-09"); err == nil || !strings.Contains(err.Error(), "unknown_outlet") || !strings.Contains(err.Error(), "OUT-09") {
		t.Fatalf("outlet of another org: err = %v", err)
	}
	res, err := db.Query(ctx, "select count(*) from rk_users where email = 'k@x.test'")
	if err != nil || res.Rows[0][0].(int64) != 0 {
		t.Fatalf("a refused add wrote an account: %v", err)
	}
	if err := add("OUT-01"); err != nil {
		t.Fatalf("known outlet: %v", err)
	}
}

// Contract 0055 §2: with --apps, `rangka user add` and `remove` keep the member's employee record in step.
func TestUserAddSyncsRecord(t *testing.T) {
	url := testdb.New(t)
	prevIter := auth.Iterations
	auth.Iterations = 1000
	t.Cleanup(func() { auth.Iterations = prevIter })
	ctx := context.Background()
	apps, site := testdb.Catalogue("apps"), testdb.Catalogue("sites/kopi-senja")
	reg, rep := meta.Validate(apps, site, nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("metadata errors: %v", rep.All())
	}
	db, err := pg.NewPool(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := store.Migrate(ctx, db, reg, false, "test"); err != nil {
		t.Fatal(err)
	}
	if err := auth.EnsureOrg(ctx, db, "kopi-senja", "Kopi Senja"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Query(ctx, "insert into r_outlet (org_id, name, outlet_name) values ('kopi-senja', 'OUT-01', 'Kemang')"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RANGKA_CONNECTORS", "")
	out, err := runUserOut(t, "add", "--db", url, "--org", "kopi-senja", "--email", "k@x.test", "--password", "pw-123456", "--name", "Kasir",
		"--role", "sales_rep", "--outlets", "OUT-01", "--apps", apps, "--site", site)
	if err != nil || !strings.Contains(out, "employee record USR-") {
		t.Fatalf("add: %v %q", err, out)
	}
	access := func() string {
		t.Helper()
		res, err := db.Query(ctx, "select u.access from r_user u join rk_users a on a.id = u.account_id where a.email = 'k@x.test'")
		if err != nil || len(res.Rows) != 1 {
			t.Fatalf("record: %v %v", res, err)
		}
		return res.Rows[0][0].(string)
	}
	if got := access(); got != "pending" {
		t.Fatalf("after add: %q", got)
	}
	if _, err := runUserOut(t, "remove", "--db", url, "--org", "kopi-senja", "--email", "k@x.test", "--apps", apps, "--site", site); err != nil {
		t.Fatal(err)
	}
	if got := access(); got != "removed" {
		t.Fatalf("after remove: %q", got)
	}
}

// Contract 0055 §3: `rangka user add --record` refuses a record that is another account's or would move the member
// off its record (record_taken), or does not exist (unknown_record), and writes nothing; the record of the account
// itself, or one with no account, is fine.
func TestUserAddRecordTaken(t *testing.T) {
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
	if _, err := db.Exec(ctx, store.SystemSQL()+`; create table r_user (org_id text not null, name text not null, account_id bigint);
		insert into r_user values ('t', 'USR-001', 0), ('t', 'USR-002', 0)`); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RANGKA_APPS", "")
	add := func(email, record string) error {
		_, err := runUserOut(t, "add", "--db", url, "--org", "t", "--create-org", "--email", email, "--password", "pw-123456",
			"--role", "cashier", "--record", record)
		return err
	}
	if err := add("a@x.test", "USR-001"); err != nil {
		t.Fatalf("free record: %v", err)
	}
	// another membership points at USR-001
	if err := add("b@x.test", "USR-001"); err == nil || !strings.Contains(err.Error(), "record_taken") {
		t.Fatalf("pointed at: err = %v", err)
	}
	// USR-002 carries a's account
	if _, err := db.Query(ctx, "update r_user set account_id = (select id from rk_users where email = 'a@x.test') where name = 'USR-002'"); err != nil {
		t.Fatal(err)
	}
	if err := add("b@x.test", "USR-002"); err == nil || !strings.Contains(err.Error(), "record_taken") {
		t.Fatalf("account_id of another: err = %v", err)
	}
	res, err := db.Query(ctx, "select count(*) from rk_users where email = 'b@x.test'")
	if err != nil || res.Rows[0][0].(int64) != 0 {
		t.Fatalf("a refused add wrote an account: %v", err)
	}
	// a member is never given a second record: not while another record carries its account, nor away from the one
	// its membership points at; its own record is fine
	if err := add("a@x.test", "USR-001"); err == nil || !strings.Contains(err.Error(), "record_taken") {
		t.Fatalf("another record carries the account: err = %v", err)
	}
	if _, err := db.Query(ctx, "update r_user set account_id = 0 where name = 'USR-002'"); err != nil {
		t.Fatal(err)
	}
	if err := add("a@x.test", "USR-002"); err == nil || !strings.Contains(err.Error(), "record_taken") {
		t.Fatalf("moving a member: err = %v", err)
	}
	if err := add("a@x.test", "USR-001"); err != nil {
		t.Fatalf("the account's own record: %v", err)
	}
	// a record that does not exist
	if err := add("c@x.test", "USR-999"); err == nil || !strings.Contains(err.Error(), "unknown_record") {
		t.Fatalf("unknown record: err = %v", err)
	}
	res, err = db.Query(ctx, "select count(*) from rk_users where email = 'c@x.test'")
	if err != nil || res.Rows[0][0].(int64) != 0 {
		t.Fatalf("a refused add wrote an account: %v", err)
	}
}
