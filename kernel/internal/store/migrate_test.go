package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

var day0 = time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)

const regionField = "  - name: region\n    type: link\n    to: outlet\n    label: {id: Wilayah}\n"

// migrateEnv is a fresh database and a copy of the real apps.
type migrateEnv struct {
	t    *testing.T
	ctx  context.Context
	db   *pg.Pool
	apps string
}

func newMigrateEnv(t *testing.T) *migrateEnv {
	url := testdb.New(t)
	ctx := context.Background()
	db, err := pg.NewPool(ctx, url, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	apps := filepath.Join(t.TempDir(), "apps")
	if err := exec.Command("cp", "-r", "../../../../rangka-apps/apps", apps).Run(); err != nil {
		t.Fatal(err)
	}
	return &migrateEnv{t, ctx, db, apps}
}

// edit replaces old with new in the deal model.
func (e *migrateEnv) edit(old, new string) {
	e.t.Helper()
	p := filepath.Join(e.apps, "crm", "models", "deal.rk")
	b, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(b), old) {
		e.t.Fatalf("deal.rk has no %q: %v", old, err)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), old, new, 1)), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *migrateEnv) migrate(o MigrateOpts) (*MigrateResult, error) {
	if o.Now == nil {
		o.Now = func() time.Time { return day0 }
	}
	return MigrateWith(e.ctx, e.db, load(e.t, e.apps), o)
}

func (e *migrateEnv) mustMigrate(o MigrateOpts) *MigrateResult {
	e.t.Helper()
	res, err := e.migrate(o)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

// snapshot is the live schema plus the migration log.
func (e *migrateEnv) snapshot() string {
	e.t.Helper()
	live, err := Introspect(e.ctx, e.db)
	if err != nil {
		e.t.Fatal(err)
	}
	r, err := e.db.Query(e.ctx, "select id, source, risk, rolled_back_at is not null from rk_migrations order by id")
	if err != nil {
		e.t.Fatal(err)
	}
	b, _ := json.Marshal([]any{live, r.Rows})
	return string(b)
}

func (e *migrateEnv) column(table, col string) string {
	e.t.Helper()
	live, err := Introspect(e.ctx, e.db)
	if err != nil {
		e.t.Fatal(err)
	}
	return live.Columns[table][col]
}

func (e *migrateEnv) count(sql string) int64 {
	e.t.Helper()
	r, err := e.db.Query(e.ctx, sql)
	if err != nil {
		e.t.Fatal(err)
	}
	return r.Rows[0][0].(int64)
}

func recordedSteps(t *testing.T, v any) []Step {
	t.Helper()
	b, _ := json.Marshal(v)
	var out []Step
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("steps %s: %v", b, err)
	}
	return out
}

func TestStepsRecorded(t *testing.T) {
	e := newMigrateEnv(t)
	res := e.mustMigrate(MigrateOpts{Source: "test"})
	if len(res.Steps) == 0 || len(res.Steps) != len(res.Statements) || res.Risk != "low" {
		t.Fatalf("steps %d statements %d risk %q", len(res.Steps), len(res.Statements), res.Risk)
	}
	r, err := e.db.Query(e.ctx, "select steps, risk, statements from rk_migrations")
	if err != nil || len(r.Rows) != 1 {
		t.Fatalf("rk_migrations: %v %v", r, err)
	}
	steps := recordedSteps(t, r.Rows[0][0])
	if !reflect.DeepEqual(steps, res.Steps) || r.Rows[0][1] != "low" || len(r.Rows[0][2].([]any)) != len(steps) {
		t.Fatalf("recorded %v risk %v", steps, r.Rows[0][1])
	}
	for _, s := range steps {
		if s.Reverse == "" || s.Risk != "low" || s.Destructive {
			t.Fatalf("step %+v", s)
		}
		if strings.HasPrefix(s.SQL, "create table") && !strings.HasPrefix(s.Reverse, "drop table") ||
			strings.HasPrefix(s.SQL, "create ") && strings.Contains(s.SQL, " index ") && !strings.HasPrefix(s.Reverse, "drop index") {
			t.Fatalf("reverse of %q is %q", s.SQL, s.Reverse)
		}
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	e := newMigrateEnv(t)
	tables := `select count(*) from information_schema.tables where table_schema = current_schema()`
	if n := e.count(tables); n != 0 {
		t.Fatalf("fresh database has %d tables", n)
	}
	if res := e.mustMigrate(MigrateOpts{DryRun: true, Source: "test"}); len(res.Steps) == 0 {
		t.Fatal("dry run on an empty database planned nothing")
	}
	if n := e.count(tables); n != 0 {
		t.Fatalf("dry run on an empty database created %d tables", n)
	}
	if _, err := Rollback(e.ctx, e.db, RollbackOpts{N: 1, DryRun: true}); err == nil {
		t.Fatal("dry rollback on an empty database should find nothing to roll back")
	}
	if n := e.count(tables); n != 0 {
		t.Fatalf("dry rollback on an empty database created %d tables", n)
	}
	e.mustMigrate(MigrateOpts{Source: "test"})
	e.edit("rules:\n", regionField+"rules:\n")
	before := e.snapshot()
	res := e.mustMigrate(MigrateOpts{DryRun: true, Source: "test", Before: func(context.Context, []Step) error {
		t.Fatal("Before called on a dry run")
		return nil
	}})
	if len(res.Steps) != 2 || res.Steps[0].SQL != `alter table "r_deal" add column "region" text` ||
		res.Steps[0].Reverse != `alter table "r_deal" drop column "region"` {
		t.Fatalf("steps %+v", res.Steps)
	}
	if after := e.snapshot(); after != before {
		t.Fatalf("dry run changed the database:\n%s\n%s", before, after)
	}
}

func TestRetypeRefused(t *testing.T) {
	e := newMigrateEnv(t)
	e.mustMigrate(MigrateOpts{Source: "test"})
	e.edit("  - name: contact_person\n    type: data\n", "  - name: contact_person\n    type: int\n")
	e.edit("rules:\n", regionField+"rules:\n")
	before := e.snapshot()
	called := false
	res, err := e.migrate(MigrateOpts{Source: "test", Before: func(context.Context, []Step) error { called = true; return nil }})
	if !errors.Is(err, ErrDestructive) || res == nil || len(res.Refused) != 1 || called ||
		!strings.Contains(err.Error(), `"r_deal" alter column "contact_person"`) || !strings.Contains(err.Error(), "rangka migrate --allow-destructive") {
		t.Fatalf("err %v res %+v called %v", err, res, called)
	}
	ref := res.Refused[0]
	if ref.SQL != `alter table "r_deal" alter column "contact_person" type bigint using "contact_person"::bigint` ||
		ref.Reverse != `alter table "r_deal" alter column "contact_person" type text using "contact_person"::text` ||
		ref.Risk != "high" || !ref.Destructive || res.Risk != "high" {
		t.Fatalf("refused %+v risk %s", ref, res.Risk)
	}
	if e.snapshot() != before {
		t.Fatal("a refused plan changed the database")
	}
	var seen []Step
	res = e.mustMigrate(MigrateOpts{Source: "test", AllowDestructive: true, Before: func(_ context.Context, s []Step) error { seen = s; return nil }})
	if len(res.Refused) != 0 || len(seen) != 3 || e.column("r_deal", "contact_person") != "bigint" || e.column("r_deal", "region") != "text" {
		t.Fatalf("allowed: %+v seen %d", res, len(seen))
	}
	if risk := e.count("select count(*) from rk_migrations where risk = 'high'"); risk != 1 {
		t.Fatalf("high-risk migrations recorded: %d", risk)
	}
}

func TestFailedStatementRollsBack(t *testing.T) {
	e := newMigrateEnv(t)
	reg := load(t, e.apps)
	bad := Table{Name: "r_bad", Model: "bad", Columns: []Column{{"org_id", "text", true, ""}, {"name", "text", true, ""},
		{"v", "bigint", false, "no_such_function()"}}}
	desired := []Table{Tables(reg)[0], bad}
	_, err := migrate(e.ctx, e.db, reg, desired, MigrateOpts{Source: "test", Now: func() time.Time { return day0 }})
	if err == nil || !strings.Contains(err.Error(), "no_such_function") {
		t.Fatalf("err = %v", err)
	}
	if n := e.count(`select count(*) from information_schema.tables where table_schema = current_schema() and table_name like 'r\_%'`); n != 0 {
		t.Fatalf("%d r_ tables left after a failed migration", n)
	}
	if n := e.count("select count(*) from rk_migrations"); n != 0 {
		t.Fatalf("%d rk_migrations rows after a failed migration", n)
	}
}

func TestRollback(t *testing.T) {
	e := newMigrateEnv(t)
	e.mustMigrate(MigrateOpts{Source: "test"})
	e.edit("rules:\n", regionField+"rules:\n")
	e.mustMigrate(MigrateOpts{Source: "test"})
	if _, err := e.db.Exec(e.ctx, `insert into r_deal (org_id, name, region) values ('o', 'DEAL-1', 'Bandung')`); err != nil {
		t.Fatal(err)
	}
	before := e.snapshot()
	want := []string{`drop index if exists "r_deal_region_idx"`, `alter table "r_deal" drop column "region"`}
	// dropping the added column loses its data: refused without AllowDestructive, dry run or not
	for _, dry := range []bool{true, false} {
		rb, err := Rollback(e.ctx, e.db, RollbackOpts{N: 1, DryRun: dry, Before: func(context.Context, []Step) error {
			t.Fatal("Before called on a refused rollback")
			return nil
		}})
		if !errors.Is(err, ErrDestructive) || rb == nil || len(rb.Refused) != 1 || rb.Refused[0].SQL != want[1] || !rb.Refused[0].Destructive || rb.Refused[0].Risk != "high" {
			t.Fatalf("rollback without flag (dry %v): %+v %v", dry, rb, err)
		}
		if e.snapshot() != before || e.count(`select count(*) from r_deal where region = 'Bandung'`) != 1 {
			t.Fatal("a refused rollback changed the database")
		}
	}
	rb, err := Rollback(e.ctx, e.db, RollbackOpts{N: 1, DryRun: true, AllowDestructive: true})
	if err != nil || len(rb.Migrations) != 1 || !reflect.DeepEqual(rb.Statements, want) {
		t.Fatalf("dry rollback %+v %v", rb, err)
	}
	if e.snapshot() != before {
		t.Fatal("dry rollback changed the database")
	}
	var seen []Step
	rb, err = Rollback(e.ctx, e.db, RollbackOpts{N: 1, AllowDestructive: true, Before: func(_ context.Context, s []Step) error { seen = s; return nil }})
	if err != nil || !reflect.DeepEqual(rb.Statements, want) || len(seen) != 2 || seen[0].Risk != "low" || seen[1].Reverse != `alter table "r_deal" add column "region" text` {
		t.Fatalf("rollback %+v %v seen %+v", rb, err, seen)
	}
	if e.column("r_deal", "region") != "" {
		t.Fatal("region still there")
	}
	if n := e.count("select count(*) from rk_migrations where rolled_back_at is not null and id = " + strconv.FormatInt(rb.Migrations[0], 10)); n != 1 {
		t.Fatal("rolled_back_at not set")
	}
	if _, err := Rollback(e.ctx, e.db, RollbackOpts{N: 5, AllowDestructive: true}); err == nil {
		t.Fatal("rolling back more migrations than recorded should fail")
	}
}

func TestRollbackIrreversible(t *testing.T) {
	e := newMigrateEnv(t)
	e.mustMigrate(MigrateOpts{Source: "test"})
	irr := []Step{{SQL: `alter table "r_deal" drop column "_del_20261001_x"`, Risk: "high", Destructive: true}}
	if _, err := e.db.Query(e.ctx, "insert into rk_migrations (source, statements, steps, risk) values ('test', $1, $2, 'high')",
		pg.Array{irr[0].SQL}, pg.JSON{V: irr}); err != nil {
		t.Fatal(err)
	}
	before := e.snapshot()
	for _, n := range []int{1, 2} {
		if _, err := Rollback(e.ctx, e.db, RollbackOpts{N: n, AllowDestructive: true}); !errors.Is(err, ErrIrreversible) {
			t.Fatalf("Rollback(%d) err = %v", n, err)
		}
	}
	if e.snapshot() != before {
		t.Fatal("an irreversible rollback changed the database")
	}
}

func TestSoftDeleteAndDrop(t *testing.T) {
	e := newMigrateEnv(t)
	e.edit("rules:\n", regionField+"rules:\n")
	e.mustMigrate(MigrateOpts{Source: "test"})
	e.edit(regionField, "")
	res := e.mustMigrate(MigrateOpts{Source: "test"})
	if len(res.Steps) != 0 || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "r_deal.region is no longer in the model") {
		t.Fatalf("without flag: %+v %v", res.Steps, res.Notes)
	}
	res = e.mustMigrate(MigrateOpts{Source: "test", AllowDestructive: true})
	want := Step{SQL: `alter table "r_deal" rename column "region" to "_del_20261103_region"`,
		Reverse: `alter table "r_deal" rename column "_del_20261103_region" to "region"`, Risk: "high", Destructive: true}
	if len(res.Steps) != 1 || res.Steps[0] != want || e.column("r_deal", "_del_20261103_region") != "text" || e.column("r_deal", "region") != "" {
		t.Fatalf("soft delete: %+v", res.Steps)
	}
	res = e.mustMigrate(MigrateOpts{Source: "test"})
	if len(res.Steps) != 0 || len(res.Notes) != 0 {
		t.Fatalf("soft-deleted column reported: %+v %v", res.Steps, res.Notes)
	}
	res = e.mustMigrate(MigrateOpts{Source: "test", AllowDestructive: true, Now: func() time.Time { return day0.AddDate(0, 0, 30).Add(14 * time.Hour) }})
	if len(res.Steps) != 0 {
		t.Fatalf("dropped too early: %+v", res.Steps)
	}
	res = e.mustMigrate(MigrateOpts{Source: "test", AllowDestructive: true, Now: func() time.Time { return day0.AddDate(0, 0, 31) }})
	if len(res.Steps) != 1 || res.Steps[0].SQL != `alter table "r_deal" drop column "_del_20261103_region"` || res.Steps[0].Reverse != "" ||
		!res.Steps[0].Destructive || e.column("r_deal", "_del_20261103_region") != "" {
		t.Fatalf("drop: %+v", res.Steps)
	}
}

func TestPlanStepsRisk(t *testing.T) {
	live := &Live{Columns: map[string]map[string]string{
		"r_t": {"org_id": "text", "name": "text", "a": "integer", "b": "text", "c": "character varying", "s": "smallint",
			"gone": "text", "_del_20260901_old": "text", "_del_20261101_new": "text"},
		"r_old":              {"org_id": "text", "name": "text"},
		"_del_20260101_r_x":  {"org_id": "text"},
		"_del_20261020_r_y2": {"org_id": "text"},
	}, Indexes: map[string]bool{}}
	desired := []Table{
		{Name: "r_t", Columns: []Column{{"org_id", "text", true, ""}, {"name", "text", true, ""}, {"a", "bigint", false, ""},
			{"b", "bigint", false, ""}, {"c", "text", false, ""}, {"s", "integer", false, ""}, {"d", "date", false, ""}},
			Indexes: []Index{{"r_t_d_idx", `create index if not exists "r_t_d_idx" on "r_t" (org_id, "d")`}}},
		{Name: "r_u", Columns: []Column{{"org_id", "text", true, ""}, {"name", "text", true, ""}}},
	}
	type row struct {
		SQL, Reverse, Risk string
		Destructive        bool
	}
	got := func(steps []Step) map[string]row {
		m := map[string]row{}
		for _, s := range steps {
			m[s.SQL] = row{s.SQL, s.Reverse, s.Risk, s.Destructive}
		}
		return m
	}
	steps, notes := PlanSteps(desired, live, day0, false)
	m := got(steps)
	expect := []row{
		{`alter table "r_t" alter column "a" type bigint`, `alter table "r_t" alter column "a" type integer`, "medium", false},
		{`alter table "r_t" alter column "b" type bigint using "b"::bigint`, `alter table "r_t" alter column "b" type text using "b"::text`, "high", true},
		{`alter table "r_t" alter column "c" type text`, `alter table "r_t" alter column "c" type character varying`, "medium", false},
		{`alter table "r_t" alter column "s" type integer`, `alter table "r_t" alter column "s" type smallint`, "medium", false},
		{`alter table "r_t" add column "d" date`, `alter table "r_t" drop column "d"`, "low", false},
		{`create index if not exists "r_t_d_idx" on "r_t" (org_id, "d")`, `drop index if exists "r_t_d_idx"`, "low", false},
	}
	for _, x := range expect {
		if m[x.SQL] != x {
			t.Errorf("step %q = %+v, want %+v", x.SQL, m[x.SQL], x)
		}
	}
	var create row
	for k, v := range m {
		if strings.HasPrefix(k, `create table "r_u"`) {
			create = v
		}
	}
	if create.Reverse != `drop table "r_u"` || create.Risk != "low" || create.Destructive {
		t.Errorf("create table r_u = %+v", create)
	}
	if len(steps) != len(expect)+1 {
		t.Errorf("without allowDestructive: %d steps %+v", len(steps), steps)
	}
	joined := strings.Join(notes, "\n")
	for _, n := range []string{"r_t.gone is no longer in the model", "r_old is no longer in the model", "--allow-destructive"} {
		if !strings.Contains(joined, n) {
			t.Errorf("notes lack %q:\n%s", n, joined)
		}
	}
	if strings.Contains(joined, "_del_") {
		t.Errorf("soft-deleted names reported:\n%s", joined)
	}

	steps, _ = PlanSteps(desired, live, day0, true)
	m = got(steps)
	for _, x := range []row{
		{`alter table "r_t" rename column "gone" to "_del_20261103_gone"`, `alter table "r_t" rename column "_del_20261103_gone" to "gone"`, "high", true},
		{`alter table "r_old" rename to "_del_20261103_r_old"`, `alter table "_del_20261103_r_old" rename to "r_old"`, "high", true},
		{`alter table "r_t" drop column "_del_20260901_old"`, "", "high", true},
		{`drop table "_del_20260101_r_x"`, "", "high", true},
	} {
		if m[x.SQL] != x {
			t.Errorf("step %q = %+v, want %+v", x.SQL, m[x.SQL], x)
		}
	}
	if len(steps) != len(expect)+1+4 {
		t.Errorf("with allowDestructive: %d steps", len(steps))
	}
	// Plan keeps its old shape: the statements of PlanSteps without the destructive extras.
	stmts, _ := Plan(desired, live)
	if len(stmts) != len(expect)+1 {
		t.Errorf("Plan: %v", stmts)
	}
	if long := softDeleteName("x_"+strings.Repeat("a", 70), day0); len(long) > 63 || !strings.HasPrefix(long, "_del_20261103_x_aaa") {
		t.Errorf("long soft-delete name %q", long)
	}
}
