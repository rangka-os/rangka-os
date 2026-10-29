// Package store maps models to Postgres tables: it compiles models to DDL, migrates by diffing against the
// live schema, and reads and writes records (one table r_<model> per model, r_<child> for table fields).
package store

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

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

// Live is the introspected schema of r_ tables.
type Live struct {
	Columns map[string]map[string]string // table → column → information_schema data_type
	Indexes map[string]bool
}

// Introspect reads the live r_ tables.
func Introspect(ctx context.Context, q pg.Querier) (*Live, error) {
	l := &Live{Columns: map[string]map[string]string{}, Indexes: map[string]bool{}}
	res, err := q.Query(ctx, `select table_name, column_name, data_type from information_schema.columns
		where table_schema = current_schema() and table_name like 'r\_%'`)
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
	res, err = q.Query(ctx, `select indexname from pg_indexes where schemaname = current_schema() and tablename like 'r\_%'`)
	if err != nil {
		return nil, err
	}
	for _, r := range res.Rows {
		l.Indexes[r[0].(string)] = true
	}
	return l, nil
}

// Plan diffs desired tables against the live schema: new tables, added columns and missing indexes.
// Columns are never dropped or retyped automatically; notes report drift for a person to resolve.
func Plan(desired []Table, live *Live) (stmts, notes []string) {
	for _, t := range desired {
		cols, exists := live.Columns[t.Name]
		if !exists {
			stmts = append(stmts, t.createSQL())
		} else {
			want := map[string]bool{}
			for _, c := range t.Columns {
				want[c.Name] = true
				dt, ok := cols[c.Name]
				if !ok {
					s := fmt.Sprintf("alter table %s add column %s %s", pg.QuoteIdent(t.Name), pg.QuoteIdent(c.Name), c.Type)
					if c.Default != "" {
						s += " default " + c.Default
					}
					if c.NotNull && c.Default != "" {
						s += " not null"
					}
					stmts = append(stmts, s)
				} else if infoType[c.Type] != "" && infoType[c.Type] != dt {
					notes = append(notes, fmt.Sprintf("%s.%s is %s in the database but %s in the model; change it with a manual migration", t.Name, c.Name, dt, c.Type))
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
				notes = append(notes, fmt.Sprintf("%s.%s is no longer in the model; the column and its data are kept", t.Name, c))
			}
		}
		for _, ix := range t.Indexes {
			if !live.Indexes[ix.Name] {
				stmts = append(stmts, ix.Def)
			}
		}
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
	Notes      []string `json:"notes"`
	Files      int      `json:"files"`
	Changed    []string `json:"changed_files"`
}

// Migrate creates kernel tables, applies the model diff in one transaction and records file hashes.
func Migrate(ctx context.Context, db *pg.Pool, reg *meta.Registry, dryRun bool, source string) (*MigrateResult, error) {
	if _, err := db.Exec(ctx, systemSQL); err != nil {
		return nil, fmt.Errorf("kernel tables: %w", err)
	}
	live, err := Introspect(ctx, db)
	if err != nil {
		return nil, err
	}
	stmts, notes := Plan(Tables(reg), live)
	res := &MigrateResult{Statements: stmts, Notes: notes}
	old := map[string]string{}
	if r, err := db.Query(ctx, "select path, hash from rk_meta_files"); err == nil {
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
	if dryRun {
		return res, nil
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	for _, s := range stmts {
		if _, err := tx.Exec(ctx, s); err != nil {
			return nil, fmt.Errorf("%s: %w", firstLine(s), err)
		}
	}
	if len(stmts) > 0 {
		if _, err := tx.Query(ctx, "insert into rk_migrations (source, statements) values ($1, $2)", source, pg.Array(stmts)); err != nil {
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
