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
	files := fs.String("files", "", "uploads directory (default: system.rk files.path resolved against the site dir)")
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
	if *files == "" && c.site != "" {
		reg, err := c.registry()
		if err != nil {
			fmt.Fprintln(os.Stderr, "rangka: warning: metadata did not load, using the default files dir:", err)
		}
		*files = backup.FilesDir(reg, c.site)
	}
	if *files == "" {
		fmt.Fprintln(os.Stderr, "rangka: warning: no --site or --files given, uploaded files are not backed up")
	}
	db, err := c.pool(ctx)
	if err != nil {
		return fail(err)
	}
	defer db.Close()
	// write next to the target and rename, so a failed run never leaves a complete-looking archive
	tmp := *out + ".partial"
	f, err := os.Create(tmp)
	if err != nil {
		return fail(err)
	}
	m, err := backup.Write(ctx, db, f, backup.Options{Org: *org, SiteDir: c.site, AppsDir: c.apps, FilesDir: *files, Version: Version,
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
	into := fs.String("into", "", "directory for the archived site/ and apps/ (default ./restore-<archive name>)")
	files := fs.String("files", "", "uploads directory (default: system.rk files.path resolved against <into>/site)")
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
		Registry: func(apps, site string) (*meta.Registry, error) {
			rc := c
			rc.apps, rc.site = apps, site
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
