package backup

import (
	"strings"
	"testing"
)

// A whole-database archive leaves the ruang coba out (contract 0052 §2); an org archive is unchanged.
func TestWholeArchiveSkipsSandbox(t *testing.T) {
	model := table{name: "r_customer", cols: []Column{{Name: "name"}, {Name: "org_id"}}}
	if f := model.filter(""); !strings.Contains(f, "not in (select id from rk_orgs where sandbox)") {
		t.Fatalf("model table: %q", f)
	}
	if f := (table{name: "rk_orgs", cols: []Column{{Name: "id"}, {Name: "sandbox"}}}).filter(""); f != " where not sandbox" {
		t.Fatalf("rk_orgs: %q", f)
	}
	if f := (table{name: "rk_sandboxes", cols: []Column{{Name: "org_id"}}}).filter(""); f != " where false" {
		t.Fatalf("rk_sandboxes: %q", f)
	}
	if f := (table{name: "rk_meta_files", cols: []Column{{Name: "path"}}}).filter(""); f != "" {
		t.Fatalf("install table: %q", f)
	}
	if f := model.filter("kopi-senja"); strings.Contains(f, "sandbox") {
		t.Fatalf("org archive: %q", f)
	}
}
