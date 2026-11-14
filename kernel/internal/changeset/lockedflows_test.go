package changeset

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/meta"
	"github.com/rangka-os/rangka-os/kernel/internal/schema"
)

// lockSite copies testdata/config-modules with a company flow that writes customer.city on create; with lock, the
// contacts module's customer.city is read_only_for sales_rep (as an upgrade of the module would make it).
func lockSite(t *testing.T, lock bool) *meta.Registry {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("cp", "-r", "../../testdata/config-modules/.", dir).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	flow := `name: isi_kota
label: {id: Isi kota}
on: customer.created
steps:
  - update: doc
    set: {city: "'Bandung'"}
`
	if err := os.MkdirAll(filepath.Join(dir, "site", "flows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "site", "flows", "isi_kota.rk"), []byte(flow), 0o644); err != nil {
		t.Fatal(err)
	}
	if lock {
		p := filepath.Join(dir, "modules", "contacts", "models", "customer.rk")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		s := strings.Replace(string(b), "    label: {id: Kota}\n", "    label: {id: Kota}\n    read_only_for: [sales_rep]\n", 1)
		if s == string(b) {
			t.Fatal("fixture changed: customer.city not found")
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg, rep := meta.ValidateOpts(meta.Options{AppsDir: filepath.Join(dir, "apps"), SiteDir: filepath.Join(dir, "site")}, nil, schema.Embedded())
	if rep.Errors() > 0 {
		t.Fatalf("metadata errors: %v", rep.All())
	}
	return reg
}

// Audit flow-locked F4: an upgrade that locks a field an installed company flow writes is reported; without a new
// lock nothing is.
func TestNewlyLockedFlowWrites(t *testing.T) {
	before, after := lockSite(t, false), lockSite(t, true)
	got := newlyLockedFlowWrites(before, after)
	if len(got) != 1 || got[0].Flow != "flows/isi_kota.rk" || got[0].Field != "city" {
		t.Fatalf("newly locked writes = %+v, want flows/isi_kota.rk writes city", got)
	}
	if note := lockedFlowNote(got); !strings.Contains(note, "flows/isi_kota.rk menulis city") {
		t.Errorf("note %q", note)
	}
	if got := newlyLockedFlowWrites(after, after); len(got) != 0 {
		t.Errorf("a lock that was there before is not new: %+v", got)
	}
	if got := newlyLockedFlowWrites(before, before); len(got) != 0 {
		t.Errorf("no lock, nothing to report: %+v", got)
	}
}
