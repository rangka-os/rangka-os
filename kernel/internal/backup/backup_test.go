package backup_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/backup"
	"github.com/rangka-os/rangka-os/kernel/internal/files"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/server"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

const version = "0.4.0-dev"

var fixedNow = func() time.Time { return time.Date(2026, 11, 3, 2, 30, 0, 0, time.UTC) }

// source is a seeded kopi-senja database with its apps, site and files dir in a temp dir.
type source struct {
	db                         *pg.Pool
	reg                        *meta.Registry
	dir, apps, site, files, fx string
}

func loadRegistry(d backup.Dirs) (*meta.Registry, error) {
	return server.LoadRegistry(server.Config{AppsDir: d.Apps, SiteDir: d.Site, ModulesDir: d.Modules, ConnectorsDir: d.Connectors,
		Schemas: schema.Embedded()})
}

func pool(t *testing.T, url string) *pg.Pool {
	t.Helper()
	p, err := pg.NewPool(context.Background(), url, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func newSource(t *testing.T) *source {
	t.Helper()
	auth.BcryptCost = 4
	dir := t.TempDir()
	for _, p := range []string{testdb.Catalogue("apps"), testdb.Catalogue("fixtures"), testdb.Catalogue("sites")} {
		if err := exec.Command("cp", "-r", p, filepath.Join(dir, filepath.Base(p))).Run(); err != nil {
			t.Fatal(err)
		}
	}
	s := &source{dir: dir, apps: filepath.Join(dir, "apps"), site: filepath.Join(dir, "sites", "kopi-senja"), fx: filepath.Join(dir, "fixtures")}
	s.files = filepath.Join(s.site, "data", "files")
	if err := os.MkdirAll(filepath.Join(s.files, "lead", "LEAD-001"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.files, "lead", "LEAD-001", "kartu nama.jpg"), []byte("\xff\xd8\xff not really a jpeg \x00\x01"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := loadRegistry(backup.Dirs{Apps: s.apps, Site: s.site})
	if err != nil {
		t.Fatal(err)
	}
	s.reg = reg
	s.db = pool(t, testdb.New(t))
	s.seed(t, "kopi-senja", "ceo@saga.co.id")
	return s
}

func (s *source) seed(t *testing.T, org, email string) {
	t.Helper()
	if _, err := server.Seed(context.Background(), s.db, s.reg, server.SeedOptions{Org: org, FixturesDir: s.fx, Email: email,
		Password: "rangka-demo", UserRecord: "USR-001", Now: fixedNow}); err != nil {
		t.Fatal(err)
	}
}

func (s *source) backup(t *testing.T, org string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "a.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := backup.Write(context.Background(), s.db, f, backup.Options{Org: org, SiteDir: s.site, AppsDir: s.apps, FilesDir: s.files, Version: version, Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if m.Format != 1 || m.Version != version || m.Org != org || !m.CreatedAt.Equal(fixedNow()) || len(m.Tables) == 0 {
		t.Fatalf("manifest = %+v", m)
	}
	return path
}

func restoreOpts(t *testing.T) backup.RestoreOptions {
	d := t.TempDir()
	return backup.RestoreOptions{Version: version, Into: filepath.Join(d, "r"), FilesDir: filepath.Join(d, "files"), Registry: loadRegistry}
}

func count(t *testing.T, db *pg.Pool, sql string) int64 {
	t.Helper()
	r, err := db.Query(context.Background(), sql)
	if err != nil {
		t.Fatal(err)
	}
	return r.Rows[0][0].(int64)
}

func TestBackupRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newSource(t)
	// an S6 attachment: a row in rk_attachments and its content-addressed blob in the files dir
	fs, err := files.NewLocal(s.files)
	if err != nil {
		t.Fatal(err)
	}
	blob := []byte("%PDF-1.4 bast\n")
	sum, size, err := fs.Put("kopi-senja", bytes.NewReader(blob))
	if err != nil {
		t.Fatal(err)
	}
	att := &store.Attachment{Org: "kopi-senja", Model: "deal", Record: "DEAL-00011", Filename: "bast.pdf",
		ContentType: "application/pdf", Size: size, SHA256: sum, CreatedBy: "ceo@saga.co.id"}
	if err := store.InsertAttachment(ctx, s.db, att); err != nil {
		t.Fatal(err)
	}
	archive := s.backup(t, "")

	dst := pool(t, testdb.New(t))
	o := restoreOpts(t)
	m, err := backup.Restore(ctx, dst, archive, o)
	if err != nil {
		t.Fatal(err)
	}
	a, err := backup.Sums(ctx, s.db, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := backup.Sums(ctx, dst, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("sums differ:\n%+v\n%+v", a, b)
	}
	if !reflect.DeepEqual(a, m.Tables) {
		t.Errorf("manifest tables differ from the source sums")
	}
	seen := 0
	for _, md := range s.reg.InstalledModels() {
		q := "select count(*) from " + pg.QuoteIdent(store.TableName(md.Name))
		n1, n2 := count(t, s.db, q), count(t, dst, q)
		if n1 != n2 {
			t.Errorf("%s: %d records, restored %d", md.Name, n1, n2)
		}
		if n1 > 0 {
			seen++
		}
	}
	if seen < 3 {
		t.Errorf("only %d models have records; the demo seed should fill more", seen)
	}
	want, _ := os.ReadFile(filepath.Join(s.files, "lead", "LEAD-001", "kartu nama.jpg"))
	got, err := os.ReadFile(filepath.Join(o.FilesDir, "lead", "LEAD-001", "kartu nama.jpg"))
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("file = %q, %v; want %q", got, err, want)
	}
	row, err := store.GetAttachment(ctx, dst, "kopi-senja", att.ID)
	if err != nil || row.SHA256 != sum || row.Record != "DEAL-00011" || row.Filename != "bast.pdf" {
		t.Errorf("restored attachment = %+v, %v", row, err)
	}
	rfs, err := files.NewLocal(o.FilesDir)
	if err != nil {
		t.Fatal(err)
	}
	if f, err := rfs.Open("kopi-senja", sum); err != nil {
		t.Errorf("restored blob: %v", err)
	} else {
		got, _ := io.ReadAll(f)
		f.Close()
		if !bytes.Equal(got, blob) {
			t.Errorf("restored blob = %q, want %q", got, blob)
		}
	}
	if _, err := os.Stat(filepath.Join(o.Into, "site", "system.rk")); err != nil {
		t.Error("site not extracted:", err)
	}
	if _, err := os.Stat(filepath.Join(o.Into, "site", "data")); !os.IsNotExist(err) {
		t.Error("site data/ should not be in the archive")
	}
	if _, err := os.Stat(filepath.Join(o.Into, "apps", "crm")); err != nil {
		t.Error("apps not extracted:", err)
	}
	// sequences continue after the restored ids
	if _, err := dst.Exec(ctx, "insert into rk_audit (kind) values ('restore.test')"); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.Exec(ctx, "insert into rk_migrations (statements) values ('{}')"); err != nil {
		t.Fatal(err)
	}
}

// TestFilesInsideSite: a files dir inside the site (not under data/) is archived once, under files/.
func TestFilesInsideSite(t *testing.T) {
	ctx := context.Background()
	s := newSource(t)
	up := filepath.Join(s.site, "uploads")
	os.MkdirAll(up, 0o755)
	os.WriteFile(filepath.Join(up, "a.txt"), []byte("a"), 0o644)
	var buf bytes.Buffer
	m, err := backup.Write(ctx, s.db, &buf, backup.Options{SiteDir: s.site, AppsDir: s.apps, FilesDir: up, Version: version, Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range m.Files {
		if strings.HasSuffix(f.Path, "/a.txt") {
			paths = append(paths, f.Path)
		}
	}
	if !reflect.DeepEqual(paths, []string{"files/a.txt"}) {
		t.Errorf("a.txt archived as %v", paths)
	}
}

func TestRestoreRefusesNonEmpty(t *testing.T) {
	ctx := context.Background()
	s := newSource(t)
	archive := s.backup(t, "")
	dst := pool(t, testdb.New(t))
	if _, err := dst.Exec(ctx, "create table something (a int)"); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Restore(ctx, dst, archive, restoreOpts(t)); !errors.Is(err, backup.ErrNotEmpty) {
		t.Fatalf("err = %v, want ErrNotEmpty", err)
	}
	if _, err := backup.Restore(ctx, s.db, archive, restoreOpts(t)); !errors.Is(err, backup.ErrNotEmpty) {
		t.Fatalf("restore into the source: err = %v, want ErrNotEmpty", err)
	}
	// a non-empty --files dir is refused before the database is touched
	empty := pool(t, testdb.New(t))
	o := restoreOpts(t)
	os.MkdirAll(o.FilesDir, 0o755)
	os.WriteFile(filepath.Join(o.FilesDir, "old.txt"), []byte("old"), 0o644)
	if _, err := backup.Restore(ctx, empty, archive, o); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("non-empty files dir: err = %v", err)
	}
	if n := tableCount(t, empty); n != 0 {
		t.Fatalf("target has %d tables after a refused restore", n)
	}
	if n := count(t, dst, "select count(*) from pg_tables where schemaname = 'public'"); n != 1 {
		t.Fatalf("the non-empty target has %d tables, want its 1", n)
	}
}

// rewrite copies a tar.gz archive, passing each entry through fn (nil drops it).
func rewrite(t *testing.T, src string, fn func(name string, data []byte) []byte) string {
	t.Helper()
	return rewriteExtra(t, src, fn)
}

// rewriteExtra is rewrite plus extra entries appended at the end (each with the body "x").
func rewriteExtra(t *testing.T, src string, fn func(name string, data []byte) []byte, extra ...*tar.Header) string {
	t.Helper()
	f, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	tw := tar.NewWriter(zw)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		data = fn(h.Name, data)
		if data == nil {
			continue
		}
		h.Size = int64(len(data))
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		tw.Write(data)
	}
	for _, h := range extra {
		if h.Typeflag == tar.TypeReg {
			h.Size = 1
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte("x"))
		}
	}
	tw.Close()
	zw.Close()
	path := filepath.Join(t.TempDir(), "b.tar.gz")
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func tableCount(t *testing.T, db *pg.Pool) int64 {
	return count(t, db, "select count(*) from pg_tables where schemaname = 'public'")
}

func TestRestoreRefusesCorrupt(t *testing.T) {
	ctx := context.Background()
	s := newSource(t)
	archive := s.backup(t, "")
	flipped := false
	cases := map[string]string{
		"flipped byte": rewrite(t, archive, func(name string, data []byte) []byte {
			if !flipped && strings.HasPrefix(name, "data/") && strings.HasSuffix(name, ".copy") && len(data) > 10 {
				flipped = true
				data[len(data)/2] ^= 0x01
			}
			return data
		}),
		"missing table": rewrite(t, archive, func(name string, data []byte) []byte {
			if name == "data/rk_orgs.copy" {
				return nil
			}
			return data
		}),
		"changed file": rewrite(t, archive, func(name string, data []byte) []byte {
			if strings.HasPrefix(name, "files/") {
				return append(data, '!')
			}
			return data
		}),
	}
	raw, _ := os.ReadFile(archive)
	trunc := filepath.Join(t.TempDir(), "t.tar.gz")
	os.WriteFile(trunc, raw[:len(raw)/2], 0o644)
	cases["truncated gzip"] = trunc
	if !flipped {
		t.Fatal("no data entry to corrupt")
	}
	dst := pool(t, testdb.New(t))
	for name, path := range cases {
		o := restoreOpts(t)
		if _, err := backup.Restore(ctx, dst, path, o); !errors.Is(err, backup.ErrCorrupt) {
			t.Errorf("%s: err = %v, want ErrCorrupt", name, err)
		}
		if n := tableCount(t, dst); n != 0 {
			t.Fatalf("%s: target has %d tables after a refused restore", name, n)
		}
		if _, err := os.Stat(o.Into); !os.IsNotExist(err) {
			t.Errorf("%s: --into dir created on a refused restore", name)
		}
	}
}

func TestRestoreRefusesBadEntries(t *testing.T) {
	ctx := context.Background()
	s := newSource(t)
	archive := s.backup(t, "")
	keep := func(_ string, data []byte) []byte { return data }
	reg := func(name string) *tar.Header { return &tar.Header{Name: name, Mode: 0o644, Typeflag: tar.TypeReg} }
	cases := map[string]string{
		"dotdot":       rewriteExtra(t, archive, keep, reg("../x")),
		"inner dotdot": rewriteExtra(t, archive, keep, reg("files/../../x")),
		"backslash":    rewriteExtra(t, archive, keep, reg(`..\x`)),
		"drive":        rewriteExtra(t, archive, keep, reg("C:/x")),
		"absolute":     rewriteExtra(t, archive, keep, reg("/tmp/x")),
		"symlink":      rewriteExtra(t, archive, keep, &tar.Header{Name: "files/l", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink}),
		"hardlink":     rewriteExtra(t, archive, keep, &tar.Header{Name: "files/h", Linkname: "manifest.json", Typeflag: tar.TypeLink}),
		"unlisted":     rewriteExtra(t, archive, keep, reg("site/extra.rk")),
		"bad type": rewrite(t, archive, func(name string, data []byte) []byte {
			if name == "manifest.json" {
				return bytes.Replace(data, []byte(`"type": "text"`), []byte(`"type": "text; drop table x"`), 1)
			}
			return data
		}),
	}
	for name, path := range cases {
		dst := pool(t, testdb.New(t))
		if _, err := backup.Restore(ctx, dst, path, restoreOpts(t)); !errors.Is(err, backup.ErrCorrupt) {
			t.Errorf("%s: err = %v, want ErrCorrupt", name, err)
		}
		if n := tableCount(t, dst); n != 0 {
			t.Errorf("%s: target has %d tables after a refused restore", name, n)
		}
	}
}

// TestSymlinks: roots given through symlinks are walked at their targets, a symlink to a file inside a tree is
// archived as the file, a symlink to a directory fails the backup. rangka.lock at the site root is archived.
func TestSymlinks(t *testing.T) {
	ctx := context.Background()
	s := newSource(t)
	os.WriteFile(filepath.Join(s.site, "rangka.lock"), []byte("lock: 1\n"), 0o644)
	if err := os.Symlink(filepath.Join(s.files, "lead", "LEAD-001", "kartu nama.jpg"), filepath.Join(s.files, "linked.jpg")); err != nil {
		t.Fatal(err)
	}
	links := t.TempDir()
	for name, target := range map[string]string{"site": s.site, "apps": s.apps, "files": s.files} {
		if err := os.Symlink(target, filepath.Join(links, name)); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "a.tar.gz")
	f, _ := os.Create(path)
	m, err := backup.Write(ctx, s.db, f, backup.Options{SiteDir: filepath.Join(links, "site"), AppsDir: filepath.Join(links, "apps"),
		FilesDir: filepath.Join(links, "files"), Version: version, Now: fixedNow})
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, fs := range m.Files {
		got[fs.Path] = true
	}
	for _, p := range []string{"site/system.rk", "site/rangka.lock", "apps/crm/models/lead.rk", "files/lead/LEAD-001/kartu nama.jpg", "files/linked.jpg"} {
		if !got[p] {
			t.Errorf("%s not archived", p)
		}
	}
	o := restoreOpts(t)
	if _, err := backup.Restore(ctx, pool(t, testdb.New(t)), path, o); err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(filepath.Join(s.files, "lead", "LEAD-001", "kartu nama.jpg"))
	for _, p := range []string{"linked.jpg", "lead/LEAD-001/kartu nama.jpg"} {
		if b, err := os.ReadFile(filepath.Join(o.FilesDir, p)); err != nil || !bytes.Equal(b, want) {
			t.Errorf("restored %s = %q, %v", p, b, err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(o.Into, "site", "rangka.lock")); err != nil || string(b) != "lock: 1\n" {
		t.Errorf("rangka.lock = %q, %v", b, err)
	}
	// a directory symlink inside the files tree is refused, naming the path
	if err := os.Symlink(s.files, filepath.Join(s.files, "loop")); err != nil {
		t.Fatal(err)
	}
	_, err = backup.Write(ctx, s.db, io.Discard, backup.Options{SiteDir: s.site, AppsDir: s.apps, FilesDir: filepath.Join(links, "files"), Version: version, Now: fixedNow})
	if err == nil || !strings.Contains(err.Error(), "loop") {
		t.Fatalf("dir symlink: err = %v", err)
	}
}

// TestRestoreAddsArchivedColumns: a column and a soft-deleted table the models no longer have are recreated
// from the manifest and keep their rows.
func TestRestoreAddsArchivedColumns(t *testing.T) {
	ctx := context.Background()
	s := newSource(t)
	if _, err := s.db.Exec(ctx, `alter table r_lead add column x_legacy numeric(18,2);
		update r_lead set x_legacy = 12.5;
		create table _del_20260101_old (name text, tags text[], at timestamp with time zone);
		insert into _del_20260101_old values ('a', '{x,y}', '2026-01-01 07:00+07');
		create table _del_20260101_r_gone (org_id text not null, name text not null);
		insert into _del_20260101_r_gone values ('kopi-senja', 'G-1')`); err != nil {
		t.Fatal(err)
	}
	archive := s.backup(t, "")
	dst := pool(t, testdb.New(t))
	if _, err := backup.Restore(ctx, dst, archive, restoreOpts(t)); err != nil {
		t.Fatal(err)
	}
	// an archived model table gets the org policy back (contract 0010 §4); a table without org_id does not
	r, err := dst.Query(ctx, `select relname, relrowsecurity and relforcerowsecurity from pg_class
		where relname in ('_del_20260101_r_gone', '_del_20260101_old') order by relname`)
	if err != nil || len(r.Rows) != 2 || r.Rows[0][1] != false || r.Rows[1][1] != true {
		t.Fatalf("row-level security on archived tables: %v %v", r, err)
	}
	a, _ := backup.Sums(ctx, s.db, "")
	b, _ := backup.Sums(ctx, dst, "")
	if !reflect.DeepEqual(a, b) {
		t.Error("sums differ")
	}
}

func TestRestoreRefusesNewer(t *testing.T) {
	ctx := context.Background()
	s := newSource(t)
	archive := s.backup(t, "")
	newer := rewrite(t, archive, func(name string, data []byte) []byte {
		if name == "manifest.json" {
			return bytes.Replace(data, []byte(`"`+version+`"`), []byte(`"99.0.0"`), 1)
		}
		return data
	})
	dst := pool(t, testdb.New(t))
	o := restoreOpts(t)
	if _, err := backup.Restore(ctx, dst, newer, o); !errors.Is(err, backup.ErrNewerArchive) {
		t.Fatalf("err = %v, want ErrNewerArchive", err)
	}
	if n := tableCount(t, dst); n != 0 {
		t.Fatalf("target has %d tables after a refused restore", n)
	}
	// a dev binary skips the check
	o.Version = "dev"
	if _, err := backup.Restore(ctx, dst, newer, o); err != nil {
		t.Fatalf("dev binary: %v", err)
	}
}

func TestOrgBackup(t *testing.T) {
	ctx := context.Background()
	s := newSource(t)
	s.seed(t, "toko-lain", "pemilik@toko-lain.test")
	if n := count(t, s.db, "select count(*) from r_lead where org_id = 'toko-lain'"); n == 0 {
		t.Fatal("second org has no leads")
	}
	// an attachment in each org, and the installation's OIDC signing key (rk_keys)
	own := s.attach(t, "kopi-senja", "DEAL-00011", []byte("%PDF-1.4 kopi-senja\n"))
	other := s.attach(t, "toko-lain", "DEAL-00011", []byte("%PDF-1.4 toko-lain\n"))
	if _, err := (&auth.Service{DB: s.db}).SignJWT(ctx, map[string]any{"sub": "x"}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s.db, "select count(*) from rk_keys"); n != 1 {
		t.Fatalf("source rk_keys rows = %d", n)
	}
	archive := s.backup(t, "kopi-senja")

	dst := pool(t, testdb.New(t))
	o := restoreOpts(t)
	m, err := backup.Restore(ctx, dst, archive, o)
	if err != nil {
		t.Fatal(err)
	}
	// the tenant's plan and AI credits (contract 0028) belong to the installation, not to one org
	if want := []string{"rk_ai_usage", "rk_keys", "rk_plan_changes", "rk_site_plan"}; !reflect.DeepEqual(m.Excluded, want) {
		t.Errorf("excluded = %v, want %v", m.Excluded, want)
	}
	for _, ts := range m.Tables {
		if ts.Name == "rk_keys" {
			t.Error("rk_keys is in the org archive")
		}
	}
	for _, f := range m.Files {
		if strings.HasPrefix(f.Path, "files/") && !strings.HasPrefix(f.Path, "files/kopi-senja/") {
			t.Errorf("org archive holds %s", f.Path)
		}
	}
	if _, err := os.Stat(filepath.Join(o.FilesDir, "toko-lain")); !os.IsNotExist(err) {
		t.Error("the other org's files were restored")
	}
	rfs, _ := files.NewLocal(o.FilesDir)
	if f, err := rfs.Open("kopi-senja", own.SHA256); err != nil {
		t.Errorf("own blob not restored: %v", err)
	} else {
		f.Close()
	}
	if _, err := rfs.Open("toko-lain", other.SHA256); err == nil {
		t.Error("the other org's blob was restored")
	}
	// the restored kernel has no signing key and makes a fresh one on first use, as on a first start
	if n := count(t, dst, "select count(*) from rk_keys"); n != 0 {
		t.Errorf("rk_keys restored: %d rows", n)
	}
	if _, err := (&auth.Service{DB: dst}).SignJWT(ctx, map[string]any{"sub": "x"}); err != nil {
		t.Fatalf("fresh signing key: %v", err)
	}
	srcKid, _ := s.db.Query(ctx, "select kid from rk_keys")
	dstKid, _ := dst.Query(ctx, "select kid from rk_keys")
	if len(dstKid.Rows) != 1 || reflect.DeepEqual(srcKid.Rows, dstKid.Rows) {
		t.Errorf("restored kernel keys %v, source %v: want one fresh key", dstKid.Rows, srcKid.Rows)
	}
	r, err := dst.Query(ctx, `select c.relname from pg_class c join pg_attribute a on a.attrelid = c.oid
		where c.relnamespace = 'public'::regnamespace and c.relkind = 'r' and a.attname = 'org_id' and not a.attisdropped order by 1`)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range r.Rows {
		tbl := pg.QuoteIdent(row[0].(string))
		if n := count(t, dst, "select count(*) from "+tbl+" where org_id = 'toko-lain'"); n != 0 {
			t.Errorf("%s: %d rows of the other org restored", tbl, n)
		}
		q := "select count(*) from " + tbl + " where org_id = 'kopi-senja'"
		if n1, n2 := count(t, s.db, q), count(t, dst, q); n1 != n2 {
			t.Errorf("%s: %d kopi-senja rows, restored %d", tbl, n1, n2)
		}
	}
	if n := count(t, dst, "select count(*) from rk_orgs where id <> 'kopi-senja'"); n != 0 {
		t.Errorf("%d other orgs restored", n)
	}
	if n := count(t, dst, "select count(*) from rk_users where email = 'pemilik@toko-lain.test'"); n != 0 {
		t.Error("the other org's user was restored")
	}
	if n := count(t, dst, "select count(*) from rk_users where email = 'ceo@saga.co.id'"); n != 1 {
		t.Error("the org's own user is missing")
	}
	a, _ := backup.Sums(ctx, s.db, "kopi-senja")
	b, _ := backup.Sums(ctx, dst, "kopi-senja")
	if !reflect.DeepEqual(a, b) {
		t.Error("org sums differ")
	}
}

// TestFilesDir: resolved like rangka serve (contract 0011 §1): override, else system.rk files.path, else
// ./data/files; a relative path stays relative to the working directory, never to the site dir.
func TestFilesDir(t *testing.T) {
	dir := t.TempDir()
	site := filepath.Join(dir, "site")
	os.MkdirAll(site, 0o755)
	apps, _ := filepath.Abs(testdb.Catalogue("apps"))
	reg, err := loadRegistry(backup.Dirs{Apps: apps, Site: site})
	if err != nil {
		t.Fatal(err)
	}
	if got := backup.FilesDir(reg, "/srv/uploads"); got != "/srv/uploads" {
		t.Errorf("override: got %q", got)
	}
	if got := backup.FilesDir(reg, ""); got != "data/files" {
		t.Errorf("default: got %q, want data/files", got)
	}
	if got := backup.FilesDir(nil, ""); got != "data/files" {
		t.Errorf("no registry: got %q, want data/files", got)
	}
	os.WriteFile(filepath.Join(site, "system.rk"), []byte("# yaml-language-server: $schema=https://rangka-os.github.io/rangka-os/schema/0.4/system.json\n"+
		"db: {driver: postgres, url: '${DATABASE_URL}'}\nfiles: {driver: local, path: uploads}\n"), 0o644)
	if reg, err = loadRegistry(backup.Dirs{Apps: apps, Site: site}); err != nil {
		t.Fatal(err)
	}
	if got := backup.FilesDir(reg, ""); got != "uploads" {
		t.Errorf("files.path: got %q, want uploads (relative to the working directory)", got)
	}
}

// attach stores blob for org in the source's files dir and inserts its rk_attachments row.
func (s *source) attach(t *testing.T, org, record string, blob []byte) *store.Attachment {
	t.Helper()
	fs, err := files.NewLocal(s.files)
	if err != nil {
		t.Fatal(err)
	}
	sum, size, err := fs.Put(org, bytes.NewReader(blob))
	if err != nil {
		t.Fatal(err)
	}
	att := &store.Attachment{Org: org, Model: "deal", Record: record, Filename: "bast.pdf",
		ContentType: "application/pdf", Size: size, SHA256: sum, CreatedBy: "ceo@saga.co.id"}
	if err := store.InsertAttachment(context.Background(), s.db, att); err != nil {
		t.Fatal(err)
	}
	return att
}

// TestBackupMissingBlobs: an attachment row without its blob fails the backup instead of writing an archive
// that restores "verified" without the file, also when the files dir does not exist at all.
func TestBackupMissingBlobs(t *testing.T) {
	ctx := context.Background()
	s := newSource(t)
	att := s.attach(t, "kopi-senja", "DEAL-00011", []byte("%PDF-1.4 bast\n"))
	write := func(org, dir string) error {
		_, err := backup.Write(ctx, s.db, io.Discard, backup.Options{Org: org, SiteDir: s.site, AppsDir: s.apps, FilesDir: dir, Version: version, Now: fixedNow})
		return err
	}
	if err := write("", s.files); err != nil {
		t.Fatalf("with the blob: %v", err)
	}
	// the wrong (missing) files dir, for the whole database and for the org
	gone := filepath.Join(t.TempDir(), "nowhere")
	for _, org := range []string{"", "kopi-senja"} {
		if err := write(org, gone); !errors.Is(err, backup.ErrMissingBlobs) || !strings.Contains(err.Error(), "does not exist") {
			t.Errorf("org %q, missing files dir: err = %v, want ErrMissingBlobs naming the missing dir", org, err)
		}
	}
	// the blob deleted from an existing files dir
	fs, _ := files.NewLocal(s.files)
	if err := fs.Delete("kopi-senja", att.SHA256); err != nil {
		t.Fatal(err)
	}
	if err := write("", s.files); !errors.Is(err, backup.ErrMissingBlobs) || !strings.Contains(err.Error(), att.SHA256) {
		t.Errorf("missing blob: err = %v, want ErrMissingBlobs naming the blob", err)
	}
}

// TestRestoreMissingBlob: restore verification fails when a restored attachment row has no blob, even when
// the archive's own checksums all match (an archive written without the blob).
func TestRestoreMissingBlob(t *testing.T) {
	ctx := context.Background()
	s := newSource(t)
	att := s.attach(t, "kopi-senja", "DEAL-00011", []byte("%PDF-1.4 bast\n"))
	archive := s.backup(t, "")
	blobPath := "files/kopi-senja/" + att.SHA256[:2] + "/" + att.SHA256
	dropped := false
	stripped := rewrite(t, archive, func(name string, data []byte) []byte {
		switch name {
		case blobPath:
			dropped = true
			return nil
		case "manifest.json":
			var m backup.Manifest
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			var keep []backup.FileSum
			for _, f := range m.Files {
				if f.Path != blobPath {
					keep = append(keep, f)
				}
			}
			m.Files = keep
			out, _ := json.Marshal(m)
			return out
		}
		return data
	})
	if !dropped {
		t.Fatalf("archive has no %s", blobPath)
	}
	_, err := backup.Restore(ctx, pool(t, testdb.New(t)), stripped, restoreOpts(t))
	if !errors.Is(err, backup.ErrVerify) || !strings.Contains(err.Error(), att.SHA256) {
		t.Fatalf("err = %v, want ErrVerify naming the blob", err)
	}
}

// ownerPool is a fresh empty database owned by, and connected as, a NOSUPERUSER NOBYPASSRLS role.
func ownerPool(t *testing.T) *pg.Pool {
	t.Helper()
	ctx := context.Background()
	admin := pool(t, os.Getenv("RANGKA_TEST_DATABASE_URL"))
	role := "rk_owner_" + strings.ToLower(store.RandomName(8))
	if _, err := admin.Exec(ctx, "create role "+role+" login nosuperuser nobypassrls"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec(context.Background(), "drop role if exists "+role) })
	u, err := url.Parse(testdb.New(t)) // dropped before the role (cleanups run last-in first-out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "alter database "+pg.QuoteIdent(strings.TrimPrefix(u.Path, "/"))+" owner to "+role); err != nil {
		t.Fatal(err)
	}
	u.User = url.User(role)
	return pool(t, u.String())
}

// A hosted install connects as the database owner, a role RLS applies to (NOSUPERUSER NOBYPASSRLS, contract
// 0010 §4). Postgres refuses COPY FROM into a table whose RLS binds the caller, so restore must still load
// every row as that role and leave RLS forced afterwards.
func TestRestoreAsTableOwner(t *testing.T) {
	ctx := context.Background()
	s := newSource(t)
	archive := s.backup(t, "")

	dst := ownerPool(t)
	if _, err := backup.Restore(ctx, dst, archive, restoreOpts(t)); err != nil {
		t.Fatalf("restore as the owner role: %v", err)
	}
	a, err := backup.Sums(ctx, s.db, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := backup.Sums(ctx, dst, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("sums differ after restoring as the owner role")
	}
	if n := count(t, dst, `select count(*) from pg_class where relnamespace = 'public'::regnamespace
		and relkind = 'r' and relrowsecurity and not relforcerowsecurity`); n != 0 {
		t.Errorf("%d tables lost forced row-level security", n)
	}
	if n := count(t, dst, `select count(*) from pg_class where relnamespace = 'public'::regnamespace
		and relkind = 'r' and relforcerowsecurity`); n == 0 {
		t.Errorf("no table has forced row-level security; the test does not exercise RLS")
	}
}

// The contract 0001 layout: models live in modules/ next to apps/. The archive must carry modules/ (and
// connectors/), and restore must rebuild from them alone, as the owner role a hosted install runs as.
func TestModulesAppsRoundTripAsOwner(t *testing.T) {
	ctx := context.Background()
	auth.BcryptCost = 4
	dir := t.TempDir()
	if err := exec.Command("cp", "-r", filepath.Join(testdb.RepoRoot(), "kernel", "testdata", "modules-apps")+"/.", dir).Run(); err != nil {
		t.Fatal(err)
	}
	conn := filepath.Join(dir, "connectors", "probe")
	if err := os.MkdirAll(conn, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conn, "README.md"), []byte("not a connector, only an archived file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := backup.Dirs{Apps: filepath.Join(dir, "apps"), Site: filepath.Join(dir, "site"), Modules: filepath.Join(dir, "modules")}
	reg, err := loadRegistry(d)
	if err != nil {
		t.Fatal(err)
	}
	src := pool(t, testdb.New(t))
	if _, err := server.Seed(ctx, src, reg, server.SeedOptions{Org: "demo", FixturesDir: filepath.Join(dir, "fixtures"),
		Email: "ceo@saga.co.id", Password: "rangka-demo", UserRecord: "USR-001", Now: fixedNow}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "a.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := backup.Write(ctx, src, f, backup.Options{SiteDir: d.Site, AppsDir: d.Apps, ModulesDir: d.Modules,
		ConnectorsDir: filepath.Join(dir, "connectors"), FilesDir: filepath.Join(dir, "files"), Version: version, Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	have := map[string]bool{}
	for _, fs := range m.Files {
		have[fs.Path] = true
	}
	for _, p := range []string{"modules/pipeline/module.rk", "modules/contacts/module.rk", "apps/crm/app.rk", "site/config.rk", "connectors/probe/README.md"} {
		if !have[p] {
			t.Errorf("%s is not in the archive manifest", p)
		}
	}

	dst := ownerPool(t)
	o := restoreOpts(t)
	if _, err := backup.Restore(ctx, dst, path, o); err != nil {
		t.Fatalf("restore a modules + apps install as the owner role: %v", err)
	}
	a, err := backup.Sums(ctx, src, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := backup.Sums(ctx, dst, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("sums differ after restoring a modules + apps install")
	}
	if n := count(t, dst, "select count(*) from r_deal"); n == 0 || n != count(t, src, "select count(*) from r_deal") {
		t.Errorf("r_deal (a module model): restored %d rows", n)
	}
	for _, p := range []string{"modules/pipeline/models/deal.rk", "connectors/probe/README.md"} {
		if _, err := os.Stat(filepath.Join(o.Into, filepath.FromSlash(p))); err != nil {
			t.Errorf("%s was not restored: %v", p, err)
		}
	}
}

// RANGKA_CONNECTORS or the modules dir may name a folder that does not exist (serve and the loader tolerate
// it). Backup skips such a folder and lists it as absent; restore rebuilds without it. Missing apps/ is still
// an error: nothing loads without it.
func TestMissingOptionalDirs(t *testing.T) {
	ctx := context.Background()
	s := newSource(t)
	gone := filepath.Join(t.TempDir(), "does-not-exist")
	path := filepath.Join(t.TempDir(), "a.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	m, err := backup.Write(ctx, s.db, f, backup.Options{SiteDir: s.site, AppsDir: s.apps, ModulesDir: gone + "/modules",
		ConnectorsDir: gone + "/connectors", FilesDir: s.files, Version: version, Now: fixedNow})
	if err != nil {
		t.Fatalf("backup with missing modules and connectors dirs: %v", err)
	}
	f.Close()
	if !reflect.DeepEqual(m.Absent, []string{"modules", "connectors"}) {
		t.Errorf("absent = %v, want [modules connectors]", m.Absent)
	}
	o := restoreOpts(t)
	if _, err := backup.Restore(ctx, pool(t, testdb.New(t)), path, o); err != nil {
		t.Fatalf("restore an archive without modules and connectors: %v", err)
	}
	for _, d := range []string{"modules", "connectors"} {
		if _, err := os.Stat(filepath.Join(o.Into, d)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s/ should not be created by restore: %v", d, err)
		}
	}

	_, err = backup.Write(ctx, s.db, io.Discard, backup.Options{SiteDir: s.site, AppsDir: gone + "/apps", FilesDir: s.files, Version: version, Now: fixedNow})
	if err == nil {
		t.Errorf("backup with a missing apps dir should fail")
	}
}
