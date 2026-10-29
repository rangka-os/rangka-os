package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// Record is a record as plain values: fields, system fields and child rows ([]map[string]any as []any).
type Record = map[string]any

// ErrNotFound is returned when a record does not exist.
var ErrNotFound = errors.New("record not found")

// Zero is the value of a missing field (CONTRACTS: ” for text/date/link, 0 for numbers, false for check).
func Zero(f *meta.Field) any {
	switch f.Type {
	case "int", "rating":
		return int64(0)
	case "float", "percent", "currency":
		return float64(0)
	case "check":
		return false
	case "table", "multiselect":
		return []any{}
	case "json", "daterange":
		return nil
	}
	return ""
}

// Coerce converts an input value to the Go type of a field (numbers from text, bools, dates as text).
func Coerce(f *meta.Field, v any) (any, error) {
	if v == nil {
		return Zero(f), nil
	}
	switch f.Type {
	case "int", "rating":
		switch x := v.(type) {
		case int64:
			return x, nil
		case int:
			return int64(x), nil
		case float64:
			return int64(math.Round(x)), nil
		case string:
			if strings.TrimSpace(x) == "" {
				return int64(0), nil
			}
			n, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
			if err != nil {
				return nil, fmt.Errorf("%s needs a whole number", f.Name)
			}
			return int64(math.Round(n)), nil
		case bool:
			if x {
				return int64(1), nil
			}
			return int64(0), nil
		}
	case "float", "percent", "currency":
		switch x := v.(type) {
		case float64:
			return x, nil
		case int64:
			return float64(x), nil
		case int:
			return float64(x), nil
		case string:
			if strings.TrimSpace(x) == "" {
				return float64(0), nil
			}
			n, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
			if err != nil {
				return nil, fmt.Errorf("%s needs a number", f.Name)
			}
			return n, nil
		case bool:
			if x {
				return float64(1), nil
			}
			return float64(0), nil
		}
	case "check":
		switch x := v.(type) {
		case bool:
			return x, nil
		case string:
			return x == "true" || x == "1" || x == "yes", nil
		case int64:
			return x != 0, nil
		case float64:
			return x != 0, nil
		}
	case "date":
		switch x := v.(type) {
		case string:
			if x != "" && len(x) >= 10 {
				if _, err := time.Parse("2006-01-02", x[:10]); err != nil {
					return nil, fmt.Errorf("%s needs a date (YYYY-MM-DD)", f.Name)
				}
				return x[:10], nil
			}
			return x, nil
		case time.Time:
			return x.Format("2006-01-02"), nil
		}
	case "datetime":
		switch x := v.(type) {
		case string:
			return x, nil
		case time.Time:
			return x.Format(time.RFC3339), nil
		}
	case "multiselect", "json", "daterange", "table":
		return v, nil
	default:
		switch x := v.(type) {
		case string:
			return x, nil
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64), nil
		case int64:
			return strconv.FormatInt(x, 10), nil
		case bool:
			return strconv.FormatBool(x), nil
		}
	}
	return nil, fmt.Errorf("%s: unexpected value %v", f.Name, v)
}

// sqlValue converts a record value into a query parameter ("" becomes NULL).
func sqlValue(f *meta.Field, v any) any {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok && s == "" {
		return nil
	}
	switch f.Type {
	case "multiselect", "json", "daterange":
		return pg.JSON{V: v}
	}
	return v
}

func fromSQL(f *meta.Field, v any) any {
	if v == nil {
		return Zero(f)
	}
	switch f.Type {
	case "int", "rating":
		switch x := v.(type) {
		case float64:
			return int64(x)
		}
	case "datetime":
		if t, ok := v.(time.Time); ok {
			return t.Format(time.RFC3339)
		}
	}
	return v
}

func ts(v any) string {
	if t, ok := v.(time.Time); ok {
		return t.UTC().Format(time.RFC3339)
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// fromRow builds a record from a table row.
func fromRow(m *meta.Model, row map[string]any) Record {
	r := Record{"name": row["name"]}
	if !m.IsChild {
		r["created_by"] = str(row["created_by"])
		r["created_at"] = ts(row["created_at"])
		r["modified_at"] = ts(row["modified_at"])
		if m.IsSubmittable {
			r["docstatus"] = row["docstatus"]
			r["amended_from"] = str(row["amended_from"])
		}
	} else {
		r["idx"] = row["idx"]
	}
	for _, f := range m.Fields {
		if f.Type == "table" {
			r[f.Name] = []any{}
			continue
		}
		if reserved[f.Name] {
			continue
		}
		if strings.HasPrefix(f.Name, "x_") {
			x, _ := row["x"].(map[string]any)
			v, err := Coerce(f, x[f.Name])
			if err != nil {
				v = Zero(f)
			}
			r[f.Name] = v
			continue
		}
		r[f.Name] = fromSQL(f, row[f.Name])
	}
	if x, ok := row["x"].(map[string]any); ok {
		for k, v := range x {
			if strings.HasPrefix(k, "x_") {
				r[k] = v
			}
		}
	}
	return r
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func xData(r Record) map[string]any {
	x := map[string]any{}
	for k, v := range r {
		if strings.HasPrefix(k, "x_") {
			x[k] = v
		}
	}
	return x
}

// Get loads one record with its child rows.
func Get(ctx context.Context, q pg.Querier, reg *meta.Registry, org string, m *meta.Model, name string) (Record, error) {
	res, err := q.Query(ctx, fmt.Sprintf("select * from %s where org_id = $1 and name = $2", pg.QuoteIdent(TableName(m.Name))), org, name)
	if err != nil {
		return nil, err
	}
	if len(res.Rows) == 0 {
		return nil, ErrNotFound
	}
	r := fromRow(m, res.Maps()[0])
	if err := loadChildren(ctx, q, reg, org, m, []Record{r}); err != nil {
		return nil, err
	}
	return r, nil
}

// Exists reports whether a record exists.
func Exists(ctx context.Context, q pg.Querier, org, model, name string) (bool, error) {
	res, err := q.Query(ctx, fmt.Sprintf("select 1 from %s where org_id = $1 and name = $2", pg.QuoteIdent(TableName(model))), org, name)
	if err != nil {
		return false, err
	}
	return len(res.Rows) > 0, nil
}

// All loads every record of a model for an org (children when withChildren).
func All(ctx context.Context, q pg.Querier, reg *meta.Registry, org string, m *meta.Model, withChildren bool) ([]Record, error) {
	res, err := q.Query(ctx, fmt.Sprintf("select * from %s where org_id = $1 order by created_at desc, name desc", pg.QuoteIdent(TableName(m.Name))), org)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(res.Rows))
	for _, row := range res.Maps() {
		out = append(out, fromRow(m, row))
	}
	if withChildren {
		if err := loadChildren(ctx, q, reg, org, m, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Count counts records of a model for an org.
func Count(ctx context.Context, q pg.Querier, org, model string) (int64, error) {
	res, err := q.Query(ctx, fmt.Sprintf("select count(*) from %s where org_id = $1", pg.QuoteIdent(TableName(model))), org)
	if err != nil {
		return 0, err
	}
	return res.Rows[0][0].(int64), nil
}

func loadChildren(ctx context.Context, q pg.Querier, reg *meta.Registry, org string, m *meta.Model, recs []Record) error {
	if len(recs) == 0 {
		return nil
	}
	for _, f := range m.Fields {
		if f.Type != "table" {
			continue
		}
		cm := reg.Models[f.Of]
		if cm == nil {
			continue
		}
		byName := map[string]Record{}
		names := make([]string, 0, len(recs))
		for _, r := range recs {
			n := str(r["name"])
			byName[n] = r
			names = append(names, n)
		}
		res, err := q.Query(ctx, fmt.Sprintf("select * from %s where org_id = $1 and parent_field = $2 and parent = any($3) order by parent, idx",
			pg.QuoteIdent(TableName(cm.Name))), org, f.Name, pg.Array(names))
		if err != nil {
			return err
		}
		for _, row := range res.Maps() {
			p := byName[str(row["parent"])]
			if p == nil {
				continue
			}
			l, _ := p[f.Name].([]any)
			p[f.Name] = append(l, fromRow(cm, row))
		}
	}
	return nil
}

// RandomName returns a random upper-case name part (DRAFT-XXXXX).
func RandomName(n int) string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func childName() string {
	b := make([]byte, 6)
	rand.Read(b)
	return "ROW-" + hex.EncodeToString(b)
}

// Insert writes a new record and its child rows.
func Insert(ctx context.Context, q pg.Querier, reg *meta.Registry, org string, m *meta.Model, r Record) error {
	cols := []string{"org_id", "name", "x"}
	args := []any{org, r["name"], pg.JSON{V: xData(r)}}
	if !m.IsChild {
		cols, args = append(cols, "created_by"), append(args, nilIfEmpty(r["created_by"]))
	}
	if m.IsSubmittable {
		cols, args = append(cols, "docstatus", "amended_from"), append(args, toInt(r["docstatus"]), nilIfEmpty(r["amended_from"]))
	}
	if m.IsChild {
		cols, args = append(cols, "parent", "parent_field", "idx"), append(args, r["parent"], r["parent_field"], toInt(r["idx"]))
	}
	for _, f := range m.Fields {
		if !IsColumn(f) {
			continue
		}
		cols = append(cols, f.Name)
		args = append(args, sqlValue(f, r[f.Name]))
	}
	ph := make([]string, len(cols))
	qc := make([]string, len(cols))
	for i, c := range cols {
		ph[i] = fmt.Sprintf("$%d", i+1)
		qc[i] = pg.QuoteIdent(c)
	}
	sql := fmt.Sprintf("insert into %s (%s) values (%s) returning created_at, modified_at", pg.QuoteIdent(TableName(m.Name)), strings.Join(qc, ", "), strings.Join(ph, ", "))
	res, err := q.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	if !m.IsChild {
		r["created_at"], r["modified_at"] = ts(res.Rows[0][0]), ts(res.Rows[0][1])
	}
	return writeChildren(ctx, q, reg, org, m, r)
}

func writeChildren(ctx context.Context, q pg.Querier, reg *meta.Registry, org string, m *meta.Model, r Record) error {
	for _, f := range m.Fields {
		if f.Type != "table" {
			continue
		}
		cm := reg.Models[f.Of]
		if cm == nil {
			continue
		}
		rows, _ := r[f.Name].([]any)
		for i, rv := range rows {
			row, ok := rv.(map[string]any)
			if !ok {
				continue
			}
			if str(row["name"]) == "" {
				row["name"] = childName()
			}
			row["parent"], row["parent_field"], row["idx"] = r["name"], f.Name, int64(i+1)
			if err := Insert(ctx, q, reg, org, cm, row); err != nil {
				return err
			}
		}
	}
	return nil
}

func deleteChildren(ctx context.Context, q pg.Querier, reg *meta.Registry, org string, m *meta.Model, name string) error {
	for _, f := range m.Fields {
		if f.Type != "table" {
			continue
		}
		if cm := reg.Models[f.Of]; cm != nil {
			if _, err := q.Query(ctx, fmt.Sprintf("delete from %s where org_id = $1 and parent = $2 and parent_field = $3", pg.QuoteIdent(TableName(cm.Name))), org, name, f.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

// Update rewrites a record (renaming it when r["name"] differs from oldName) and replaces its child rows.
func Update(ctx context.Context, q pg.Querier, reg *meta.Registry, org string, m *meta.Model, oldName string, r Record) error {
	sets := []string{"name = $3", "x = $4", "modified_at = now()"}
	args := []any{org, oldName, r["name"], pg.JSON{V: xData(r)}}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", pg.QuoteIdent(col), len(args)))
	}
	if m.IsSubmittable {
		add("docstatus", toInt(r["docstatus"]))
		add("amended_from", nilIfEmpty(r["amended_from"]))
	}
	for _, f := range m.Fields {
		if !IsColumn(f) {
			continue
		}
		add(f.Name, sqlValue(f, r[f.Name]))
	}
	sql := fmt.Sprintf("update %s set %s where org_id = $1 and name = $2 returning modified_at", pg.QuoteIdent(TableName(m.Name)), strings.Join(sets, ", "))
	res, err := q.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	if len(res.Rows) == 0 {
		return ErrNotFound
	}
	r["modified_at"] = ts(res.Rows[0][0])
	if err := deleteChildren(ctx, q, reg, org, m, oldName); err != nil {
		return err
	}
	return writeChildren(ctx, q, reg, org, m, r)
}

// Delete removes a record and its child rows.
func Delete(ctx context.Context, q pg.Querier, reg *meta.Registry, org string, m *meta.Model, name string) error {
	if err := deleteChildren(ctx, q, reg, org, m, name); err != nil {
		return err
	}
	res, err := q.Query(ctx, fmt.Sprintf("delete from %s where org_id = $1 and name = $2", pg.QuoteIdent(TableName(m.Name))), org, name)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func nilIfEmpty(v any) any {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok && s == "" {
		return nil
	}
	return v
}

func toInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}
