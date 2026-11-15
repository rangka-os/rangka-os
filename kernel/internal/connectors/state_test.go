package connectors

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/jobs"
	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/pg"
	"github.com/rangka-os/rangka-os/kernel/internal/secrets"
	"github.com/rangka-os/rangka-os/kernel/internal/store"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// stateRT builds a Runtime over a temp copy of the x_demo site connector, with a database holding rk_connector_checks.
// tokens maps org -> access_token literal bound through system.rk connector_auth (env driver).
func stateRT(t *testing.T, tokens map[string]string) (*Runtime, string) {
	return stateRTEdit(t, tokens, nil)
}

// stateRTEdit is stateRT with edit applied to the copied connector.rk before the registry loads it.
func stateRTEdit(t *testing.T, tokens map[string]string, edit func(string) string) (*Runtime, string) {
	t.Helper()
	ctx := context.Background()
	db, err := pg.NewPool(ctx, testdb.New(t), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Exec(ctx, store.SystemSQL()); err != nil {
		t.Fatal(err)
	}
	site := t.TempDir()
	src := "../../testdata/company-connector/site"
	for _, f := range []string{"config.rk", "connectors/x_demo/connector.rk"} {
		b, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatal(err)
		}
		if edit != nil && strings.HasSuffix(f, "connector.rk") {
			b = []byte(edit(string(b)))
		}
		dst := filepath.Join(site, f)
		os.MkdirAll(filepath.Dir(dst), 0o755)
		os.WriteFile(dst, b, 0o644)
	}
	reg := meta.LoadOpts(meta.Options{AppsDir: "../../testdata/modules-apps/apps", SiteDir: site, ConnectorsDir: t.TempDir()}, nil)
	orgs := map[string]any{}
	for o, tok := range tokens {
		orgs[o] = map[string]any{"x_demo": map[string]any{"access_token": tok}}
	}
	if reg.Site == nil {
		t.Fatal("no site")
	}
	if reg.Site.System == nil {
		reg.Site.System = map[string]any{}
	}
	reg.Site.System["connector_auth"] = map[string]any{"orgs": orgs}
	get := func() *meta.Registry { return reg }
	rt := &Runtime{Resolver: Resolver{Reg: get, Secrets: &secrets.Service{Reg: get, DB: db}}, DB: db}
	return rt, reg.ConnectorPath("x_demo")
}

func TestStateCatalogue(t *testing.T) {
	rt, _ := stateRT(t, nil)
	st, h, w, err := rt.State(context.Background(), "o1", "moka")
	if err != nil || st != "tested" || h != "" || !w {
		t.Errorf("got %q %q %v %v", st, h, w, err)
	}
}

func TestStateNeedsAuth(t *testing.T) {
	rt, _ := stateRT(t, nil)
	st, h, w, err := rt.State(context.Background(), "o1", "x_demo")
	if err != nil || st != "needs_auth" || h == "" || w {
		t.Errorf("got %q %q %v %v", st, h, w, err)
	}
}

func TestStateUntestedNoRow(t *testing.T) {
	rt, _ := stateRT(t, map[string]string{"o1": "tok"})
	st, h, w, err := rt.State(context.Background(), "o1", "x_demo")
	if err != nil || st != "untested" || h == "" || w {
		t.Errorf("got %q %q %v %v", st, h, w, err)
	}
}

func TestCheckResetOnHashChange(t *testing.T) {
	ctx := context.Background()
	rt, path := stateRT(t, map[string]string{"o1": "tok"})
	_, h1, _, _ := rt.State(ctx, "o1", "x_demo")
	if err := store.SaveConnectorTest(ctx, rt.DB, "o1", "x_demo", h1, "owner", true, nil); err != nil {
		t.Fatal(err)
	}
	st, _, w, _ := rt.State(ctx, "o1", "x_demo")
	if st != "tested" || w {
		t.Fatalf("after test: %q %v", st, w)
	}
	if err := store.ConfirmConnectorWrites(ctx, rt.DB, "o1", "x_demo", h1, "owner"); err != nil {
		t.Fatal(err)
	}
	if st, _, w, _ = rt.State(ctx, "o1", "x_demo"); st != "tested" || !w {
		t.Fatalf("after confirm: %q %v", st, w)
	}
	b, _ := os.ReadFile(path)
	os.WriteFile(path, append(b, []byte("\n# edited\n")...), 0o644)
	st, h2, w, err := rt.State(ctx, "o1", "x_demo")
	if err != nil || st != "untested" || w || h2 == h1 {
		t.Errorf("after edit: %q %q %v %v", st, h2, w, err)
	}
}

func TestCheckIsPerOrg(t *testing.T) {
	ctx := context.Background()
	rt, _ := stateRT(t, map[string]string{"o1": "a", "o2": "b"})
	_, h, _, _ := rt.State(ctx, "o1", "x_demo")
	store.SaveConnectorTest(ctx, rt.DB, "o1", "x_demo", h, "owner", true, nil)
	if st, _, _, _ := rt.State(ctx, "o1", "x_demo"); st != "tested" {
		t.Errorf("o1 = %q", st)
	}
	if st, _, _, _ := rt.State(ctx, "o2", "x_demo"); st != "untested" {
		t.Errorf("o2 = %q", st)
	}
}

// Call refuses an untested company connector before rendering anything: permanent, coded connector_untested.
func TestCallRefusesUntested(t *testing.T) {
	rt, _ := stateRT(t, map[string]string{"o1": "tok"})
	_, err := rt.Call(context.Background(), "o1", "x_demo", "ping", map[string]any{"message": "halo"})
	var p *jobs.Permanent
	if !errors.As(err, &p) || !strings.Contains(err.Error(), "connector_untested") {
		t.Fatalf("got %v", err)
	}
}
