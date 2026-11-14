// Package backup writes and restores one verified archive of a Rangka installation: every table in schema
// public as COPY text, the site and apps metadata, and the uploaded files (contract 0005 §2). It is native Go
// (COPY through pgx), so the single binary needs no Postgres client tools.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/files"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

// Format is the archive layout version written into the manifest.
const Format = 1

var (
	// ErrNotEmpty: the restore target already has tables in schema public.
	ErrNotEmpty = errors.New("backup: the target database is not empty")
	// ErrNewerArchive: the archive was written by a newer kernel than this binary.
	ErrNewerArchive = errors.New("backup: the archive is newer than this binary")
	// ErrCorrupt: the archive cannot be read or an entry does not match its checksum.
	ErrCorrupt = errors.New("backup: the archive is corrupt")
	// ErrVerify: the restored database does not match the archive's checksums, or an attachment has no blob.
	ErrVerify = errors.New("backup: the restored database does not match the archive")
	// ErrMissingBlobs: attachment rows in scope have no blob in the files dir (or the files dir is missing),
	// so the archive would be incomplete.
	ErrMissingBlobs = errors.New("backup: attachment files are missing from the files dir")
)

// Options say what Write archives.
type Options struct {
	Org                                 string // "" = the whole database
	SiteDir, AppsDir, FilesDir, Version string
	// ModulesDir and ConnectorsDir are the other metadata directories the loader read (contract 0001
	// modules, connectors); "" = none. Restore needs all of them to rebuild the schema.
	ModulesDir, ConnectorsDir string
	Now                       func() time.Time // default time.Now
}

// Manifest is manifest.json, the first entry of an archive.
type Manifest struct {
	Format    int        `json:"format"`
	Version   string     `json:"version"`
	Org       string     `json:"org"`
	CreatedAt time.Time  `json:"created_at"`
	Tables    []TableSum `json:"tables"`
	Files     []FileSum  `json:"files"`
	// Excluded lists the tables an org archive leaves out: installation-level tables without an org link
	// (rk_keys, …). Restore starts them empty; the kernel fills them as on first start.
	Excluded []string `json:"excluded"`
	// Absent lists the optional metadata directories (modules, connectors) that were configured but do not
	// exist; the loader tolerates them, so the archive does too and restore rebuilds without them.
	Absent []string `json:"absent,omitempty"`
	// KEKIDs names the key-encryption keys that wrap the archived org data keys (contract 0017 §2): the archive holds
	// ciphertext and wrapped keys only, so a restore on another machine needs RANGKA_MASTER_KEY with one of these ids.
	KEKIDs []string `json:"kek_ids,omitempty"`
}

// TableSum is a table's row count and the SHA-256 of its canonical COPY text (see Sums).
type TableSum struct {
	Name    string   `json:"name"`
	Rows    int64    `json:"rows"`
	SHA256  string   `json:"sha256"`
	Columns []Column `json:"columns"`
}

// Column is a column name and its SQL type (format_type, usable in DDL).
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// FileSum is a non-table archive entry (site/…, apps/…, modules/…, connectors/…, files/…).
type FileSum struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// settings make COPY text independent of the server's and session's defaults.
const settings = `set local timezone = 'UTC'; set local datestyle = 'ISO, YMD'; set local intervalstyle = 'postgres';
set local extra_float_digits = 1; set local bytea_output = 'hex'`

// table is a table and the columns Sums and Write read, sorted by name.
type table struct {
	name string
	cols []Column
}

// liveTables lists the base tables of schema public with their columns sorted by name.
func liveTables(ctx context.Context, q pg.Querier) ([]table, error) {
	r, err := q.Query(ctx, `select c.relname, a.attname, format_type(a.atttypid, a.atttypmod)
		from pg_class c join pg_attribute a on a.attrelid = c.oid
		where c.relnamespace = 'public'::regnamespace and c.relkind in ('r', 'p') and a.attnum > 0 and not a.attisdropped
		order by c.relname collate "C", a.attname collate "C"`)
	if err != nil {
		return nil, err
	}
	var out []table
	for _, row := range r.Rows {
		name := row[0].(string)
		if len(out) == 0 || out[len(out)-1].name != name {
			out = append(out, table{name: name})
		}
		t := &out[len(out)-1]
		t.cols = append(t.cols, Column{Name: row[1].(string), Type: row[2].(string)})
	}
	return out, nil
}

func (t table) has(col string) bool {
	for _, c := range t.cols {
		if c.Name == col {
			return true
		}
	}
	return false
}

// filter is the where clause limiting a table to one org: tables with an org column by it, rk_orgs by id,
// rk_users and tables with user_id to the org's members. Other tables are whole.
func (t table) filter(org string) string {
	if org == "" {
		return t.notSandbox()
	}
	lit := pg.QuoteLiteral(org)
	members := "(select user_id from rk_members where org_id = " + lit + ")"
	var conds []string
	switch {
	case t.name == "rk_orgs":
		conds = append(conds, "id = "+lit)
	case t.name == "rk_users":
		conds = append(conds, "id in "+members)
	}
	switch {
	case t.has("org_id"):
		conds = append(conds, "org_id = "+lit)
	case t.has("org"):
		conds = append(conds, "org = "+lit)
	}
	if t.has("user_id") && t.name != "rk_members" {
		conds = append(conds, "user_id in "+members)
	}
	if len(conds) == 0 {
		return ""
	}
	return " where " + strings.Join(conds, " and ")
}

// notSandbox is the where clause of a whole-database archive: the ruang coba (contract 0052 §2) is left out, so
// a restore never recreates it. Tables without an org link are whole.
func (t table) notSandbox() string {
	sandboxes := "(select id from rk_orgs where sandbox)"
	switch {
	case t.name == "rk_orgs":
		if t.has("sandbox") {
			return " where not sandbox"
		}
		return ""
	case t.name == "rk_sandboxes":
		return " where false"
	case t.has("org_id"):
		return " where coalesce(org_id, '') not in " + sandboxes
	case t.has("org"):
		return " where coalesce(org, '') not in " + sandboxes
	}
	return ""
}

// installTables are tables without an org link that an org archive still carries: the restored kernel needs
// them to know which metadata and migrations are applied.
var installTables = map[string]bool{"rk_meta_files": true, "rk_migrations": true}

// scope returns the tables an archive of org holds and the names it leaves out: for one org, the tables
// without an org link (no filter) other than installTables. org "" keeps every table.
func scope(tables []table, org string) (kept []table, excluded []string) {
	excluded = []string{}
	if org == "" {
		return tables, excluded
	}
	for _, t := range tables {
		if t.filter(org) == "" && !installTables[t.name] {
			excluded = append(excluded, t.name)
			continue
		}
		kept = append(kept, t)
	}
	return kept, excluded
}

// blob is an attachment's content in the files dir.
type blob struct{ org, sha string }

// attachmentBlobs lists the distinct blobs the rk_attachments rows of org ("" = every org) point at; none when
// the table does not exist.
func attachmentBlobs(ctx context.Context, q pg.Querier, org string) ([]blob, error) {
	r, err := q.Query(ctx, "select to_regclass('rk_attachments') is not null")
	if err != nil {
		return nil, err
	}
	if ok, _ := r.Rows[0][0].(bool); !ok {
		return nil, nil
	}
	sql := "select org_id, sha256 from rk_attachments"
	var args []any
	if org != "" {
		sql += " where org_id = $1"
		args = append(args, org)
	}
	if r, err = q.Query(ctx, sql+` group by org_id, sha256 order by org_id collate "C", sha256 collate "C"`, args...); err != nil {
		return nil, err
	}
	out := make([]blob, len(r.Rows))
	for i, row := range r.Rows {
		out[i] = blob{row[0].(string), row[1].(string)}
	}
	return out, nil
}

// missingBlobs returns the blobs whose files/<rel> path has() does not report, as slash paths.
func missingBlobs(blobs []blob, has func(rel string) bool) []string {
	var missing []string
	for _, b := range blobs {
		rel, err := files.BlobRel(b.org, b.sha)
		if err != nil || !has(rel) {
			if err != nil {
				rel = b.org + "/" + b.sha
			}
			missing = append(missing, rel)
		}
	}
	return missing
}

// listSome is the first few of names for an error message.
func listSome(names []string) string {
	if len(names) > 5 {
		return strings.Join(names[:5], ", ") + fmt.Sprintf(" and %d more", len(names)-5)
	}
	return strings.Join(names, ", ")
}

func (t table) colList() string {
	q := make([]string, len(t.cols))
	for i, c := range t.cols {
		q[i] = pg.QuoteIdent(c.Name)
	}
	return strings.Join(q, ", ")
}

// copyOut is the canonical COPY: columns sorted by name, rows ordered by the text of every column (collation
// "C", so two servers with different locales agree).
func (t table) copyOut(org string) string {
	order := make([]string, len(t.cols))
	for i, c := range t.cols {
		order[i] = pg.QuoteIdent(c.Name) + `::text collate "C"`
	}
	return fmt.Sprintf("copy (select %s from %s%s order by %s) to stdout", t.colList(), pg.QuoteIdent(t.name), t.filter(org), strings.Join(order, ", "))
}

// snapshot runs fn in a read-only repeatable-read transaction with the canonical COPY settings, so every
// table is read from the same snapshot.
func snapshot(ctx context.Context, db *pg.Pool, fn func(tx *pg.Tx) error) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "set transaction isolation level repeatable read, read only; "+settings); err != nil {
		return err
	}
	return fn(tx)
}

// sumTables hashes the canonical COPY of each table, also writing it to out(table) when out is not nil.
func sumTables(ctx context.Context, tx *pg.Tx, tables []table, org string, out func(name string) (io.Writer, error)) ([]TableSum, error) {
	sums := make([]TableSum, 0, len(tables))
	for _, t := range tables {
		h := sha256.New()
		var w io.Writer = h
		if out != nil {
			f, err := out(t.name)
			if err != nil {
				return nil, err
			}
			w = io.MultiWriter(h, f)
		}
		n, err := tx.CopyTo(ctx, w, t.copyOut(org))
		if err != nil {
			return nil, fmt.Errorf("copy %s: %w", t.name, err)
		}
		sums = append(sums, TableSum{Name: t.name, Rows: n, SHA256: hex.EncodeToString(h.Sum(nil)), Columns: t.cols})
	}
	return sums, nil
}

// Sums returns each public table's row count and the SHA-256 of
// `COPY (select <columns sorted by name> from t [org filter] order by <same columns>) TO STDOUT` with
// timezone UTC. org "" covers the whole database; for one org, the tables an org archive excludes are left out.
func Sums(ctx context.Context, db *pg.Pool, org string) ([]TableSum, error) {
	var sums []TableSum
	err := snapshot(ctx, db, func(tx *pg.Tx) error {
		tables, err := liveTables(ctx, tx)
		if err != nil {
			return err
		}
		tables, _ = scope(tables, org)
		sums, err = sumTables(ctx, tx, tables, org, nil)
		return err
	})
	return sums, err
}

// FilesDir is the attachments directory, resolved exactly like rangka serve (contract 0011 §1): override
// (--files-dir / --files / $RANGKA_FILES_DIR), else system.rk files.path, else data/files. A relative path is
// relative to the working directory, not to the site dir; deployments set RANGKA_FILES_DIR.
func FilesDir(reg *meta.Registry, override string) string {
	if override != "" {
		return override
	}
	if reg != nil {
		if v, ok := reg.SystemConfig("files.path").(string); ok && v != "" {
			return v
		}
	}
	return "data/files"
}

// Write archives the database (or one org), the site (minus data/ and backups/), the apps and the files dir
// as tar.gz into w and returns the manifest. Tables are read from one snapshot. An org archive holds only that
// org's rows and its area of the files dir; tables without an org link (rk_keys, …) other than rk_meta_files
// and rk_migrations are left out and listed in Manifest.Excluded. Write fails with ErrMissingBlobs when an
// rk_attachments row in scope has no blob in the files dir, also when the files dir does not exist.
func Write(ctx context.Context, db *pg.Pool, w io.Writer, o Options) (*Manifest, error) {
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	m := &Manifest{Format: Format, Version: o.Version, Org: o.Org, CreatedAt: now().UTC(), Files: []FileSum{}}
	tmp, err := os.MkdirTemp("", "rangka-backup-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	// 1. tables into temp files (the tar header needs each entry's size up front)
	var open []*os.File
	var blobs []blob
	defer func() {
		for _, f := range open {
			f.Close()
		}
	}()
	err = snapshot(ctx, db, func(tx *pg.Tx) error {
		tables, err := liveTables(ctx, tx)
		if err != nil {
			return err
		}
		if o.Org != "" {
			r, err := tx.Query(ctx, "select 1 from rk_orgs where id = $1", o.Org)
			if err != nil {
				return err
			}
			if len(r.Rows) == 0 {
				return fmt.Errorf("backup: org %q not found", o.Org)
			}
		}
		tables, m.Excluded = scope(tables, o.Org)
		for _, t := range tables {
			if t.name != "rk_org_keys" {
				continue
			}
			r, err := tx.Query(ctx, "select distinct kek_id from rk_org_keys where $1 = '' or org_id = $1 order by 1", o.Org)
			if err != nil {
				return err
			}
			for _, row := range r.Rows {
				m.KEKIDs = append(m.KEKIDs, row[0].(string))
			}
		}
		if blobs, err = attachmentBlobs(ctx, tx, o.Org); err != nil {
			return err
		}
		m.Tables, err = sumTables(ctx, tx, tables, o.Org, func(name string) (io.Writer, error) {
			f, err := os.Create(filepath.Join(tmp, name+".copy"))
			if err == nil {
				open = append(open, f)
			}
			return f, err
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	// 2. site, apps and files
	filesAbs := ""
	if o.FilesDir != "" {
		filesAbs = abs(o.FilesDir)
		if r, err := filepath.EvalSymlinks(o.FilesDir); err == nil {
			filesAbs = r
		}
	}
	type entry struct{ name, src string }
	var entries []entry
	isFiles := func(prefix string) bool { return prefix == "files" || strings.HasPrefix(prefix, "files/") }
	add := func(prefix, dir string, skip map[string]bool) error {
		if dir == "" {
			return nil
		}
		if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) && isFiles(prefix) {
			return nil // no uploads yet; checked against rk_attachments below
		}
		// a root given through a symlink (a mounted volume, a linked site) is walked at its target
		root, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return err
		}
		return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			if d.IsDir() {
				// the files dir goes under files/ only, also when system.rk puts it inside the site
				if skip[filepath.ToSlash(rel)] || (!isFiles(prefix) && filesAbs != "" && abs(p) == filesAbs) {
					return filepath.SkipDir
				}
				return nil
			}
			name := prefix + "/" + filepath.ToSlash(rel)
			switch {
			case d.Type().IsRegular():
			case d.Type()&fs.ModeSymlink != 0:
				// a link to a file is archived as the file's content under the link's path; a link to a
				// directory is refused (links can form cycles), never skipped silently
				st, err := os.Stat(p)
				if err != nil {
					return fmt.Errorf("backup: %s: broken symlink: %w", p, err)
				}
				if !st.Mode().IsRegular() {
					return fmt.Errorf("backup: %s is a symlink to a directory or special file; replace it with the files themselves", p)
				}
			default:
				return fmt.Errorf("backup: %s is not a regular file", p)
			}
			entries = append(entries, entry{name, p})
			return nil
		})
	}
	if err := add("site", o.SiteDir, map[string]bool{"data": true, "backups": true}); err != nil {
		return nil, err
	}
	for _, d := range []struct{ name, dir string }{{"apps", o.AppsDir}, {"modules", o.ModulesDir}, {"connectors", o.ConnectorsDir}} {
		if _, err := os.Stat(d.dir); d.name != "apps" && d.dir != "" && errors.Is(err, fs.ErrNotExist) {
			m.Absent = append(m.Absent, d.name)
			continue
		}
		if err := add(d.name, d.dir, nil); err != nil {
			return nil, err
		}
	}
	filesPrefix, filesDir := "files", o.FilesDir
	if o.Org != "" && o.FilesDir != "" {
		// an org archive carries only the org's own area: files/<orgdir>/…
		od, err := files.OrgDir(o.Org)
		if err != nil {
			return nil, err
		}
		filesPrefix, filesDir = "files/"+od, filepath.Join(o.FilesDir, od)
	}
	if err := add(filesPrefix, filesDir, nil); err != nil {
		return nil, err
	}
	archived := map[string]bool{}
	for _, e := range entries {
		archived[e.name] = true
	}
	if missing := missingBlobs(blobs, func(rel string) bool { return archived["files/"+rel] }); len(missing) > 0 {
		where := fmt.Sprintf("files dir %s", o.FilesDir)
		if o.FilesDir == "" {
			where = "no files dir given"
		} else if _, err := os.Stat(o.FilesDir); errors.Is(err, fs.ErrNotExist) {
			where += " does not exist"
		}
		return nil, fmt.Errorf("%w: %d attachment blob(s) not found (%s): %s; point --files-dir or RANGKA_FILES_DIR at the directory rangka serve uses",
			ErrMissingBlobs, len(missing), where, listSome(missing))
	}
	for _, e := range entries {
		sum, size, err := hashFile(e.src)
		if err != nil {
			return nil, err
		}
		m.Files = append(m.Files, FileSum{Path: e.name, Size: size, SHA256: sum})
	}
	// 3. the archive: manifest first, then data/, then the files in manifest order
	zw := gzip.NewWriter(w)
	tw := tar.NewWriter(zw)
	mj, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	mtime := m.CreatedAt
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o644, Size: int64(len(mj)), ModTime: mtime, Typeflag: tar.TypeReg}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(mj); err != nil {
		return nil, err
	}
	for _, t := range m.Tables {
		if err := addFile(tw, "data/"+t.Name+".copy", filepath.Join(tmp, t.Name+".copy"), mtime); err != nil {
			return nil, err
		}
	}
	for i, e := range entries {
		if err := addFile(tw, e.name, e.src, mtime); err != nil {
			return nil, err
		}
		// a file that changed while we archived it no longer matches its checksum: fail now, not on restore
		if sum, _, err := hashFile(e.src); err != nil || sum != m.Files[i].SHA256 {
			return nil, fmt.Errorf("backup: %s changed while it was archived; run the backup again", e.src)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return m, nil
}

func abs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return filepath.Clean(p)
}

func addFile(tw *tar.Writer, name, src string, mtime time.Time) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: st.Size(), ModTime: mtime, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err := io.CopyN(tw, f, st.Size()); err != nil {
		return fmt.Errorf("%s: %w", src, err)
	}
	return nil
}

func hashFile(p string) (sum string, size int64, err error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	size, err = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), size, err
}

// sortedNames returns map keys sorted.
func sortedNames[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// cleanEntry reports whether an archive entry name is a plain relative slash path that stays inside the
// directory it is extracted to on every OS: no .., not absolute, no backslashes or drive letters.
func cleanEntry(name string) bool {
	return name != "" && !strings.HasPrefix(name, "/") && !strings.ContainsAny(name, `\:`) &&
		path.Clean(name) == name && filepath.IsLocal(filepath.FromSlash(name))
}
