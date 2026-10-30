package rk

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Every workspace .rk file survives Load → Dump → Parse with the same data.
func TestDumpRoundTrip(t *testing.T) {
	n := 0
	filepath.WalkDir("../../../..", func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".rk") || strings.Contains(p, "testdata") {
			return nil
		}
		doc, err := Load(p)
		if err != nil {
			return nil
		}
		out := Dump(doc.Data, doc.Root, "https://rangka-os.github.io/rangka-os/schema/0.4/x.json")
		back, perr := LoadBytes(p, []byte(out))
		if perr != nil {
			t.Errorf("%s: dumped text does not parse: %v\n%s", p, perr, out)
			return nil
		}
		if !reflect.DeepEqual(back.Data, doc.Data) {
			t.Errorf("%s: round trip changed data\n%s", p, out)
		}
		n++
		return nil
	})
	if n < 20 {
		t.Fatalf("only %d files", n)
	}
	tricky := map[string]any{"a": "yes", "b": "0812", "c": "08:30", "d": "x: y", "e": "- z", "f": "", "g": "multi\nline\n", "h": []any{"on", "1", int64(1), 1.5, true, nil},
		"i": "#hash", "j": "it's", "k": map[string]any{"x": "a,b"}, "l": "trailing ", "m": "-5", "n": "a\tb"}
	out := Dump(tricky, nil, "")
	back, err := LoadBytes("x.rk", []byte(out))
	if err != nil || !reflect.DeepEqual(back.Data, tricky) {
		t.Errorf("tricky values: %v\n%s\n%v", err, out, back)
	}
}
