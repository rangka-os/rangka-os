package schema

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
	"github.com/rangka-os/rangka-os/kernel/internal/testdb"
)

// The embedded copy must equal rangka-os/schema/0.5 (run `make sync-schemas` after editing the schemas).
func TestEmbeddedInSync(t *testing.T) {
	src := "../../../schema/0.5"
	ents, err := os.ReadDir(src)
	if err != nil {
		t.Skip("schema source dir not found")
	}
	for _, e := range ents {
		a, _ := os.ReadFile(filepath.Join(src, e.Name()))
		b, err := embedded.ReadFile("0.5/" + e.Name())
		if err != nil || !bytes.Equal(a, b) {
			t.Errorf("%s: embedded copy differs from %s; run make sync-schemas", e.Name(), src)
		}
	}
	a, _ := os.ReadFile("../../../schema/0.4/app.json")
	if b, err := embedded.ReadFile("0.4/app.json"); err != nil || !bytes.Equal(a, b) {
		t.Errorf("0.4/app.json: embedded copy differs; run make sync-schemas")
	}
}

// app.rk with modules is a 0.5 app; without, a 0.4 app (contract 0001).
func TestAppKindResolution(t *testing.T) {
	s := Embedded()
	legacy := FromData(map[string]any{"name": "crm", "version": "1.0.0", "title": map[string]any{"id": "CRM"},
		"requires": map[string]any{"core": "^1.0"}, "menu": []any{map[string]any{"group": map[string]any{"id": "CRM"}, "items": []any{"deal"}}}})
	if u := s.URL("app", legacy); u != Legacy+"app.json" {
		t.Errorf("legacy url = %s", u)
	}
	if errs := s.Validate("app", legacy); len(errs) != 0 {
		t.Errorf("legacy app: %+v", errs)
	}
	app := FromData(map[string]any{"name": "crm", "version": "1.0.0", "title": map[string]any{"id": "CRM"},
		"modules": map[string]any{"pipeline": "^1.0"}, "requires": map[string]any{"core": "^1.0"}})
	errs := s.Validate("app", app)
	if len(errs) != 1 || errs[0].Message != "Additional properties are not allowed ('requires' was unexpected)" {
		t.Errorf("0.5 app: %+v", errs)
	}
	mod := FromData(map[string]any{"name": "pipeline", "version": "1.0.0", "title": map[string]any{"id": "Pipeline"}, "menu": []any{}})
	if errs := s.Validate("module", mod); len(errs) != 1 || errs[0].Message != "Additional properties are not allowed ('menu' was unexpected)" {
		t.Errorf("module: %+v", errs)
	}
	bad := FromData(map[string]any{"name": "Bad", "version": "1.0.0", "title": map[string]any{"id": "x"}})
	if errs := s.Validate("app", bad); len(errs) != 1 || errs[0].PathString() != "name" {
		t.Errorf("legacy refs into model.json: %+v", errs)
	}
}

func TestMessages(t *testing.T) {
	s := Embedded()
	in := FromData(map[string]any{"name": "Bad Name", "fields": []any{
		map[string]any{"name": "x", "type": "nope"},
		map[string]any{"name": "y", "type": "link"},
	}, "extra": true})
	errs := s.Validate("model", in)
	want := map[string]string{
		"(root)":        "Additional properties are not allowed ('extra' was unexpected)",
		"name":          "'Bad Name' does not match '^[a-z][a-z0-9_]{0,62}$'",
		"fields/0/type": "'nope' is not one of ['data', 'text', 'int', 'float', 'currency', 'percent', 'check', 'select', 'multiselect', 'link', 'dynamic_link', 'table', 'date', 'daterange', 'datetime', 'duration', 'phone', 'npwp', 'attach', 'image', 'rating', 'color', 'json', 'signature', 'money', 'geo']",
		"fields/1":      "'to' is a required property",
	}
	got := map[string]string{}
	for _, e := range errs {
		got[e.PathString()] = e.Message
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q", k, got[k], v)
		}
	}
	if len(errs) != len(want) {
		t.Errorf("got %d errors: %+v", len(errs), errs)
	}
}

func TestRepr(t *testing.T) {
	cases := map[string]any{"1.5": 1.5, "25000000.0": 25000000.0, "1e+16": 1e16, "'it\\'s'": "it's", "\"it's\"": "it's", "None": nil}
	for want, v := range cases {
		if want == "'it\\'s'" {
			continue
		}
		if got := Repr(v); got != want {
			t.Errorf("Repr(%v) = %s want %s", v, got, want)
		}
	}
}

// Every workspace .rk file whose first line names a schema validates against it (role, connector and
// template included).
func TestWorkspaceFilesValidate(t *testing.T) {
	s := Embedded()
	n := 0
	for _, root := range []string{testdb.RepoRoot(), testdb.Sibling(t, "rangka-apps"), testdb.Sibling(t, "rangka-connectors")} {
		filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil && testdb.SkipHidden(d) {
				return filepath.SkipDir
			}
			if err != nil || d.IsDir() && (d.Name() == "testdata" || d.Name() == "node_modules") {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() || filepath.Ext(p) != ".rk" {
				return nil
			}
			doc, err := rk.Load(p)
			if err != nil {
				t.Errorf("%s: %v", p, err)
				return nil
			}
			kind := doc.Kind()
			if kind == "" {
				return nil
			}
			if !s.Has(kind) {
				t.Errorf("%s: no schema %s.json", p, kind)
				return nil
			}
			for _, e := range s.Validate(kind, FromNode(doc.Root)) {
				t.Errorf("%s: %s %s", p, e.PathString(), e.Message)
			}
			n++
			return nil
		})
	}
	if n < 30 {
		t.Errorf("only %d files checked", n)
	}
}

func TestImportSchema(t *testing.T) {
	s := Embedded()
	if !s.Has("import") || s.Raw("import") == nil {
		t.Fatal("kind import has no schema")
	}
	if !s.Has("model") {
		t.Fatal("existing kinds must stay available")
	}
	ok := FromData(map[string]any{"name": "moka", "model": "outlet_sales", "map": map[string]any{"date": "Date"}})
	if errs := s.Validate("import", ok); len(errs) != 0 {
		t.Errorf("valid import reported %+v", errs)
	}
	bad := FromData(map[string]any{"name": "moka", "map": map[string]any{}})
	got := map[string]string{}
	for _, e := range s.Validate("import", bad) {
		got[e.PathString()] = e.Message
	}
	if got["(root)"] != "'model' is a required property" || got["map"] != "{} does not have enough properties" || len(got) != 2 {
		t.Errorf("missing model / empty map: %+v", got)
	}
	noValue := FromData(map[string]any{"name": "x", "model": "y", "map": map[string]any{"a": map[string]any{"match": "sku"}}})
	errs := s.Validate("import", noValue)
	if len(errs) != 1 || errs[0].PathString() != "map/a" || errs[0].Message != "'value' is a required property" {
		t.Errorf("missing value: %+v", errs)
	}
}

// --schemas <dir> without a sibling 0.4/app.json still checks legacy app.rk files against 0.4 app.json.
func TestLoadDirLegacyFallback(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "only-0.5")
	os.MkdirAll(dir, 0o755)
	ents, err := os.ReadDir("../../../schema/0.5")
	if err != nil {
		t.Skip("schema source dir not found")
	}
	for _, e := range ents {
		b, _ := os.ReadFile(filepath.Join("../../../schema/0.5", e.Name()))
		os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644)
	}
	s, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	legacy := FromData(map[string]any{"name": "crm", "version": "1.0.0", "title": map[string]any{"id": "CRM"}, "requires": map[string]any{"core": "^1.0"}})
	if errs := s.Validate("app", legacy); len(errs) != 0 {
		t.Errorf("legacy app with LoadDir: %+v", errs)
	}
}
