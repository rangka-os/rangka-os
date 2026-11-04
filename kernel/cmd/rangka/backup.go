package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rangka-os/rangka-os/kernel/internal/backup"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
)

// cmdBackup writes one verified archive: rangka backup [--out file] [--org id] [--files dir].
func cmdBackup(args []string) int {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	var c common
	c.flags(fs)
	out := fs.String("out", "", "archive path (default rangka-<org|all>-<YYYYMMDD-HHMMSS>.tar.gz)")
	org := fs.String("org", "", "back up one organisation (default: the whole database)")
	files := fs.String("files", envOr("RANGKA_FILES_DIR", ""), "uploads directory (default: $RANGKA_FILES_DIR, else system.rk files.path, else ./data/files; relative to the working directory, like serve)")
	fs.StringVar(files, "files-dir", envOr("RANGKA_FILES_DIR", ""), "same as --files (the flag name serve and migrate use)")
	fs.Parse(reorder(args))
	ctx := context.Background()
	now := time.Now()
	if *out == "" {
		who := *org
		if who == "" {
			who = "all"
		}
		*out = fmt.Sprintf("rangka-%s-%s.tar.gz", who, now.Format("20060102-150405"))
	}
	if *files == "" {
		var reg *meta.Registry
		if c.site != "" {
			var err error
			if reg, err = c.registry(); err != nil {
				fmt.Fprintln(os.Stderr, "rangka: warning: metadata did not load, using the default files dir:", err)
			}
		}
		*files = backup.FilesDir(reg, "")
	}
	db, err := c.pool(ctx)
	if err != nil {
		return fail(err)
	}
	defer db.Close()
	// write next to the target and rename, so a failed run never leaves a complete-looking archive
	// 0600: the archive holds password, token and session hashes and the OIDC signing key
	tmp := *out + ".partial"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err == nil {
		err = f.Chmod(0o600) // also when a stale .partial with wider permissions was there
	}
	if err != nil {
		if f != nil {
			f.Close()
			os.Remove(tmp)
		}
		return fail(err)
	}
	m, err := backup.Write(ctx, db, f, backup.Options{Org: *org, SiteDir: c.site, AppsDir: c.apps, ModulesDir: c.modulesDir(), ConnectorsDir: c.connectorsDir(),
		FilesDir: *files, Version: Version,
		Now: func() time.Time { return now }})
	if err == nil {
		err = f.Sync() // on disk before the rename makes it look complete
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, *out)
	}
	if err != nil {
		os.Remove(tmp)
		return fail(err)
	}
	var rows int64
	for _, t := range m.Tables {
		rows += t.Rows
	}
	fmt.Printf("backup %s: %d table(s), %d row(s), %d file(s)\n", *out, len(m.Tables), rows, len(m.Files))
	return 0
}

// cmdRestore rebuilds an archive into an empty database: rangka restore <file> --db <url> [--into dir] [--files dir].
func cmdRestore(args []string) int {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	var c common
	c.flags(fs)
	into := fs.String("into", "", "directory for the archived site/, apps/, modules/ and connectors/ (default ./restore-<archive name>)")
	files := fs.String("files", envOr("RANGKA_FILES_DIR", ""), "uploads directory (default: $RANGKA_FILES_DIR, else the archived system.rk files.path, else ./data/files; relative to the working directory)")
	fs.Parse(reorder(args))
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: rangka restore <file> --db <url> [--into dir] [--files dir]")
		return 2
	}
	archive := fs.Arg(0)
	if *into == "" {
		base := filepath.Base(archive)
		base = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(base, ".gz"), ".tgz"), ".tar")
		*into = "restore-" + base
	}
	ctx := context.Background()
	db, err := c.pool(ctx)
	if err != nil {
		return fail(err)
	}
	defer db.Close()
	m, err := backup.Restore(ctx, db, archive, backup.RestoreOptions{Version: Version, Into: *into, FilesDir: *files,
		Registry: func(d backup.Dirs) (*meta.Registry, error) {
			// only what the archive holds: never the host's modules or connectors next to --apps
			rc := c
			rc.apps, rc.site, rc.modules, rc.connectors = d.Apps, d.Site, d.Modules, d.Connectors
			return rc.registry()
		}})
	if err != nil {
		return fail(err)
	}
	var rows int64
	for _, t := range m.Tables {
		rows += t.Rows
	}
	org := m.Org
	if org == "" {
		org = "all orgs"
	}
	fmt.Printf("restored %s (%s, %s, rangka %s): %d table(s), %d row(s), %d file(s) into %s; verified\n",
		archive, org, m.CreatedAt.Format(time.RFC3339), m.Version, len(m.Tables), rows, len(m.Files), *into)
	return 0
}
