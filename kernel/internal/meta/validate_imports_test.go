package meta

import (
	"os"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/schema"
)

func TestValidateImports(t *testing.T) {
	wd, _ := os.Getwd()
	os.Chdir("../..")
	defer os.Chdir(wd)
	_, rep := ValidateOpts(Options{AppsDir: "testdata/broken-apps"}, nil, schema.Embedded())
	got := map[string]bool{}
	for _, it := range rep.All() {
		if strings.Contains(it.File, "/imports/") {
			got[strings.TrimPrefix(it.File, "broken-apps/")+" "+it.Code+" "+it.Where] = true
		}
	}
	for _, w := range []string{
		"sales/imports/bad_import.rk not_a_table lines/field",
		"sales/imports/bad_import.rk unknown_field map/nope",
		"sales/imports/bad_import.rk cel_syntax map/customer",
		"sales/imports/bad_import.rk unknown_field map/customer/match",
		"sales/imports/bad_import.rk not_a_link map/notes/match",
		"sales/imports/bad_lines.rk unknown_field lines/field",
		"sales/imports/bad_lines_map.rk unknown_field lines/map/ghost",
		"sales/imports/bad_lines_map.rk unknown_field lines/map/item/match",
		"sales/imports/bad_lines_map.rk cel_syntax lines/map/qty",
		"sales/imports/bad_model.rk unknown_model model",
		"sales/imports/bad_per.rk import_per_outlet per",
		"sales/imports/bad_per.rk import_no_key per",
		"sales/imports/wrong_name.rk name_mismatch name",
		"sales/imports/bad_lines.rk schema (root)",
	} {
		if !got[w] {
			t.Errorf("missing %q", w)
		}
	}
}
