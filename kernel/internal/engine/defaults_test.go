package engine

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/store"
)

// Contract 0076: a module's defaults fill an org's empty tables of its own models, once.
func TestModuleDefaultsFillEmptyTablesOfTheirOwnModels(t *testing.T) {
	e, a := setup(t)
	ctx := context.Background()
	reg := e.Reg()
	// the v0 test catalogue: app core owns outlet, app crm owns lead
	cat := reg.App[reg.Models["outlet"].App]
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "defaults"), 0o755)
	os.WriteFile(filepath.Join(dir, "defaults", "outlet.rk"), []byte("outlet:\n  - {outlet_name: Pusat}\n  - {outlet_name: Gudang}\n"), 0o644)
	// crm owns lead: core's defaults may not fill it
	os.WriteFile(filepath.Join(dir, "defaults", "other.rk"), []byte("lead:\n  - {lead_name: Calon umum}\n"), 0o644)
	orig := cat.Dir
	cat.Dir = dir
	t.Cleanup(func() { cat.Dir = orig })

	fx, skipped, err := ModuleDefaults(reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(fx["outlet"]) != 2 || fx["lead"] != nil || !reflect.DeepEqual(skipped, []string{cat.Name + "/lead"}) {
		t.Fatalf("defaults %v skipped %v", fx, skipped)
	}

	// the seeded org already has outlets (fixtures): nothing is added to them
	before, _ := store.Count(ctx, e.DB, a.Org, "outlet")
	st, err := e.SeedDefaults(ctx, a.Org)
	if err != nil {
		t.Fatal(err)
	}
	if after, _ := store.Count(ctx, e.DB, a.Org, "outlet"); before == 0 || after != before || st.Created["outlet"] != 0 {
		t.Fatalf("an org with outlets got defaults: %d -> %d, %v", before, after, st.Created)
	}

	// a new org starts empty: it gets both outlets, numbered by the model's naming, and only once
	st, err = e.SeedDefaults(ctx, "baru")
	if err != nil {
		t.Fatal(err)
	}
	if st.Created["outlet"] != 2 {
		t.Fatalf("created %v", st.Created)
	}
	if n, _ := store.Count(ctx, e.DB, "baru", "lead"); n != 0 {
		t.Fatalf("another unit's model was filled: %d leads", n)
	}
	st, err = e.SeedDefaults(ctx, "baru")
	if err != nil || st.Created["outlet"] != 0 {
		t.Fatalf("second run added %v (%v)", st.Created, err)
	}
	if n, _ := store.Count(ctx, e.DB, "baru", "outlet"); n != 2 {
		t.Fatalf("outlets after two runs: %d", n)
	}
}

// A defaults file that does not load is an error for that module (the server logs it and goes on).
func TestModuleDefaultsBrokenFile(t *testing.T) {
	e, _ := setup(t)
	cat := e.Reg().App[e.Reg().Models["outlet"].App]
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "defaults"), 0o755)
	os.WriteFile(filepath.Join(dir, "defaults", "outlet.rk"), []byte("outlet: [\n"), 0o644)
	orig := cat.Dir
	cat.Dir = dir
	t.Cleanup(func() { cat.Dir = orig })
	if _, _, err := ModuleDefaults(e.Reg()); err == nil {
		t.Fatal("a broken defaults file loaded")
	}
}
