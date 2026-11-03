// Package store maps models to Postgres tables: it compiles models to DDL, migrates by diffing against the
// live schema, and reads and writes records (one table r_<model> per model, r_<child> for table fields).
package store

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

//go:embed system.sql
var systemSQL string

// SystemSQL is the DDL of the kernel's own tables.
func SystemSQL() string { return systemSQL }

// TableName returns the table of a model.
func TableName(model string) string { return "r_" + model }

// SQLType maps a field type to a column type ("" for table fields, which become child tables).
func SQLType(fieldType string) string {
	switch fieldType {
	case "table":
		return ""
	case "int", "rating":
		return "bigint"
	case "float", "percent":
		return "double precision"
	case "currency":
		return "numeric"
	case "check":
		return "boolean"
	case "date":
		return "date"
	case "datetime":
		return "timestamptz"
	case "multiselect", "json", "daterange":
		return "jsonb"
	}
	return "text"
}

// information_schema.columns data_type for each SQL type.
var infoType = map[string]string{"bigint": "bigint", "double precision": "double precision", "numeric": "numeric",
	"boolean": "boolean", "date": "date", "timestamptz": "timestamp with time zone", "jsonb": "jsonb", "text": "text",
	"smallint": "smallint", "integer": "integer"}

// Column of a table.
type Column struct {
	Name, Type string
	NotNull    bool
	Default    string
}

// Index of a table.
type Index struct {
	Name, Def string
}

// Table is the desired shape of a model's table.
type Table struct {
	Name    string
	Model   string
	Child   bool
	Columns []Column
	Indexes []Index
}

// identName shortens identifiers to Postgres' 63-byte limit with a stable hash suffix.
func identName(s string) string {
	if len(s) <= 63 {
		return s
	}
	h := sha256.Sum256([]byte(s))
	return s[:54] + "_" + hex.EncodeToString(h[:4])
}

// IsColumn reports whether a field has its own column: table fields are child tables and company x_ fields
// live in the x JSONB column.
func IsColumn(f *meta.Field) bool {
	return SQLType(f.Type) != "" && !reserved[f.Name] && !strings.HasPrefix(f.Name, "x_")
}

// reserved are base columns a model field may not redefine.
var reserved = map[string]bool{"org_id": true, "name": true, "created_by": true, "created_at": true, "modified_at": true, "x": true,
	"docstatus": true, "amended_from": true, "parent": true, "parent_field": true, "idx": true}

// Tables compiles installed models into tables.
func Tables(reg *meta.Registry) []Table {
	var out []Table
	for _, m := range reg.InstalledModels() {
		out = append(out, ModelTable(m))
	}
	return out
}

// ModelTable compiles one model.
func ModelTable(m *meta.Model) Table {
	t := Table{Name: TableName(m.Name), Model: m.Name, Child: m.IsChild}
	col := func(name, typ string, notNull bool, def string) {
		t.Columns = append(t.Columns, Column{name, typ, notNull, def})
	}
	col("org_id", "text", true, "")
	col("name", "text", true, "")
	if m.IsChild {
		col("parent", "text", true, "")
		col("parent_field", "text", true, "")
		col("idx", "integer", true, "0")
	} else {
		col("created_by", "text", false, "")
		if m.IsSubmittable {
			col("docstatus", "smallint", true, "0")
			col("amended_from", "text", false, "")
		}
	}
	col("created_at", "timestamptz", true, "now()")
	col("modified_at", "timestamptz", true, "now()")
	col("x", "jsonb", true, "'{}'")
	for _, f := range m.Fields {
		if !IsColumn(f) {
			continue
		}
		col(f.Name, SQLType(f.Type), false, "")
	}
	tn := t.Name
	idx := func(suffix, def string) {
		name := identName(tn + "_" + suffix)
		t.Indexes = append(t.Indexes, Index{name, strings.ReplaceAll(def, "{name}", pg.QuoteIdent(name))})
	}
	if m.IsChild {
		idx("parent_idx", fmt.Sprintf("create index if not exists {name} on %s (org_id, parent)", pg.QuoteIdent(tn)))
	}
	if m.IsSubmittable {
		idx("docstatus_idx", fmt.Sprintf("create index if not exists {name} on %s (org_id, docstatus)", pg.QuoteIdent(tn)))
	}
	for _, f := range m.Fields {
		if !IsColumn(f) {
			continue
		}
		q := pg.QuoteIdent(f.Name)
		if f.Unique {
			where := q + " is not null"
			if SQLType(f.Type) == "text" {
				where += " and " + q + " <> ''"
			}
			idx(f.Name+"_uniq", fmt.Sprintf("create unique index if not exists {name} on %s (org_id, %s) where %s", pg.QuoteIdent(tn), q, where))
		} else if f.Type == "link" || f.Type == "dynamic_link" || f.Index {
			idx(f.Name+"_idx", fmt.Sprintf("create index if not exists {name} on %s (org_id, %s)", pg.QuoteIdent(tn), q))
		}
	}
	return t
}

func (t Table) createSQL() string {
	var parts []string
	for _, c := range t.Columns {
		s := pg.QuoteIdent(c.Name) + " " + c.Type
		if c.NotNull {
			s += " not null"
		}
		if c.Default != "" {
			s += " default " + c.Default
		}
		parts = append(parts, s)
	}
	parts = append(parts, "primary key (org_id, name)")
	return fmt.Sprintf("create table %s (\n  %s\n)", pg.QuoteIdent(t.Name), strings.Join(parts, ",\n  "))
}

// Live is the introspected schema of r_ tables and soft-deleted _del_ tables.
type Live struct {
	Columns map[string]map[string]string // table → column → information_schema data_type
	Types   map[string]map[string]string // table → column → full SQL type (format_type), when introspected
	Indexes map[string]bool
}

// Introspect reads the live r_ tables (and soft-deleted _del_ tables).
func Introspect(ctx context.Context, q pg.Querier) (*Live, error) {
	l := &Live{Columns: map[string]map[string]string{}, Types: map[string]map[string]string{}, Indexes: map[string]bool{}}
	res, err := q.Query(ctx, `select table_name, column_name, data_type from information_schema.columns
		where table_schema = current_schema() and (table_name like 'r\_%' or table_name like '\_del\_%')`)
	if err != nil {
		return nil, err
	}
	for _, r := range res.Rows {
		t, c, dt := r[0].(string), r[1].(string), r[2].(string)
		if l.Columns[t] == nil {
			l.Columns[t] = map[string]string{}
		}
		l.Columns[t][c] = dt
	}
	res, err = q.Query(ctx, `select c.relname, a.attname, format_type(a.atttypid, a.atttypmod)
		from pg_attribute a join pg_class c on c.oid = a.attrelid join pg_namespace n on n.oid = c.relnamespace
		where n.nspname = current_schema() and c.relkind in ('r', 'p') and a.attnum > 0 and not a.attisdropped
		and (c.relname like 'r\_%' or c.relname like '\_del\_%')`)
	if err != nil {
		return nil, err
	}
	for _, r := range res.Rows {
		t, c, ft := r[0].(string), r[1].(string), r[2].(string)
		if l.Types[t] == nil {
			l.Types[t] = map[string]string{}
		}
		l.Types[t][c] = ft
	}
	res, err = q.Query(ctx, `select indexname from pg_indexes where schemaname = current_schema() and tablename like 'r\_%'`)
	if err != nil {
		return nil, err
	}
	for _, r := range res.Rows {
		l.Indexes[r[0].(string)] = true
	}
	return l, nil
}

// Step is one planned DDL statement with its risk and the statement that undoes it ("" when it cannot be undone).
type Step struct {
	SQL         string `json:"sql"`
	Reverse     string `json:"reverse"`
	Risk        string `json:"risk"` // low | medium | high
	Destructive bool   `json:"destructive"`
}

// Risk levels of a step.
const (
	RiskLow    = "low"
	RiskMedium = "medium"
	RiskHigh   = "high"
)

var riskRank = map[string]int{RiskLow: 0, RiskMedium: 1, RiskHigh: 2}

// HighestRisk returns the highest risk of the steps ("low" when there are none).
func HighestRisk(steps []Step) string {
	r := RiskLow
	for _, s := range steps {
		if riskRank[s.Risk] > riskRank[r] {
			r = s.Risk
		}
	}
	return r
}

// widening retypes keep every value (information_schema data_type before → after).
var widening = map[[2]string]bool{
	{"integer", "bigint"}: true, {"smallint", "integer"}: true, {"smallint", "bigint"}: true,
	{"character varying", "text"}: true,
}

// softDeletePrefix marks a column or table the model no longer has; it is dropped after softDeleteDays.
const (
	softDeletePrefix = "_del_"
	softDeleteDays   = 30
)

// softDeleteName is the name a column or table gets when it is soft-deleted on day now (max 63 bytes).
func softDeleteName(name string, now time.Time) string {
	return identName(softDeletePrefix + now.Format("20060102") + "_" + name)
}

// softDeletedOn returns the day a _del_<YYYYMMDD>_ name was soft-deleted.
func softDeletedOn(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, softDeletePrefix) || len(name) < 15 || name[13] != '_' {
		return time.Time{}, false
	}
	d, err := time.Parse("20060102", name[5:13])
	return d, err == nil
}

// expired reports whether a soft-deleted name is more than softDeleteDays calendar days old at now (dates are
// compared in now's zone, the zone softDeleteName formats them in).
func expired(name string, now time.Time) bool {
	d, ok := softDeletedOn(name)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return ok && today.Sub(d) > softDeleteDays*24*time.Hour
}

// PlanSteps diffs desired tables against the live schema. New tables, columns and indexes are low risk;
// widening retypes are medium; other retypes are high and destructive. With allowDestructive, columns and
// tables the model no longer has are soft-deleted (renamed to _del_<YYYYMMDD>_<name>) and soft-deleted ones
// older than 30 days are dropped; without it they are reported as notes.
func PlanSteps(desired []Table, live *Live, now time.Time, allowDestructive bool) (steps []Step, notes []string) {
	add := func(sql, reverse, risk string, destructive bool) {
		steps = append(steps, Step{SQL: sql, Reverse: reverse, Risk: risk, Destructive: destructive})
	}
	wanted := map[string]bool{}
	for _, t := range desired {
		wanted[t.Name] = true
		tq := pg.QuoteIdent(t.Name)
		cols, exists := live.Columns[t.Name]
		if !exists {
			add(t.createSQL(), "drop table "+tq, RiskLow, false)
		} else {
			want := map[string]bool{}
			for _, c := range t.Columns {
				want[c.Name] = true
				cq := pg.QuoteIdent(c.Name)
				dt, ok := cols[c.Name]
				if !ok {
					s := fmt.Sprintf("alter table %s add column %s %s", tq, cq, c.Type)
					if c.Default != "" {
						s += " default " + c.Default
					}
					if c.NotNull && c.Default != "" {
						s += " not null"
					}
					add(s, fmt.Sprintf("alter table %s drop column %s", tq, cq), RiskLow, false)
				} else if infoType[c.Type] != "" && infoType[c.Type] != dt {
					old := dt
					if ft := live.Types[t.Name][c.Name]; ft != "" {
						old = ft
					}
					alter := fmt.Sprintf("alter table %s alter column %s type ", tq, cq)
					if widening[[2]string{dt, infoType[c.Type]}] {
						add(alter+c.Type, alter+old, RiskMedium, false)
					} else {
						add(alter+c.Type+" using "+cq+"::"+c.Type, alter+old+" using "+cq+"::"+old, RiskHigh, true)
						if !allowDestructive {
							notes = append(notes, fmt.Sprintf("%s.%s is %s in the database but %s in the model; retyping it may lose data and needs --allow-destructive", t.Name, c.Name, dt, c.Type))
						}
					}
				}
			}
			var extra []string
			for c := range cols {
				if !want[c] {
					extra = append(extra, c)
				}
			}
			sort.Strings(extra)
			for _, c := range extra {
				cq := pg.QuoteIdent(c)
				switch {
				case strings.HasPrefix(c, softDeletePrefix):
					if allowDestructive && expired(c, now) {
						add(fmt.Sprintf("alter table %s drop column %s", tq, cq), "", RiskHigh, true)
					}
				case allowDestructive:
					del := pg.QuoteIdent(softDeleteName(c, now))
					add(fmt.Sprintf("alter table %s rename column %s to %s", tq, cq, del),
						fmt.Sprintf("alter table %s rename column %s to %s", tq, del, cq), RiskHigh, true)
				default:
					notes = append(notes, fmt.Sprintf("%s.%s is no longer in the model; the column and its data are kept", t.Name, c))
				}
			}
		}
		for _, ix := range t.Indexes {
			if !live.Indexes[ix.Name] {
				add(ix.Def, "drop index if exists "+pg.QuoteIdent(ix.Name), RiskLow, false)
			}
		}
	}
	var gone []string
	for t := range live.Columns {
		if !wanted[t] {
			gone = append(gone, t)
		}
	}
	sort.Strings(gone)
	for _, t := range gone {
		tq := pg.QuoteIdent(t)
		switch {
		case strings.HasPrefix(t, softDeletePrefix):
			if allowDestructive && expired(t, now) {
				add("drop table "+tq, "", RiskHigh, true)
			}
		case allowDestructive:
			del := pg.QuoteIdent(softDeleteName(t, now))
			add(fmt.Sprintf("alter table %s rename to %s", tq, del), fmt.Sprintf("alter table %s rename to %s", del, tq), RiskHigh, true)
		default:
			notes = append(notes, fmt.Sprintf("%s is no longer in the model; the table and its data are kept", t))
		}
	}
	return steps, notes
}

// Plan returns the statements of PlanSteps (without soft deletes or drops) and its notes.
func Plan(desired []Table, live *Live) (stmts, notes []string) {
	steps, notes := PlanSteps(desired, live, time.Now(), false)
	for _, s := range steps {
		stmts = append(stmts, s.SQL)
	}
	return stmts, notes
}

// FileHash is the hash stored in rk_meta_files.
func FileHash(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

// MigrateResult reports what a migration did.
type MigrateResult struct {
	Statements []string `json:"statements"`
	Steps      []Step   `json:"steps"`
	Risk       string   `json:"risk"`              // highest risk of Steps and Refused
	Refused    []Step   `json:"refused,omitempty"` // destructive steps not allowed; nothing was applied
	Notes      []string `json:"notes"`
	Files      int      `json:"files"`
	Changed    []string `json:"changed_files"`
}

// ErrDestructive is returned (wrapped) when a plan has destructive steps and they are not allowed.
var ErrDestructive = errors.New("destructive migration steps need --allow-destructive")

// MigrateOpts configures MigrateWith.
type MigrateOpts struct {
	DryRun, AllowDestructive bool
	Source                   string
	Now                      func() time.Time                              // default time.Now
	Before                   func(ctx context.Context, steps []Step) error // runs before any statement is applied
}

// Migrate creates kernel tables, applies the model diff in one transaction and records file hashes.
func Migrate(ctx context.Context, db *pg.Pool, reg *meta.Registry, dryRun bool, source string) (*MigrateResult, error) {
	return MigrateWith(ctx, db, reg, MigrateOpts{DryRun: dryRun, Source: source})
}

// MigrateWith plans the model diff as steps, refuses destructive steps unless allowed, and applies the rest in
// one transaction, recording them with their reverses in rk_migrations.
func MigrateWith(ctx context.Context, db *pg.Pool, reg *meta.Registry, o MigrateOpts) (*MigrateResult, error) {
	return migrate(ctx, db, reg, Tables(reg), o)
}

func migrate(ctx context.Context, db *pg.Pool, reg *meta.Registry, desired []Table, o MigrateOpts) (*MigrateResult, error) {
	q, done, err := kernelTables(ctx, db, o.DryRun)
	if err != nil {
		return nil, err
	}
	defer done()
	live, err := Introspect(ctx, q)
	if err != nil {
		return nil, err
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	steps, notes := PlanSteps(desired, live, now(), o.AllowDestructive)
	res := &MigrateResult{Notes: notes, Risk: HighestRisk(steps)}
	for _, s := range steps {
		if s.Destructive && !o.AllowDestructive {
			res.Refused = append(res.Refused, s)
			continue
		}
		res.Steps = append(res.Steps, s)
		res.Statements = append(res.Statements, s.SQL)
	}
	old := map[string]string{}
	if r, err := q.Query(ctx, "select path, hash from rk_meta_files"); err == nil {
		for _, row := range r.Rows {
			old[row[0].(string)] = row[1].(string)
		}
	}
	paths := sortedDocPaths(reg)
	for _, p := range paths {
		if old[p] != FileHash(reg.Docs[p].Text) {
			res.Changed = append(res.Changed, p)
		}
	}
	res.Files = len(paths)
	if len(res.Refused) > 0 {
		return res, refusal(res.Refused, "rangka migrate --dry-run --allow-destructive", "rangka migrate --allow-destructive")
	}
	if o.DryRun {
		return res, nil
	}
	if o.Before != nil && len(res.Steps) > 0 {
		if err := o.Before(ctx, res.Steps); err != nil {
			return nil, fmt.Errorf("before migrate: %w", err)
		}
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	for _, s := range res.Steps {
		if _, err := tx.Exec(ctx, s.SQL); err != nil {
			return nil, fmt.Errorf("%s: %w", firstLine(s.SQL), err)
		}
	}
	if len(res.Steps) > 0 {
		if _, err := tx.Query(ctx, "insert into rk_migrations (source, statements, steps, risk) values ($1, $2, $3, $4)",
			o.Source, pg.Array(res.Statements), pg.JSON{V: res.Steps}, HighestRisk(res.Steps)); err != nil {
			return nil, err
		}
	}
	for _, p := range paths {
		d := reg.Docs[p]
		if _, err := tx.Query(ctx, `insert into rk_meta_files (path, kind, hash) values ($1, $2, $3)
			on conflict (path) do update set kind = excluded.kind, hash = excluded.hash,
			applied_at = case when rk_meta_files.hash = excluded.hash then rk_meta_files.applied_at else now() end`, p, d.Kind(), FileHash(d.Text)); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Query(ctx, "delete from rk_meta_files where not (path = any($1))", pg.Array(paths)); err != nil {
		return nil, err
	}
	return res, tx.Commit(ctx)
}

// kernelTables creates the kernel tables. On a dry run it does so in a transaction that done rolls back, so a
// dry run changes nothing even on an empty database; q reads through that transaction.
func kernelTables(ctx context.Context, db *pg.Pool, dryRun bool) (q pg.Querier, done func(), err error) {
	if !dryRun {
		if _, err := db.Exec(ctx, systemSQL); err != nil {
			return nil, nil, fmt.Errorf("kernel tables: %w", err)
		}
		return db, func() {}, nil
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	done = func() { tx.Rollback(ctx) }
	if _, err := tx.Exec(ctx, systemSQL); err != nil {
		done()
		return nil, nil, fmt.Errorf("kernel tables: %w", err)
	}
	return tx, done, nil
}

// refusal is the ErrDestructive error for refused steps: it names them and the operator commands to run.
func refusal(refused []Step, review, run string) error {
	var what []string
	for _, s := range refused {
		what = append(what, firstLine(s.SQL))
	}
	return fmt.Errorf("refused %d destructive step(s), nothing applied: %s; review with `%s`, then run `%s`: %w",
		len(refused), strings.Join(what, "; "), review, run, ErrDestructive)
}

// ErrIrreversible is returned (wrapped) when a migration to roll back has a step without a reverse.
var ErrIrreversible = errors.New("migration cannot be rolled back")

// RollbackOpts configures Rollback.
type RollbackOpts struct {
	N                        int // how many of the latest migrations to undo
	DryRun, AllowDestructive bool
	Before                   func(ctx context.Context, steps []Step) error // runs before any statement is applied
}

// RollbackResult reports what a rollback did.
type RollbackResult struct {
	Migrations []int64  `json:"migrations"` // rk_migrations ids, newest first
	Statements []string `json:"statements"`
	Steps      []Step   `json:"steps"`             // the reverse steps, in the order they run
	Refused    []Step   `json:"refused,omitempty"` // destructive reverse steps not allowed; nothing was applied
}

// dropsData reports whether a statement drops a table or a column.
func dropsData(sql string) bool {
	return strings.HasPrefix(sql, "drop table ") || strings.Contains(sql, " drop column ")
}

// Rollback undoes the last N migrations that are not rolled back yet, newest first, by running the recorded
// reverse of each step in reverse order in one transaction, and marks them rolled_back_at. Nothing runs when
// any step has no reverse (ErrIrreversible), or when a reverse is destructive (it drops a table or column, or
// undoes a destructive step) and AllowDestructive is not set (ErrDestructive). Rolling back does not change
// the models: the next migrate plans the same steps again unless the models are reverted too.
func Rollback(ctx context.Context, db *pg.Pool, o RollbackOpts) (*RollbackResult, error) {
	if o.N < 1 {
		return nil, fmt.Errorf("rollback: n must be at least 1")
	}
	q, done, err := kernelTables(ctx, db, o.DryRun)
	if err != nil {
		return nil, err
	}
	defer done()
	res, err := planRollback(ctx, q, o, false)
	if err != nil || o.DryRun {
		return res, err
	}
	if o.Before != nil {
		if err := o.Before(ctx, res.Steps); err != nil {
			return nil, fmt.Errorf("before rollback: %w", err)
		}
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// re-plan under a row lock so a concurrent migrate or rollback cannot change what is undone
	again, err := planRollback(ctx, tx, o, true)
	if err != nil {
		return nil, err
	}
	if !equalIDs(again.Migrations, res.Migrations) {
		return nil, fmt.Errorf("rollback: the migration log changed while preparing the rollback; try again")
	}
	for _, s := range res.Statements {
		if _, err := tx.Exec(ctx, s); err != nil {
			return nil, fmt.Errorf("%s: %w", firstLine(s), err)
		}
	}
	for _, id := range res.Migrations {
		if _, err := tx.Exec(ctx, "update rk_migrations set rolled_back_at = now() where id = $1", id); err != nil {
			return nil, err
		}
	}
	return res, tx.Commit(ctx)
}

func planRollback(ctx context.Context, q pg.Querier, o RollbackOpts, lock bool) (*RollbackResult, error) {
	sql := "select id, steps from rk_migrations where rolled_back_at is null order by id desc limit $1"
	if lock {
		sql += " for update"
	}
	r, err := q.Query(ctx, sql, int64(o.N))
	if err != nil {
		return nil, err
	}
	if len(r.Rows) < o.N {
		return nil, fmt.Errorf("rollback: %d migration(s) asked, %d can be rolled back", o.N, len(r.Rows))
	}
	res := &RollbackResult{}
	for _, row := range r.Rows {
		id := row[0].(int64)
		if row[1] == nil {
			return nil, fmt.Errorf("migration %d has no recorded steps: %w", id, ErrIrreversible)
		}
		b, err := json.Marshal(row[1])
		if err != nil {
			return nil, err
		}
		var steps []Step
		if err := json.Unmarshal(b, &steps); err != nil {
			return nil, fmt.Errorf("migration %d steps: %w", id, err)
		}
		for i := len(steps) - 1; i >= 0; i-- {
			s := steps[i]
			if s.Reverse == "" {
				return nil, fmt.Errorf("migration %d: %s has no reverse: %w", id, firstLine(s.SQL), ErrIrreversible)
			}
			rs := Step{SQL: s.Reverse, Reverse: s.SQL, Risk: s.Risk, Destructive: s.Destructive || dropsData(s.Reverse)}
			if rs.Destructive {
				rs.Risk = RiskHigh
			}
			if rs.Destructive && !o.AllowDestructive {
				res.Refused = append(res.Refused, rs)
			}
			res.Steps = append(res.Steps, rs)
			res.Statements = append(res.Statements, rs.SQL)
		}
		res.Migrations = append(res.Migrations, id)
	}
	if len(res.Refused) > 0 {
		n := fmt.Sprint(o.N)
		return res, refusal(res.Refused, "rangka migrate --rollback "+n+" --dry-run --allow-destructive", "rangka migrate --rollback "+n+" --allow-destructive")
	}
	return res, nil
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortedDocPaths(reg *meta.Registry) []string {
	out := make([]string, 0, len(reg.Docs))
	for p := range reg.Docs {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}
