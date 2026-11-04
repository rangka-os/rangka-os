package changeset

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/schema"
)

// "Tambah tabel" on the 0.5 apps of testdata/canvas: the ops the app builds (web/app/test/ubah.test.js writes them to
// testdata/ubah) validate and are medium risk. The same table under apps/<app>/models is what an app may not hold.
func TestUbahAddTableIsValidMedium(t *testing.T) {
	canvas := filepath.Join("..", "..", "testdata", "canvas")
	for _, app := range []string{"crm", "jasa_proyek"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "ubah", "add_table_"+app+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var cs ChangeSet
		if err := json.Unmarshal(b, &cs); err != nil {
			t.Fatal(err)
		}
		s := &Service{AppsDir: filepath.Join(canvas, "apps"), SiteDir: filepath.Join(canvas, "site"), ModulesDir: filepath.Join(canvas, "modules"), Schemas: schema.Embedded()}
		v, _, _, cleanup, err := s.check(&cs)
		if err != nil {
			t.Fatal(err)
		}
		cleanup()
		if len(v.Errors) > 0 {
			t.Errorf("%s: %d error(s), first %s %s %s", app, len(v.Errors), v.Errors[0].File, v.Errors[0].Code, v.Errors[0].Message)
		}
		live := s.live()
		files, err := live.result(cs.Ops)
		if err != nil {
			t.Fatal(err)
		}
		if a := Assess(cs.Ops, parsed(live, nil), parsed(live, files), s.appModel); a.Risk != "medium" {
			t.Errorf("%s: risk %s (%v), want medium", app, a.Risk, a.Reasons)
		}

		// the same table in the app's own directory is refused
		bad := ChangeSet{Ops: append([]Op{}, cs.Ops...)}
		for i, op := range bad.Ops {
			if strings.HasPrefix(op.Path, "modules/") && strings.Contains(op.Path, "/models/") {
				op.Path = "apps/" + app + "/models/" + filepath.Base(op.Path)
				bad.Ops[i] = op
			}
		}
		v, _, _, cleanup, err = s.check(&bad)
		if err != nil {
			t.Fatal(err)
		}
		cleanup()
		if len(v.Errors) == 0 {
			t.Errorf("%s: a model under apps/%s/models validated", app, app)
		} else if v.Errors[0].Code != "app_defines_model" {
			t.Errorf("%s: apps/ model refused with %s, want app_defines_model", app, v.Errors[0].Code)
		}
	}
}
