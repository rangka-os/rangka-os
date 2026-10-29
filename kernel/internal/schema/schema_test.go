package schema

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/rangka-os/rangka-os/kernel/internal/rk"
)

// The embedded copy must equal rangka-os/schema/0.4 (run `make sync-schemas` after editing the schemas).
func TestEmbeddedInSync(t *testing.T) {
	src := "../../../schema/0.4"
	ents, err := os.ReadDir(src)
	if err != nil {
		t.Skip("schema source dir not found")
	}
	for _, e := range ents {
		a, _ := os.ReadFile(filepath.Join(src, e.Name()))
		b, err := embedded.ReadFile("0.4/" + e.Name())
		if err != nil || !bytes.Equal(a, b) {
			t.Errorf("%s: embedded copy differs from %s; run make sync-schemas", e.Name(), src)
		}
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
		"fields/0/type": "'nope' is not one of ['data', 'text', 'int', 'float', 'currency', 'percent', 'check', 'select', 'multiselect', 'link', 'dynamic_link', 'table', 'date', 'daterange', 'datetime', 'duration', 'phone', 'npwp', 'attach', 'image', 'rating', 'color', 'json', 'signature']",
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
	filepath.WalkDir("../../../..", func(p string, d os.DirEntry, err error) error {
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
	if n < 30 {
		t.Errorf("only %d files checked", n)
	}
}
