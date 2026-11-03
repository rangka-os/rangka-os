package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// RestoreOptions say where Restore puts what is not in the database.
type RestoreOptions struct {
	Version  string // the binary's version; "dev" skips the archive version check
	Into     string // the archived site/ and apps/ are extracted here (must not exist or be empty)
	FilesDir string // uploads go here; "" = FilesDir(registry, ""), like serve (no site archived: <Into>/files)
	// Registry loads and validates the extracted metadata (rangka's usual loader).
	Registry func(appsDir, siteDir string) (*meta.Registry, error)
}

// Restore rebuilds an archive into an empty database: it checks every checksum and the version, migrates
// from the archived metadata, loads the tables in one transaction in foreign-key order, resets sequences,
// copies the files and verifies the result against the manifest.
func Restore(ctx context.Context, db *pg.Pool, archive string, o RestoreOptions) (*Manifest, error) {
	if o.Into == "" || o.Registry == nil {
		return nil, errors.New("backup: restore needs Into and Registry")
	}
	tmp, err := os.MkdirTemp("", "rangka-restore-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	// 1. the whole archive into a temp dir, every checksum checked
	m, names, err := extract(archive, tmp)
	if err != nil {
		return nil, err
	}
	if err := check(m, tmp, names); err != nil {
		return nil, err
	}
	// 2. version, then an empty target
	if m.Format > Format || newer(m.Version, o.Version) {
		return nil, fmt.Errorf("%w: archive %s (format %d), binary %s (format %d); restore with a %s binary or newer",
			ErrNewerArchive, m.Version, m.Format, o.Version, Format, m.Version)
	}
	r, err := db.Query(ctx, "select count(*) from pg_class where relnamespace = 'public'::regnamespace and relkind in ('r', 'p')")
	if err != nil {
		return nil, err
	}
	if n := r.Rows[0][0].(int64); n > 0 {
		return nil, fmt.Errorf("%w: it has %d table(s) in schema public; restore into a new database", ErrNotEmpty, n)
	}
	// 3. metadata into Into, migrate from it
	if !emptyDir(o.Into) {
		return nil, fmt.Errorf("backup: %s is not empty; pick another --into directory", o.Into)
	}
	if o.FilesDir != "" && !emptyDir(o.FilesDir) {
		return nil, fmt.Errorf("backup: files dir %s is not empty; pick another --files directory", o.FilesDir)
	}
	apps, site := filepath.Join(o.Into, "apps"), ""
	for _, d := range []string{"site", "apps"} {
		if err := copyTree(filepath.Join(tmp, d), filepath.Join(o.Into, d), 0o755, 0o644); err != nil {
			return nil, err
		}
	}
	if st, err := os.Stat(filepath.Join(o.Into, "site")); err == nil && st.IsDir() {
		site = filepath.Join(o.Into, "site")
	}
	reg, err := o.Registry(apps, site)
	if err != nil {
		return nil, fmt.Errorf("archived metadata: %w", err)
	}
	files := o.FilesDir
	if files == "" {
		files = FilesDir(reg, "")
		if site == "" {
			files = filepath.Join(o.Into, "files")
		}
		if !emptyDir(files) {
			return nil, fmt.Errorf("backup: files dir %s is not empty; pass an empty --files directory", files)
		}
	}
	// from here on the database is written; a failure leaves it partly restored
	partly := func(err error) error {
		return fmt.Errorf("%w (the database is now partly restored: drop and recreate it before retrying)", err)
	}
	if _, err := store.MigrateWith(ctx, db, reg, store.MigrateOpts{Source: "restore"}); err != nil {
		return nil, partly(fmt.Errorf("migrate: %w", err))
	}
	// 4. tables and columns the models no longer have (soft-deleted ones, x_ history) from the manifest
	if err := addMissing(ctx, db, m.Tables); err != nil {
		return nil, partly(err)
	}
	// 5. the rows, in one transaction
	if err := load(ctx, db, m.Tables, tmp); err != nil {
		return nil, partly(err)
	}
	// 6. files
	if err := copyTree(filepath.Join(tmp, "files"), files, 0o750, 0o640); err != nil {
		return nil, partly(err)
	}
	for _, f := range m.Files {
		rel, ok := strings.CutPrefix(f.Path, "files/")
		if !ok {
			continue
		}
		if sum, _, err := hashFile(filepath.Join(files, filepath.FromSlash(rel))); err != nil || sum != f.SHA256 {
			return nil, partly(fmt.Errorf("%w: file %s", ErrVerify, f.Path))
		}
	}
	// 7. verify: the tables against the manifest, and every restored attachment row against its blob
	if err := verify(ctx, db, m); err != nil {
		return nil, partly(err)
	}
	blobs, err := attachmentBlobs(ctx, db, "")
	if err != nil {
		return nil, partly(err)
	}
	if missing := missingBlobs(blobs, func(rel string) bool {
		st, err := os.Stat(filepath.Join(files, filepath.FromSlash(rel)))
		return err == nil && st.Mode().IsRegular()
	}); len(missing) > 0 {
		return nil, partly(fmt.Errorf("%w: %d attachment blob(s) missing from %s: %s", ErrVerify, len(missing), files, listSome(missing)))
	}
	return m, nil
}

// extract reads every regular entry of a tar.gz into dir and returns the manifest and the entry names.
func extract(archive, dir string) (*Manifest, map[string]bool, error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	tr := tar.NewReader(zr)
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Typeflag != tar.TypeReg || !cleanEntry(h.Name) || seen[h.Name] {
			return nil, nil, fmt.Errorf("%w: unexpected entry %q", ErrCorrupt, h.Name)
		}
		seen[h.Name] = true
		dst := filepath.Join(dir, filepath.FromSlash(h.Name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, nil, err
		}
		out, err := os.Create(dst)
		if err != nil {
			return nil, nil, err
		}
		_, err = io.Copy(out, tr)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %s: %v", ErrCorrupt, h.Name, err)
		}
	}
	// gzip checks its CRC at the end of the stream; read to it
	if _, err := io.Copy(io.Discard, zr); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: no manifest.json", ErrCorrupt)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, nil, fmt.Errorf("%w: manifest.json: %v", ErrCorrupt, err)
	}
	return &m, seen, nil
}

// pgType is a Postgres type name as format_type prints it (text, numeric(18,2), character varying(20),
// timestamp with time zone, text[]). Manifest types go into DDL, so anything else is refused.
var pgType = regexp.MustCompile(`^[a-z][a-z0-9_ ]*(\([0-9]+(,[0-9]+)?\))?(\[\])?$`)

// check compares every data/ and file entry with its manifest checksum, checks table names and column types,
// and refuses entries the manifest does not list.
func check(m *Manifest, dir string, names map[string]bool) error {
	listed := map[string]bool{"manifest.json": true}
	if m.Format < 1 || len(m.Tables) == 0 {
		return fmt.Errorf("%w: manifest has no tables", ErrCorrupt)
	}
	for _, t := range m.Tables {
		if !cleanEntry(t.Name) || strings.Contains(t.Name, "/") || len(t.Columns) == 0 {
			return fmt.Errorf("%w: bad table %q in the manifest", ErrCorrupt, t.Name)
		}
		for _, c := range t.Columns {
			if c.Name == "" || !pgType.MatchString(c.Type) {
				return fmt.Errorf("%w: bad column %q %q of table %s in the manifest", ErrCorrupt, c.Name, c.Type, t.Name)
			}
		}
		listed["data/"+t.Name+".copy"] = true
		if sum, _, err := hashFile(filepath.Join(dir, "data", t.Name+".copy")); err != nil || sum != t.SHA256 {
			return fmt.Errorf("%w: data/%s.copy is missing or does not match its checksum", ErrCorrupt, t.Name)
		}
	}
	for _, f := range m.Files {
		top, _, _ := strings.Cut(f.Path, "/")
		if !cleanEntry(f.Path) || (top != "site" && top != "apps" && top != "files") {
			return fmt.Errorf("%w: bad path %q in the manifest", ErrCorrupt, f.Path)
		}
		if sum, size, err := hashFile(filepath.Join(dir, filepath.FromSlash(f.Path))); err != nil || sum != f.SHA256 || size != f.Size {
			return fmt.Errorf("%w: %s is missing or does not match its checksum", ErrCorrupt, f.Path)
		}
		listed[f.Path] = true
	}
	for _, n := range sortedNames(names) {
		if !listed[n] {
			return fmt.Errorf("%w: entry %s is not in the manifest", ErrCorrupt, n)
		}
	}
	return nil
}

// emptyDir reports whether dir does not exist or has no entries.
func emptyDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	return errors.Is(err, fs.ErrNotExist) || (err == nil && len(entries) == 0)
}

// newer reports whether archive's major.minor.patch is above binary's. Pre-release and build suffixes are
// ignored; a binary without a version (dev, test builds) skips the check, as does an unreadable archive version.
func newer(archive, binary string) bool {
	a, okA := semver(archive)
	b, okB := semver(binary)
	if !okA || !okB {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func semver(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// addMissing creates manifest tables the migration did not create and adds manifest columns it did not.
func addMissing(ctx context.Context, db *pg.Pool, want []TableSum) error {
	live, err := liveTables(ctx, db)
	if err != nil {
		return err
	}
	have := map[string]table{}
	for _, t := range live {
		have[t.name] = t
	}
	for _, t := range want {
		lt, ok := have[t.Name]
		if !ok {
			defs := make([]string, len(t.Columns))
			for i, c := range t.Columns {
				defs[i] = pg.QuoteIdent(c.Name) + " " + c.Type
			}
			if _, err := db.Exec(ctx, fmt.Sprintf("create table %s (%s)", pg.QuoteIdent(t.Name), strings.Join(defs, ", "))); err != nil {
				return fmt.Errorf("create archived table %s: %w", t.Name, err)
			}
			// an archived model table (an org_id column; kernel rk_ tables always exist) keeps the org policy
			if !strings.HasPrefix(t.Name, "rk_") && slices.ContainsFunc(t.Columns, func(c Column) bool { return c.Name == "org_id" }) {
				for _, s := range store.RLSStatements(t.Name) {
					if _, err := db.Exec(ctx, s); err != nil {
						return fmt.Errorf("row-level security on archived table %s: %w", t.Name, err)
					}
				}
			}
			continue
		}
		for _, c := range t.Columns {
			if lt.has(c.Name) {
				continue
			}
			if _, err := db.Exec(ctx, fmt.Sprintf("alter table %s add column if not exists %s %s", pg.QuoteIdent(t.Name), pg.QuoteIdent(c.Name), c.Type)); err != nil {
				return fmt.Errorf("add archived column %s.%s: %w", t.Name, c.Name, err)
			}
		}
	}
	return nil
}

// fkOrder sorts tables so referenced tables come before the tables that reference them (ties by name).
func fkOrder(ctx context.Context, q pg.Querier, tables []TableSum) ([]TableSum, error) {
	r, err := q.Query(ctx, `select f.relname, t.relname from pg_constraint c
		join pg_class f on f.oid = c.conrelid join pg_class t on t.oid = c.confrelid
		where c.contype = 'f' and f.relnamespace = 'public'::regnamespace`)
	if err != nil {
		return nil, err
	}
	byName := map[string]TableSum{}
	for _, t := range tables {
		byName[t.Name] = t
	}
	deps := map[string]map[string]bool{}
	for _, row := range r.Rows {
		from, to := row[0].(string), row[1].(string)
		if from == to {
			continue
		}
		if _, ok := byName[to]; !ok {
			continue
		}
		if deps[from] == nil {
			deps[from] = map[string]bool{}
		}
		deps[from][to] = true
	}
	var out []TableSum
	done := map[string]bool{}
	for len(out) < len(tables) {
		progress := false
		for _, name := range sortedNames(byName) {
			if done[name] {
				continue
			}
			ready := true
			for d := range deps[name] {
				ready = ready && done[d]
			}
			if ready {
				out = append(out, byName[name])
				done[name] = true
				progress = true
			}
		}
		if !progress { // a cycle: the rest in name order
			for _, name := range sortedNames(byName) {
				if !done[name] {
					out = append(out, byName[name])
					done[name] = true
				}
			}
		}
	}
	return out, nil
}

// load empties what the migration wrote, COPYs every table in foreign-key order and sets every owned
// sequence past the restored ids, in one transaction.
func load(ctx context.Context, db *pg.Pool, tables []TableSum, dir string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, settings); err != nil {
		return err
	}
	live, err := liveTables(ctx, tx)
	if err != nil {
		return err
	}
	names := make([]string, len(live))
	for i, t := range live {
		names[i] = pg.QuoteIdent(t.name)
	}
	// the target was empty before the restore: whatever is there now (rk_migrations, rk_meta_files) the
	// migration wrote, and the archive has the source's own rows for it
	if _, err := tx.Exec(ctx, "truncate "+strings.Join(names, ", ")); err != nil {
		return err
	}
	ordered, err := fkOrder(ctx, tx, tables)
	if err != nil {
		return err
	}
	// COPY FROM is refused on a table whose row-level security binds the caller, which forced RLS does for
	// the owner role a hosted install runs as. The owner may lift the force for this transaction; it is put
	// back before commit, and a failed load rolls the ALTERs back with everything else.
	r, err := tx.Query(ctx, `select quote_ident(relname) from pg_class
		where relnamespace = 'public'::regnamespace and relkind = 'r' and relforcerowsecurity`)
	if err != nil {
		return err
	}
	var forced []string
	for _, row := range r.Rows {
		forced = append(forced, row[0].(string))
	}
	for _, t := range forced {
		if _, err := tx.Exec(ctx, "alter table "+t+" no force row level security"); err != nil {
			return fmt.Errorf("lift forced row-level security on %s for the load: %w", t, err)
		}
	}
	for _, t := range ordered {
		f, err := os.Open(filepath.Join(dir, "data", t.Name+".copy"))
		if err != nil {
			return err
		}
		tt := table{name: t.Name, cols: t.Columns}
		n, err := tx.CopyFrom(ctx, f, fmt.Sprintf("copy %s (%s) from stdin", pg.QuoteIdent(t.Name), tt.colList()))
		f.Close()
		if err != nil {
			return fmt.Errorf("load %s: %w", t.Name, err)
		}
		if n != t.Rows {
			return fmt.Errorf("%w: %s: loaded %d rows, the manifest says %d", ErrVerify, t.Name, n, t.Rows)
		}
	}
	for _, t := range forced {
		if _, err := tx.Exec(ctx, "alter table "+t+" force row level security"); err != nil {
			return fmt.Errorf("force row-level security on %s again: %w", t, err)
		}
	}
	r, err = tx.Query(ctx, `select quote_ident(n.nspname) || '.' || quote_ident(s.relname), quote_ident(t.relname), quote_ident(a.attname)
		from pg_class s join pg_namespace n on n.oid = s.relnamespace
		join pg_depend d on d.objid = s.oid and d.classid = 'pg_class'::regclass and d.refclassid = 'pg_class'::regclass and d.deptype in ('a', 'i')
		join pg_class t on t.oid = d.refobjid join pg_attribute a on a.attrelid = t.oid and a.attnum = d.refobjsubid
		where s.relkind = 'S' and t.relnamespace = 'public'::regnamespace`)
	if err != nil {
		return err
	}
	for _, row := range r.Rows {
		seq, tbl, col := row[0].(string), row[1].(string), row[2].(string)
		// next value = max + 1 (or 1 on an empty table)
		if _, err := tx.Query(ctx, fmt.Sprintf("select setval($1::regclass, coalesce((select max(%s) from %s), 0) + 1, false)", col, tbl), seq); err != nil {
			return fmt.Errorf("sequence %s: %w", seq, err)
		}
	}
	return tx.Commit(ctx)
}

// verify recomputes the manifest's sums on the restored database (the manifest's columns, the manifest's org).
func verify(ctx context.Context, db *pg.Pool, m *Manifest) error {
	tables := make([]table, len(m.Tables))
	for i, t := range m.Tables {
		tables[i] = table{name: t.Name, cols: t.Columns}
	}
	var got []TableSum
	err := snapshot(ctx, db, func(tx *pg.Tx) error {
		var err error
		got, err = sumTables(ctx, tx, tables, m.Org, nil)
		return err
	})
	if err != nil {
		return err
	}
	for i, t := range m.Tables {
		if got[i].Rows != t.Rows || got[i].SHA256 != t.SHA256 {
			return fmt.Errorf("%w: table %s has %d rows (sha256 %s), the archive %d (%s)", ErrVerify, t.Name, got[i].Rows, got[i].SHA256, t.Rows, t.SHA256)
		}
	}
	return nil
}

// copyTree copies the regular files under src into dst with the given modes (nothing when src does not exist).
func copyTree(src, dst string, dirMode, fileMode os.FileMode) error {
	if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(to, dirMode)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}
