package server

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/auth"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// The host's display name (--org-name, $RANGKA_ORG_NAME; Rangka Cloud passes the business title) replaces the name
// the seed gave the org from its id, on every start; without it the seeded name stays.
func TestOrgNameSetOnStart(t *testing.T) {
	url := testdb.New(t)
	auth.BcryptCost = 4
	dir := t.TempDir()
	for _, p := range []string{testdb.Catalogue("apps"), testdb.Catalogue("fixtures"), testdb.Catalogue("sites")} {
		if err := exec.Command("cp", "-r", p, filepath.Join(dir, filepath.Base(p))).Run(); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Config{AppsDir: filepath.Join(dir, "apps"), SiteDir: filepath.Join(dir, "sites", "kopi-senja"), DeployTarget: "docker",
		DatabaseURL: url, Addr: ":0", Org: "mogu-s61i", Schemas: schema.Embedded(), Version: "test", Now: fixedNow,
		WebDir: filepath.Join(testdb.RepoRoot(), "web"), TemplatesDir: filepath.Join(testdb.RepoRoot(), "templates"),
		Logf: func(string, ...any) {}, FilesDir: t.TempDir()}
	ctx := context.Background()
	reg, err := LoadRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pg.NewPool(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := Seed(ctx, pool, reg, SeedOptions{Org: "mogu-s61i", FixturesDir: filepath.Join(dir, "fixtures"), Email: "ceo@saga.co.id",
		Password: "rangka-demo", UserRecord: "USR-001", Now: fixedNow}); err != nil {
		t.Fatal(err)
	}
	name := func() string {
		t.Helper()
		res, err := pool.Query(ctx, "select name from rk_orgs where id = 'mogu-s61i'")
		if err != nil || len(res.Rows) != 1 {
			t.Fatalf("rk_orgs: %v %v", res, err)
		}
		return res.Rows[0][0].(string)
	}
	start := func(orgName string) {
		t.Helper()
		c := cfg
		c.OrgName = orgName
		s, err := New(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
	if got := name(); got != "Mogu S61i" {
		t.Fatalf("seeded name %q, want the id title-cased", got)
	}
	start("")
	if got := name(); got != "Mogu S61i" {
		t.Fatalf("without --org-name the name changed to %q", got)
	}
	start("Mogu")
	if got := name(); got != "Mogu" {
		t.Fatalf("with --org-name Mogu the name is %q", got)
	}
	start("")
	if got := name(); got != "Mogu" {
		t.Fatalf("a start without --org-name reset the name to %q", got)
	}
}
