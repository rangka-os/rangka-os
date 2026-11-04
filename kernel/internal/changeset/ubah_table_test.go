package changeset

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/schema"
)

// "Tambah tabel" on the 0.5 apps of testdata/canvas: the ops the app builds (web/app/test/ubah.test.js writes them to
// testdata/ubah) put the table into the site company module (contract 0016), validate, and are medium risk in layer 4
// (with the layout, layer 6). The same table under apps/<app>/models is what an app may not hold, and a company
// model without the x_ prefix is refused.
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
		if cs.Ops[0].Path != "models/x_kunjungan_toko.rk" {
			t.Fatalf("%s: table at %s, want the site company module", app, cs.Ops[0].Path)
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
		if a := Assess(cs.Ops, parsed(live, nil), parsed(live, files), s.appModel); a.Risk != "medium" || !slices.Equal(a.Layers, []int{4, 6}) {
			t.Errorf("%s: risk %s layers %v (%v), want medium [4 6]", app, a.Risk, a.Layers, a.Reasons)
		}

		for _, c := range []struct{ path, code string }{
			{"apps/" + app + "/models/kunjungan_toko.rk", "app_defines_model"}, // an app defines no models
			{"models/kunjungan_toko.rk", "company_model_name"},                // a company model starts with x_
		} {
			bad := ChangeSet{Ops: append([]Op{}, cs.Ops...)}
			for i, op := range bad.Ops {
				if op.Path == cs.Ops[0].Path {
					op.Path = c.path
					op.Content = strings.Replace(op.Content, "x_kunjungan_toko", "kunjungan_toko", 1)
					bad.Ops[i] = op
				}
			}
			v, _, _, cleanup, err = s.check(&bad)
			if err != nil {
				t.Fatal(err)
			}
			cleanup()
			var codes []string
			for _, e := range v.Errors {
				codes = append(codes, e.Code)
			}
			if !slices.Contains(codes, c.code) {
				t.Errorf("%s: %s refused with %v, want %s", app, c.path, codes, c.code)
			}
		}
	}
}
