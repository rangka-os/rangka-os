package imports

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/engine"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Service runs the import flow of contract 0012: preview (read, map, dry run, log entry) and commit.
type Service struct {
	DB     *pg.Pool
	Engine *engine.Engine
}

// Entry is one upload in the import log (rk_imports).
type Entry struct {
	ID          string           `json:"id"`
	Import      string           `json:"import"`
	Scope       string           `json:"scope"`
	FileName    string           `json:"file_name"`
	FileSHA     string           `json:"file_sha"`
	Status      string           `json:"status"` // previewed | committed
	Rows        int              `json:"rows"`
	Skipped     int              `json:"skipped"`
	Duplicate   bool             `json:"duplicate"`
	Message     string           `json:"message"` // "File ini sudah diimpor" | "Semua baris sudah diimpor" | ""
	Errors      []RowError       `json:"errors"`
	Preview     []map[string]any `json:"preview"`
	Records     []string         `json:"records"`
	CreatedBy   string           `json:"created_by"`
	CreatedAt   time.Time        `json:"created_at"`
	CommittedAt *time.Time       `json:"committed_at"`
}

const (
	msgDuplicateFile = "File ini sudah diimpor"
	msgDuplicateRows = "Semua baris sudah diimpor"
	previewLimit     = 20
	listLimit        = 50
)

// planned is the stored form of Planned (rk_imports.plan), so commit does not read the file again.
type planned struct {
	Input map[string]any `json:"input"`
	Rows  []int          `json:"rows"`
	Keys  []string       `json:"keys"`
}

func errAlreadyImported() error {
	return &engine.StatusError{Status: 409, Code: "already_imported", Msg: "Sudah diimpor"}
}

func (s *Service) imp(name string) (*meta.Import, error) {
	im := s.Engine.Reg().Imports[name]
	if im == nil {
		return nil, &engine.StatusError{Status: 404, Code: "not_found", Msg: fmt.Sprintf(`Impor "%s" tidak ada`, name)}
	}
	return im, nil
}

// lookup resolves link values with store.FindBy inside one org.
type lookup struct {
	ctx context.Context
	q   pg.Querier
	reg *meta.Registry
	org string
}

func (l *lookup) Find(model, field, value string) ([]string, error) {
	m := l.reg.Models[model]
	if m == nil {
		return nil, fmt.Errorf("model %s tidak ada", model)
	}
	return store.FindBy(l.ctx, l.q, l.reg, l.org, m, field, value)
}

// Preview reads data, maps it, drops rows whose key was already imported, dry-runs the creates and stores
// the result as a previewed log entry.
func (s *Service) Preview(ctx context.Context, a engine.Actor, name, fileName string, data []byte, outlet string) (*Entry, error) {
	im, err := s.imp(name)
	if err != nil {
		return nil, err
	}
	reg := s.Engine.Reg()
	var scope map[string]any
	scopeName := ""
	if im.Per == "outlet" {
		if outlet == "" {
			return nil, &engine.StatusError{Status: 400, Code: "outlet_required", Msg: "Pilih outlet dulu"}
		}
		ok, err := store.Exists(ctx, s.DB, a.Org, "outlet", outlet)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, &engine.StatusError{Status: 422, Code: "unknown_outlet", Msg: fmt.Sprintf(`Outlet "%s" tidak ditemukan`, outlet)}
		}
		scope = map[string]any{"outlet": outlet}
		scopeName = outlet
	}
	t, err := Read(data, im.Sheet, im.HeaderRow)
	switch {
	case errors.Is(err, ErrTooBig), errors.Is(err, ErrTooManyRows):
		return nil, &engine.StatusError{Status: 413, Code: "too_large", Msg: err.Error()}
	case err != nil:
		return nil, &engine.StatusError{Status: 400, Code: "unreadable", Msg: err.Error()}
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])

	now := s.Engine.Now().In(s.Engine.Location())
	mp := Map(reg, im, t, scope, &lookup{ctx: ctx, q: s.DB, reg: reg, org: a.Org}, now)

	seen, err := s.seenKeys(ctx, a.Org, name, scopeName, mp.Records)
	if err != nil {
		return nil, err
	}
	linesField := ""
	if im.Lines != nil {
		linesField = im.Lines.Field
	}
	plan, skippedRows := dropSeen(mp.Records, seen, linesField)
	// Errors of skipped rows do not matter: those rows are not imported again. File-level errors (the header
	// row) always stay.
	rowErrs := []RowError{}
	for _, e := range mp.Errors {
		if !skippedRows[e.Row] {
			rowErrs = append(rowErrs, e)
		}
	}

	stored := make([]planned, len(plan))
	inputs := make([]map[string]any, len(plan))
	for i, p := range plan {
		in, err := jsonInput(p.Input)
		if err != nil {
			return nil, err
		}
		stored[i] = planned{Input: in, Rows: p.Rows, Keys: p.Keys}
		inputs[i] = in
	}
	if len(rowErrs) == 0 && len(inputs) > 0 {
		res, err := s.Engine.CreateMany(ctx, a, im.Model, inputs, engine.ManyOpts{DryRun: true})
		if err != nil {
			return nil, err
		}
		rowErrs = append(rowErrs, validationRowErrors(res, stored)...)
	}
	preview := inputs
	if len(preview) > previewLimit {
		preview = preview[:previewLimit]
	}
	id := "imp_" + strings.ToLower(store.RandomName(10))
	_, err = s.DB.Exec(ctx, `insert into rk_imports (id, org_id, import, scope, file_name, file_sha, rows, skipped, errors, preview, plan, created_by)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		id, a.Org, name, scopeName, fileName, sha, len(t.Rows), len(skippedRows),
		pg.JSON{V: rowErrs}, pg.JSON{V: preview}, pg.JSON{V: stored}, a.ID)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, a, name, id)
}

// seenKeys returns the keys of recs already committed for (org, import, scope).
func (s *Service) seenKeys(ctx context.Context, org, name, scope string, recs []Planned) (map[string]bool, error) {
	var keys pg.Array
	for _, p := range recs {
		keys = append(keys, p.Keys...)
	}
	seen := map[string]bool{}
	if len(keys) == 0 {
		return seen, nil
	}
	res, err := s.DB.Query(ctx, `select key_hash from rk_import_keys where org_id = $1 and import = $2 and scope = $3 and key_hash = any($4)`,
		org, name, scope, keys)
	if err != nil {
		return nil, err
	}
	for _, r := range res.Rows {
		seen[fmt.Sprint(r[0])] = true
	}
	return seen, nil
}

// dropSeen removes the rows whose key is in seen: a record keeps only its new rows (and their lines), and a
// record with no row left is dropped. It returns the remaining records and the skipped row numbers.
func dropSeen(recs []Planned, seen map[string]bool, linesField string) ([]Planned, map[int]bool) {
	skipped := map[int]bool{}
	var out []Planned
	for _, p := range recs {
		if len(p.Keys) == 0 {
			out = append(out, p)
			continue
		}
		lines, _ := p.Input[linesField].([]map[string]any)
		q := Planned{Input: make(map[string]any, len(p.Input))}
		for k, v := range p.Input {
			q.Input[k] = v
		}
		var keep []map[string]any
		for i, k := range p.Keys {
			if seen[k] {
				skipped[p.Rows[i]] = true
				continue
			}
			q.Rows = append(q.Rows, p.Rows[i])
			q.Keys = append(q.Keys, k)
			if i < len(lines) {
				keep = append(keep, lines[i])
			}
		}
		if len(q.Rows) == 0 {
			continue
		}
		if lines != nil {
			q.Input[linesField] = keep
		}
		out = append(out, q)
	}
	return out, skipped
}

// jsonInput gives an input the shape it has after a JSON round trip (lines as []any, integers as int64), the
// shape the engine gets from REST and the one commit reads back from rk_imports.plan.
func jsonInput(in map[string]any) (map[string]any, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	out, _ := pg.DecodeJSON(b).(map[string]any)
	return out, nil
}

// validationRowErrors turns the engine's per-record validation errors into row errors on the first source row.
func validationRowErrors(res []engine.ManyResult, plan []planned) []RowError {
	var out []RowError
	for i, r := range res {
		var ve *engine.ValidationError
		if !errors.As(r.Err, &ve) {
			continue
		}
		row := 0
		if i < len(plan) && len(plan[i].Rows) > 0 {
			row = plan[i].Rows[0]
		}
		for _, is := range ve.Errors {
			out = append(out, RowError{Row: row, Column: is.Field, Message: is.Message})
		}
	}
	return out
}

// Commit creates the records of a previewed entry in one transaction, records its keys and marks it committed.
func (s *Service) Commit(ctx context.Context, a engine.Actor, name, id string) (*Entry, error) {
	im, err := s.imp(name)
	if err != nil {
		return nil, err
	}
	ent, plan, err := s.load(ctx, a, name, id, true)
	if err != nil {
		return nil, err
	}
	if ent.Status == "committed" || ent.Duplicate {
		return nil, errAlreadyImported()
	}
	if len(ent.Errors) > 0 {
		return nil, errImportErrors()
	}
	inputs := make([]map[string]any, len(plan))
	var keys pg.Array
	for i, p := range plan {
		inputs[i] = p.Input
		keys = append(keys, p.Keys...)
	}
	before := func(ctx context.Context, tx pg.Querier) error {
		// Marking the entry committed first: a concurrent commit of the same entry waits on this row and then
		// finds no previewed row; one of another entry with the same file waits on the partial unique index
		// rk_imports_committed_file and then fails with a unique violation.
		res, err := tx.Exec(ctx, `update rk_imports set status = 'committed', committed_at = now()
			where org_id = $1 and import = $2 and id = $3 and status = 'previewed'`, a.Org, name, id)
		if pg.IsCode(err, "23505") {
			return errAlreadyImported()
		}
		if err != nil {
			return err
		}
		if res.RowsAffected() == 0 {
			return errAlreadyImported()
		}
		if len(keys) == 0 {
			return nil
		}
		// A key committed since the preview (an overlapping file) violates the primary key of rk_import_keys.
		_, err = tx.Exec(ctx, `insert into rk_import_keys (org_id, import, scope, key_hash, import_id)
			select $1, $2, $3, k, $4 from unnest($5::text[]) as k`, a.Org, name, ent.Scope, id, keys)
		if pg.IsCode(err, "23505") {
			return errAlreadyImported()
		}
		return err
	}
	res, err := s.Engine.CreateMany(ctx, a, im.Model, inputs, engine.ManyOpts{Before: before})
	if err != nil {
		return nil, err
	}
	if errs := validationRowErrors(res, plan); len(errs) > 0 {
		// Nothing was written (CreateMany rolled back); keep the errors on the entry so it shows why.
		if _, err := s.DB.Exec(ctx, `update rk_imports set errors = $1 where org_id = $2 and id = $3`, pg.JSON{V: errs}, a.Org, id); err != nil {
			return nil, err
		}
		return nil, errImportErrors()
	}
	names := make([]string, 0, len(res))
	for _, r := range res {
		names = append(names, r.Name)
	}
	if _, err := s.DB.Exec(ctx, `update rk_imports set records = $1 where org_id = $2 and id = $3`, pg.JSON{V: names}, a.Org, id); err != nil {
		return nil, err
	}
	return s.Get(ctx, a, name, id)
}

func errImportErrors() error {
	return &engine.StatusError{Status: 422, Code: "import_errors", Msg: "Perbaiki dulu baris yang salah"}
}

// Get returns one entry of the caller's org.
func (s *Service) Get(ctx context.Context, a engine.Actor, name, id string) (*Entry, error) {
	if _, err := s.imp(name); err != nil {
		return nil, err
	}
	ent, _, err := s.load(ctx, a, name, id, false)
	return ent, err
}

// List returns the newest entries of the caller's org for one import.
func (s *Service) List(ctx context.Context, a engine.Actor, name string) ([]*Entry, error) {
	if _, err := s.imp(name); err != nil {
		return nil, err
	}
	res, err := s.DB.Query(ctx, selectEntry+` from rk_imports i where i.org_id = $1 and i.import = $2 order by i.created_at desc, i.id desc limit `+fmt.Sprint(listLimit),
		a.Org, name)
	if err != nil {
		return nil, err
	}
	out := make([]*Entry, 0, len(res.Rows))
	for _, row := range res.Maps() {
		ent, err := scanEntry(row)
		if err != nil {
			return nil, err
		}
		out = append(out, ent)
	}
	return out, nil
}

// selectEntry reads entries with dup_file: another entry committed the same file for the same scope.
const selectEntry = `select i.id, i.import, i.scope, i.file_name, i.file_sha, i.status, i.rows, i.skipped, i.errors::text as errors,
	i.preview::text as preview, i.records::text as records, i.created_by, i.created_at, i.committed_at,
	exists (select 1 from rk_imports c where c.org_id = i.org_id and c.import = i.import and c.scope = i.scope
		and c.file_sha = i.file_sha and c.status = 'committed' and c.id <> i.id) as dup_file`

func (s *Service) load(ctx context.Context, a engine.Actor, name, id string, withPlan bool) (*Entry, []planned, error) {
	sql := selectEntry
	if withPlan {
		sql += `, i.plan::text as plan`
	}
	res, err := s.DB.Query(ctx, sql+` from rk_imports i where i.org_id = $1 and i.import = $2 and i.id = $3`, a.Org, name, id)
	if err != nil {
		return nil, nil, err
	}
	if len(res.Rows) == 0 {
		return nil, nil, &engine.StatusError{Status: 404, Code: "not_found", Msg: fmt.Sprintf(`Entri impor "%s" tidak ada`, id)}
	}
	row := res.Maps()[0]
	ent, err := scanEntry(row)
	if err != nil {
		return nil, nil, err
	}
	if !withPlan {
		return ent, nil, nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(str(row["plan"])), &raw); err != nil {
		return nil, nil, err
	}
	plan := make([]planned, len(raw))
	for i, r := range raw {
		var p struct {
			Input json.RawMessage `json:"input"`
			Rows  []int           `json:"rows"`
			Keys  []string        `json:"keys"`
		}
		if err := json.Unmarshal(r, &p); err != nil {
			return nil, nil, err
		}
		// DecodeJSON keeps integers (money) as int64, never float64.
		in, _ := pg.DecodeJSON(p.Input).(map[string]any)
		plan[i] = planned{Input: in, Rows: p.Rows, Keys: p.Keys}
	}
	return ent, plan, nil
}

func scanEntry(row map[string]any) (*Entry, error) {
	ent := &Entry{
		ID: str(row["id"]), Import: str(row["import"]), Scope: str(row["scope"]), FileName: str(row["file_name"]),
		FileSHA: str(row["file_sha"]), Status: str(row["status"]), CreatedBy: str(row["created_by"]),
		Errors: []RowError{}, Preview: []map[string]any{}, Records: []string{},
	}
	ent.Rows = int(toInt64(row["rows"]))
	ent.Skipped = int(toInt64(row["skipped"]))
	if t, ok := row["created_at"].(time.Time); ok {
		ent.CreatedAt = t
	}
	if t, ok := row["committed_at"].(time.Time); ok {
		ent.CommittedAt = &t
	}
	if err := json.Unmarshal([]byte(str(row["errors"])), &ent.Errors); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(str(row["records"])), &ent.Records); err != nil {
		return nil, err
	}
	if prev, ok := pg.DecodeJSON([]byte(str(row["preview"]))).([]any); ok {
		for _, p := range prev {
			if m, ok := p.(map[string]any); ok {
				ent.Preview = append(ent.Preview, m)
			}
		}
	}
	switch {
	case row["dup_file"] == true:
		ent.Duplicate, ent.Message = true, msgDuplicateFile
	case ent.Rows > 0 && ent.Skipped == ent.Rows:
		ent.Duplicate, ent.Message = true, msgDuplicateRows
	}
	return ent, nil
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func toInt64(v any) int64 {
	n, _ := v.(int64)
	return n
}
