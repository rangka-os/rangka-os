package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
)

type common struct {
	apps, site, db, schemas, connectors string
}

func (c *common) flags(fs *flag.FlagSet) {
	fs.StringVar(&c.apps, "apps", envOr("RANGKA_APPS", "apps"), "apps directory")
	fs.StringVar(&c.site, "site", os.Getenv("RANGKA_SITE"), "site directory")
	fs.StringVar(&c.db, "db", os.Getenv("DATABASE_URL"), "Postgres URL (default $DATABASE_URL)")
	fs.StringVar(&c.schemas, "schemas", "", "schema directory (default: built in)")
	fs.StringVar(&c.connectors, "connectors", os.Getenv("RANGKA_CONNECTORS"), "connectors directory (default: rangka-connectors next to rangka-apps)")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// registry validates the metadata and refuses to continue on errors.
func (c *common) registry() (*meta.Registry, error) {
	set, err := loadSchemas(c.schemas)
	if err != nil {
		return nil, err
	}
	reg, rep := meta.ValidateOpts(meta.Options{AppsDir: c.apps, SiteDir: c.site, ConnectorsDir: c.connectorsDir()}, nil, set)
	if rep.Errors() > 0 {
		rep.Print(os.Stderr)
		return nil, fmt.Errorf("metadata has errors; fix them first (rangka validate)")
	}
	return reg, nil
}

func (c *common) pool(ctx context.Context) (*pg.Pool, error) {
	if c.db == "" {
		return nil, fmt.Errorf("no database: pass --db or set DATABASE_URL")
	}
	size := 10
	return pg.NewPool(ctx, c.db, size)
}

// orgID is the organisation a site serves: the site directory name (kopi-senja).
func (c *common) orgID(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if c.site != "" {
		return filepath.Base(filepath.Clean(c.site))
	}
	return "default"
}

func titleCase(id string) string {
	parts := strings.FieldsFunc(id, func(r rune) bool { return r == '-' || r == '_' })
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "rangka:", err)
	return 1
}

var errNoDB = fmt.Errorf("no database: pass --db or set DATABASE_URL")

// connectorsDir is --connectors, else rangka-connectors next to the apps repo when it exists.
func (c *common) connectorsDir() string {
	if c.connectors != "" {
		return c.connectors
	}
	d := filepath.Join(filepath.Dir(filepath.Dir(filepath.Clean(c.apps))), "rangka-connectors")
	if st, err := os.Stat(d); err == nil && st.IsDir() {
		return d
	}
	return ""
}
